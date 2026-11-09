package lbh

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func fixturePath(name string) string {
	return filepath.Join("..", "..", "..", "tests", "fixtures", name)
}

func TestParseBenchJSONLRealFixture(t *testing.T) {
	r, err := parseBenchJSONL(fixturePath("bench-qwen3-14b-q4km.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if r.PPMedian == nil || math.Abs(*r.PPMedian-3632.48) > 0.01 {
		t.Errorf("pp median = %v, want 3632.48", r.PPMedian)
	}
	if r.TGMedian == nil || math.Abs(*r.TGMedian-65.7433) > 0.001 {
		t.Errorf("tg median = %v, want 65.7433", r.TGMedian)
	}
	if got, ok := r.Meta["build_number"].(float64); !ok || got != 11960 {
		t.Errorf("build_number = %v, want 11960", r.Meta["build_number"])
	}
}

func TestParseRepoInput(t *testing.T) {
	cases := []struct {
		in, repo, hint string
	}{
		{"Qwen/Qwen3-14B-GGUF", "Qwen/Qwen3-14B-GGUF", ""},
		{"https://huggingface.co/Qwen/Qwen3-14B-GGUF", "Qwen/Qwen3-14B-GGUF", ""},
		{"https://huggingface.co/Qwen/Qwen3-14B-GGUF/blob/main/Qwen3-14B-Q4_K_M.gguf",
			"Qwen/Qwen3-14B-GGUF", "Qwen3-14B-Q4_K_M.gguf"},
		{"https://huggingface.co/ggml-org/gpt-oss-20b-GGUF/tree/main",
			"ggml-org/gpt-oss-20b-GGUF", ""},
	}
	for _, c := range cases {
		repo, hint, _, err := parseRepoInput(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if repo != c.repo || hint != c.hint {
			t.Errorf("%s -> (%q, %q), want (%q, %q)", c.in, repo, hint, c.repo, c.hint)
		}
	}
}

const coderP4 = "## メリット  \n・通勤時間の削減でリラックスできる  \n・作業環境の自由な調整が可能  \n・時間管理がしやすく、効率が向上  \n\n## 注意点  \n・集中力の維持が難しい場合がある  \n・コミュニケーションが不足しやすい  \n・仕事と生活の境界が曖昧になることがある  \n\n在宅勤務はメリットも多いが、自発的な管理が重要である。"

const q4P4 = "## メリット  \n- 通勤時間の削減で効率向上  \n- 柔軟な時間調整が可能  \n- 交通費や食費の削減  \n\n## 注意点  \n- 孤立感のリスク  \n- 仕事と生活の区別が難しい  \n- コミュニケーションの遅れ  \n\n通勤の負担が減る一方で、注意点を考慮した環境整備が重要です。"

var p4Checks = []CheckDef{
	{Type: "max_chars", Value: float64(400)},
	{Type: "count_equals", Needle: "通勤", Value: float64(1)},
	{Type: "contains", Needle: "## メリット"},
	{Type: "contains", Needle: "## 注意点"},
	{Type: "bullets_exactly", Section: "## メリット", Value: float64(3)},
}

func TestChecksCoderP4AllPass(t *testing.T) {
	for _, r := range runChecks(coderP4, p4Checks) {
		if !r.OK {
			t.Errorf("coder P4 check failed: %+v", r)
		}
	}
}

func TestChecksQ4P4FailsOnlyTsuukinCount(t *testing.T) {
	var failed []CheckResult
	for _, r := range runChecks(q4P4, p4Checks) {
		if !r.OK {
			failed = append(failed, r)
		}
	}
	if len(failed) != 1 || failed[0].Type != "count_equals" || failed[0].Actual != 2 {
		t.Errorf("failed = %+v, want only count_equals with actual 2", failed)
	}
}

func TestSentenceMax(t *testing.T) {
	five := "文です。文です。文です。文です。文です。"
	r := runChecks(five, []CheckDef{{Type: "sentence_max", Value: float64(3)}})
	if r[0].OK || r[0].Actual != 5 {
		t.Errorf("result = %+v, want NG with actual 5", r[0])
	}
}

func TestSplitList(t *testing.T) {
	got := splitList([]string{"p1,p2,p3"})
	if len(got) != 3 || got[0] != "p1" || got[2] != "p3" {
		t.Errorf("splitList = %v", got)
	}
	got = splitList([]string{"a", "b,c", " d "})
	if len(got) != 4 || got[3] != "d" {
		t.Errorf("splitList = %v", got)
	}
}

func TestEstimate(t *testing.T) {
	const total = 16376
	q8 := estimateVramMiB(15698866586, 8192, "q8_0") // ≈14.62 GiB
	q4 := estimateVramMiB(8997966845, 8192, "q8_0")  // ≈8.38 GiB
	if q8 <= total {
		t.Errorf("q8 estimate %.0f should exceed %d", q8, total)
	}
	if q4 >= total {
		t.Errorf("q4 estimate %.0f should fit in %d", q4, total)
	}
}

func TestDefaultPromptSet(t *testing.T) {
	prompts, err := parsePromptSet(defaultPromptsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 6 {
		t.Fatalf("default set has %d prompts, want 6", len(prompts))
	}
	var p4 *Prompt
	for i := range prompts {
		if prompts[i].ID == "p4" {
			p4 = &prompts[i]
		}
	}
	if p4 == nil {
		t.Fatal("p4 missing")
	}
	foundSection := false
	for _, c := range p4.Checks {
		if c.Type == "bullets_exactly" && c.Section == "## メリット" {
			foundSection = true
		}
	}
	if !foundSection {
		t.Error("p4 bullets_exactly check lost its section scope")
	}
}

func TestPyFloat(t *testing.T) {
	if got := pyFloat(3550.0); got != "3550.0" {
		t.Errorf("pyFloat(3550) = %q", got)
	}
	if got := pyFloat(3632.48); got != "3632.48" {
		t.Errorf("pyFloat(3632.48) = %q", got)
	}
	if got := pyFloat(65.7433); got != "65.74" {
		t.Errorf("pyFloat(65.7433) = %q", got)
	}
}

func TestNormalizePathAndBaseName(t *testing.T) {
	want := filepath.FromSlash("C:/tools/llama.cpp/b11960")
	if got := normalizePath(`C:\tools\llama.cpp/b11960`); got != want {
		t.Errorf("normalizePath mixed = %q, want %q", got, want)
	}
	if got := normalizePath(""); got != "" {
		t.Errorf("normalizePath empty = %q", got)
	}
	if got := baseNameAny(`sub\dir\Model-Q4_K_M.gguf`); got != "Model-Q4_K_M.gguf" {
		t.Errorf("baseNameAny backslash = %q", got)
	}
	if got := baseNameAny("sub/dir/Model-Q4_K_M.gguf"); got != "Model-Q4_K_M.gguf" {
		t.Errorf("baseNameAny slash = %q", got)
	}
}

func TestLoadConfigWindowsPaths(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	wantBin := filepath.FromSlash("C:/tools/llama.cpp")
	wantModels := filepath.FromSlash("D:/models/gguf")

	// Literal (single-quoted) strings: the recommended Windows form.
	p := write("literal.toml", "[paths]\nllama_bin = 'C:\\tools\\llama.cpp'\nmodels_dir = 'D:\\models\\gguf'\n")
	cfg, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Data.Paths.LlamaBin != wantBin || cfg.ModelsDir() != wantModels {
		t.Errorf("literal: llama_bin=%q models_dir=%q", cfg.Data.Paths.LlamaBin, cfg.ModelsDir())
	}

	// Raw single backslashes inside double quotes: invalid TOML as-is,
	// must load via the rescue path and come out normalized.
	p = write("raw.toml", "[paths]\nllama_bin = \"C:\\tools\\llama.cpp\"\nmodels_dir = \"D:\\models\\gguf\"\n")
	cfg, err = loadConfig(p)
	if err != nil {
		t.Fatalf("raw windows path should be rescued: %v", err)
	}
	if cfg.Data.Paths.LlamaBin != wantBin || cfg.ModelsDir() != wantModels {
		t.Errorf("raw: llama_bin=%q models_dir=%q", cfg.Data.Paths.LlamaBin, cfg.ModelsDir())
	}

	// Escaped backslashes in double quotes keep working.
	p = write("escaped.toml", "[paths]\nllama_bin = \"C:\\\\tools\\\\llama.cpp\"\n")
	cfg, err = loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Data.Paths.LlamaBin != wantBin {
		t.Errorf("escaped: llama_bin=%q", cfg.Data.Paths.LlamaBin)
	}
}

func TestLoadConfigSilentTabTrap(t *testing.T) {
	// "C:\tools" alone contains only \t, a *valid* TOML escape, so the
	// file parses without error — into a path containing a TAB. The
	// control-character check must catch it and re-read with rescue.
	dir := t.TempDir()
	p := filepath.Join(dir, "tab.toml")
	if err := os.WriteFile(p, []byte("[paths]\nllama_bin = \"C:\\tools\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.FromSlash("C:/tools")
	if cfg.Data.Paths.LlamaBin != want {
		t.Errorf("llama_bin = %q, want %q (TAB trap not rescued)", cfg.Data.Paths.LlamaBin, want)
	}
}

func TestRescueKeepsValidTomlUntouched(t *testing.T) {
	// A document that parses as-is must not be altered by the rescue:
	// unmarshalTOML reports rescued=false.
	var v struct {
		Paths struct {
			ModelsDir string `toml:"models_dir"`
		} `toml:"paths"`
	}
	rescued, err := unmarshalTOML([]byte("[paths]\nmodels_dir = \"models\"\n"), &v)
	if err != nil || rescued {
		t.Errorf("rescued=%v err=%v, want false/nil", rescued, err)
	}
}

func TestDefaultConfigValues(t *testing.T) {
	cfg := defaultFileConfig()
	if cfg.Bench.PromptTokens != 512 || cfg.Bench.GenTokens != 128 || cfg.Bench.Reps != 3 {
		t.Errorf("bench defaults wrong: %+v", cfg.Bench)
	}
	if cfg.Quality.Port != 8080 || cfg.Quality.MaxTokensFloor != 1024 {
		t.Errorf("quality defaults wrong: %+v", cfg.Quality)
	}
}
