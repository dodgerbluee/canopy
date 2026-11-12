# Public acceptance record: UI02

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| UI02 | 2026-11-03 | Consistent shared modal data entry across shipped frontend; native choices retained; common spacing/actions, dirty/busy/navigation guards, media recovery and explicit AI/receipt review preserved | `.artifacts/agents/UI02/` (SUMMARY.md, final-unit.log, full-e2e.log, final-build.log, final-boundary.log, final-plan.log, final-packet.log, routed-smoke.json, journal-smoke.json) | Node24:317 Angular units passed with no unhandled errors;17 real-stack browser journeys passed; production build, boundary and planning checks passed; desktop/mobile light/dark axe smoke passed. No backend/contracts/dependency changes or broad make-check claim. |

# UI02 — consistent entry system

Owner request2026-11-03: all garden data entry consistent; popup modal forms preferred over inline/expanding entry; owner explicitly clarified native choice fields stay; spacing and common look/feel throughout. Baseline603fb8e. Work in isolated /Users/dodgerbluee/dev/plant-tracker-ui02; primary checkout and running UI remain untouched until verified integration. All application coding goes through sequential local Strata sessions; root handles QA/review/metadata.

1. Foundation: extend existing CDK dialog mechanics; declarative UiEntryDialog; native choice fields retained; shared spacing/form classes. Lock API before consumers.
2. Route forms and choice migration: plant/purchase/care-plan route modals over parent pages; reuse existing forms, validation and mutations. Native select values/ReactiveForms/FormData preserved unchanged.
3. Inline forms: journal log/edit, plant moves/tags/upload metadata, care complete/snooze/correct flows all modal. Queue recovery preserved.
4. New insights/identification/receipt-review/confirmation + account/card actions consistent with foundation. No automatic billed analysis or accepted suggestions; actual domain consent/revisions preserved.
5. Independent validation/repairs/real-stack QA; publish verified local Docker build and integrate only owned source paths. No broad make check or main service restarts/deletions.

Shared look: regular680px/wide960px dialogs, phone fills available viewport with12px gutters, fixed header, contained scroll, consistent24px section spacing /16px field gaps /8px label-hint gaps /44px targets, shared action row with Cancel left and primary right. Preserve existing forest/editorial visual design, both themes, reduced motion. Login remains its authentication screen with the same control styling. No demo data.

Never run bare host node/npm/npx. Implementers do not build/test (root owns pinned Node24 checks with explicit isolated outputs and dependency mounts). Do not read credentials, change deps/contracts/backend, write ledger/task status, commit, or touch primary checkout. Use bounded small edits; never invent competing component APIs.

Owner clarification received during foundation read: “Modal forms, but keep native choice fields.” Initial picker plan cancelled before any source edits. No custom picker/directive is required. All forms retain native selects/radios.

Completed and verified:317 Angular units,17 real-stack browser journeys, production build, boundary/plan/packet/dispatch checks, desktop/mobile light/dark axe smoke. See `.artifacts/agents/UI02/SUMMARY.md`. No backend/contracts/dependency changes; no broad make-check claim.
