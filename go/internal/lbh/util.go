package lbh

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Version of the Go beta build.
const Version = "2.0.0-beta.2"

// normalizePath makes Windows-style paths usable everywhere: both '\'
// and '/' are treated as separators and rewritten to the OS separator.
// This lets config values such as 'C:\tools\llama.cpp' (or a mix like
// 'C:\tools/llama.cpp') work regardless of how they were written.
// Note: on Unix a literal backslash inside a file name would be
// rewritten too; lbh paths are not expected to contain one.
func normalizePath(p string) string {
	if p == "" {
		return p
	}
	return filepath.FromSlash(strings.ReplaceAll(p, "\\", "/"))
}

// baseNameAny returns the final path element, splitting on both '/'
// and '\' regardless of the host OS (filepath.Base only splits on the
// host separator).
func baseNameAny(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

var stdinReader = bufio.NewReader(os.Stdin)

func nowStamp() string {
	return time.Now().Format("20060102-150405")
}

func fmtBytes(n *int64) string {
	if n == nil {
		return "-"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	v := float64(*n)
	for i, u := range units {
		if v < 1024 || i == len(units)-1 {
			if u == "B" {
				return fmt.Sprintf("%d B", int64(v))
			}
			return fmt.Sprintf("%.2f %s", v, u)
		}
		v /= 1024
	}
	return fmt.Sprintf("%.2f TiB", v)
}

func fmtBytesVal(n int64) string { return fmtBytes(&n) }

func median(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	s := append([]float64(nil), values...)
	sort.Float64s(s)
	mid := len(s) / 2
	var m float64
	if len(s)%2 == 1 {
		m = s[mid]
	} else {
		m = (s[mid-1] + s[mid]) / 2
	}
	return &m
}

// pyFloat formats a float the way Python's str(round(x, 2)) does for the
// values lbh writes into CSV files (e.g. 3550.0 rather than 3550).
func pyFloat(v float64) string {
	r := math.Round(v*100) / 100
	if r == math.Trunc(r) {
		return strconv.FormatFloat(r, 'f', 1, 64)
	}
	return strconv.FormatFloat(r, 'f', -1, 64)
}

func findExecutable(name string, searchDir string) string {
	if searchDir != "" {
		p := expandUser(searchDir)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		} else if err == nil && st.IsDir() {
			for _, cand := range exeCandidates(name) {
				full := filepath.Join(p, cand)
				if st2, err := os.Stat(full); err == nil && !st2.IsDir() {
					return full
				}
			}
		}
	}
	if found, err := exec.LookPath(name); err == nil {
		return found
	}
	return ""
}

func exeCandidates(name string) []string {
	if os.PathSeparator == '\\' || strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
		if strings.HasSuffix(strings.ToLower(name), ".exe") {
			return []string{name}
		}
		return []string{name + ".exe", name}
	}
	return []string{name}
}

func expandUser(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			if len(p) == 1 {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// runStreaming runs a command with stdio attached to the terminal and
// returns its exit code (127 when the executable cannot be found).
func runStreaming(cmd []string) int {
	fmt.Println("  $ " + strings.Join(cmd, " "))
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "  ERROR: executable not found: %s\n", cmd[0])
		return 127
	}
	return 0
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeJSON(path string, data any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func readJSONFile(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// splitList normalizes list-like CLI values: every element is split on
// commas and trimmed (a "a,b,c" value may arrive as one element).
func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func ask(prompt string, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	line, err := stdinReader.ReadString('\n')
	if err != nil && line == "" {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func confirm(prompt string, def bool, assumeYes bool) bool {
	if assumeYes {
		return true
	}
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	fmt.Printf("%s (%s): ", prompt, hint)
	line, err := stdinReader.ReadString('\n')
	if err != nil && line == "" {
		return def
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return def
	}
	return line == "y" || line == "yes"
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
