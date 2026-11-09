package lbh

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type globalOpts struct {
	config string
	yes    bool
}

func modelsFor(cfg *Config, wanted []string) []*ModelEntry {
	entries := scanModels(cfg.ModelsDir(), loadModelsConfig(cfg.ModelsConfigPath()))
	return selectModels(entries, splitList(wanted))
}

func pickModelsInteractive(cfg *Config) []*ModelEntry {
	entries := scanModels(cfg.ModelsDir(), loadModelsConfig(cfg.ModelsConfigPath()))
	if len(entries) == 0 {
		fmt.Println("No models found. Check paths.models_dir in config.toml.")
		return nil
	}
	for i, e := range entries {
		extra := ""
		if e.Vision {
			extra = " [vision]"
		}
		fmt.Printf("  %d. %s (%s)%s\n", i+1, e.File, fmtBytesVal(e.SizeBytes), extra)
	}
	ans := ask("Models to use (numbers, comma separated; empty = all)", "")
	if ans == "" {
		return entries
	}
	var picked []*ModelEntry
	for _, part := range splitList([]string{ans}) {
		var n int
		if _, err := fmt.Sscanf(part, "%d", &n); err == nil && n >= 1 && n <= len(entries) {
			picked = append(picked, entries[n-1])
		}
	}
	return picked
}

// ---------------------------------------------------------------------------

func cmdInit(g globalOpts, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dir := fs.String("dir", "", "destination directory")
	force := fs.Bool("force", false, "overwrite existing files")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	dest := normalizePath(*dir)
	if dest == "" {
		dest, _ = os.Getwd()
	}
	if err := initFiles(dest, *force); err != nil {
		return errExit(err)
	}
	return 0
}

func cmdModels(g globalOpts, args []string) int {
	cfg, err := loadConfig(g.config)
	if err != nil {
		return errExit(err)
	}
	entries := scanModels(cfg.ModelsDir(), loadModelsConfig(cfg.ModelsConfigPath()))
	if len(entries) == 0 {
		fmt.Println("No models found.")
		return 0
	}
	for _, e := range entries {
		line := fmt.Sprintf("%s  (%s)", e.File, fmtBytesVal(e.SizeBytes))
		if e.Vision {
			line += fmt.Sprintf("  vision, mmproj: %s", e.Mmproj)
		}
		if len(e.ExtraBenchArgs) > 0 || len(e.ExtraServerArgs) > 0 {
			line += fmt.Sprintf("  extra bench: %v server: %v", e.ExtraBenchArgs, e.ExtraServerArgs)
		}
		fmt.Println(line)
	}
	return 0
}

// extractValueFlags pulls "--name value" / "--name=value" pairs out of
// args (Go's flag package stops parsing at the first positional, while
// users naturally put flags after the positional argument).
func extractValueFlags(args []string, names ...string) (map[string]string, []string) {
	vals := map[string]string{}
	var rest []string
	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		matched := false
		for n := range nameSet {
			if a == "--"+n && i+1 < len(args) {
				vals[n] = args[i+1]
				i++
				matched = true
				break
			}
			if strings.HasPrefix(a, "--"+n+"=") {
				vals[n] = strings.TrimPrefix(a, "--"+n+"=")
				matched = true
				break
			}
		}
		if !matched {
			rest = append(rest, a)
		}
	}
	return vals, rest
}

