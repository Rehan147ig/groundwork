#!/usr/bin/env python3
"""Migration order check (CI gate).

Asserts that the numeric prefixes of migrations/*_*.up.sql form a contiguous,
gap-free, duplicate-free sequence. The sequence need not start at 1 (this repo's
baseline begins at 003) — it only needs to be sequential from its minimum.

Also verifies every *.up.sql has a matching *.down.sql, so migrations stay
reversible. Exits non-zero (failing CI) on any violation.
"""
from __future__ import annotations

import os
import re
import sys

MIGRATIONS_DIR = os.path.join(os.path.dirname(__file__), "..", "migrations")
UP_RE = re.compile(r"^(\d+)_[A-Za-z0-9_]+\.up\.sql$")


def main() -> int:
    if not os.path.isdir(MIGRATIONS_DIR):
        print(f"FAIL: migrations directory not found: {MIGRATIONS_DIR}")
        return 1

    entries = sorted(os.listdir(MIGRATIONS_DIR))
    ups = [f for f in entries if f.endswith(".up.sql")]
    if not ups:
        print("FAIL: no *.up.sql migration files found")
        return 1

    numbered: list[tuple[int, str]] = []
    for name in ups:
        m = UP_RE.match(name)
        if not m:
            print(f"FAIL: migration '{name}' does not match <number>_<name>.up.sql")
            return 1
        numbered.append((int(m.group(1)), name))

    numbered.sort(key=lambda t: t[0])

    # Duplicate numeric prefixes.
    by_num: dict[int, list[str]] = {}
    for num, name in numbered:
        by_num.setdefault(num, []).append(name)
    dupes = {n: names for n, names in by_num.items() if len(names) > 1}
    if dupes:
        for n, names in sorted(dupes.items()):
            print(f"FAIL: duplicate migration number {n:03d}: {', '.join(names)}")
        return 1

    # Contiguous from the minimum present number (no gaps).
    ordered = [n for n, _ in numbered]
    for prev, cur in zip(ordered, ordered[1:]):
        if cur != prev + 1:
            print(f"FAIL: non-sequential migrations: {prev:03d} is followed by {cur:03d} (missing {prev + 1:03d})")
            return 1

    # Every up migration must have a matching down migration (reversibility).
    missing_down = [name for _, name in numbered if not os.path.exists(os.path.join(MIGRATIONS_DIR, name[:-len(".up.sql")] + ".down.sql"))]
    if missing_down:
        for name in missing_down:
            print(f"FAIL: missing down migration for {name}")
        return 1

    print(f"OK: {len(ordered)} migrations sequential and reversible ({ordered[0]:03d}..{ordered[-1]:03d})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
