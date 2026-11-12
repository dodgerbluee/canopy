#!/usr/bin/env python3
"""Generate tests/ai-eval/review-sheet.md from cases.json + recorded
results. The owner fills the rating columns; the >=90% useful-follow-up
gate and the abstention/label-match review are human judgments (a
text-only model cannot be the sole judge of image/label correctness).
"""
import json
import pathlib

here = pathlib.Path(__file__).parent
cases = json.loads((here / "cases.json").read_text())["cases"]

lines = [
    "# T35 reviewer sheet (owner)",
    "",
    "Rate each case. `useful` = every follow-up check is something you",
    "would actually do (Y/N); `abstain-ok` = the model named what it could",
    "not know wherever required (Y/N); `label` = the output is consistent",
    "with the construction label (Y/N). Open the fixture images named in",
    "each row before rating vision cases.",
    "",
    "| id | kind | label (ground truth) | follow-up checks (model) | useful | abstain-ok | label |",
    "|---|---|---|---|---|---|---|",
]

for c in cases:
    p = here / "results" / (c["id"] + ".json")
    if not p.exists():
        lines.append(f"| {c['id']} | {c['kind']} | {c['label']} | (no recorded result) | | | |")
        continue
    rec = json.loads(p.read_text())
    res = rec["result"]
    if c["kind"] == "RECEIPT":
        checks = [
            "currency=%r total=%r tax=%r date=%r reconciled=%s"
            % (res.get("currency"), res.get("totalMinor"), res.get("taxMinor"),
               res.get("purchasedOn"), res.get("needsReconciliation")),
            "lines: " + "; ".join(
                f"{l.get('description')}={l.get('amountMinor')}" for l in res.get("lines", [])),
        ]
    else:
        checks = res.get("followUpChecks", []) or ["(none)"]
        hyps = res.get("hypotheses", [])
        if hyps:
            checks.append("hypotheses: " + "; ".join(
                f"[{h.get('confidence')}] {h.get('text')}" for h in hyps))
        miss = res.get("missingInformation", [])
        if miss:
            checks.append("missing: " + "; ".join(miss))
    fixtures = " ".join(f"`fixtures/{f}`" for f in c.get("fixtures", []))
    label = c["label"] + (" " + fixtures if fixtures else "")
    cell = "<br>".join(str(x).replace("|", "\\|") for x in checks)
    lines.append(f"| {c['id']} | {c['kind']} | {label.replace('|', chr(92) + '|')} | {cell} | | | |")

(here / "review-sheet.md").write_text("\n".join(lines) + "\n")
print(f"wrote review-sheet.md ({len(cases)} cases)")
