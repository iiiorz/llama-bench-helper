package lbh

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func nvidiaSmi() string { return findExecutable("nvidia-smi", "") }

func totalVramMiB() *int64 {
	smi := nvidiaSmi()
	if smi == "" {
		return nil
	}
	out, err := exec.Command(smi, "--query-gpu=memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		return nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(lines[0]), 64)
	if err != nil {
		return nil
	}
	iv := int64(v)
	return &iv
}

func gpuName() string {
	smi := nvidiaSmi()
	if smi == "" {
		return ""
	}
	out, err := exec.Command(smi, "--query-gpu=name", "--format=csv,noheader").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

// VramLogger polls nvidia-smi once per second and appends each sample
// immediately, so records survive even if the run is killed mid-way.
type VramLogger struct {
	file     string
	interval time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}
	mu       sync.Mutex
	peakMiB  *float64
}

func NewVramLogger(file string) *VramLogger {
	return &VramLogger{file: file, interval: time.Second,
		stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

func (l *VramLogger) loop() {
	defer close(l.doneCh)
	smi := nvidiaSmi()
	if smi == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(l.file), 0o755)
	f, err := os.OpenFile(l.file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	for {
		select {
		case <-l.stopCh:
			return
		default:
		}
		out, err := exec.Command(smi, "--query-gpu=memory.used", "--format=csv,noheader,nounits").Output()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) > 0 {
				if v, perr := strconv.ParseFloat(strings.TrimSpace(lines[0]), 64); perr == nil {
					fmt.Fprintf(f, "%.0f\n", v)
					_ = f.Sync()
					l.mu.Lock()
					if l.peakMiB == nil || v > *l.peakMiB {
						vv := v
						l.peakMiB = &vv
					}
					l.mu.Unlock()
				}
			}
		}
		select {
		case <-l.stopCh:
			return
		case <-time.After(l.interval):
		}
	}
}

func (l *VramLogger) Start() {
	go l.loop()
	time.Sleep(200 * time.Millisecond)
}

func (l *VramLogger) Stop() *float64 {
	close(l.stopCh)
	select {
	case <-l.doneCh:
	case <-time.After(5 * time.Second):
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.peakMiB == nil && isFile(l.file) {
		if b, err := os.ReadFile(l.file); err == nil {
			var max *float64
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if v, err := strconv.ParseFloat(line, 64); err == nil {
					if max == nil || v > *max {
						vv := v
						max = &vv
					}
				}
			}
			l.peakMiB = max
		}
	}
	return l.peakMiB
}

// Rough per-token KV cache cost by KV quant (MiB/token). Deliberately
// conservative; the estimate is a warning aid, not a precise prediction.
var kvMiBPerToken = map[string]float64{"f16": 0.12, "bf16": 0.12, "q8_0": 0.06, "q4_0": 0.03}

const overheadMiB = 1024 // compute buffers, CUDA context, misc

func estimateVramMiB(modelSizeBytes int64, context int, kvQuant string) float64 {
	per, ok := kvMiBPerToken[strings.ToLower(kvQuant)]
	if !ok {
		per = 0.06
	}
	return float64(modelSizeBytes)/(1024*1024) + overheadMiB + float64(context)*per
}

func vramWarning(modelSizeBytes int64, context int, kvQuant string) string {
	total := totalVramMiB()
	if total == nil {
		return ""
	}
	est := estimateVramMiB(modelSizeBytes, context, kvQuant)
	if est <= float64(*total) {
		return ""
	}
	return fmt.Sprintf(
		"estimated VRAM need ~%.0f MiB exceeds GPU total %d MiB "+
			"(rough estimate: file size + %d MiB overhead + KV cache at %d ctx). "+
			"It may still appear to run via shared system memory (WDDM) at much lower speed; "+
			"consider partial offload options (e.g. -ncmoe / lower -ngl).",
		est, *total, overheadMiB, context)
}
