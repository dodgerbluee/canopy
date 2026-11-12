#!/usr/bin/env python3
"""Task-packet validator (T03).

Checks, against docs/plan/task-graph.json and docs/plan/07-tasks.md:
  1. every dispatchable node is covered by a packet (its own heading, or the
     parent-group heading for suffixed children like T01.r1);
  2. packets carry the required fields (Depends/Own/Acceptance/Verify);
     group headings are exempt from Depends because groups cannot dispatch;
  3. ACCEPTED nodes have non-empty evidence under .artifacts/agents/<prefix>/
     and a row in docs/plan/ledger.md (accepted predecessors are evidenced,
     not asserted);
  4. ACCEPTED nodes own at least one path that exists on disk.

No application dependencies, no network.
"""
from pathlib import Path
import json
import re
import sys

ROOT = Path(__file__).resolve().parent.parent
PLAN = ROOT / "docs" / "plan"
errors: list[str] = []

graph = json.loads((PLAN / "task-graph.json").read_text())
nodes, groups = graph["nodes"], graph["groups"]
text = (PLAN / "07-tasks.md").read_text()

heads = [(m.start(), m.group(1)) for m in re.finditer(r"^### ([A-Za-z0-9._]+)", text, re.M)]
sections = {}
for i, (pos, h) in enumerate(heads):
    end = heads[i + 1][0] if i + 1 < len(heads) else len(text)
    sections[h] = text[pos:end]

REQUIRED = ("Depends:", "Own:", "Acceptance:", "Verify:")

for name in nodes:
    parent = name.split(".")[0]
    heading = name if name in sections else parent if parent in sections else None
    if heading is None:
        errors.append(f"{name}: no packet heading (nor parent group packet)")
        continue
    sec = sections[heading]
    required = REQUIRED if heading not in groups else tuple(f for f in REQUIRED if f != "Depends:")
    for field in required:
        if field not in sec:
            errors.append(f"{name}: packet '{heading}' missing field {field!r}")

ledger_path = PLAN / "ledger.md"
ledger = ledger_path.read_text() if ledger_path.exists() else ""

for name, node in nodes.items():
    if node.get("status") != "ACCEPTED":
        continue
    prefix = name.split(".")[0]
    ev = ROOT / ".artifacts" / "agents" / prefix
    if not ev.is_dir() or not any(ev.rglob("*")):
        errors.append(f"{name}: ACCEPTED but evidence dir {ev.relative_to(ROOT)} is missing/empty")
    if f"| {name} |" not in ledger:
        errors.append(f"{name}: ACCEPTED but no row in docs/plan/ledger.md")
    heading = name if name in sections else name.split(".")[0]
    owned = re.findall(r"`([^`]+)`", sections.get(heading, ""))
    if owned and not any((ROOT / p.rstrip("/")).exists() for p in owned):
        errors.append(f"{name}: ACCEPTED but none of its claimed Own paths exist")

if errors:
    print("packet validation FAILED:")
    for e in errors:
        print("  -", e)
    sys.exit(1)
print(f"packet validation OK ({len(nodes)} nodes, {len(groups)} groups, "
      f"{sum(1 for n in nodes.values() if n.get('status') == 'ACCEPTED')} accepted)")
