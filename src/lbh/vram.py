"""VRAM helpers: total VRAM query, polling logger, rough usage estimate."""

from __future__ import annotations

import subprocess
import threading
import time
from pathlib import Path


def _nvidia_smi() -> str | None:
    from .util import find_executable

    p = find_executable("nvidia-smi")
    return str(p) if p else None


def total_vram_mib() -> int | None:
    smi = _nvidia_smi()
    if not smi:
        return None
    try:
        out = subprocess.run(
            [smi, "--query-gpu=memory.total", "--format=csv,noheader,nounits"],
            capture_output=True, text=True, timeout=15,
        )
        first = out.stdout.strip().splitlines()[0].strip()
        return int(float(first))
    except Exception:
        return None


def gpu_name() -> str | None:
    smi = _nvidia_smi()
    if not smi:
        return None
    try:
        out = subprocess.run(
            [smi, "--query-gpu=name", "--format=csv,noheader"],
            capture_output=True, text=True, timeout=15,
        )
        return out.stdout.strip().splitlines()[0].strip()
    except Exception:
        return None


class VramLogger:
    """Poll nvidia-smi once per second and append each sample immediately.

    The append-as-you-go design is deliberate: loggers that buffer in the
    child process lose everything when the process is killed.
    """

    def __init__(self, file: Path, interval: float = 1.0):
        self.file = Path(file)
        self.interval = interval
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None
        self.peak_mib: float | None = None

    def _loop(self) -> None:
        smi = _nvidia_smi()
        if not smi:
            return
        self.file.parent.mkdir(parents=True, exist_ok=True)
        with open(self.file, "a", encoding="utf-8") as f:
            while not self._stop.is_set():
                try:
                    out = subprocess.run(
                        [smi, "--query-gpu=memory.used", "--format=csv,noheader,nounits"],
                        capture_output=True, text=True, timeout=10,
                    )
                    v = out.stdout.strip().splitlines()[0].strip()
                    val = float(v)
                    f.write(f"{val:.0f}\n")
                    f.flush()
                    if self.peak_mib is None or val > self.peak_mib:
                        self.peak_mib = val
                except Exception:
                    pass
                self._stop.wait(self.interval)

    def start(self) -> None:
        self._thread = threading.Thread(target=self._loop, daemon=True)
        self._thread.start()
        time.sleep(0.2)

    def stop(self) -> float | None:
        self._stop.set()
        if self._thread:
            self._thread.join(timeout=5)
        if self.peak_mib is None and self.file.is_file():
            vals: list[float] = []
            for line in self.file.read_text(encoding="utf-8").splitlines():
                line = line.strip()
                if line:
                    try:
                        vals.append(float(line))
                    except ValueError:
                        pass
            if vals:
                self.peak_mib = max(vals)
        return self.peak_mib


# Rough per-token KV cache cost by KV quant (MiB/token). Deliberately
# conservative; the estimate is a warning aid, not a precise prediction.
_KV_MIB_PER_TOKEN = {"f16": 0.12, "bf16": 0.12, "q8_0": 0.06, "q4_0": 0.03}
_OVERHEAD_MIB = 1024  # compute buffers, CUDA context, misc


def estimate_vram_mib(model_size_bytes: int, context: int, kv_quant: str) -> float:
    per = _KV_MIB_PER_TOKEN.get(str(kv_quant).lower(), 0.06)
    return model_size_bytes / (1024 * 1024) + _OVERHEAD_MIB + context * per


def vram_warning(model_size_bytes: int, context: int, kv_quant: str) -> str | None:
    """Return a warning string when the estimate exceeds total VRAM."""
    total = total_vram_mib()
    if total is None:
        return None
    est = estimate_vram_mib(model_size_bytes, context, kv_quant)
    if est <= total:
        return None
    return (
        f"estimated VRAM need ~{est:,.0f} MiB exceeds GPU total {total:,} MiB "
        f"(rough estimate: file size + {_OVERHEAD_MIB} MiB overhead + KV cache at {context} ctx). "
        "It may still appear to run via shared system memory (WDDM) at much lower speed; "
        "consider partial offload options (e.g. -ncmoe / lower -ngl)."
    )
