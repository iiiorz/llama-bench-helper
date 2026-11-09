package lbh

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

//go:embed assets/config_template.toml
var configTemplate string

//go:embed assets/models_template.toml
var modelsTemplate string

//go:embed assets/default_prompts.json
var defaultPromptsJSON []byte

type PathsConfig struct {
	LlamaBin   string `toml:"llama_bin"`
	ModelsDir  string `toml:"models_dir"`
	ResultsDir string `toml:"results_dir"`
}

type BenchConfig struct {
	PromptTokens int    `toml:"prompt_tokens"`
	GenTokens    int    `toml:"gen_tokens"`
	Reps         int    `toml:"reps"`
	NGL          int    `toml:"ngl"`
	FlashAttn    bool   `toml:"flash_attn"`
	KVQuant      string `toml:"kv_quant"`
	Context      int    `toml:"context"`
}

type QualityConfig struct {
	PromptSet      string `toml:"prompt_set"`
	Port           int    `toml:"port"`
	Context        int    `toml:"context"`
	MaxTokensFloor int    `toml:"max_tokens_floor"`
	ImagePath      string `toml:"image_path"`
	CodeExecCheck  bool   `toml:"code_exec_check"`
}

type DownloadConfig struct {
	Dest string `toml:"dest"`
}

type FileConfig struct {
	Paths    PathsConfig    `toml:"paths"`
	Bench    BenchConfig    `toml:"bench"`
	Quality  QualityConfig  `toml:"quality"`
	Download DownloadConfig `toml:"download"`
}

type Config struct {
	Data   FileConfig
	Source string // config file path, "" when running on defaults only
}

func defaultFileConfig() FileConfig {
	return FileConfig{
		Paths: PathsConfig{LlamaBin: "", ModelsDir: "models", ResultsDir: "results"},
		Bench: BenchConfig{PromptTokens: 512, GenTokens: 128, Reps: 3, NGL: 99,
			FlashAttn: true, KVQuant: "q8_0", Context: 8192},
		Quality: QualityConfig{PromptSet: "", Port: 8080, Context: 8192,
			MaxTokensFloor: 1024, ImagePath: "", CodeExecCheck: false},
		Download: DownloadConfig{Dest: ""},
	}
}

// rescueBackslashStrings rewrites a TOML document so that Windows paths
// written with single backslashes inside double-quoted strings parse as
// intended. Inside a basic ("...") string, a backslash is doubled unless
// it escapes a backslash or a quote (\\ and \" keep their meaning).
// Literal strings and comments are passed through untouched. This is
// only applied as a fallback for documents that fail to parse as-is, or
// whose parsed path values contain control characters — the signature
// of a raw "\t" / "\n" in a path being read as a TOML escape.
func rescueBackslashStrings(b []byte) []byte {
	const (
		stNormal       = iota
		stBasic        // "..."
		stLiteral      // '...'
		stBasicMulti   // """..."""
		stLiteralMulti // '''...'''
		stComment
	)
	var out []byte
	state := stNormal
	i := 0
	for i < len(b) {
		c := b[i]
		switch state {
		case stNormal:
			switch {
			case c == '#':
				state = stComment
				out = append(out, c)
				i++
			case c == '"' && i+2 < len(b) && b[i+1] == '"' && b[i+2] == '"':
				state = stBasicMulti
				out = append(out, b[i:i+3]...)
				i += 3
			case c == '"':
				state = stBasic
				out = append(out, c)
				i++
			case c == '\'' && i+2 < len(b) && b[i+1] == '\'' && b[i+2] == '\'':
				state = stLiteralMulti
				out = append(out, b[i:i+3]...)
				i += 3
			case c == '\'':
				state = stLiteral
				out = append(out, c)
				i++
			default:
				out = append(out, c)
				i++
			}
		case stComment:
			out = append(out, c)
			i++
			if c == '\n' {
				state = stNormal
			}
		case stBasic, stBasicMulti:
			if c == '\\' && i+1 < len(b) {
				n := b[i+1]
				if n == '\\' || n == '"' {
					// Escaped backslash / quote: keep as-is.
					out = append(out, c, n)
					i += 2
				} else {
					// In rescue mode every other backslash is a path
					// separator the user typed (e.g. \t in "C:\tools"
					// must stay backslash+t, not become a TAB).
					out = append(out, '\\', '\\', n)
					i += 2
				}
				continue
			}
			out = append(out, c)
			i++
			if state == stBasic && c == '"' {
				state = stNormal
			}
			if state == stBasicMulti && c == '"' && i+1 < len(b) && b[i] == '"' && b[i+1] == '"' {
				out = append(out, b[i:i+2]...)
				i += 2
				state = stNormal
			}
		case stLiteral, stLiteralMulti:
			out = append(out, c)
			i++
			if state == stLiteral && c == '\'' {
				state = stNormal
			}
			if state == stLiteralMulti && c == '\'' && i+1 < len(b) && b[i] == '\'' && b[i+1] == '\'' {
				out = append(out, b[i:i+2]...)
				i += 2
				state = stNormal
			}
		}
	}
	return out
}

