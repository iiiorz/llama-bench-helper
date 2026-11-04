"""Mechanical checks for quality responses (countable conditions only)."""

from __future__ import annotations

import re

_BULLET_PREFIXES = ("- ", "* ", "・", "・ ", "- ", "‐ ")


def _bullet_count(text: str) -> int:
    n = 0
    for line in text.splitlines():
        s = line.lstrip()
        if s.startswith(("- ", "* ", "・")):
            n += 1
    return n


def _section(text: str, heading: str) -> str:
    """Text from the given heading line to the next '## ' heading (or end)."""
    lines = text.splitlines()
    start = None
    for i, line in enumerate(lines):
        if line.strip() == heading:
            start = i
            break
    if start is None:
        return ""
    out: list[str] = []
    for line in lines[start + 1:]:
        if line.lstrip().startswith("## "):
            break
        out.append(line)
    return "\n".join(out)


def run_checks(text: str, checks: list[dict]) -> list[dict]:
    """Evaluate check definitions against a response text.

    Supported types: max_chars, min_chars, sentence_max (count of '。'),
    count_equals / count_max (occurrences of 'needle'), contains,
    not_contains, bullets_exactly, regex_count (pattern occurrences == value).
    """
    results: list[dict] = []
    for c in checks or []:
        ctype = c.get("type", "")
        value = c.get("value")
        needle = c.get("needle", "")
        actual = None
        ok = True
        detail = ""
        if ctype == "max_chars":
            actual = len(text)
            ok = actual <= int(value)
            detail = f"{actual} chars (limit {value})"
        elif ctype == "min_chars":
            actual = len(text)
            ok = actual >= int(value)
            detail = f"{actual} chars (minimum {value})"
        elif ctype == "sentence_max":
            actual = text.count("。")
            ok = actual <= int(value)
            detail = f"{actual} sentences (limit {value})"
        elif ctype == "count_equals":
            actual = text.count(needle)
            ok = actual == int(value)
            detail = f"'{needle}' x{actual} (expected {value})"
        elif ctype == "count_max":
            actual = text.count(needle)
            ok = actual <= int(value)
            detail = f"'{needle}' x{actual} (limit {value})"
        elif ctype == "contains":
            actual = needle in text
            ok = bool(actual)
            detail = f"contains '{needle}': {actual}"
        elif ctype == "not_contains":
            actual = needle in text
            ok = not actual
            detail = f"absence of '{needle}': {not actual}"
        elif ctype == "bullets_exactly":
            scope = _section(text, str(c["section"])) if c.get("section") else text
            actual = _bullet_count(scope) if scope else 0
            ok = actual == int(value)
            detail = f"{actual} bullet lines (expected {value})"
        elif ctype == "regex_count":
            actual = len(re.findall(str(c.get("pattern", "")), text))
            ok = actual == int(value)
            detail = f"pattern matches x{actual} (expected {value})"
        else:
            ok = False
            detail = f"unknown check type: {ctype}"
        results.append({"type": ctype, "needle": needle, "value": value,
                        "actual": actual, "ok": ok, "detail": detail})
    return results


def summarize(results: list[dict]) -> str:
    if not results:
        return "no checks"
    passed = sum(1 for r in results if r["ok"])
    return f"{passed}/{len(results)} checks passed"