func cmdDownload(g globalOpts, args []string) int {
	vals, rest := extractValueFlags(args, "files", "dest")
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	if err := fs.Parse(rest); err != nil {
		return 1
	}
	filesFlag := vals["files"]
	destFlag := vals["dest"]
	cfg, err := loadConfig(g.config)
	if err != nil {
		return errExit(err)
	}
	target := ""
	if fs.NArg() > 0 {
		target = fs.Arg(0)
	}
	if target == "" {
		target = ask("Hugging Face URL or user/repo", "")
	}
	if target == "" {
		fmt.Println("Nothing to do.")
		return 1
	}
	repo, hint, rev, err := parseRepoInput(target)
	if err != nil {
		return errExit(err)
	}
	files, err := listRepoFiles(repo)
	if err != nil {
		return errExit(err)
	}
	if len(files) == 0 {
		fmt.Println("No files found in this repository.")
		return 1
	}
	var ggufs []*RepoFile
	for _, f := range files {
		if f.IsGGUF() {
			ggufs = append(ggufs, f)
		}
	}
	fmt.Printf("\nRepository: %s — %d files, %d GGUF\n", repo, len(files), len(ggufs))
	for i, f := range files {
		tag := ""
		if f.IsMmproj() {
			tag = " [mmproj]"
		} else if f.IsGGUF() {
			tag = " [gguf]"
		}
		fmt.Printf("  %3d. %s  (%s)%s\n", i+1, f.Path, fmtBytes(f.Size), tag)
	}
	if len(ggufs) == 0 {
		fmt.Println("\nWARNING: this repository contains no .gguf files. llama.cpp cannot use it " +
			"as-is (a format conversion would be needed).")
		if !confirm("Continue anyway?", false, g.yes) {
			return 1
		}
	}
	var chosen []*RepoFile
	switch {
	case filesFlag != "":
		wanted := splitList([]string{filesFlag})
		wantedSet := map[string]bool{}
		for _, w := range wanted {
			wantedSet[w] = true
		}
		for _, f := range files {
			if wantedSet[f.Path] || wantedSet[filepath.Base(f.Path)] {
				chosen = append(chosen, f)
			}
		}
		for _, w := range wanted {
			found := false
			for _, f := range files {
				if f.Path == w || filepath.Base(f.Path) == w {
					found = true
				}
			}
			if !found {
				fmt.Printf("  WARNING: no such file in repository: %s\n", w)
			}
		}
	case hint != "":
		for _, f := range files {
			if f.Path == hint {
				chosen = append(chosen, f)
			}
		}
	default:
		ans := ask("Files to download (numbers, 'gguf' for all GGUF, or 'all')", "gguf")
		switch strings.ToLower(ans) {
		case "all":
			chosen = files
		case "gguf":
			chosen = ggufs
		default:
			for _, part := range splitList([]string{ans}) {
				var n int
				if _, err := fmt.Sscanf(part, "%d", &n); err == nil && n >= 1 && n <= len(files) {
					chosen = append(chosen, files[n-1])
				}
			}
		}
	}
	var hasMm, hasMain bool
	for _, f := range chosen {
		if f.IsMmproj() {
			hasMm = true
		}
		if f.IsGGUF() && !f.IsMmproj() {
			hasMain = true
		}
	}
	var repoMm []*RepoFile
	for _, f := range files {
		if f.IsMmproj() {
			repoMm = append(repoMm, f)
		}
	}
	if hasMain && len(repoMm) > 0 && !hasMm {
		if confirm("This repository also has an mmproj (vision) file. Download it too?", true, g.yes) {
			chosen = append(chosen, repoMm...)
		}
	}
	if len(chosen) == 0 {
		fmt.Println("No files selected.")
		return 1
	}
	dest := destFlag
	if dest == "" {
		dest = cfg.Data.Download.Dest
	}
	if dest == "" {
		dest = cfg.ModelsDir()
	}
	dest = normalizePath(dest)
	if err := downloadFiles(repo, rev, chosen, dest, g.yes); err != nil {
		return errExit(err)
	}
	return 0
}

