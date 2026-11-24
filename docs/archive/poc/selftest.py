#!/usr/bin/env python3
"""用已知真值自检字形解码表。"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from extract import build_decode_table, available_ref_fonts  # noqa: E402

GROUND_TRUTH = {
    "苧": "败", "寠": "严", "寡": "峻", "實": "层", "寥": "生", "寑": "实",
    "寣": "群", "冩": "虎", "寬": "狐", "寵": "奋", "潯": "战",
}


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--secret", default="poc/out/cxsecret.ttf")
    ap.add_argument("--ref-font")
    ap.add_argument("--cache-dir", default="poc/cache")
    args = ap.parse_args()

    ttf = Path(args.secret).read_bytes()
    table, detail, _ = build_decode_table(
        ttf, available_ref_fonts(args.ref_font), Path(args.cache_dir)
    )

    failed = 0
    for carrier, truth in GROUND_TRUTH.items():
        got = table.get(carrier)
        ok = got == truth
        failed += not ok
        info = detail.get(carrier, {})
        print(
            f"{'✓' if ok else '✗'} {carrier} -> {got} "
            f"(truth {truth}, score {info.get('score')}, margin {info.get('margin')})"
        )

    total = len(GROUND_TRUTH)
    print(f"\n{total - failed}/{total} 通过")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
