# llama-bench-helper (lbh)

English | [日本語](README.md)

A benchmark helper CLI for llama.cpp. It runs the whole workflow — downloading models, measuring speed, and checking output quality — with preset conditions, and records the results in a consistent format.

## What it does

1. **Model download** (`lbh download`)
   - Paste a Hugging Face URL to see the file list with sizes, then download the selected files with `hf download`
   - Warns when a repository contains no GGUF files ("not usable with llama.cpp as-is")
   - If the repository has an mmproj (vision) file, offers to download it together; after downloading, file sizes are verified and SHA256 hashes are recorded in a manifest
   - If the `hf` CLI is missing, detects it and offers to install it
2. **Benchmark** (`lbh bench`)
   - Runs llama-bench across multiple models in sequence. Prompt/generation token counts, repetitions, `-ngl`, context size and more are configurable
   - Per-model extra arguments (e.g. `-ncmoe`) are applied automatically from models.toml
   - Warns before a run when the estimated VRAM usage is likely to exceed the GPU total
   - Records VRAM usage every second during the run, and aggregates results as the median of `samples_ts` (each run is saved in its own timestamped folder)
3. **Quality checks** (`lbh quality`)
   - Runs a shared prompt set against every model under identical conditions (the bundled default set covers a strict-format summary, code generation, a logic puzzle, a constrained long-form text, a default-temperature sample, and image reading)
   - Prompt sets are editable JSON. You can define **mechanical checks** (character limits, keyword occurrence counts, and more) that are graded automatically
   - Image prompts run only for models with a paired mmproj file
   - At the end, an **AI evaluation bundle** (`ai-eval-bundle.md`) is generated automatically: evaluator instructions, per-prompt criteria, every model's responses, and the mechanical check results in a single file — paste it into ChatGPT or Claude and get scores back (with an anonymization option)

## Requirements

- Python 3.11 or later
- A llama.cpp build (`llama-bench` / `llama-server`)
- Hugging Face CLI (`hf`, for the download feature)
- NVIDIA GPU + `nvidia-smi` (for VRAM recording; benchmarks still run without it)
- Windows / Linux / macOS

## Installation

```powershell
git clone https://github.com/iiiorz/llama-bench-helper.git
cd llama-bench-helper
pip install .
lbh init   # creates config.toml and models.toml templates
```

Every item in `config.toml` is documented with comments. Start by setting `llama_bin` (the folder containing the llama.cpp binaries) and `models_dir` under `[paths]` to match your environment.

## Usage

Running `lbh` with no arguments opens an interactive menu. Subcommands work directly too.

```powershell
# Download models (paste a URL, then pick from the list)
lbh download https://huggingface.co/Qwen/Qwen3-14B-GGUF

# List detected models
lbh models

# Benchmark (multiple models, comma separated)
lbh bench --models Qwen3-14B-Q4_K_M,gpt-oss-20b-MXFP4

# Quality checks (--image is the test image; used only for VL models)
lbh quality --models Qwen3-14B-Q4_K_M --image samples/vl-test-image.png

# Rebuild the AI evaluation bundle (anonymized; created in the latest quality run folder)
lbh export --anonymize

# Generate a Markdown report from the latest run (created in the latest quality run folder)
lbh report
```

## Configuration files

- `config.toml` — global settings: paths, default benchmark conditions, the quality-check port, the `max_tokens` warning floor, and more
- `models.toml` — optional per-model differences only: mmproj pairing, partial-offload arguments, and built-in presets for known models (e.g. `--jinja` is added automatically for gpt-oss)

See `config.example.toml` / `models.example.toml` for templates; every item is explained in comments (currently in Japanese — the keys and values are language-neutral).

## Prompt set format

```json
{
  "name": "my prompts",
  "prompts": [
    {
      "id": "p1", "title": "Summary", "kind": "text",
      "text": "Summarize the following in at most 3 sentences and 200 characters...",
      "temperature": 0, "max_tokens": 2048,
      "checks": [
        {"type": "max_chars", "value": 200},
        {"type": "sentence_max", "value": 3}
      ]
    }
  ]
}
```

- `kind` is `text` (temperature is sent) / `default` (temperature is not sent) / `image` (with an image; runs only for models with an mmproj)
- Mechanical check types: `max_chars`, `min_chars`, `sentence_max` (count of the Japanese full stop "。"), `count_equals` / `count_max` (occurrences of a keyword), `contains`, `not_contains`, `bullets_exactly` (optionally scoped with `section`), `regex_count`
- Give `max_tokens` enough headroom for reasoning models ("thinking" + final answer). The tool warns when a value is below the floor (default 1024, configurable)

## Output files

Each run creates a folder under `results_dir`.

- `<timestamp>-bench/bench-summary.csv` — pp / tg medians and peak VRAM
- `<timestamp>-quality/quality-summary.csv` — load time, peak VRAM, and per-prompt status
- `<timestamp>-quality/quality/<model>/` — full prompt and response texts
- `<timestamp>-quality/ai-eval-bundle.md` — everything needed to ask another AI to grade the responses
- `run-metadata.json` — the exact run conditions, for reproducibility

## Tests

```powershell
python -m unittest discover -s tests
python tests/stub_e2e.py   # end-to-end check with stub llama-bench / llama-server binaries
```

## License

MIT