// parseModelsFlag collects the --models value plus any positional args
// (Go's flag package cannot do Python-style nargs="*").
func parseModelsFlag(fs *flag.FlagSet, args []string) (modelsVal string, interactive bool, rest []string, err error) {
	models := fs.String("models", "", "model file names or fragments (comma separated)")
	inter := fs.Bool("interactive", false, "pick models interactively")
	// Reorder: pull --models value out manually so subcommand flags that
	// follow it still parse. Simplest robust approach: pre-scan.
	var cleaned []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--models" && i+1 < len(args) {
			*models = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(a, "--models=") {
			*models = strings.TrimPrefix(a, "--models=")
			continue
		}
		cleaned = append(cleaned, a)
	}
	if err := fs.Parse(cleaned); err != nil {
		return "", false, nil, err
	}
	return *models, *inter, fs.Args(), nil
}

func cmdBench(g globalOpts, args []string) int {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	pTok := fs.Int("p", 0, "prompt tokens")
	gTok := fs.Int("n", 0, "generation tokens")
	reps := fs.Int("reps", 0, "repetitions")
	ngl := fs.Int("ngl", -1, "GPU layers")
	ctxF := fs.Int("context", 0, "context used for the VRAM estimate")
	modelsVal, interactive, rest, err := parseModelsFlag(fs, args)
	if err != nil {
		return 1
	}
	cfg, err := loadConfig(g.config)
	if err != nil {
		return errExit(err)
	}
	wanted := splitList([]string{modelsVal})
	wanted = append(wanted, rest...)
	var models []*ModelEntry
	if interactive || len(wanted) == 0 {
		models = pickModelsInteractive(cfg)
	} else {
		models = modelsFor(cfg, wanted)
	}
	if len(models) == 0 {
		fmt.Println("No models selected.")
		return 1
	}
	var ov benchOverrides
	if *pTok != 0 {
		ov.PromptTokens = pTok
	}
	if *gTok != 0 {
		ov.GenTokens = gTok
	}
	if *reps != 0 {
		ov.Reps = reps
	}
	if *ngl >= 0 {
		ov.NGL = ngl
	}
	if *ctxF != 0 {
		ov.Context = ctxF
	}
	runDir, err := runBench(cfg, models, g.yes, ov)
	if err != nil {
		return errExit(err)
	}
	if _, err := makeReport(runDir); err != nil {
		return errExit(err)
	}
	return 0
}

func cmdQuality(g globalOpts, args []string) int {
	fs := flag.NewFlagSet("quality", flag.ContinueOnError)
	prompts := fs.String("prompts", "", "prompt set JSON (default: bundled set)")
	image := fs.String("image", "", "test image for vision models")
	anon := fs.Bool("anonymize", false, "anonymize model names in the AI eval bundle")
	modelsVal, interactive, rest, err := parseModelsFlag(fs, args)
	if err != nil {
		return 1
	}
	cfg, err := loadConfig(g.config)
	if err != nil {
		return errExit(err)
	}
	wanted := splitList([]string{modelsVal})
	wanted = append(wanted, rest...)
	var models []*ModelEntry
	if interactive || len(wanted) == 0 {
		models = pickModelsInteractive(cfg)
	} else {
		models = modelsFor(cfg, wanted)
	}
	if len(models) == 0 {
		fmt.Println("No models selected.")
		return 1
	}
	runDir, err := runQuality(cfg, models, normalizePath(*prompts), normalizePath(*image), g.yes)
	if err != nil {
		return errExit(err)
	}
	bundle, mapping, err := exportAIEval(runDir, *anon)
	if err != nil {
		return errExit(err)
	}
	fmt.Printf("AI evaluation bundle: %s\n", bundle)
	if mapping != "" {
		fmt.Printf("Anonymization mapping (keep private): %s\n", mapping)
	}
	if _, err := makeReport(runDir); err != nil {
		return errExit(err)
	}
	return 0
}

func cmdExport(g globalOpts, args []string) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	run := fs.String("run", "", "run folder (default: latest quality run)")
	anon := fs.Bool("anonymize", false, "anonymize model names")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	cfg, err := loadConfig(g.config)
	if err != nil {
		return errExit(err)
	}
	runDir := normalizePath(*run)
	if runDir == "" {
		runDir = latestRun(cfg.ResultsDir(), "quality")
	}
	if runDir == "" {
		fmt.Println("No quality run found.")
		return 1
	}
	bundle, mapping, err := exportAIEval(runDir, *anon)
	if err != nil {
		return errExit(err)
	}
	fmt.Printf("AI evaluation bundle: %s\n", bundle)
	if mapping != "" {
		fmt.Printf("Anonymization mapping (keep private): %s\n", mapping)
	}
	return 0
}

