# Strata / OpenCode delegation protocol

## Verified local environment

Inspected 2026-11-01:

- CLI: `/Users/dodgerbluee/.opencode/bin/opencode`, version **v2.0.29**.
- Config: `/Users/dodgerbluee/.config/opencode/opencode.jsonc` (contains credentials; never attach/copy into prompts/repository).
- Provider alias: **strata**; model alias: **flash-next**; provider model ID: **qwen3.8-flash-next-iq3_s**.
- Variants: **fast** (reasoning none), **deep** (reasoning high); configured default reasoning low.
- Advertised context 131,072, input 114,688, output 16,384; **text input/output only**, tools supported. No vision capability inferred.
- CLI model selection syntax from installed help: `--model provider/model#variant`.
- A real read-only run with `strata/flash-next#fast` returned `STRATA_READY`. Sandbox initially prevented OpenCode log/server startup; the verified check used a permitted escalated run. Future sandboxed launches may require the same environment permission. No config edits were necessary.

Do not copy the LAN endpoint or credential into application config by default. Runtime plant analysis has its own provider configuration and capability tests. Coding access is not evidence of image-analysis capability or permission to send private data to a different provider.

## Roles and authority

Primary integrator owns architecture, task selection, contracts, integration, final acceptance. Local model implements exactly one task packet in a fresh session. Another fresh local session reviews its diff; integrator verifies evidence. The fresh independent planning grader sees only artifact paths and rubric, no conversation history; its grades are evidence, not a guarantee of runtime correctness.

The user requested planning and local-model task delegation; current turn creates the plan and review artifacts. Do not start all implementation tasks during planning. During later execution, all coding tasks use Strata unless user changes the assignment. If model cannot complete a task, split/revise packet or report blocked; no silent cloud-model implementation fallback.

## Task size and execution

- Default **one active local coding session**, due to one configured model endpoint and unmeasured capacity. Do not assume parallel requests improve throughput. Increase to two only after T00 bench confirms memory/latency and each task has disjoint files. Worktrees protect files, not shared GPU memory.
- One task should fit 30–90 minutes of model work, ideally <=8 human-authored files and <=500 authored lines per submission; these are split triggers, not permission to omit needed behavior. Tests/fixtures may exceed size with explicit explanation. If larger, split into suffix packets Txx.a/Txx.b before dispatch, each with independently checkable behavior.
- Read only task packet + relevant contract excerpts + interfaces + tests + narrow source files; target <=20k input tokens. Never attach the whole repo or all plan docs routinely. Prompt requests a compact final report, not giant inline generated files.
- No architecture choices, dependencies, schema edits, migration numbering, or global config changes outside packet ownership. Generated files changed only through approved generators. Dependency addition requires integrator decision/update to lockfile in its own serialized task.
- Integrator reserves migration numbers before dispatch. API/schema/codegen/global style/lockfile changes are serialized ownership lanes. All merges go through integrator; no two agents edit shared glue concurrently.
- Each task has base commit, branch/worktree, allowed paths, read paths, predecessors, fixture identifiers, acceptance criteria, exact test commands, prohibited shortcuts, and report format.

## Command template (verified CLI syntax)

Create an isolated git worktree after T03 initializes the application repository and its initial accepted commit. Do not auto-init git or commit this plan in the current planning turn. Use an integrator-created UTF-8 prompt file; pass file path via structured tool argument or proper shell quoting. Do not interpolate model text into shell.

```sh
# Run from the task worktree, not the owner's home directory.
opencode run --standalone \
  --model 'strata/flash-next#deep' \
  --format json \
  --file '/absolute/path/to/T09-prompt.md' \
  'Implement only the attached task packet. Read AGENTS.md first. Report evidence.'
```

Use default/deep for implementation and reasoning; fast for bounded formatting/docs/fixture work only after benchmark. Do not use `--continue` between unrelated tasks; no inherited assumptions. Review uses a fresh run with a read-only worktree and review packet. Do not pass `--auto`: task-level writes are authorized through constrained policy, not blanket shell/network permissions. Verify installed OpenCode agent permission configuration during T00 before automating unattended work. A prompt allowlist alone is not a sandbox: integrator enforces diff allowlist and uses isolated worktree, no production credentials, restricted network, and tool permissions where supported.

OpenCode logs include session IDs/output; store transcripts outside git in ignored `.artifacts/agents/Txx/`. Redact secrets and personal data. Do not alter shared OpenCode config to run tasks. Use the existing configured provider/model alias.

## Task packet template

```text
ID / title / release / model variant:
Goal: one observable behavior.
Base commit / worktree:
Dependencies and accepted artifact versions:
Read first: AGENTS.md + exact relevant sections/files.
Allowed writes: explicit globs; forbidden writes: contracts/lockfiles/etc unless named.
Inputs and fixtures: names, shape, expected values.
Contract: exact function/use-case/operation signatures from accepted T01/T02 artifacts.
Implementation steps: 3–7 concrete steps, no architectural alternatives.
Acceptance: numbered observable assertions.
Tests: exact executable commands, expected exit/result; one negative/concurrency case as relevant.
Non-goals / stop conditions:
Output: changed paths, behavior, tests with real output, risks, unresolved questions.
```

07-tasks supplies scope and acceptance per task. T00/T01.r1/T02.r1/T03 establish R1; T01.care/T02.care and T01.ai/T02.ai freeze later release contracts just before those coding lanes. T01/T02/T32/T38 are non-executable groups; canonical suffix DAG lives in task-graph.json. Once accepted, integrator materializes each task into `docs/plan/tasks/Txx.md`, replacing references with exact accepted signatures and available commands before dispatch. That is deliberate: do not pretend non-existent file line numbers or tests are already known. A task is NOT READY while any field, interface name, allowed path, or test command is unresolved. Generated downstream packets are reviewed for consistency, not freeform interpretation.

## Review and integration gate

1. Integrator checks dependency evidence and clean task worktree; creates narrow packet.
2. Local model implements; no other agent edits that worktree.
3. Collect diff, changed-path allowlist, test logs, dependency changes, TODO scan. Task report alone is not proof; rerun relevant commands independently.
4. Fresh Strata review inspects diff against contract, tests, negative cases, maintenance cost. It cannot edit. Report P0/P1/P2 with paths and actionable findings. UI tasks also require actual browser screenshots; text-only model cannot visually approve them.
5. Return only findings to same task session for correction or fresh repair session; maintain same base/scope. Two failed repair cycles trigger integrator split/root-cause revision, not weaker gates.
6. Integrator accepts patch only when no open correctness/security P0/P1; P2 accepted only with explicit tracked rationale and no release blocker. Run affected cross-module contract/integration checks after merge.
7. Update task ledger with commit, evidence links, reviewer, status, deviations. No large unreviewed “integration cleanup” at the end.

Task statuses: PLANNED → READY → RUNNING → REVIEW → ACCEPTED; BLOCKED records concrete dependency/error; SUPERSEDED points to split/replacement. ACCEPTED means checks actually executed, not planned.

## Planning grade protocol

Fresh grader reads all eight plan docs and optionally reports discovered ambiguity. Weighted score: product completeness 15, architecture/simplicity 15, contracts/data correctness 20, testability/failure recovery 20, UX/accessibility 10, delegation executability 15, honesty/scope 5. A >=90/100 with no P0/P1 and no section <70% earns A; 97+ A+. Otherwise revise artifacts, preserve original review, ask fresh grader to reassess independently. Never ask grader to award A or hide unresolved findings. No forced iteration limit; external missing facts remain explicit configurable assumptions and must not be fabricated to get a grade.
