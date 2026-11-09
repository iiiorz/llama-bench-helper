package lbh

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Prompt is one entry of a prompt set.
type Prompt struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Kind        string     `json:"kind"`
	Text        string     `json:"text"`
	Temperature *float64   `json:"temperature"`
	MaxTokens   *int       `json:"max_tokens"`
	Checks      []CheckDef `json:"checks"`
}

func (p *Prompt) MaxTokensOr(def int) int {
	if p.MaxTokens == nil {
		return def
	}
	return *p.MaxTokens
}

var kindByLegacy = map[string]string{
	"p1_summary": "text", "p2_code": "text", "p3_logic": "text",
	"p4_long": "text", "default_sample": "default", "image_prompt": "image",
}

// loadPromptSet loads a prompt set JSON (tool format or the legacy dict
// format), falling back to the bundled default set.
func loadPromptSet(cfg *Config, explicit string) ([]Prompt, string, error) {
	p := explicit
	if p == "" {
		p = cfg.Data.Quality.PromptSet
	}
	if p != "" && isFile(p) {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, "", err
		}
		prompts, err := parsePromptSet(b)
		return prompts, p, err
	}
	prompts, err := parsePromptSet(defaultPromptsJSON)
	return prompts, "(bundled default prompt set)", err
}

func parsePromptSet(b []byte) ([]Prompt, error) {
	var head struct {
		Prompts []Prompt `json:"prompts"`
	}
	if err := json.Unmarshal(b, &head); err == nil && head.Prompts != nil {
		return head.Prompts, nil
	}
	// Legacy dict format: {p1_summary: {text: ...}, ...}
	var legacy map[string]struct {
		Title       string     `json:"title"`
		Text        *string    `json:"text"`
		Temperature *float64   `json:"temperature"`
		MaxTokens   *int       `json:"max_tokens"`
		Checks      []CheckDef `json:"checks"`
	}
	if err := json.Unmarshal(b, &legacy); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(legacy))
	for k := range legacy {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var prompts []Prompt
	for _, k := range keys {
		e := legacy[k]
		if e.Text == nil {
			continue
		}
		kind, ok := kindByLegacy[k]
		if !ok {
			kind = "text"
		}
		title := e.Title
		if title == "" {
			title = k
		}
		mt := e.MaxTokens
		if mt == nil {
			v := 1024
			mt = &v
		}
		temp := e.Temperature
		if temp == nil {
			v := 0.0
			temp = &v
		}
		prompts = append(prompts, Prompt{
			ID: k, Title: title, Kind: kind, Text: *e.Text,
			Temperature: temp, MaxTokens: mt, Checks: e.Checks,
		})
	}
	return prompts, nil
}

// ---------------------------------------------------------------------------
// llama-server management
// ---------------------------------------------------------------------------

type server struct {
	exe    string
	args   []string
	outDir string
	port   int
	cmd    *exec.Cmd
	waitCh chan error
	exited bool
}

func newServer(exe string, args []string, outDir string, port int) *server {
	return &server{exe: exe, args: args, outDir: outDir, port: port,
		waitCh: make(chan error, 1)}
}

func (s *server) start() (float64, error) {
	if err := os.MkdirAll(s.outDir, 0o755); err != nil {
		return 0, err
	}
	stdout, err := os.Create(filepath.Join(s.outDir, "server.stdout.log"))
	if err != nil {
		return 0, err
	}
	stderr, err := os.Create(filepath.Join(s.outDir, "server.stderr.log"))
	if err != nil {
		_ = stdout.Close()
		return 0, err
	}
	cmdArgs := append([]string{s.exe}, s.args...)
	fmt.Println("  $ " + strings.Join(cmdArgs, " "))
	s.cmd = exec.Command(s.exe, s.args...)
	s.cmd.Stdout = stdout
	s.cmd.Stderr = stderr
	t0 := time.Now()
	if err := s.cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return 0, err
	}
	go func() {
		s.waitCh <- s.cmd.Wait()
		_ = stdout.Close()
		_ = stderr.Close()
	}()
	deadline := t0.Add(600 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case err := <-s.waitCh:
			s.exited = true
			code := -1
			if s.cmd.ProcessState != nil {
				code = s.cmd.ProcessState.ExitCode()
			}
			_ = err
			return 0, fmt.Errorf("llama-server exited early (code %d); see %s/server.stderr.log", code, s.outDir)
		default:
		}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", s.port))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return time.Since(t0).Seconds(), nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0, fmt.Errorf("llama-server did not become ready within 600 s")
}

