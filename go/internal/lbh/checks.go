package lbh

import (
	"fmt"
	"regexp"
	"strings"
)

// CheckDef is one mechanical check definition from a prompt set.
type CheckDef struct {
	Type    string `json:"type"`
	Value   any    `json:"value"`
	Needle  string `json:"needle"`
	Section string `json:"section"`
	Pattern string `json:"pattern"`
}

// CheckResult is the outcome of one mechanical check.
type CheckResult struct {
	Type   string `json:"type"`
	Needle string `json:"needle"`
	Value  any    `json:"value"`
	Actual any    `json:"actual"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func bulletCount(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") || strings.HasPrefix(s, "・") {
			n++
		}
	}
	return n
}

// sectionText returns the text from the given heading line to the next
// '## ' heading (or end of text).
func sectionText(text, heading string) string {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == heading {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	var out []string
	for _, line := range lines[start+1:] {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "## ") {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func valueAsInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

// runChecks evaluates check definitions against a response text.
// Supported types: max_chars, min_chars, sentence_max (count of '。'),
// count_equals / count_max, contains, not_contains, bullets_exactly,
// regex_count (pattern occurrences == value).
// Character counts are Unicode code points, matching Python's len().
func runChecks(text string, checks []CheckDef) []CheckResult {
	var results []CheckResult
	runes := []rune(text)
	for _, c := range checks {
		var actual any
		ok := true
		detail := ""
		switch c.Type {
		case "max_chars":
			actual = len(runes)
			ok = len(runes) <= valueAsInt(c.Value)
			detail = fmt.Sprintf("%d chars (limit %v)", len(runes), c.Value)
		case "min_chars":
			actual = len(runes)
			ok = len(runes) >= valueAsInt(c.Value)
			detail = fmt.Sprintf("%d chars (minimum %v)", len(runes), c.Value)
		case "sentence_max":
			n := strings.Count(text, "。")
			actual = n
			ok = n <= valueAsInt(c.Value)
			detail = fmt.Sprintf("%d sentences (limit %v)", n, c.Value)
		case "count_equals":
			n := strings.Count(text, c.Needle)
			actual = n
			ok = n == valueAsInt(c.Value)
			detail = fmt.Sprintf("'%s' x%d (expected %v)", c.Needle, n, c.Value)
		case "count_max":
			n := strings.Count(text, c.Needle)
			actual = n
			ok = n <= valueAsInt(c.Value)
			detail = fmt.Sprintf("'%s' x%d (limit %v)", c.Needle, n, c.Value)
		case "contains":
			has := strings.Contains(text, c.Needle)
			actual = has
			ok = has
			detail = fmt.Sprintf("contains '%s': %v", c.Needle, has)
		case "not_contains":
			has := strings.Contains(text, c.Needle)
			actual = has
			ok = !has
			detail = fmt.Sprintf("absence of '%s': %v", c.Needle, !has)
		case "bullets_exactly":
			scope := text
			if c.Section != "" {
				scope = sectionText(text, c.Section)
			}
			n := 0
			if scope != "" {
				n = bulletCount(scope)
			}
			actual = n
			ok = n == valueAsInt(c.Value)
			detail = fmt.Sprintf("%d bullet lines (expected %v)", n, c.Value)
		case "regex_count":
			re, err := regexp.Compile(c.Pattern)
			if err != nil {
				ok = false
				detail = fmt.Sprintf("invalid pattern: %v", err)
				break
			}
			n := len(re.FindAllString(text, -1))
			actual = n
			ok = n == valueAsInt(c.Value)
			detail = fmt.Sprintf("pattern matches x%d (expected %v)", n, c.Value)
		default:
			ok = false
			detail = fmt.Sprintf("unknown check type: %s", c.Type)
		}
		results = append(results, CheckResult{
			Type: c.Type, Needle: c.Needle, Value: c.Value,
			Actual: actual, OK: ok, Detail: detail,
		})
	}
	return results
}

func summarizeChecks(results []CheckResult) string {
	if len(results) == 0 {
		return "no checks"
	}
	passed := 0
	for _, r := range results {
		if r.OK {
			passed++
		}
	}
	return fmt.Sprintf("%d/%d checks passed", passed, len(results))
}
