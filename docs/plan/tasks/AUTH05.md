# AUTH05 — Registration simplification

Status: ACCEPTED.

Removed registration name entry and defaulted display name to username in the
backend. Lowered minimum password length to ten characters across setup,
password changes and admin account creation/reset. Retained existing accounts.

Verified backend identity/HTTP suites, password boundaries and name-free setup,
493 Angular tests, production image build, desktop/mobile browser checks and
queued photo/restart smoke. Local preview refreshed without data reset.
Evidence: `.artifacts/agents/AUTH05/EVIDENCE.md`.
