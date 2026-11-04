"""Unit tests using real captured data from the 2026-11 benchmark runs."""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "src"))

from lbh import checks as checks_mod
from lbh.bench import parse_bench_jsonl
from lbh.hf import parse_repo_input
from lbh.util import split_list
from lbh.vram import estimate_vram_mib

FIXTURES = Path(__file__).parent / "fixtures"

CODER_P4 = """## メリット  
・通勤時間の削減でリラックスできる  
・作業環境の自由な調整が可能  
・時間管理がしやすく、効率が向上  

## 注意点  
・集中力の維持が難しい場合がある  
・コミュニケーションが不足しやすい  
・仕事と生活の境界が曖昧になることがある  

在宅勤務はメリットも多いが、自発的な管理が重要である。"""

Q4_P4 = """## メリット  
- 通勤時間の削減で効率向上  
- 柔軟な時間調整が可能  
- 交通費や食費の削減  

## 注意点  
- 孤立感のリスク  
- 仕事と生活の区別が難しい  
- コミュニケーションの遅れ  

通勤の負担が減る一方で、注意点を考慮した環境整備が重要です。"""

P4_CHECKS = [
    {"type": "max_chars", "value": 400},
    {"type": "count_equals", "needle": "通勤", "value": 1},
    {"type": "contains", "needle": "## メリット"},
    {"type": "contains", "needle": "## 注意点"},
    {"type": "bullets_exactly", "section": "## メリット", "value": 3},
]


class TestBenchParse(unittest.TestCase):
    def test_real_jsonl_medians(self):
        r = parse_bench_jsonl(FIXTURES / "bench-qwen3-14b-q4km.jsonl")
        self.assertAlmostEqual(r["pp_median"], 3632.48, places=2)
        self.assertAlmostEqual(r["tg_median"], 65.7433, places=3)
        self.assertEqual(r["meta"]["build_number"], 11960)


class TestRepoParse(unittest.TestCase):
    def test_forms(self):
        self.assertEqual(parse_repo_input("Qwen/Qwen3-14B-GGUF"), ("Qwen/Qwen3-14B-GGUF", None))
        self.assertEqual(parse_repo_input("https://huggingface.co/Qwen/Qwen3-14B-GGUF"),
                         ("Qwen/Qwen3-14B-GGUF", None))
        repo, hint = parse_repo_input(
            "https://huggingface.co/Qwen/Qwen3-14B-GGUF/blob/main/Qwen3-14B-Q4_K_M.gguf")
        self.assertEqual(repo, "Qwen/Qwen3-14B-GGUF")
        self.assertEqual(hint, "Qwen3-14B-Q4_K_M.gguf")
        repo, _ = parse_repo_input("https://huggingface.co/ggml-org/gpt-oss-20b-GGUF/tree/main")
        self.assertEqual(repo, "ggml-org/gpt-oss-20b-GGUF")


class TestChecks(unittest.TestCase):
    def test_coder_p4_all_pass(self):
        results = checks_mod.run_checks(CODER_P4, P4_CHECKS)
        self.assertTrue(all(r["ok"] for r in results), results)

    def test_q4_p4_fails_only_tsuukin_count(self):
        results = checks_mod.run_checks(Q4_P4, P4_CHECKS)
        failed = [r for r in results if not r["ok"]]
        self.assertEqual(len(failed), 1)
        self.assertEqual(failed[0]["type"], "count_equals")
        self.assertEqual(failed[0]["actual"], 2)

    def test_sentence_max(self):
        five = "文です。文です。文です。文です。文です。"
        r = checks_mod.run_checks(five, [{"type": "sentence_max", "value": 3}])
        self.assertFalse(r[0]["ok"])
        self.assertEqual(r[0]["actual"], 5)


class TestUtilAndEstimate(unittest.TestCase):
    def test_split_list(self):
        self.assertEqual(split_list(["p1,p2,p3"]), ["p1", "p2", "p3"])
        self.assertEqual(split_list(["a", "b,c", " d "]), ["a", "b", "c", "d"])

    def test_estimate_q8_exceeds_16gb_but_q4_fits(self):
        total = 16376
        q8 = estimate_vram_mib(int(14.62 * 1024 * 1024 * 1024), 8192, "q8_0")
        q4 = estimate_vram_mib(int(8.38 * 1024 * 1024 * 1024), 8192, "q8_0")
        self.assertGreater(q8, total)
        self.assertLess(q4, total)


if __name__ == "__main__":
    unittest.main()
