# Public acceptance record: UI31

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| 2026-11-11 | UI31 | ACCEPTED | Desktop Rounds removed; grouped header photo/watering/edit actions. Fixed history evidence prompting and bounded repair diagnostics; Meyer lemon rerun succeeded through live UI/queue, original failure retained. 73 frontend tests, insights/GraphQL suites, five accessible browser layouts and real phone check pass. Matched runtime and frontend published. Evidence: `.artifacts/agents/UI31/`. |

# UI31 — Plant header quick actions and reliable history evidence

Status: ACCEPTED.

Rounds is removed from desktop primary navigation and remains available in the mobile bottom navigation. Plant identity now groups camera, watering and pencil actions together, removing the duplicate camera overlay from the hero. Photo opens the existing capture-date-aware queue. Water opens the shared entry modal with Water preselected, editable local date/time and optional quantity; save refreshes the mounted Journey. The modal tracks actual edits, guards dismissal while saving, and preserves drafts on Cancel/Escape. Other quick-log entry points retain their Note default; editing retains the stored activity kind.

Diagnosis: the failed Meyer lemon SYNTHESIS run cited plant.id as an evidence ID, which the validator correctly rejected. Its bounded repair also failed. The selected OpenAI-compatible connection previously received an ambiguous instruction to use snapshot IDs and a truncated prior reply without the validation diagnostic.

The text-history prompt now enumerates the same sorted closed evidence set as the validator, explicitly excludes plant/workspace/asset/location identities, permits empty citations for profile facts, distinguishes photo metadata from visual evidence, and requests concise arrays within validation limits. Repair includes the exact validation reason plus bounded prior output as untrusted data. Strict result/evidence validation and the one-repair budget remain unchanged. Final invalid-repair failures now have a clearer user message and diagnostic logging. A local-model replay of the original stored input succeeded; a subsequent explicit UI rerun completed through the real queue on its first worker attempt. The original FAILED record remains unchanged and the new run is SUCCEEDED. No advice was accepted or applied to care records.

Executed evidence under `.artifacts/agents/UI31/`:

- `history-diagnosis.json`: offending reference equals plant.id; snapshot has four photo records and six activities.
- `history-replay.log`: closed-evidence/repair regression and opt-in real managed Strata replay pass; six observations and two hypotheses validate, with no replay DB mutations.
- `backend-tests.log`, `vet.log`: insights and GraphQL suites pass against disposable PostgreSQL schemas; insights vet passes.
- `frontend.log`: 73 Angular tests pass, including desktop/mobile navigation and Water creation default with unknown quantity blank. `build.log`: final production build passes using Node 24 Docker.
- `browser-report.json`, `browser.log`: five phone/desktop light/dark layouts pass; header actions, Water form/save, photo modal, desktop Rounds omission/mobile retention, existing filter/edit flows and zero axe/browser errors. Mutations use synthetic fixtures.
- `live-browser.log`, `live-plant-phone.png`: actual authenticated Android-emulated garden shows the three header icons; Water opens correctly and is dismissed without saving, desktop/mobile navigation and existing dialogs work. No garden mutations from this layout check.
- `live-history.json`, `live-history.log`, `live-worker-proof.json`: real UI retry of Meyer lemon produces a new SUCCEEDED run, completed queued analysis job, original failure preserved. Suggestions remain proposals, no care acceptance.
- `runtime-live.json`, `restart.log`: matched API/worker deployment preserves database, media storage, selected AI connection and MCP configuration.
- `published.json`, `local-release.json`: final frontend published identically on all three local previews; API ready. Task-specific QA container stopped.
- `plan-boundary.log`: plan/packet/dispatch and import checks pass.

The model remains probabilistic; unexpected results still fail validation rather than being saved as verified insights. Text history reads records; visual analysis remains the separate photo comparison action.
