package lbh

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func csvTable(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "(empty)"
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) == 0 {
		return "(empty)"
	}
	head, body := rows[0], rows[1:]
	sep := "|"
	for range head {
		sep += "---|"
	}
	lines := []string{"| " + strings.Join(head, " | ") + " |", sep}
	for _, r := range body {
		lines = append(lines, "| "+strings.Join(r, " | ")+" |")
	}
	return strings.Join(lines, "\n")
}

func makeReport(runDir string) (string, error) {
	parts := []string{"# lbh report: " + filepath.Base(runDir) + "\n"}
	if isFile(filepath.Join(runDir, "bench-summary.csv")) {
		parts = append(parts, "## Benchmark (llama-bench)\n", csvTable(filepath.Join(runDir, "bench-summary.csv")))
	}
	if isFile(filepath.Join(runDir, "quality-summary.csv")) {
		parts = append(parts, "\n## Quality (llama-server)\n", csvTable(filepath.Join(runDir, "quality-summary.csv")))
	}
	if len(parts) == 1 {
		parts = append(parts, "No summary CSV found in this folder.")
	}
	out := filepath.Join(runDir, "report.md")
	if err := os.WriteFile(out, []byte(strings.Join(parts, "\n")+"\n"), 0o644); err != nil {
		return "", err
	}
	return out, nil
}

func latestRun(resultsDir string, kind string) string {
	if !isDir(resultsDir) {
		return ""
	}
	des, err := os.ReadDir(resultsDir)
	if err != nil {
		return ""
	}
	var dirs []string
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		if kind != "" && !strings.HasSuffix(de.Name(), "-"+kind) {
			continue
		}
		dirs = append(dirs, de.Name())
	}
	if len(dirs) == 0 {
		return ""
	}
	sort.Strings(dirs)
	return filepath.Join(resultsDir, dirs[len(dirs)-1])
}