func (s *server) stop() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	if !s.exited {
		select {
		case <-s.waitCh:
			s.exited = true
		default:
		}
	}
	if !s.exited {
		_ = terminateProcess(s.cmd.Process)
		select {
		case <-s.waitCh:
		case <-time.After(10 * time.Second):
			_ = s.cmd.Process.Kill()
			<-s.waitCh
		}
	}
	time.Sleep(1 * time.Second)
}

// ---------------------------------------------------------------------------
// Chat API
// ---------------------------------------------------------------------------

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatPayload struct {
	Model       string        `json:"model"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature *float64      `json:"temperature,omitempty"`
	Messages    []chatMessage `json:"messages"`
}

func chat(port int, payload chatPayload) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	client := &http.Client{Timeout: 1200 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llama-server returned %s", resp.Status)
	}
	var data struct {
		Choices []struct {
			Message struct {
				Content          *string `json:"content"`
				ReasoningContent *string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if len(data.Choices) == 0 {
		return "", nil
	}
	msg := data.Choices[0].Message
	if msg.Content != nil && *msg.Content != "" {
		return *msg.Content, nil
	}
	if msg.ReasoningContent != nil {
		return *msg.ReasoningContent, nil
	}
	return "", nil
}

func saveExchange(path, title, prompt, response string) error {
	return os.WriteFile(path,
		[]byte(fmt.Sprintf("# %s\n\n## Prompt\n%s\n\n## Response\n%s\n", title, prompt, response)),
		0o644)
}

var codeBlockRe = regexp.MustCompile("(?s)```(?:python)?\n(.*?)```")

func codeExecCheck(text string) map[string]any {
	blocks := codeBlockRe.FindAllStringSubmatch(text, -1)
	if len(blocks) == 0 {
		return map[string]any{"ran": false, "detail": "no code block found"}
	}
	var parts []string
	for _, b := range blocks {
		parts = append(parts, b[1])
	}
	code := strings.Join(parts, "\n\n")
	py := findExecutable("python3", "")
	if py == "" {
		py = findExecutable("python", "")
	}
	if py == "" {
		return map[string]any{"ran": false, "detail": "no python interpreter found"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, "-c", code)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return map[string]any{"ran": true, "ok": false, "detail": "timed out after 10 s"}
	}
	detail := strings.TrimSpace(errBuf.String())
	if detail == "" {
		detail = strings.TrimSpace(outBuf.String())
	}
	if len([]rune(detail)) > 300 {
		r := []rune(detail)
		detail = string(r[len(r)-300:])
	}
	return map[string]any{"ran": true, "ok": err == nil, "detail": detail}
}

// ---------------------------------------------------------------------------
// Quality run
// ---------------------------------------------------------------------------

func runQuality(cfg *Config, models []*ModelEntry, promptsFile, imagePath string, assumeYes bool) (string, error) {
	q := cfg.Data.Quality
	port, ctx, floor := q.Port, q.Context, q.MaxTokensFloor
	prompts, usedPath, err := loadPromptSet(cfg, promptsFile)
	if err != nil {
		return "", err
	}
	fmt.Printf("Prompt set: %s (%d prompts)\n", usedPath, len(prompts))
	var small []string
	for _, p := range prompts {
		if p.MaxTokensOr(0) < floor {
			small = append(small, p.ID)
		}
	}
	if len(small) > 0 {
		fmt.Printf("WARNING: max_tokens below the floor (%d) for: %s. "+
			"Thinking models may be cut off before finishing their answer.\n",
			floor, strings.Join(small, ", "))
	}
	img := imagePath
	if img == "" {
		img = q.ImagePath
	}
	serverExe := findExecutable("llama-server", cfg.Data.Paths.LlamaBin)
	if serverExe == "" {
		return "", fmt.Errorf("llama-server not found. Set paths.llama_bin in config.toml or add it to PATH")
	}

	runDir := filepath.Join(cfg.ResultsDir(), nowStamp()+"-quality")
	qroot := filepath.Join(runDir, "quality")
	if err := os.MkdirAll(qroot, 0o755); err != nil {
		return "", err
	}
	fmt.Printf("Run folder: %s\n", runDir)

	var promptIDs []string
	for _, p := range prompts {
		promptIDs = append(promptIDs, p.ID)
	}
	type qrow struct {
		model, status, load, peak string
		perPrompt                 map[string]string
	}
	var rows []qrow
	allChecks := map[string]map[string][]CheckResult{}

	hasImagePrompt := false
	for _, p := range prompts {
		if p.Kind == "image" {
			hasImagePrompt = true
		}
	}

	for _, m := range models {
		fmt.Printf("\n== %s ==\n", m.File)
		outDir := filepath.Join(qroot, m.Key())
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return "", err
		}
		row := qrow{model: m.File, status: "OK", perPrompt: map[string]string{}}
		for _, pid := range promptIDs {
			row.perPrompt[pid] = ""
		}
		args := []string{"-m", m.Path, "-ngl", "99", "-c", fmt.Sprint(ctx),
			"-fa", "on", "-ctk", "q8_0", "-ctv", "q8_0",
			"--host", "127.0.0.1", "--port", fmt.Sprint(port)}
		args = append(args, m.ExtraServerArgs...)
		if m.Vision && m.MmprojPath != "" && hasImagePrompt {
			args = append(args, "--mmproj", m.MmprojPath)
		}
		srv := newServer(serverExe, args, outDir, port)
		logger := NewVramLogger(filepath.Join(outDir, "vram-server.csv"))
		logger.Start()
		func() {
			defer func() {
				peak := logger.Stop()
				if peak != nil {
					row.peak = fmt.Sprint(int64(*peak))
				}
				srv.stop()
			}()
			loadSec, serr := srv.start()
			if serr != nil {
				row.status = "SERVER FAILED: " + serr.Error()
				fmt.Printf("  %s\n", row.status)
				return
			}
			row.load = fmt.Sprintf("%.1f", loadSec)
			fmt.Printf("  server ready in %.1f s\n", loadSec)
			modelChecks := map[string][]CheckResult{}
			for _, p := range prompts {
				pid := p.ID
				kind := p.Kind
				if kind == "" {
					kind = "text"
				}
				if kind == "image" {
					if !m.Vision {
						row.perPrompt[pid] = "SKIPPED (model has no mmproj)"
						continue
					}
					if img == "" {
						row.perPrompt[pid] = "SKIPPED (no image given)"
						continue
					}
				}
				payload := chatPayload{Model: "local", MaxTokens: p.MaxTokensOr(1024)}
				if kind != "default" && p.Temperature != nil {
					payload.Temperature = p.Temperature
				}
				var promptLabel string
				if kind == "image" {
					ext := strings.ToLower(filepath.Ext(img))
					mime := map[string]string{
						".jpg": "image/jpeg", ".jpeg": "image/jpeg",
						".webp": "image/webp", ".bmp": "image/bmp",
					}[ext]
					if mime == "" {
						mime = "image/png"
					}
					imgBytes, rerr := os.ReadFile(img)
					if rerr != nil {
						row.perPrompt[pid] = "FAILED: " + rerr.Error()
						continue
					}
					b64 := base64.StdEncoding.EncodeToString(imgBytes)
					content := []map[string]any{
						{"type": "text", "text": p.Text},
						{"type": "image_url", "image_url": map[string]any{
							"url": fmt.Sprintf("data:%s;base64,%s", mime, b64)}},
					}
					payload.Messages = []chatMessage{{Role: "user", Content: content}}
					promptLabel = fmt.Sprintf("(image: %s) %s", img, p.Text)
				} else {
					payload.Messages = []chatMessage{{Role: "user", Content: p.Text}}
					promptLabel = p.Text
				}
				resp, cerr := chat(port, payload)
				if cerr != nil {
					row.perPrompt[pid] = "FAILED: " + cerr.Error()
					fmt.Printf("  %s: FAILED: %v\n", pid, cerr)
					continue
				}
				title := p.Title
				if title == "" {
					title = pid
				}
				_ = saveExchange(filepath.Join(outDir, fmt.Sprintf("%s-%s.txt", m.Key(), pid)),
					title, promptLabel, resp)
				results := runChecks(resp, p.Checks)
				modelChecks[pid] = results
				note := summarizeChecks(results)
				if kind == "text" && q.CodeExecCheck && strings.Contains(resp, "```") {
					ex := codeExecCheck(resp)
					okEx, _ := ex["ok"].(bool)
					detailEx, _ := ex["detail"].(string)
					modelChecks[pid] = append(append([]CheckResult{}, results...), CheckResult{
						Type: "code_exec", OK: okEx, Detail: detailEx, Actual: ex["ran"],
					})
					if okEx {
						note += "; code exec: OK"
					} else {
						note += "; code exec: FAILED"
					}
				}
				row.perPrompt[pid] = fmt.Sprintf("OK (%d chars; %s)", len([]rune(resp)), note)
				fmt.Printf("  %s: %s\n", pid, row.perPrompt[pid])
			}
			allChecks[m.Key()] = modelChecks
		}()
		rows = append(rows, row)
	}

	summary := filepath.Join(runDir, "quality-summary.csv")
	f, err := os.Create(summary)
	if err != nil {
		return "", err
	}
	w := csv.NewWriter(f)
	fields := append([]string{"model", "status", "load_time_sec", "vram_peak_mib"}, promptIDs...)
	_ = w.Write(fields)
	for _, r := range rows {
		rec := []string{r.model, r.status, r.load, r.peak}
		for _, pid := range promptIDs {
			rec = append(rec, r.perPrompt[pid])
		}
		_ = w.Write(rec)
	}
	w.Flush()
	_ = f.Close()
	if err := writeJSON(filepath.Join(runDir, "checks.json"), allChecks); err != nil {
		return "", err
	}
	var promptMeta []map[string]any
	for _, p := range prompts {
		promptMeta = append(promptMeta, map[string]any{
			"id": p.ID, "title": p.Title, "kind": p.Kind, "max_tokens": p.MaxTokensOr(1024),
		})
	}
	if err := writeJSON(filepath.Join(runDir, "run-metadata.json"), map[string]any{
		"kind": "quality", "prompt_set": usedPath, "port": port, "context": ctx,
		"prompts": promptMeta,
	}); err != nil {
		return "", err
	}
	fmt.Printf("\nSummary: %s\n", summary)
	return runDir, nil
}