func cmdReport(g globalOpts, args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	run := fs.String("run", "", "run folder (default: latest run)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	cfg, err := loadConfig(g.config)
	if err != nil {
		return errExit(err)
	}
	runDir := normalizePath(*run)
	if runDir == "" {
		runDir = latestRun(cfg.ResultsDir(), "")
	}
	if runDir == "" {
		fmt.Println("No run folders found.")
		return 1
	}
	out, err := makeReport(runDir)
	if err != nil {
		return errExit(err)
	}
	fmt.Printf("Report: %s\n", out)
	return 0
}

func errExit(err error) int {
	fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
	return 1
}

func menu(g globalOpts) int {
	fmt.Printf("llama-bench-helper (lbh) %s\n", Version)
	fmt.Println("  1) Download models from Hugging Face")
	fmt.Println("  2) Run llama-bench")
	fmt.Println("  3) Run quality checks")
	fmt.Println("  4) Make a report from the latest run")
	fmt.Println("  5) List detected models")
	fmt.Println("  0) Exit")
	switch ask("Select", "0") {
	case "1":
		return cmdDownload(g, nil)
	case "2":
		return cmdBench(g, []string{"--interactive"})
	case "3":
		return cmdQuality(g, []string{"--interactive"})
	case "4":
		return cmdReport(g, nil)
	case "5":
		return cmdModels(g, nil)
	}
	return 0
}

const usageText = `llama-bench-helper (lbh) ` + Version + `
Usage:
  lbh [--config PATH] [--yes] <command> [options]
  lbh                  open the interactive menu

Commands:
  init       write config.toml / models.toml templates  [--dir DIR] [--force]
  models     list detected models
  download   download GGUF files from Hugging Face     [URL] [--files a,b] [--dest DIR]
  bench      run llama-bench                            [--models a,b] [-p N] [-n N] [--reps N] [--ngl N] [--context N]
  quality    run shared-prompt quality checks           [--models a,b] [--prompts JSON] [--image PATH] [--anonymize]
  export     (re)build the AI evaluation bundle         [--run DIR] [--anonymize]
  report     build report.md for a run folder           [--run DIR]
  version    print the lbh version
`

// Run is the CLI entry point. It returns the process exit code.
func Run(argv []string) int {
	var g globalOpts
	args := argv
	// Parse leading global flags (they precede the subcommand).
	for len(args) > 0 {
		switch {
		case args[0] == "--config" && len(args) >= 2:
			g.config = normalizePath(args[1])
			args = args[2:]
		case strings.HasPrefix(args[0], "--config="):
			g.config = normalizePath(strings.TrimPrefix(args[0], "--config="))
			args = args[1:]
		case args[0] == "--yes" || args[0] == "-yes":
			g.yes = true
			args = args[1:]
		default:
			goto parsed
		}
	}
parsed:
	if len(args) == 0 {
		return menu(g)
	}
	switch args[0] {
	case "init":
		return cmdInit(g, args[1:])
	case "models":
		return cmdModels(g, args[1:])
	case "download":
		return cmdDownload(g, args[1:])
	case "bench":
		return cmdBench(g, args[1:])
	case "quality":
		return cmdQuality(g, args[1:])
	case "export":
		return cmdExport(g, args[1:])
	case "report":
		return cmdReport(g, args[1:])
	case "version", "--version", "-version":
		fmt.Printf("lbh %s\n", Version)
		return 0
	case "help", "--help", "-h":
		fmt.Print(usageText)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", args[0], usageText)
		return 1
	}
}