// hasControl reports whether s contains control characters. Real file
// paths never do (Windows forbids them in names), so finding one in a
// parsed path value means a raw backslash escape was misread by TOML.
func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// unmarshalTOML parses TOML into v. If parsing fails, it retries once
// with backslash-rescued text so that config files containing raw
// Windows paths in double quotes (e.g. llama_bin = "C:\tools\llama.cpp")
// still load. Returns true when the rescue path was used.
func unmarshalTOML(b []byte, v any) (bool, error) {
	if err := toml.Unmarshal(b, v); err != nil {
		rescued := rescueBackslashStrings(b)
		if err2 := toml.Unmarshal(rescued, v); err2 != nil {
			return false, err // report the original error
		}
		return true, nil
	}
	return false, nil
}

func findConfigFile(explicit string) string {
	if explicit != "" {
		if isFile(explicit) {
			return explicit
		}
		return ""
	}
	if env := os.Getenv("LBH_CONFIG"); env != "" && isFile(env) {
		return env
	}
	if cwd, err := os.Getwd(); err == nil {
		p := filepath.Join(cwd, "config.toml")
		if isFile(p) {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".lbh", "config.toml")
		if isFile(p) {
			return p
		}
	}
	return ""
}

func loadConfig(explicit string) (*Config, error) {
	cfg := &Config{Data: defaultFileConfig()}
	src := findConfigFile(explicit)
	if src == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	data := defaultFileConfig()
	rescued, err := unmarshalTOML(b, &data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", src, err)
	}
	if !rescued {
		// Silent trap: a raw "\t" or "\n" inside a path is a *valid*
		// TOML escape, so the file parses — but the path now contains a
		// TAB or newline. Real paths never contain control characters,
		// so if one shows up, re-read the file with backslash rescue.
		paths := []string{data.Paths.LlamaBin, data.Paths.ModelsDir, data.Paths.ResultsDir,
			data.Quality.PromptSet, data.Quality.ImagePath, data.Download.Dest}
		dirty := false
		for _, p := range paths {
			if hasControl(p) {
				dirty = true
			}
		}
		if dirty {
			fresh := defaultFileConfig()
			if _, err2 := unmarshalTOML(rescueBackslashStrings(b), &fresh); err2 == nil {
				data = fresh
				rescued = true
			}
		}
	}
	if rescued {
		fmt.Printf("NOTE: %s was read with Windows-path backslash rescue. "+
			"Writing paths as 'C:\\tools\\...' (single quotes) avoids this.\n", src)
	}
	// Accept both '/' and '\' as path separators in config values.
	data.Paths.LlamaBin = normalizePath(data.Paths.LlamaBin)
	data.Paths.ModelsDir = normalizePath(data.Paths.ModelsDir)
	data.Paths.ResultsDir = normalizePath(data.Paths.ResultsDir)
	data.Quality.PromptSet = normalizePath(data.Quality.PromptSet)
	data.Quality.ImagePath = normalizePath(data.Quality.ImagePath)
	data.Download.Dest = normalizePath(data.Download.Dest)
	cfg.Data = data
	cfg.Source = src
	return cfg, nil
}

// ModelsConfigPath: models.toml sits next to config.toml, else in the CWD.
func (c *Config) ModelsConfigPath() string {
	if c.Source != "" {
		return filepath.Join(filepath.Dir(c.Source), "models.toml")
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, "models.toml")
}

func (c *Config) ModelsDir() string  { return expandUser(c.Data.Paths.ModelsDir) }
func (c *Config) ResultsDir() string { return expandUser(c.Data.Paths.ResultsDir) }

func initFiles(destDir string, force bool) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	for _, item := range []struct{ name, body string }{
		{"config.toml", configTemplate},
		{"models.toml", modelsTemplate},
	} {
		p := filepath.Join(destDir, item.name)
		if isFile(p) && !force {
			fmt.Printf("  exists, skipped: %s\n", p)
			continue
		}
		if err := os.WriteFile(p, []byte(item.body), 0o644); err != nil {
			return err
		}
		fmt.Printf("  wrote: %s\n", p)
	}
	return nil
}
