# Independent plan review — round 2

Reviewed 2026-11-01. Fresh assessment of README, all eight plan documents, task-graph.json, validate_plan.py, and tasks/T00.md. Prior reviews were not read. This assesses implementation readiness of a plan, not application quality or runtime correctness.

**Grade: A — 92/100.** No P0/P1 found. Every rubric section exceeds 70%. The remaining findings are bounded contract/automation clarifications to resolve during the already-required contract freeze and packet preparation; they do not require a different architecture.

| Section | Score | Assessment |
|---|---:|---|
| Product completeness | 15/15 | Purchases/provenance, photo journey, contextual seasonal care, and evidence-based assistance are explicitly covered; unknown owner facts and no-provider behavior are handled honestly. |
| Architecture and simplicity | 14/15 | Appropriate modular monolith, typed SQL, transactional queue, small provider ports, and restrained client state. Privacy/backup machinery adds real complexity but has an explicit purpose. |
| Contracts and data correctness | 17/20 | Strong exact-money, ownership, idempotency, correction, purge, and generation-fencing contracts. A few transitions and accounting edge cases still need exact semantics. |
| Testability and failure recovery | 19/20 | Real PostgreSQL/API/browser tests, crash boundaries, restore/deletion-ledger rehearsal, and meaningful media limits. The planning validator has narrower coverage than the release prose implies. |
| UX and accessibility | 9/10 | Concrete compositions, tokens, phone capture recovery, keyboard comparison, and mandatory visual/manual accessibility evidence. Small route/action inconsistencies remain. |
| Delegation executability | 13/15 | Existing local model, fresh sessions, narrow ownership, tested permissions, evidence gates, and staged contract freeze are credible. Downstream packets are intentionally not executable yet; gate inheritance needs tightening. |
| Honesty and scope | 5/5 | No application claims, mock vision claims, invented climate, or unsupported delivery promises. Live capability evaluation and independent implementation review remain explicit gates. |
| **Total** | **92/100** | **A threshold met.** |

## Findings, ordered by impact

### P2 — Canonical intermediate release gates do not inherit all prior release acceptance

**Where:** task-graph.json `gates`, 07-tasks.md delivery order, validate_plan.py.

Prose requires R2 to include R1 and R3 to include R2. Canonical R2 is only T28; its dependency closure omits T38.a. R3 likewise can close without the R1 export/backup/restore task. The final T41 closure does include it, so final v1 is protected, but a gate-driven executor could mark an intermediate release accepted without its promised recovery capability. The validator checks R1 isolation and the final closure, not monotonic release acceptance.

**Fix:** define cumulative gate membership or explicitly make release evaluation include every preceding gate. Validate that each gate closure contains the previous gate closure. Add a negative validator fixture that removes T38.a from intermediate acceptance and must fail.

### P2 — Cost reservation does not spell out charged retries, cancellation, and unknown final usage

**Where:** 03-contracts.md AI budget/lifecycle; T29.

The contract reserves worst-case cost and reconciles/releases on finish, while allowing retries, a repair request, cancellation, and provider timeouts. It does not say whether the reservation covers all potentially billable attempts or what happens when a request may have been charged but returned no usage. A straightforward implementation could refund a timed-out/cancelled run and permit further spend beyond the stated monthly ceiling.

**Fix:** reserve the maximum total across allowed attempts/repair, or reserve each attempt atomically before dispatch. Treat potentially charged attempts as spent/encumbered until reconciled; cancel must not refund already dispatched work merely because its result is discarded. Define budget timezone/month boundary and add timeout-after-charge, retry-plus-repair, cancellation, and month-boundary concurrency cases.

### P2 — Completion-relative skip and recurrence regeneration need a precise anchor rule

**Where:** 03-contracts.md scheduling; T22/T23.

An interval can anchor to the last qualifying completion, but skip must advance one cycle without an activity. It is unclear whether the skipped due date becomes a virtual scheduling anchor, how repeated skips behave, and how a later backdated completion/correction affects those skipped cycles. Existing requirements preserve skipped history, but do not resolve the next actionable date in this combination.

**Fix:** define the recurrence cursor independently from completed activity and state exactly how skip advances it. Supply dated examples for interval skip→skip, skip→late completion, and backdated correction after a skip. Require exact next-date and preserved-history assertions.

### P2 — Purchase edits must explicitly protect acquisition-price linkage

**Where:** 03-contracts.md acquisitions/purchase transitions; T11/T02.r1.

KNOWN acquisition requires an allocation to that plant, with locking and unique constraints specified. Voiding a purchase correctly clears linked pricing. Editing a purchase can also remove/reassign/recreate allocations, yet the linked-acquisition behavior for that command is unstated. The invariant prevents accepting an invalid final state, but agents still need to know whether the edit is rejected, preserves stable allocation IDs, or atomically transitions affected acquisitions to UNKNOWN.

**Fix:** choose one policy and include linked acquisitions in the purchase-edit lock/revision set. Add edit/delete/reassign-allocation versus priceAcquisition race fixtures. Test both spending totals and acquisition-price projection after the edit.

### P2 — Small operation and UX inventory mismatches should be closed at SDL freeze

**Where:** 03-contracts.md top-level operation inventory; 04-experience.md Care composition; T09/T10/T23/T25.

Tags and tag filtering are promised and modeled, but no tag/link commands or explicit ownership through plant inputs appear in the API inventory. The Care composition also describes correction through activity edit/void, while the stronger contract requires linked completions to use correctOccurrenceCompletion. The backend rejection makes the latter safe, but the frontend packet can otherwise implement a failed user journey.

**Fix:** explicitly place tag management/linking in named operations or plant inputs, including revisions/idempotency. State that the activity UI routes linked completion corrections to the dedicated operation and preview. Include generated-operation/browser fixtures for both paths.

## Evidence and limits

`python3 docs/plan/validate_plan.py` exited 0: 48 executable nodes, four non-executable groups, acyclic DAG, R1 closure of 23 nodes without care/AI prerequisites, and final review dependency/link checks passed. Inspection confirms R1 capture and portability are split from later assistance and full-schema restore.

The exact SQL/SDL, supported package pins, constrained OpenCode execution, real vision capability, visual polish, and restoration performance are future work with explicit owners and acceptance gates. They have not been verified by this review. T00 is appropriately a disposable compatibility packet rather than an instruction to begin the whole application. No application or plan content was modified during this review.
