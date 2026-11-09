package lbh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ModelConfig struct {
	File            string   `toml:"file"`
	Mmproj          string   `toml:"mmproj"`
	Vision          bool     `toml:"vision"`
	ExtraBenchArgs  []string `toml:"extra_bench_args"`
	ExtraServerArgs []string `toml:"extra_server_args"`
}

type modelsFile struct {
	Models []ModelConfig `toml:"model"`
}

type ModelEntry struct {
	File            string
	Path            string
	SizeBytes       int64
	Mmproj          string
	MmprojPath      string
	Vision          bool
	ExtraBenchArgs  []string
	ExtraServerArgs []string
}

// Key is the short identifier used for output folders (file stem, sanitized).
func (m *ModelEntry) Key() string {
	stem := strings.TrimSuffix(m.File, filepath.Ext(m.File))
	var b strings.Builder
	for _, r := range stem {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

var serverPresets = []struct {
	fragment string
	args     []string
}{
	{"gpt-oss", []string{"--jinja"}}, // harmony chat template needs the jinja engine
}

func loadModelsConfig(path string) []ModelConfig {
	if !isFile(path) {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var mf modelsFile
	if _, err := unmarshalTOML(b, &mf); err != nil {
		fmt.Printf("  WARNING: could not parse %s: %v\n", path, err)
		return nil
	}
	dirty := false
	for _, m := range mf.Models {
		if hasControl(m.File) || hasControl(m.Mmproj) {
			dirty = true
		}
	}
	if dirty {
		var mf2 modelsFile
		if _, err := unmarshalTOML(rescueBackslashStrings(b), &mf2); err == nil {
			mf = mf2
		}
	}
	for i := range mf.Models {
		mf.Models[i].File = normalizePath(mf.Models[i].File)
		mf.Models[i].Mmproj = normalizePath(mf.Models[i].Mmproj)
	}
	return mf.Models
}

// matchMmproj pairs mmproj-Qwen3VL-8B-Instruct-F16.gguf with
// Qwen3VL-8B-Instruct-Q4_K_M.gguf via the shared middle part.
func matchMmproj(modelFile string, mmprojFiles []string) string {
	stem := strings.ToLower(strings.TrimSuffix(modelFile, filepath.Ext(modelFile)))
	for _, mp := range mmprojFiles {
		name := strings.ToLower(strings.TrimSuffix(filepath.Base(mp), filepath.Ext(mp)))
		core := name
		for _, pre := range []string{"mmproj-", "mmproj_"} {
			core = strings.TrimPrefix(core, pre)
		}
		for _, suf := range []string{"-f16", "-f32", "-q8_0", "-bf16"} {
			core = strings.TrimSuffix(core, suf)
		}
		if core != "" && strings.Contains(stem, core) {
			return mp
		}
	}
	return ""
}

func scanModels(modelsDir string, modelsCfg []ModelConfig) []*ModelEntry {
	var entries []*ModelEntry
	seen := map[string]bool{}

	var allGguf []string
	if isDir(modelsDir) {
		des, err := os.ReadDir(modelsDir)
		if err == nil {
			for _, de := range des {
				if !de.IsDir() && strings.HasSuffix(strings.ToLower(de.Name()), ".gguf") {
					allGguf = append(allGguf, filepath.Join(modelsDir, de.Name()))
				}
			}
		}
	}
	var mmprojFiles []string
	for _, p := range allGguf {
		if strings.HasPrefix(strings.ToLower(filepath.Base(p)), "mmproj") {
			mmprojFiles = append(mmprojFiles, p)
		}
	}

	build := func(fileName string, cfg ModelConfig) *ModelEntry {
		base := baseNameAny(fileName)
		full := fileName
		if !filepath.IsAbs(full) {
			full = filepath.Join(modelsDir, base)
			if !isFile(full) && isFile(fileName) {
				full = fileName
			}
		}
		if !isFile(full) {
			fmt.Printf("  WARNING: model file not found, skipped: %s\n", fileName)
			return nil
		}
		var mmPath string
		if cfg.Mmproj != "" {
			cand := filepath.Join(modelsDir, baseNameAny(cfg.Mmproj))
			if isFile(cand) {
				mmPath = cand
			}
		}
		if mmPath == "" {
			mmPath = matchMmproj(filepath.Base(full), mmprojFiles)
		}
		vision := cfg.Vision || mmPath != ""
		extraServer := append([]string(nil), cfg.ExtraServerArgs...)
		low := strings.ToLower(filepath.Base(full))
		for _, pr := range serverPresets {
			if strings.Contains(low, pr.fragment) {
				for _, a := range pr.args {
					found := false
					for _, e := range extraServer {
						if e == a {
							found = true
						}
					}
					if !found {
						extraServer = append(extraServer, a)
					}
				}
			}
		}
		st, _ := os.Stat(full)
		var size int64
		if st != nil {
			size = st.Size()
		}
		e := &ModelEntry{
			File:            filepath.Base(full),
			Path:            full,
			SizeBytes:       size,
			Vision:          vision,
			ExtraBenchArgs:  append([]string(nil), cfg.ExtraBenchArgs...),
			ExtraServerArgs: extraServer,
		}
		if mmPath != "" {
			e.Mmproj = filepath.Base(mmPath)
			e.MmprojPath = mmPath
		}
		return e
	}

	for _, cfg := range modelsCfg {
		f := strings.TrimSpace(cfg.File)
		if f == "" {
			continue
		}
		if e := build(f, cfg); e != nil {
			entries = append(entries, e)
			seen[strings.ToLower(e.File)] = true
		}
	}
	for _, p := range allGguf {
		name := filepath.Base(p)
		if strings.HasPrefix(strings.ToLower(name), "mmproj") || seen[strings.ToLower(name)] {
			continue
		}
		if e := build(name, ModelConfig{}); e != nil {
			entries = append(entries, e)
			seen[strings.ToLower(name)] = true
		}
	}
	return entries
}

func selectModels(entries []*ModelEntry, wanted []string) []*ModelEntry {
	if len(wanted) == 0 {
		return entries
	}
	var out []*ModelEntry
	added := map[*ModelEntry]bool{}
	for _, w := range wanted {
		lw := strings.ToLower(w)
		var hits []*ModelEntry
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.File), lw) || strings.ToLower(e.Key()) == lw {
				hits = append(hits, e)
			}
		}
		if len(hits) == 0 {
			fmt.Printf("  WARNING: no model matches '%s'\n", w)
		}
		for _, e := range hits {
			if !added[e] {
				out = append(out, e)
				added[e] = true
			}
		}
	}
	return out
}
