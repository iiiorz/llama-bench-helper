package lbh

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const hfAPI = "https://huggingface.co/api/models/"

type RepoFile struct {
	Path   string `json:"path"`
	Size   *int64 `json:"size"`
	Sha256 string `json:"sha256,omitempty"`

	sizeKnown bool
}

func (f *RepoFile) IsGGUF() bool { return strings.HasSuffix(strings.ToLower(f.Path), ".gguf") }
func (f *RepoFile) IsMmproj() bool {
	return strings.HasPrefix(strings.ToLower(path.Base(f.Path)), "mmproj")
}

// parseRepoInput accepts a full HF URL or a bare 'user/repo' and returns
// (repo_id, file_hint, revision). Handles /tree/<rev>, /blob/<rev>/<file>
// and /resolve/<rev>/<file> forms.
func parseRepoInput(text string) (string, string, string, error) {
	t := strings.TrimSpace(text)
	rev := "main"
	if strings.Contains(t, "huggingface.co") {
		rest := strings.Trim(strings.SplitN(t, "huggingface.co", 2)[1], "/")
		var parts []string
		for _, p := range strings.Split(rest, "/") {
			if p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) < 2 {
			return "", "", "", fmt.Errorf("could not parse a repository from: %s", text)
		}
		repo := parts[0] + "/" + parts[1]
		hint := ""
		if len(parts) >= 4 && (parts[2] == "tree" || parts[2] == "blob" || parts[2] == "resolve") {
			rev = parts[3]
			if parts[2] == "blob" || parts[2] == "resolve" {
				hint = strings.Join(parts[4:], "/")
			}
		}
		return repo, hint, rev, nil
	}
	trimmed := strings.Trim(t, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 {
		return "", "", "", fmt.Errorf("expected 'user/repo' or a huggingface.co URL, got: %s", text)
	}
	return trimmed, "", rev, nil
}

type hfSibling struct {
	Filename string `json:"rfilename"`
	Size     *int64 `json:"size"`
	LFS      *struct {
		Size *int64 `json:"size"`
		Oid  string `json:"oid"`
	} `json:"lfs"`
}

func hfRequest(req *http.Request) {
	req.Header.Set("User-Agent", "llama-bench-helper/"+Version)
	if token := os.Getenv("HF_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func listRepoFiles(repo string) ([]*RepoFile, error) {
	req, err := http.NewRequest(http.MethodGet, hfAPI+repo+"?blobs=true", nil)
	if err != nil {
		return nil, err
	}
	hfRequest(req)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("repository not found: %s", repo)
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("access denied for %s. If it is gated/private, set the HF_TOKEN environment variable", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Hugging Face API returned %s", resp.Status)
	}
	var data struct {
		Siblings []hfSibling `json:"siblings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	var files []*RepoFile
	for _, s := range data.Siblings {
		f := &RepoFile{Path: s.Filename}
		if s.Size != nil {
			f.Size = s.Size
			f.sizeKnown = true
		}
		if s.LFS != nil {
			if s.LFS.Size != nil {
				f.Size = s.LFS.Size
				f.sizeKnown = true
			}
			f.Sha256 = s.LFS.Oid
		}
		files = append(files, f)
	}
	return files, nil
}

func escapePathSegments(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// downloadOne fetches one file natively over HTTPS with resume support
// (.part file + Range requests) and a simple progress display.
func downloadOne(repo, rev string, f *RepoFile, destDir string) error {
	finalPath := filepath.Join(destDir, filepath.FromSlash(f.Path))
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	if f.sizeKnown && isFile(finalPath) {
		if st, err := os.Stat(finalPath); err == nil && st.Size() == *f.Size {
			fmt.Printf("  already present, skipped: %s (%s)\n", f.Path, fmtBytesVal(st.Size()))
			return nil
		}
	}
	partPath := finalPath + ".part"
	var have int64
	if st, err := os.Stat(partPath); err == nil {
		have = st.Size()
	}
	dlURL := fmt.Sprintf("https://huggingface.co/%s/resolve/%s/%s?download=true",
		repo, rev, escapePathSegments(f.Path))
	req, err := http.NewRequest(http.MethodGet, dlURL, nil)
	if err != nil {
		return err
	}
	hfRequest(req)
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	client := &http.Client{} // no total timeout: models can be tens of GiB
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	appendMode := have > 0 && resp.StatusCode == http.StatusPartialContent
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("download of %s failed: %s", f.Path, resp.Status)
	}
	if !appendMode {
		have = 0
	}
	total := have + resp.ContentLength
	if f.sizeKnown {
		total = *f.Size
	}
	flags := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	written := have
	start := time.Now()
	lastReport := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				_ = out.Close()
				return werr
			}
			written += int64(n)
			if time.Since(lastReport) > 2*time.Second {
				elapsed := time.Since(start).Seconds()
				speed := float64(written-have) / elapsed
				if total > 0 {
					fmt.Printf("\r  %s  %.1f%%  %s / %s  (%.1f MiB/s)   ",
						f.Path, float64(written)*100/float64(total),
						fmtBytesVal(written), fmtBytesVal(total), speed/(1024*1024))
				} else {
					fmt.Printf("\r  %s  %s  (%.1f MiB/s)   ", f.Path, fmtBytesVal(written), speed/(1024*1024))
				}
				lastReport = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			_ = out.Close()
			return fmt.Errorf("download of %s interrupted (resume by running again): %w", f.Path, rerr)
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	fmt.Printf("\r  %s  done (%s)                    \n", f.Path, fmtBytesVal(written))
	return os.Rename(partPath, finalPath)
}

type manifestEntry struct {
	Repo         string `json:"repo"`
	File         string `json:"file"`
	Size         int64  `json:"size"`
	Sha256       string `json:"sha256"`
	DownloadedAt string `json:"downloaded_at"`
}

// downloadFiles downloads the selected files natively (no hf CLI needed),
// then verifies sizes, computes SHA256 hashes and updates the manifest.
func downloadFiles(repo, rev string, files []*RepoFile, dest string, assumeYes bool) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	var total int64
	for _, f := range files {
		if f.Size != nil {
			total += *f.Size
		}
	}
	freeStr := "unknown"
	if free, err := diskFreeBytes(dest); err == nil {
		freeStr = fmtBytesVal(int64(free))
		if total > 0 && free < uint64(total) {
			fmt.Println("WARNING: not enough free space for the selected files.")
			if !confirm("Continue anyway?", false, assumeYes) {
				return fmt.Errorf("aborted by user")
			}
		}
	}
	fmt.Printf("Repository : %s\n", repo)
	fmt.Printf("Files      : %d (%s)\n", len(files), fmtBytesVal(total))
	fmt.Printf("Destination: %s (free: %s)\n", dest, freeStr)

	for _, f := range files {
		if err := downloadOne(repo, rev, f, dest); err != nil {
			return err
		}
	}

	manifestPath := filepath.Join(dest, ".lbh-manifest.json")
	var manifest []manifestEntry
	if isFile(manifestPath) {
		_ = readJSONFile(manifestPath, &manifest)
	}
	now := time.Now().Format(time.RFC3339)
	for _, f := range files {
		p := filepath.Join(dest, filepath.FromSlash(f.Path))
		if !isFile(p) {
			fmt.Printf("  WARNING: not found after download: %s\n", f.Path)
			continue
		}
		st, _ := os.Stat(p)
		actual := st.Size()
		ok := f.Size == nil || actual == *f.Size
		mark := "OK "
		if !ok {
			mark = "SIZE MISMATCH "
		}
		fmt.Printf("  %s %s (%s)\n", mark, f.Path, fmtBytesVal(actual))
		digest, derr := sha256File(p)
		if derr != nil {
			return derr
		}
		if f.Sha256 != "" && digest != f.Sha256 {
			fmt.Printf("  WARNING: sha256 differs from the repository record for %s\n", f.Path)
		}
		kept := manifest[:0]
		for _, m := range manifest {
			if !(m.Repo == repo && m.File == f.Path) {
				kept = append(kept, m)
			}
		}
		manifest = append(kept, manifestEntry{
			Repo: repo, File: f.Path, Size: actual,
			Sha256: digest, DownloadedAt: now,
		})
	}
	if err := writeJSON(manifestPath, manifest); err != nil {
		return err
	}
	fmt.Printf("Manifest updated: %s\n", manifestPath)
	return nil
}
