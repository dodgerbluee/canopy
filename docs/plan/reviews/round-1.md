# Independent planning review — round 1

Grade: **B, 86/100**. Not A-ready: two P1 contract gaps remain. No P0 found.

Reviewed README and all eight numbered plan documents against the supplied user requirements and rubric. This is a document review; I did not execute application code, verify claimed local CLI/provider evidence, browse reference documentation, or treat planned tests as passing runtime evidence.

Also reviewed the subsequently supplied `tasks/T00.md`. It is a concrete, appropriately disposable first packet with bounded writes, actual commands/proofs, credential exclusions and stop conditions. Its manifest-based bootstrap avoids assuming an application repository exists before T03. T01/T02 compile spikes can precede permanent generator plumbing in T03 without a logical cycle. Downstream packets intentionally remain unready until exact accepted contracts/commands exist; I do not count that deliberate sequencing as an absent implementation packet defect. T00 is broad enough to split if its codec/security and toolchain probes exceed the stated packet limits, but that is already covered by the delegation split policy.

| Rubric | Score | Assessment |
|---|---:|---|
| Product completeness | 14/15 | Strong coverage of private provenance, dated photos, care and evidence-based assistance; useful staged journal. |
| Architecture and simplicity | 13/15 | Coherent modular monolith, typed SQL and small client state; substantial upfront schema freeze and operational machinery for one owner. |
| Contracts and data correctness | 15/20 | Excellent money/time/media rules, but unknown-cost representation and deletion retention are unresolved. |
| Testability and recovery | 18/20 | Real PostgreSQL, failure injection, actual restore and provider evaluation are well specified. |
| UX and accessibility | 9/10 | Concrete modern visual direction and real screenshot/manual review gates; a few release-state interactions need definition. |
| Delegation executability | 12/15 | Strong fresh-session, narrow-packet protocol; suffix dependencies and several omitted surface contracts need normalization. |
| Honesty and scope | 5/5 | Explicit planning-only status, capability uncertainty, no mock completion claims. |
| **Total** | **86/100** | **B** |

## P1 — fix before freezing contracts or dispatching dependent feature work

### P1-1: Unknown purchase costs have no consistent authoritative representation

Affected sections: **01-product / Missing facts and graceful behavior**; **03-contracts / Shared value rules**, **Model inventory and invariants** (`purchases`, `purchase_lines`, `purchase_allocations`), **Purchases: exact arithmetic**; **07-tasks / T01, T02, T11, T12**.

The product requires unknown purchase price to remain unknown, and spending requires an unknown-cost count. The purchase aggregate requires `totalMinor`, line subtotals and allocations to reconcile exactly, while only seller/date are explicitly nullable. There is no stated rule for recording a known acquisition with an unknown price, distinguishing “no purchase recorded” from “price unknown,” or later replacing an unknown purchase with a priced one. A local implementer must invent this relationship or misuse zero/null, affecting both history and spending.

Correction: choose an explicit representation before T01/T02: for example, a purchase pricing state UNKNOWN/KNOWN with null monetary fields and no monetary allocations while unknown, plus explicit plant acquisition links; or separate acquisition provenance from priced purchases. Specify legal transitions, counts and list/detail displays. Add fixtures for unknown cost, genuinely free purchase, absent acquisition record, and conversion to known shared purchase without duplicate provenance or journal entries. Ensure database checks and typed GraphQL inputs encode the choice.

### P1-2: Permanent purge does not define deletion across retained copies and replay records

Affected sections: **02-architecture / State and concurrency**; **03-contracts / Model inventory and invariants** (purge paragraph, `audit_revisions`, `mutation_receipts`), **AI, identification, and OCR**; **05-verification / Operational runbooks**; **07-tasks / T38**.

Purge removes plant-owned data and dependent AI snapshots, but mutations retain full responses for 30 days and audit revisions retain prior values. Export archives, completed job results, derived journal summaries and provider-result evidence can also contain copied plant details. Only backups and shared purchase labels have an explicit preservation policy. A create mutation replay after purge could return an apparently existing plant or disclose erased content; that behavior is not defined. “Removes plant-owned data” is insufficient for implementing retention across these independent tables and files.

