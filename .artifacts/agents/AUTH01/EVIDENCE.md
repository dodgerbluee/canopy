# AUTH01 executed verification

Implemented in the Canopy checkout, without changing Docked or the live local
garden. Docked's first-user administration and SSO flows informed this design.
The existing garden owner remains the data scope; separate human identities
share that garden with Administrator/Member access.

Executed checks:

- All database/backend integration packages passed against disposable
  PostgreSQL 17 schemas (`go test ./db/ ./internal/... -count=1` with PGTEST_DSN).
- Final identity, HTTP auth and portability suites passed after review fixes;
  OIDC-specific tests passed again after adding PKCE/cancellation assertions.
- Eight concurrent setup calls create exactly one owner, user and garden.
- A version-16 upgrade retains the original password, garden IDs and session.
- Disabled accounts and password resets revoke sessions. Members cannot manage
  users, OIDC settings, AI credentials or agent access; GraphQL aliases and
  fragments cannot bypass the credential permissions. Final-admin protection
  is enforced transactionally.
- A local issuer serves real discovery/JWKS/token endpoints and signs ID tokens.
  Tests cover explicit linking/login, Member-only auto-join, disabled identity
  rejection, state/browser binding, replay/expiry, changed provider settings,
  revoked linking sessions, PKCE, issuer/audience/nonce/signature/token expiry,
  and cancellation. Secrets are encrypted and absent from public settings.
- Operator backup/restore preserves users and identity links and disables SSO
  until URL/key verification. Sessions/attempts are excluded from backups.
- All 485 Angular tests and the production build passed. A competing image
  build caused three unrelated existing tests to time out on an intermediate
  run; the unchanged timeout settings passed when rerun without that contention.
- Playwright passed on the actual combined image: empty-instance setup,
  desktop/mobile layouts, accessibility checks, admin account creation,
  last-admin rejection, member access and password change, including correcting
  an invalid current password without losing the authenticated session.
- Visually inspected mobile setup and account settings screenshots. Corrected
  setup/header overlap and used existing spacing tokens throughout the panel.
- The final image smoke passed actual queued photo processing, retained capture
  date, private preview delivery and supervised worker failure/restart with
  existing owner credentials and stored media retained. Exactly two Compose
  services are used.
- Go vet, reproducible sqlc generation, plan/packet/dispatch validation,
  boundary checks and diff whitespace checks passed.

Detailed logs/screenshots are ignored artifacts beside this summary. The
separate `canopy-auth01` Docker project runs at http://localhost:7081. Browser
test records were removed afterward so the preview starts with first-time setup.
The published v1.0.1 image and original live garden were not updated.

External provider credentials were not available. OIDC protocol behavior was
verified against the signed local test issuer, not a user's live identity
provider. Settings supplies the exact callback URL and a discovery test; a full
account-link callback verifies client credentials when configured.
