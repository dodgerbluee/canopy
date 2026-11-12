#!/usr/bin/env python3
"""Dispatch gate: a task may only start when every dependency is ACCEPTED.

usage: dispatch_check.py TASK            # exit 0 iff dispatchable
       dispatch_check.py --fixtures      # positive + negative self-test
"""
from pathlib import Path
import json
import sys

ROOT = Path(__file__).resolve().parent.parent
graph = json.loads((ROOT / "docs" / "plan" / "task-graph.json").read_text())
nodes, groups = graph["nodes"], graph["groups"]


def can_dispatch(name: str) -> tuple[bool, str]:
    if name in groups:
        return False, f"{name} is a parent group; dispatch its executable children instead"
    if name not in nodes:
        return False, f"{name} is not a node in task-graph.json"
    unmet = [d for d in nodes[name]["dependsOn"] if nodes.get(d, {}).get("status") != "ACCEPTED"]
    if unmet:
        return False, f"{name} blocked; dependencies not ACCEPTED: {', '.join(unmet)}"
    return True, f"{name} dispatchable"


def main() -> int:
    args = sys.argv[1:]
    if args == ["--fixtures"]:
        ok = True
        # positive: T03 must be dispatchable right now (T00/T01.r1/T02.r1 accepted)
        good, msg = can_dispatch("T03")
        print(("PASS " if good else "FAIL ") + msg)
        ok &= good
        # negative: T39 has unmet deps (T38.final — portability final) and
        # must be blocked; chosen so it stays blocked until the
        # portability/release lane closes (moved from T35 when T34 was
        # accepted and unblocked T35/T36/T37/T38.final; same staleness
        # precedent as the T09->T16->T28->T29->T30->T35 moves)
        bad, msg = can_dispatch("T39")
        print(("PASS " if not bad else "FAIL ") + msg)
        ok &= not bad
        # negative: parent groups cannot dispatch
        bad, msg = can_dispatch("T01")
        print(("PASS " if not bad else "FAIL ") + msg)
        ok &= not bad
        return 0 if ok else 1
    if len(args) != 1:
        print(__doc__)
        return 2
    ok, msg = can_dispatch(args[0])
    print(msg)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
