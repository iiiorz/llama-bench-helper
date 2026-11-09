package lbh

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type benchResult struct {
	PPMedian  *float64
	TGMedian  *float64
	PPSamples []float64
	TGSamples []float64
	Meta      map[string]any
}

// parseBenchJSONL computes pp / tg medians from llama-bench -o jsonl
// output. pp records have n_prompt > 0 and n_gen == 0; tg records the
// reverse. The value is the median of samples_ts (NOT avg_ts).
func parseBenchJSONL(path string) (*benchResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	res := &benchResult{Meta: map[string]any{}}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec struct {
			NPrompt   int       `json:"n_prompt"`
			NGen      int       `json:"n_gen"`
			SamplesTs []float64 `json:"samples_ts"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		var raw map[string]any
		_ = json.Unmarshal([]byte(line), &raw)
		for _, k := range []string{"build_number", "build_commit", "gpu_info", "cpu_info", "model_type"} {
			if v, ok := raw[k]; ok {
				res.Meta[k] = v
			}
		}
		if rec.NPrompt != 0 && rec.NGen == 0 {
			res.PPSamples = append(res.PPSamples, rec.SamplesTs...)
		} else if rec.NGen != 0 && rec.NPrompt == 0 {
			res.TGSamples = append(res.TGSamples, rec.SamplesTs...)
		}
	}
	res.PPMedian = median(res.PPSamples)
	res.TGMedian = median(res.TGSamples)
	return res, nil
}

type benchOverrides struct {
	PromptTokens *int
	GenTokens    *int
	Reps         *int
	NGL          *int
	Context      *int
}

func runBench(cfg *Config, models []*ModelEntry, assumeYes bool, ov benchOverrides) (string, error) {
	b := cfg.Data.Bench
	pTok, gTok, reps, ngl, ctx := b.PromptTokens, b.GenTokens, b.Reps, b.NGL, b.Context
	if ov.PromptTokens != nil {
		pTok = *ov.PromptTokens
	}
	if ov.GenTokens != nil {
		gTok = *ov.GenTokens
	}
	if ov.Reps != nil {
		reps = *ov.Reps
	}
	if ov.NGL != nil {
		ngl = *ov.NGL
	}
	if ov.Context != nil {
		ctx = *ov.Context
	}
	fa, kv := b.FlashAttn, b.KVQuant

	benchExe := findExecutable("llama-bench", cfg.Data.Paths.LlamaBin)
	if benchExe == "" {
		return "", fmt.Errorf("llama-bench not found. Set paths.llama_bin in config.toml or add it to PATH")
	}

	runDir := filepath.Join(cfg.ResultsDir(), nowStamp()+"-bench")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", err
	}
	fmt.Printf("llama-bench: %s\n", benchExe)
	fmt.Printf("Run folder : %s\n", runDir)

	type row struct{ model, status, pp, tg, peak string }
	var rows []row
	metadata := map[string]any{
		"kind":           "bench",
		"started_at":     filepath.Base(runDir),
		"gpu":            gpuName(),
		"vram_total_mib": totalVramMiB(),
		"conditions": map[string]any{
			"prompt_tokens": pTok, "gen_tokens": gTok, "reps": reps,
			"ngl": ngl, "flash_attn": fa, "kv_quant": kv, "context_for_estimate": ctx,
		},
		"models": []any{},
	}
	var metaModels []any

	for _, m := range models {
		fmt.Printf("\n== %s ==\n", m.File)
		if warn := vramWarning(m.SizeBytes, ctx, kv); warn != "" {
			fmt.Printf("  VRAM WARNING: %s\n", warn)
			if !confirm("  Run this model anyway?", false, assumeYes) {
				rows = append(rows, row{m.File, "SKIPPED (VRAM warning)", "", "", ""})
				continue
			}
		}
		jsonlPath := filepath.Join(runDir, m.Key()+".jsonl")
		vramFile := filepath.Join(runDir, "vram-"+m.Key()+".csv")
		faStr := "off"
		if fa {
			faStr = "on"
		}
		args := []string{"-m", m.Path,
			"-p", fmt.Sprint(pTok), "-n", fmt.Sprint(gTok), "-r", fmt.Sprint(reps),
			"-ngl", fmt.Sprint(ngl), "-fa", faStr,
			"-ctk", kv, "-ctv", kv, "-o", "jsonl"}
		args = append(args, m.ExtraBenchArgs...)
		cmdArgs := append([]string{benchExe}, args...)
		fmt.Println("  $ " + strings.Join(cmdArgs, " "))

		logger := NewVramLogger(vramFile)
		logger.Start()
		out, ferr := os.Create(jsonlPath)
		var rc int
		if ferr != nil {
			rc = -1
		} else {
			cmd := exec.Command(benchExe, args...)
			cmd.Stdout = out
			cmd.Stderr = os.Stderr
			runErr := cmd.Run()
			_ = out.Close()
			if runErr != nil {
				if exitErr, ok := runErr.(*exec.ExitError); ok {
					rc = exitErr.ExitCode()
				} else {
					rc = -1
				}
			}
		}
		peak := logger.Stop()
		peakStr := ""
		if peak != nil {
			peakStr = fmt.Sprint(int64(*peak))
		}
		if rc != 0 || !isFile(jsonlPath) {
			status := fmt.Sprintf("FAILED (exit %d)", rc)
			rows = append(rows, row{m.File, status, "", "", peakStr})
			fmt.Printf("  %s; VRAM peak: %s\n", status, peakStr)
			continue
		}
		parsed, perr := parseBenchJSONL(jsonlPath)
		if perr != nil {
			parsed = &benchResult{Meta: map[string]any{}}
		}
		status := "OK"
		if parsed.PPMedian == nil && parsed.TGMedian == nil {
			status = "FAILED (no records in jsonl)"
		}
		ppStr, tgStr := "", ""
		if parsed.PPMedian != nil {
			ppStr = pyFloat(*parsed.PPMedian)
		}
		if parsed.TGMedian != nil {
			tgStr = pyFloat(*parsed.TGMedian)
		}
		rows = append(rows, row{m.File, status, ppStr, tgStr, peakStr})
		entry := map[string]any{"file": m.File}
		for k, v := range parsed.Meta {
			entry[k] = v
		}
		metaModels = append(metaModels, entry)
		fmt.Printf("  pp median: %s  tg median: %s  VRAM peak: %s MiB\n", ppStr, tgStr, peakStr)
	}
	metadata["models"] = metaModels

	summary := filepath.Join(runDir, "bench-summary.csv")
	f, err := os.Create(summary)
	if err != nil {
		return "", err
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"model", "status", "pp_median_tok_s", "tg_median_tok_s", "vram_peak_mib"})
	for _, r := range rows {
		_ = w.Write([]string{r.model, r.status, r.pp, r.tg, r.peak})
	}
	w.Flush()
	_ = f.Close()
	if err := writeJSON(filepath.Join(runDir, "run-metadata.json"), metadata); err != nil {
		return "", err
	}
	fmt.Printf("\nSummary: %s\n", summary)
	return runDir, nil
}
