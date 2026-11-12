# Independent round-2 addendum

Reviewed 2026-11-01 against the current artifacts. This addendum verifies the five findings in round-2.md; it is not a runtime or implementation review.

**Updated grade: A — 96/100. No P0/P1, and no remaining P2 from round 2.** The corrections resolve the identified gaps without changing the architecture or coupling journal delivery to AI.

| Section | Updated score |
|---|---:|
| Product completeness | 15/15 |
| Architecture and simplicity | 14/15 |
| Contracts and data correctness | 19/20 |
| Testability and failure recovery | 19/20 |
| UX and accessibility | 10/10 |
| Delegation executability | 14/15 |
| Honesty and scope | 5/5 |
| **Total** | **96/100** |

## Verification of corrections

1. **Cumulative gates — resolved.** task-graph.json includes prior release gate requirements through R4. validate_release_gates checks closure inclusion, and its negative missing-T38.a fixture actually exercises the previous omission. Final T41 closure still covers all executable nodes.
2. **Billable retries and uncertain usage — resolved.** The budget contract now reserves before each billable attempt, includes repair requests, retains encumbrances after potentially charged timeout/cancel, and requires authoritative reconciliation. Integer micro-USD, UTC dispatch-month attribution, locking, and explicit adverse cases substantially reduce agent interpretation.
3. **Interval skips — resolved.** A separate rule cursor and dated examples specify original-due-date skip advancement, repeated skips, snooze interaction, and corrections after a later skip. Undo semantics now consistently regard both COMPLETED and SKIPPED as later resolved decisions.
4. **Acquisition-linked purchase edits — resolved.** Retained allocations keep IDs; removal/reassignment requires explicit unlinkAcquisitionIds confirmation and an atomic UNKNOWN transition. FK RESTRICT, ordered purchase/acquisition locks, revision increments, and dependent-analysis invalidation close the missing transition.
5. **Tags and correction UX — resolved.** Named tag operations and assignment/archive behavior are explicit. Care UI now routes linked completion corrections through correctOccurrenceCompletion and its preview, while ordinary activities retain edit/void behavior.

No new material regression was found in the changed contracts or gate structure. Remaining score deductions reflect the substantial implementation/packetization work still to prove, rather than an unreported blocker: exact SQL/SDL and executable downstream packets do not exist yet, and toolchain, sandbox, visual, AI, and recovery claims remain future acceptance evidence.

## Evidence and limits

Ran `python3 docs/plan/validate_plan.py`: exit 0. It reports 48 executable nodes, four non-executable groups, an acyclic DAG, a 23-node R1 closure without later-release dependencies, cumulative gates including the negative portability fixture, staged schema freeze, final restore/review dependencies, and valid local links.

Inspected the exact revised acquisition, tag/API, recurrence/correction, and budget contracts; Care screen specification; canonical gates; and validator implementation. No application or plan files were changed by this reassessment. Only this addendum was written. Passing remains a plan-readiness grade, not evidence of an implemented application.
