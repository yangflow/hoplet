#!/usr/bin/env python3
"""Compares design/tokens.html with web/src/design/tokens.css (docs/design.md).

    python3 design/check-tokens.py

The token page carries the canonical values as JSON in <script id="tokens">; the CSS must define the same
token names with the same values in its dark (:root) and light (prefers-color-scheme) blocks.
"""
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
errors = []

html = (ROOT / "design" / "tokens.html").read_text(encoding="utf-8")
m = re.search(r'<script type="application/json" id="tokens">(.*?)</script>', html, re.S)
if not m:
    print("design/tokens.html: missing <script id=\"tokens\"> JSON block")
    sys.exit(1)
want = json.loads(m.group(1))


def norm(value: str) -> str:
    """The value without layout: one space between words, no trailing zeros in decimals."""
    value = re.sub(r"\s+", " ", value.strip())
    return re.sub(r"(\d\.\d*?)0+(?=\D|$)", lambda m: m.group(1).rstrip(".") if m.group(1).endswith(".") else m.group(1), value)


def css_block(text: str) -> dict[str, str]:
    return {name: value.strip() for name, value in re.findall(r"(--[\w-]+)\s*:\s*([^;]+);", text)}


css = (ROOT / "web" / "src" / "design" / "tokens.css").read_text(encoding="utf-8")
parts = css.split("@media")
dark = css_block(parts[0])
light = css_block(parts[1]) if len(parts) > 1 else {}

for name, value in want.get("shared", {}).items():
    have = dark.get(name)
    if have is None:
        errors.append(f"tokens.css: shared token {name} is missing (tokens.html says {value})")
    elif norm(have) != norm(value):
        errors.append(f"tokens.css: shared token {name} is {have!r}, tokens.html says {value!r}")

for scheme, got in (("dark", dark), ("light", light)):
    for name, value in want[scheme].items():
        have = got.get(name)
        if have is None:
            errors.append(f"tokens.css {scheme}: {name} is missing (tokens.html says {value})")
        elif norm(have) != norm(value):
            errors.append(f"tokens.css {scheme}: {name} is {have!r}, tokens.html says {value!r}")
    extra = set(got) - set(want[scheme]) - set(want.get("shared", {}))
    for name in sorted(extra):
        errors.append(f"tokens.css {scheme}: {name} is not on the token page")

if errors:
    print("\n".join(errors))
    print(f"\n{len(errors)} problem(s)")
    sys.exit(1)
print("tokens ok")