// ---------------------------------------------------------------------------
// AI evaluation export
// ---------------------------------------------------------------------------

const evalInstructions = `# ローカルLLM 回答品質の評価依頼

あなたはローカルLLMの回答品質を評価する審査員です。以下に、複数のモデルへ同じプロンプトを投げた結果をまとめています。

## 評価の進め方

- プロンプトごとに「評価観点」が付いています。その観点と、正確さ・指示への従い方・文章の自然さから、各モデルの回答を 5 段階（5=最良、1=最低）で評価してください。
- 「機械チェック結果」が併記されている場合は、事実として扱ってください（例: 文字数や指定語の出現回数は数え直す必要はありません）。
- 回答が途中で切れている、思考過程だけが表示されている、といった不完全な出力は、その旨を指摘したうえで低く評価してください。

## 出力形式

最後に、結果を次の形式の Markdown 表でまとめてください。

| モデル | プロンプト | 正確さ | 指示追従 | 自然さ | コメント |
|---|---|---:|---:|---:|---|

その後に、モデルごとの総評（強み・弱みを 2〜3 文）と、用途別のおすすめを書いてください。
`

func checkLines(results []CheckResult) string {
	if len(results) == 0 {
		return "（機械チェックなし）"
	}
	var lines []string
	for _, r := range results {
		mark := "NG"
		if r.OK {
			mark = "OK"
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", mark, r.Type, r.Detail))
	}
	return strings.Join(lines, "\n")
}

// exportAIEval builds ai-eval-bundle.md from a quality run folder.
// Returns (bundlePath, mappingPath, error); mappingPath is "" unless
// anonymize is set. Note: the anonymized label order uses a fixed seed
// but does not reproduce Python's shuffle order exactly.
func exportAIEval(runDir string, anonymize bool) (string, string, error) {
	qroot := filepath.Join(runDir, "quality")
	var checksAll map[string]map[string][]CheckResult
	if isFile(filepath.Join(runDir, "checks.json")) {
		checksAll = map[string]map[string][]CheckResult{}
		_ = readJSONFile(filepath.Join(runDir, "checks.json"), &checksAll)
	}

	var modelKeys []string
	if isDir(qroot) {
		des, _ := os.ReadDir(qroot)
		for _, de := range des {
			if de.IsDir() {
				modelKeys = append(modelKeys, de.Name())
			}
		}
		sort.Strings(modelKeys)
	}
	var promptIDs []string
	promptsByID := map[string]struct{ title, text string }{}
	responses := map[string]map[string]string{}
	for _, mk := range modelKeys {
		responses[mk] = map[string]string{}
		dir := filepath.Join(qroot, mk)
		des, _ := os.ReadDir(dir)
		var names []string
		for _, de := range des {
			if strings.HasPrefix(de.Name(), mk+"-") && strings.HasSuffix(de.Name(), ".txt") {
				names = append(names, de.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			pid := strings.TrimSuffix(name[len(mk)+1:], ".txt")
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			text := string(b)
			parts := strings.SplitN(text, "## Response", 2)
			head := parts[0]
			promptPart := ""
			if idx := strings.Index(head, "## Prompt"); idx >= 0 {
				promptPart = strings.TrimSpace(head[idx+len("## Prompt"):])
			}
			title := pid
			if lines := strings.Split(head, "\n"); len(lines) > 0 {
				title = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "#"))
				if title == "" {
					title = pid
				}
			}
			resp := ""
			if len(parts) > 1 {
				resp = strings.TrimSpace(parts[1])
			}
			responses[mk][pid] = resp
			if _, ok := promptsByID[pid]; !ok {
				promptIDs = append(promptIDs, pid)
				promptsByID[pid] = struct{ title, text string }{title, promptPart}
			}
		}
	}

	labelOf := map[string]string{}
	var mapping map[string]string
	if anonymize {
		shuffled := append([]string(nil), modelKeys...)
		rand.New(rand.NewSource(20261104)).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		mapping = map[string]string{}
		for i, mk := range shuffled {
			label := fmt.Sprintf("Model %c", 'A'+i)
			mapping[label] = mk
			labelOf[mk] = label
		}
	} else {
		for _, mk := range modelKeys {
			labelOf[mk] = mk
		}
	}

	out := []string{evalInstructions}
	for _, pid := range promptIDs {
		info := promptsByID[pid]
		out = append(out, fmt.Sprintf("\n## プロンプト: %s（%s）\n", info.title, pid))
		out = append(out, "### プロンプト文\n", info.text)
		out = append(out, "\n### 評価観点\n",
			"- 正確さ: 内容が正しく、求められた情報を落としていないか",
			"- 指示追従: 形式や制約（文数・文字数・構成など）を守れているか",
			"- 自然さ: 文章として自然で読みやすいか")
		for _, mk := range modelKeys {
			resp, ok := responses[mk][pid]
			if !ok {
				continue
			}
			out = append(out, fmt.Sprintf("\n### %s の回答\n", labelOf[mk]),
				"```", resp, "```", "\n機械チェック結果:")
			var rs []CheckResult
			if checksAll != nil {
				rs = checksAll[mk][pid]
			}
			out = append(out, checkLines(rs))
		}
	}
	bundle := filepath.Join(runDir, "ai-eval-bundle.md")
	if err := os.WriteFile(bundle, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		return "", "", err
	}
	mappingPath := ""
	if mapping != nil {
		mappingPath = filepath.Join(runDir, "ai-eval-mapping.json")
		if err := writeJSON(mappingPath, mapping); err != nil {
			return "", "", err
		}
	}
	return bundle, mappingPath, nil
}
