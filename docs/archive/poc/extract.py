#!/usr/bin/env python3
"""POC: 从超星作业/答题页提取内联字体与题目，还原乱码文本并输出结构化 JSON。

原理
----
答题页把一份 TTF 子集 base64 内联在 <style id="cxSecretStyle"> 里，并用它渲染
"载体字"（如 寥、寡）。载体字的 cmap 指向真实汉字的字形轮廓，所以只要把子集里
每个字形和一份完整中文字体做形状比对，就能还原出真正的文字。

这份子集来自 Source Han Sans CN Normal 1.000（思源黑体，字重 350），
与 Noto Sans CJK SC DemiLight 同源同字重，用它做比对准确率最高。
运行 `python3 poc/extract.py --fetch-font` 可自动下载（约 16MB）。

用法
----
    python3 poc/extract.py --html work.html -o poc/out/work.json
    python3 poc/extract.py --url '<答题页 URL>' --cookie '<Cookie>' -o poc/out/work.json
    python3 poc/extract.py --html work.html --md-out poc/out/work.md
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import io
import json
import re
import sys
import urllib.request
from pathlib import Path

import numpy as np
from bs4 import BeautifulSoup
from fontTools.ttLib import TTFont
from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parent
REPO = ROOT.parents[2]
FONT_DIR = REPO / "internal" / "assets"
SOURCE_FONT = FONT_DIR / "NotoSansCJKsc-DemiLight.otf"
SOURCE_FONT_URL = (
    "https://github.com/notofonts/noto-cjk/raw/main/Sans/OTF/"
    "SimplifiedChinese/NotoSansCJKsc-DemiLight.otf"
)

RENDER_SIZE = 96
GRID = 48
CJK_RANGE = range(0x4E00, 0xA000)

HIGH_SCORE, HIGH_MARGIN = 0.93, 0.070
MIN_SCORE, MIN_MARGIN = 0.86, 0.040

FALLBACK_FONTS = [
    "/System/Library/Fonts/Hiragino Sans GB.ttc",
    "/System/Library/Fonts/STHeiti Medium.ttc",
    "/System/Library/Fonts/Supplemental/Songti.ttc",
    "/Library/Fonts/Arial Unicode.ttf",
]

TYPE_LABELS = {"0": "单选题", "1": "多选题", "3": "判断题"}

STYLE_RE = re.compile(
    r"<style[^>]*\bid=[\"']cxSecretStyle[\"'][^>]*>(.*?)</style>", re.S | re.I
)
FAMILY_RE = re.compile(r"@font-face\s*\{[^}]*font-family:\s*['\"]?([\w-]+)")
B64_RE = re.compile(r"base64,([A-Za-z0-9+/=\s]+)")


# --------------------------------------------------------------------------- io


def load_html(args) -> tuple[str, str]:
    if args.html:
        path = Path(args.html)
        return path.read_text(encoding="utf-8", errors="replace"), str(path.resolve())
    if not args.url:
        sys.exit("需要 --html 或 --url")
    import requests

    headers = {"User-Agent": args.user_agent}
    if args.cookie:
        headers["Cookie"] = args.cookie.strip()
    resp = requests.get(args.url, headers=headers, timeout=30)
    resp.raise_for_status()
    resp.encoding = resp.apparent_encoding or resp.encoding
    return resp.text, args.url


def extract_font(html: str) -> tuple[str, bytes]:
    m = STYLE_RE.search(html)
    if not m:
        sys.exit("页面里没有 <style id=cxSecretStyle>，可能不是带字体混淆的答题页")
    css = m.group(1)
    fam = FAMILY_RE.search(css)
    family = fam.group(1) if fam else "font-cxsecret"
    b = B64_RE.search(css)
    if not b:
        sys.exit("cxSecretStyle 里没有 base64 字体数据")
    return family, base64.b64decode(re.sub(r"\s+", "", b.group(1)))


def fetch_source_font(force: bool = False) -> Path:
    if SOURCE_FONT.exists() and not force:
        return SOURCE_FONT
    FONT_DIR.mkdir(parents=True, exist_ok=True)
    print(f"下载同源字体 -> {SOURCE_FONT}", file=sys.stderr)
    with urllib.request.urlopen(SOURCE_FONT_URL, timeout=180) as resp:
        SOURCE_FONT.write_bytes(resp.read())
    print(f"完成: {SOURCE_FONT.stat().st_size} bytes", file=sys.stderr)
    return SOURCE_FONT


# ------------------------------------------------------------------- rendering


def make_renderer(size: int, grid: int):
    def render(font, ch: str) -> np.ndarray | None:
        img = Image.new("L", (size * 2, size * 2), 255)
        ImageDraw.Draw(img).text((size // 2, size // 2), ch, font=font, fill=0)
        bbox = img.point(lambda p: 255 if p < 200 else 0).getbbox()
        if not bbox:
            return None
        glyph = img.crop(bbox)
        side = max(glyph.size)
        square = Image.new("L", (side, side), 255)
        square.paste(glyph, ((side - glyph.size[0]) // 2, (side - glyph.size[1]) // 2))
        arr = 1.0 - np.asarray(
            square.resize((grid, grid), Image.LANCZOS), dtype=np.float32
        ) / 255.0
        norm = float(np.linalg.norm(arr))
        return (arr / norm).ravel() if norm else None

    return render


def available_ref_fonts(explicit: str | None) -> list[str]:
    if explicit:
        paths = [explicit]
    elif SOURCE_FONT.exists():
        paths = [str(SOURCE_FONT), *FALLBACK_FONTS]
    else:
        print(
            "未找到同源字体 internal/assets/NotoSansCJKsc-DemiLight.otf，"
            "可用 --fetch-font 下载；先退回系统中文字体",
            file=sys.stderr,
        )
        paths = list(FALLBACK_FONTS)

    out = []
    for path in paths:
        if not path or path in out or not Path(path).exists():
            continue
        try:
            ImageFont.truetype(path, RENDER_SIZE, index=0)
            out.append(path)
        except OSError:
            continue
    if not out:
        sys.exit("找不到可用的中文字体，请用 --ref-font 指定")
    return out


# -------------------------------------------------------------------- matching


def build_decode_table(
    ttf: bytes, font_paths: list[str], cache_dir: Path, use_cache: bool = True
):
    key = hashlib.sha1(
        ttf + ("|".join(font_paths) + f"{RENDER_SIZE}/{GRID}").encode()
    ).hexdigest()[:16]
    cache = cache_dir / f"decode-{key}.json"
    if use_cache and cache.exists():
        data = json.loads(cache.read_text())
        return data["table"], data["detail"], data["fonts"]

    render = make_renderer(RENDER_SIZE, GRID)
    secret = ImageFont.truetype(io.BytesIO(ttf), RENDER_SIZE, index=0)
    carriers = [chr(cp) for cp in TTFont(io.BytesIO(ttf)).getBestCmap()]

    matrices = []
    for path in font_paths:
        font = ImageFont.truetype(path, RENDER_SIZE, index=0)
        chars, vecs = [], []
        for cp in CJK_RANGE:
            vec = render(font, chr(cp))
            if vec is not None:
                chars.append(chr(cp))
                vecs.append(vec)
        matrices.append((path, chars, np.stack(vecs)))

    table, detail = {}, {}
    for carrier in carriers:
        vec = render(secret, carrier)
        if vec is None:
            continue

        _, chars, matrix = matrices[0]
        sims = matrix @ vec
        order = np.argsort(-sims)
        guess, score = chars[order[0]], float(sims[order[0]])
        runner_up, second = chars[order[1]], float(sims[order[1]])
        margin = score - second

        # 同源字体拿不准时，用其他字体投票做二次确认（只在候选间裁决）。
        if margin < MIN_MARGIN and len(matrices) > 1:
            candidates = [chars[i] for i in order[:3]]
            votes = {c: 0 for c in candidates}
            votes[candidates[0]] = 2
            for _, other_chars, other_matrix in matrices[1:]:
                other = other_matrix @ vec
                hit = other_chars[int(np.argmax(other))]
                if hit in votes:
                    votes[hit] += 1
            winner = max(candidates, key=lambda c: (votes[c], c == candidates[0]))
            if winner != guess:
                guess, runner_up, margin = winner, guess, -margin

        if score >= HIGH_SCORE and margin >= HIGH_MARGIN:
            confidence = "high"
        elif score >= MIN_SCORE and margin >= MIN_MARGIN:
            confidence = "medium"
        else:
            confidence = "low"

        table[carrier] = guess
        detail[carrier] = {
            "char": guess,
            "score": round(score, 4),
            "margin": round(margin, 4),
            "confidence": confidence,
            "runnerUp": runner_up,
            "alternatives": [chars[i] for i in order[1:4]],
        }

    cache_dir.mkdir(parents=True, exist_ok=True)
    cache.write_text(
        json.dumps(
            {"fonts": font_paths, "table": table, "detail": detail}, ensure_ascii=False
        ),
        encoding="utf-8",
    )
    return table, detail, font_paths


def decode(text: str, table: dict[str, str]) -> str:
    return "".join(table.get(ch, ch) for ch in text)


def clean(text: str) -> str:
    return re.sub(r"[ \t\u00a0]+", " ", text.replace("\r", "")).strip()


# --------------------------------------------------------------------- parsing


def parse_questions(html: str, table: dict[str, str], detail: dict) -> list[dict]:
    soup = BeautifulSoup(html, "lxml")
    questions = []
    section = None

    for node in soup.find_all(["h3", "div"], class_=["newTestType", "TiMu"]):
        classes = node.get("class") or []
        if "newTestType" in classes:
            section = clean(node.get_text())
            continue
        if "TiMu" not in classes:
            continue

        wrap = node.find_parent("div", class_="singleQuesId")
        qid = (wrap.get("data") if wrap else None) or (
            wrap.get("id", "")[8:] if wrap else ""
        )

        num_el = node.select_one("i.fl")
        number = clean(num_el.get_text()) if num_el else None

        stem_el = node.select_one("div.fontLabel")
        if not stem_el:
            continue
        type_el = stem_el.select_one("span.newZy_TItle")
        qtype = clean(type_el.get_text()).strip("【】") if type_el else None
        if type_el:
            type_el.extract()
        stem_raw = clean(stem_el.get_text(" ", strip=True))
        type_code = node.get("data")
        qtype = qtype or TYPE_LABELS.get(type_code)

        options = []
        for li in node.select("li[qid]"):
            letter_el = li.select_one("span.num_option, span.num_option_dx")
            text_el = li.select_one("a.after")
            if not text_el:
                continue
            text_raw = clean(text_el.get_text(" ", strip=True))
            options.append(
                {
                    "letter": clean(letter_el.get_text()) if letter_el else None,
                    "value": letter_el.get("data") if letter_el else None,
                    "text_raw": text_raw,
                    "text": decode(text_raw, table),
                }
            )

        raw_text = stem_raw + "".join(o["text_raw"] for o in options)
        uncertain = []
        seen = set()
        for ch in raw_text:
            info = detail.get(ch)
            if not info or info["confidence"] != "low" or ch in seen:
                continue
            seen.add(ch)
            uncertain.append({"raw": ch, **{k: info[k] for k in
                              ("char", "score", "margin", "alternatives")}})

        questions.append(
            {
                "id": qid,
                "number": number,
                "section": section,
                "type": qtype,
                "typeCode": type_code,
                "stem_raw": stem_raw,
                "stem": decode(stem_raw, table),
                "options": options,
                "uncertain": uncertain,
            }
        )
    return questions


# ---------------------------------------------------------------------- output


def to_markdown(result: dict) -> str:
    lines = ["# 题目", ""]
    current = None
    for q in result["questions"]:
        if q.get("section") and q["section"] != current:
            current = q["section"]
            lines += [f"## {current}", ""]
        lines.append(f"**{q['number']}. [{q['type']}]** {q['stem']}")
        if q.get("uncertain"):
            marks = "、".join(
                f"{u['raw']}→{u['char']}({','.join(u['alternatives'])})"
                for u in q["uncertain"]
            )
            lines.append(f"> 低置信：{marks}")
            lines.append("")
        for o in q["options"]:
            lines.append(f"- {o['letter']}. {o['text']}")
        lines.append("")
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--html", help="本地保存的答题页 HTML")
    ap.add_argument("--url", help="直接抓取的 URL")
    ap.add_argument("--cookie", help="Cookie 请求头")
    ap.add_argument("--user-agent", default="Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")
    ap.add_argument("--ref-font", help="替换默认的同源字体")
    ap.add_argument("--fetch-font", action="store_true", help="下载同源字体后退出")
    ap.add_argument("-o", "--out", default=str(ROOT / "out" / "work.json"))
    ap.add_argument("--md-out", help="另存一份 Markdown，方便直接喂给 AI")
    ap.add_argument("--font-out", default=str(ROOT / "out" / "cxsecret.ttf"))
    ap.add_argument("--cache-dir", default=str(ROOT / "cache"))
    ap.add_argument("--no-cache", action="store_true")
    ap.add_argument("--keep-raw", action="store_true", help="保留乱码原文")
    args = ap.parse_args()

    if args.fetch_font:
        fetch_source_font(force=True)
        return

    html, source = load_html(args)
    family, ttf = extract_font(html)

    ref_fonts = available_ref_fonts(args.ref_font)
    table, detail, ref_fonts = build_decode_table(
        ttf, ref_fonts, Path(args.cache_dir), not args.no_cache
    )

    font_out = Path(args.font_out)
    font_out.parent.mkdir(parents=True, exist_ok=True)
    font_out.write_bytes(ttf)

    questions = parse_questions(html, table, detail)
    if not args.keep_raw:
        for q in questions:
            q.pop("stem_raw", None)
            if not q["uncertain"]:
                q.pop("uncertain")
            for o in q["options"]:
                o.pop("text_raw", None)

    low = {c: d for c, d in detail.items() if d["confidence"] == "low"}
    result = {
        "source": source,
        "font": {
            "family": family,
            "ttf": str(font_out),
            "bytes": len(ttf),
            "carriers": len(table),
            "sourceFont": Path(ref_fonts[0]).name,
            "lowConfidence": low,
            "charMap": detail,
        },
        "questionCount": len(questions),
        "questions": questions,
    }

    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")

    if args.md_out:
        md = Path(args.md_out)
        md.parent.mkdir(parents=True, exist_ok=True)
        md.write_text(to_markdown(result), encoding="utf-8")
        print(f"Markdown -> {md}")

    print(f"字体 {family}: {len(ttf)} bytes, {len(table)} 个映射字形 -> {font_out}")
    print(f"比对字体: {Path(ref_fonts[0]).name}")
    if low:
        print(f"低置信 {len(low)} 个: " + ", ".join(
            f"{c}→{d['char']}({','.join(d['alternatives'])})" for c, d in low.items()
        ), file=sys.stderr)
    print(f"题目 {len(questions)} 道 -> {out}\n")
    for q in questions:
        print(f"[{q['number']}] {q['type']} {q['stem']}")
        for o in q["options"]:
            print(f"    {o['letter']}. {o['text']}")
        print()


if __name__ == "__main__":
    main()
