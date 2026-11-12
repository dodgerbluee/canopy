#!/usr/bin/env python3
"""Import-boundary checker (T03).

Rules (enforced repo-wide; scripts/fixtures/boundary-bad proves they fire):
  GO  canopy/server/db/generated may only be imported from server/db/** or
      server/internal/** — cmd/ and anything outside the server module must
      go through internal packages.
  TS  files under **/app/ui/** (presentation primitives) may not import
      '@canopy/contracts' or anything from a '/data/' layer — data access
      belongs to the data layer, not UI primitives.

usage: boundary_check.py [--root DIR] [--expect-violations N]
"""
from pathlib import Path
import re
import sys

GO_DBGEN = "canopy/server/db/generated"
GO_ALLOWED_PREFIXES = ("server/db/", "server/internal/")


def go_violations(root: Path) -> list[str]:
    out = []
    for f in root.rglob("*.go"):
        rel = f.relative_to(root).as_posix()
        if "scripts/fixtures/" in rel:
            continue  # negative fixtures are scanned via --root, not repo-wide
        src = f.read_text()
        if GO_DBGEN not in src:
            continue
        if not any(rel.startswith(p) for p in GO_ALLOWED_PREFIXES):
            out.append(f"{rel}: imports {GO_DBGEN} outside {GO_ALLOWED_PREFIXES}")
    return out


def ts_violations(root: Path) -> list[str]:
    out = []
    pat = re.compile(r"from\s+'(@canopy/contracts|\.\.?/.*?/data/[^']*)'")
    for f in root.rglob("*.ts"):
        rel = f.relative_to(root).as_posix()
        if "scripts/fixtures/" in rel or "node_modules/" in rel:
            continue
        if "/app/ui/" not in "/" + rel:
            continue
        for m in pat.finditer(f.read_text()):
            out.append(f"{rel}: ui layer imports {m.group(1)!r}")
    return out


def main() -> int:
    args = sys.argv[1:]
    root = Path(".")
    expect = 0
    if "--root" in args:
        root = Path(args[args.index("--root") + 1])
    if "--expect-violations" in args:
        expect = int(args[args.index("--expect-violations") + 1])
    v = go_violations(root) + ts_violations(root)
    for x in v:
        print("VIOLATION:", x)
    if len(v) != expect:
        print(f"boundary check FAILED: found {len(v)} violations, expected {expect} (root={root})")
        return 1
    print(f"boundary check OK ({len(v)} violations as expected, root={root})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