Correction: add a deletion matrix enumerating canonical records, audit bodies, mutation responses, analysis inputs/results, notification bodies, exports, queued jobs, shared receipts, originals/derivatives and backups. Define transactionally invalidated/replaced replay responses (retaining a minimal key/hash tombstone if needed), redaction versus retention and deadlines, export revocation, job fencing, and what the confirmation UI promises. Add a purge→replay test, purge during export/analysis, shared-receipt survival, and inspection that no live unapproved copies remain. Do not require erasing legally retained financial totals or pretend backup erasure is immediate.

## P2 — bounded corrections

### P2-1: Normalize the suffix-task DAG before dispatch

Affected sections: **07-tasks / T20, T32, T34, T38, T39, T40**, **Delivery order and concurrency lanes**, **Task ledger schema**.

The informal DAG is acyclic if T32.a and T38.a are independent nodes, and the text explicitly intends that. However, only parent task headings exist; T34 depends on T32, T39 depends on T38, T40 uses T38(final), and the final paragraph calls that T38.final. A mechanical validator cannot infer alias/parent completion semantics safely. R1's early restore also needs a precise schema cutoff because care/AI tables were already created in T02 even though features are not populated.

Correction: materialize canonical node IDs and explicit dependency sets for T32.a/T32.b/T38.a/T38.final, state whether parents are non-executable grouping nodes, and use those IDs consistently. Add a fixture asserting R1 prerequisites do not reach T21–T35, final restore reaches T23/T33, and every referenced dependency exists. This can be a small plan ledger rather than fully populated future packets.

### P2-2: Complete the source/profile and correction command inventories

Affected sections: **03-contracts / GraphQL contract envelope**, **Scheduling contract**; **04-experience / Care**; **07-tasks / T01, T21, T23, T24**.

Owner-editable/retirable sources and profiles are required, but the top-level mutation inventory only names publishCareProfile. The UI promises reversible correction of completion via activity edit/void, but the scheduling contract does not state whether voiding that activity reopens the completed occurrence or recalculates completion-relative scheduling. This invites conflicting backend/frontend assumptions.

Correction: enumerate create/edit/retire source and draft-profile operations and immutable published-version transitions. Define the exact completed-occurrence correction transaction, history/audit effects, next occurrence regeneration and notifications; add correction-after-completion and correction-after-next-completion cases before freezing SDL.

### P2-3: Resolve staged navigation and interrupted capture behavior

Affected sections: **04-experience / Information architecture and routes**, **State and accessibility contract**; **07-tasks / T07, T28, T32**.

Today is the prominent navigation destination, but its production screen arrives in R2. Capture verification includes interrupted upload/reload while the privacy rules disallow sensitive browser persistence; the plan does not distinguish server-retained completed uploads from failed local files requiring reselection.

Correction: define the R1 default route and hide/label unavailable care/insight destinations. Specify which upload state survives reload, how owner reselects an unuploaded file without duplicate attachments, and how pending private assets are resumed/abandoned. Keep sensitive bytes out of persistent browser storage.

### P2-4: Reduce the cost of the initial full-domain freeze

Affected sections: **02-architecture / Stack decisions**, **07-tasks / T01–T03**, **06-delegation / Task size and execution**.

Before the first usable journal, T01/T02 define the entire care/AI surface and schema. This is coherent but costly for bounded local-model sessions, especially as actual provider capabilities are evaluated much later. The L-task splitting rule helps, yet early changes could cascade through generated contracts and SQL long before those features run.

Correction: freeze shared values and R1 contracts first, reserve care/AI boundaries with typed design fixtures, then freeze those release-specific schemas before their own coding lanes. If retaining the full freeze, explicitly budget/review each suffix contract packet and acknowledge later additive migrations as expected rather than architectural failure. Retain the single serialized schema/codegen lane.

## Strengths and gate recommendation

The plan is unusually explicit about transaction boundaries, workspace ownership, at-least-once delivery, immutable originals, codec enforcement, optional-provider behavior and clean-instance recovery. It also gives visual quality independent evidence instead of assuming that Angular or screenshots automatically make the product polished. These are credible planning strengths, not runtime proof.

Resolve the two P1s, normalize the executable task graph and clarify the source/correction operations, then obtain a fresh independent review. No application implementation is needed to fix these planning issues.
