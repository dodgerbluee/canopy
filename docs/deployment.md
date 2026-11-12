# Deploy Canopy

The application is one image: `ghcr.io/dodgerbluee/canopy`. It includes the
frontend, API, worker, migrations and existing photo/receipt tools. Compose runs
two services: Canopy and PostgreSQL. No Docker socket or host-directory mounts
are required. Photo processing and receipt OCR run inside the app container.

## Portainer / Docker Compose

Use `deploy/compose.yaml` in a Docker Standalone stack. Set:

- `POSTGRES_PASSWORD`: a hex password (`openssl rand -hex 32`).
- `CANOPY_INITIAL_PASSWORD`: the login password to create on a new installation.
- `CANOPY_PUBLIC_URL`: your browser/agent URL, such as `http://192.168.1.10:7080`.

The app is available on port 7080. The initial password creates the first owner;
it never resets an existing owner. Existing data and the AI encryption key stay
in the persistent application volume. Database data has its own volume.
The default configuration matches the local application: HTTP and a database
reachable only on the Docker network. No database port is published.

For an HTTPS reverse proxy, set `CANOPY_PUBLIC_URL=https://your-hostname`.
Secure cookies are enabled for that URL; the frontend proxy preserves forwarded
scheme information from private-network proxies. Direct public HTTP exposure is
not intended. The existing production configuration remains available for
operators supplying their own TLS database and HTTPS setup.

The published Canopy image is public; registry credentials are not required.
For private forks, configure GHCR credentials with package-read permission in
Portainer or run `docker login ghcr.io` before pulling with Compose.

```sh
cp .env.example .env
# Fill the passwords and public URL.
docker compose pull
docker compose up -d
```

For a source build, tag the image named in `.env` first:

```sh
docker build -t ghcr.io/dodgerbluee/canopy:local .
```

## Photo processing

Starting with v1.1.2, libvips, HEIF decoding, Tesseract and Poppler run as
unprivileged subprocesses inside the Canopy container. The worker invokes only
these installed tools using direct arguments, with no shell or inherited database
or provider credentials. Each command has address-space, CPU, file-size and
open-file limits, capped captured output and a wall-clock timeout that kills its
process group. The photo pixel budget, metadata stripping and capture-date
handling remain unchanged. Temporary job directories are removed after use.

These subprocesses share the worker's UID and container filesystem/network;
they are not separate security sandboxes. Keep Canopy and codec packages updated.
The app has no access to the Docker daemon. The optional legacy Docker adapter
is reserved for development harnesses, not the published Compose deployment.

When upgrading from v1.1.0, remove the Docker socket and host work-directory
mounts, along with `TMPDIR`/`CANOPY_WORK_DIR` overrides. Keep both named data
volumes. No database or media migration is required.

## MCP and operator tools

MCP is at `CANOPY_PUBLIC_URL/mcp`. Create its scoped credential under Settings →
Agent connections. AI connections are configured in Settings; no provider keys
or personal garden records are baked into the image.

The existing operator CLI is included. In the Canopy container's console:

```sh
/usr/local/bin/admin owner create --workspace 'My garden' --timezone America/Chicago --currency USD
```

That prompts for a password if you did not use initial-password setup. The same
CLI supports owner recovery and encrypted backups/restores. Run it without
arguments for usage. This deployment starts a fresh garden; moving your local
one requires backup/restore, not mounting unrelated development files.

## Updates and releases

Back up before upgrading. Pin the image version and update the Canopy service;
its API, worker and frontend always move together. Migrations run before startup.
A child-process failure stops the bundled app so Docker restarts the whole
unit. Existing owner credentials are retained across restarts and recreation.

Main/master builds publish development tags. Version tags publish AMD64/ARM64
images, a deployment bundle, digest manifest and checksums. Manual promotion
retags the tested image digest as `latest`; it never rebuilds it. Configure
GitHub's production environment reviewers if promotion needs approval.

The release smoke test uses only a disposable two-service stack. It verifies
initial setup, login, SPA routes, MCP authentication, queued original-photo
processing, capture date, private previews and recovery from a worker exit.
No live development garden is used.

## First-time setup and users

Leave `CANOPY_INITIAL_PASSWORD` empty to create your first account in the browser.
An empty installation opens the setup screen: choose your username,
password (at least 10 characters), garden name, timezone and currency. Setup
creates one administrator and closes automatically. Local public registration
is unavailable afterward; administrators add users in Settings → Account & sign-in.

Upgrades retain the existing garden owner, password, garden IDs and data. The
original account starts with username `owner`; password-only API clients keep
working. Administrators can change usernames, roles and passwords or disable
accounts. Members can view/edit the shared garden but cannot administer users,
AI connection settings or agent credentials. Disabling an account or resetting
its password revokes its sessions. The final active administrator cannot be
disabled or demoted. The existing `admin owner recover` CLI restores the original
owner's local access, including administrator status, if needed.

### OIDC sign-in

Set `CANOPY_PUBLIC_URL` to the HTTPS address users visit (localhost HTTP works
for development). In Settings → Account & sign-in → Connect OIDC, register the
shown callback URL with your provider, then enter its exact issuer URL, client
ID and client secret. Use a confidential authorization-code web client with
PKCE S256 and `openid profile email` scopes. No extra container is required.
Authentik, Authelia, Keycloak and other standards-compatible providers can use
this generic connection; provider-specific live credentials are not included.

Test discovery to verify provider metadata. Then use “Connect” on your own
account to verify the client credentials and complete callback. Discovery alone
does not prove client authentication. Enabling the connection shows its button
on the login page. Password login remains available.

Existing users must explicitly connect SSO while signed in; matching an email
never links an account. Optional auto-join gives new SSO identities Member access
only. Enabling it admits everyone authorized for this client by your provider,
so restrict the application's audience there. An SSO-only member needs a local
password set by an administrator before disconnecting SSO.

Client secrets are encrypted using `keys/oidc.key` in the application storage
volume. Preserve that file alongside your database and media when migrating.
The operator backup includes user records, encrypted connection settings and
identity links; login sessions and pending SSO attempts are excluded. Restore
disables SSO until the public URL/key are verified and an administrator enables
it again. Re-enter the client secret if the encryption key was not transferred.

### Re-register an existing garden after migration

An operator can prepare a one-time registration handover without moving or
recreating the garden's records:

```sh
admin owner handover --url https://your-canopy-host --output /private/registration.url
```

Run this against the migrated installation after making a consistent database
and complete storage backup. The private output file contains a one-use link,
valid for 24 hours. Open it locally for the owner; never place it in a public
log or shared source repository. The link uses a URL fragment and exchanges for
an HttpOnly SameSite cookie, then clears the fragment before registration.

Preparing handover disables existing browser accounts, revokes sessions and
turns off previous OIDC sign-in. The registration keeps the existing primary
account, garden owner/workspace IDs and all related items; it changes the login
credentials to the username/password the owner chooses. Existing garden name,
timezone and currency remain unchanged. The old primary SSO link is removed;
the owner can reconnect OIDC afterward. The capability is consumed atomically.
No public signup or unprotected claim is opened on an existing installation.

Operator `admin owner recover` cancels pending handover links and restores local
owner access if the handover needs to be abandoned. Preserve the original
installation and verified backup until migration and owner access are confirmed.
