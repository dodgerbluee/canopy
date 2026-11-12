# AUTH01 — Registration, user management and OIDC

Status: ACCEPTED.

Docked reference: browser first-user setup, administrator-managed users and configurable SSO providers. Canopy adaptation keeps the existing garden owner as the data boundary and adds user identities/sessions within that garden. No automatic email matching or public local registration after setup. Existing owner becomes administrator `owner`, retaining their password and garden IDs. New installations choose their username, password, garden name and timezone. Local recovery is retained.

OIDC uses the authorization-code flow with PKCE and verified ID tokens, via coreos/go-oidc and x/oauth2. One configurable generic OIDC connection supports compatible providers. Users explicitly link their identity while signed in; optional auto-enrollment creates Members, never administrators. Configuration secrets are encrypted under persistent application storage and omitted from responses.

Protocol references: https://openid.net/specs/openid-connect-core-1_0.html and https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc.


Executed verification: all backend/database integration packages, 485 Angular
unit tests, production build, Go vet, generation reproducibility and plan/boundary
checks passed. Signed local OIDC tests include adversarial claims/state/PKCE and
revoked links. Playwright exercised first registration, admin/member flows,
mobile dialogs and accessibility against the final combined Docker image.
The image smoke processed a queued photo and recovered from worker failure.
Upgrade and backup/restore checks preserve the existing garden and users.

Evidence: `.artifacts/agents/AUTH01/EVIDENCE.md` and ignored detailed logs beside it.
Preview: http://localhost:7081, a separate empty garden left at first-time setup.
The current published release was not changed or deployed remotely. External
OIDC providers still require their real issuer/client credentials in Settings.
