# Publishing and running Canopy

Canopy follows Docked's release flow: development images from main/master,
versioned releases, then manual promotion to latest. GitHub Actions publishes
three images for linux/amd64 and linux/arm64:

- `ghcr.io/OWNER/REPO-server`: the matching API, worker, migration, admin and MCP adapter binaries.
- `ghcr.io/OWNER/REPO-web`: Angular plus Caddy's TLS termination and same-origin API/MCP proxy.
- `ghcr.io/OWNER/REPO-media`: the existing isolated HEIC/JPEG/OCR runtime.

Names are derived from the repository in lowercase. No Docker Hub credentials
are needed. Workflows use the repository's `GITHUB_TOKEN` for GHCR. For public
installs, set each package's visibility to public in GitHub Packages; visibility
is not changed automatically. Private installs must log in to GHCR with a
credential authorized to read those packages.

## GitHub workflows

1. **CI** runs on pushes/PRs and is reused as a publishing gate.
2. **Pre-release (dev)** runs on main/master, or manually from those branches.
   It tests, builds and smoke-tests the complete stack, publishes
   `dev-FULL_COMMIT_SHA`, then advances the three `dev` aliases only once all
   images succeed. The deployment bundle is available as an Actions artifact.
3. **Release** runs for `vMAJOR.MINOR.PATCH` or a prerelease such as
   `v1.0.0-rc.1`. After the same gates it publishes versioned images and a GitHub
   release with `images.json`, deployment tarball and SHA256 checksums. No release
   automatically changes `latest`. Existing version tags cannot be overwritten;
   rerun only failed jobs after a partial failure, or choose a new version. Build-metadata tags (`+...`) are rejected.
4. **Promote release to latest** takes an existing stable version, downloads its
   successful release manifest, checks all three current version digests, and
   retags those exact digests as `latest` without rebuilding, and marks that GitHub
   release as latest. Prereleases cannot
   be promoted. Configure required reviewers on the GitHub `production`
   environment if approvals should be required; naming the environment alone
   does not enable reviewers.

All publishing/promotion workflows share one concurrency group. Registry tags
are separate writes, so a transient failure can leave channel aliases partially
advanced; retry promotion, and use a pinned version for deployments. The release
manifest records the authoritative digests. Promotion verifies them before and
after retagging. Release actions publish artifacts; they do not SSH into a host,
change DNS, start a tunnel, or deploy over the running local garden.

The workflow definitions must be committed and pushed to a GitHub repository.
Create a release by tagging the intended tested commit, for example:

```sh
git tag v1.0.0
git push origin v1.0.0
```

## First deployment

Use a Linux Docker host with the Compose plugin, public DNS pointing at it, and
ports 80/443 available. Extract the release deployment tarball into a dedicated
directory, or use `deploy/` from the repository. Verify `SHA256SUMS` first.

```sh
cp .env.example .env
chmod 600 .env
# Edit .env: lower-case image prefix, version, DNS hostname, random DB password,
# absolute work directory and Docker socket group.
openssl rand -hex 32
stat -c '%g' /var/run/docker.sock
docker compose pull
docker compose up -d
```

Do not include a scheme or port in `CANOPY_SITE`. Caddy obtains and renews HTTPS
certificates. Only the web service publishes host ports; API and PostgreSQL stay
on the Compose network. PostgreSQL gets its own generated TLS certificate and
clients use `verify-full` against the shared public certificate. Private TLS key
and certificate volumes are separate; keep these along with the database/media
and Caddy volumes when upgrading. The database certificate lasts ten years;
rotate its private/public pair together before expiry and restart database,
API and worker so they trust the new certificate.

Initialization sets UID 10001 ownership on the dedicated media/work directories;
migrations must succeed before API/worker start. The API and worker always use
the same server image and storage volume. AI connection encryption keys live in
the private media volume and must be retained with the data.

Create the owner through the bundled operator CLI; it prompts for a password:

```sh
docker compose run --rm admin owner create --workspace 'My garden' --timezone America/Chicago --currency USD
```

Then visit `https://YOUR_HOSTNAME`. Configure your AI connections in Settings.
No Strata endpoint, model credential or local garden data is baked into an image.
The bundled admin also provides owner recovery and encrypted backup/restore;
run `docker compose run --rm admin` for its command reference. Mount your own
backup destination and key file when using those commands.

## Media processing and Docker host access

The worker launches restricted sibling codec containers through the Docker
socket: no network, limited RAM/PIDs, and a dedicated work-directory bind mount.
The worker itself therefore has Docker-host authority; run this stack only on a
host where that is acceptable. The API and web containers do not mount the
socket. Set `DOCKER_GID` to the socket's group. `CANOPY_WORK_DIR` must be an
absolute directory mounted at exactly the same path inside the worker and on
the Docker daemon's host; a named volume or a different container-only `/tmp`
path will break processing. Do not share this directory with other applications.
The `media-image` service pre-pulls the matching codec image into that daemon.

## Remote MCP

MCP is enabled by default in the deployment template at
`https://YOUR_HOSTNAME/mcp`, using Streamable HTTP. Create a scoped credential in
Settings → Agent connections and send `Authorization: Bearer TOKEN`. App
passwords and AI-provider keys are separate. The proxy also forwards authenticated
agent upload/export routes. Set `CANOPY_MCP_ENABLED=false` to disable MCP.
If an external proxy changes the public authority, explicitly set matching
`CANOPY_MCP_PUBLIC_URL` and `CANOPY_MCP_ALLOWED_HOSTS` in the Compose environment.

## Upgrades and rollback

Back up first. Pin `CANOPY_VERSION` to a tested version (or use manifest digests
in an override file), then update API/worker/web/media together:

```sh
docker compose stop api worker web
docker compose pull
docker compose run --rm migrate up
docker compose up -d --force-recreate
```

Stopping both backend components before migration avoids running an old worker
against a newly migrated schema. Check `docker compose ps` and
`https://YOUR_HOSTNAME/readyz`, then perform a photo upload to verify background
processing. Never remove persistent volumes during upgrades.

For rollback, restore the pre-upgrade backup into a separate/blank deployment
using the matching release and documented admin restore command. An older image
is safe against a newer schema only when that release explicitly documents the
compatibility; do not automatically run down-migrations or overwrite the garden.

## Local release verification

The pipeline builds actual release images and runs `scripts/release/smoke.py`.
It creates a unique disposable Compose project, TLS-enabled database, owner and
media volumes; checks login, SPA deep links, MCP authentication, original Android
photo upload, queued sandbox processing and capture-date attachment; then removes
only that project's volumes/work directory. It never uses the local development
DB, owner password, AI configuration or garden. Localhost's disposable Caddy
certificate is untrusted only in that smoke client, without weakening production
TLS or database verification.

Workflow structure follows [GitHub reusable workflow documentation](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows)
and [Docker’s multi-platform publishing guidance](https://docs.docker.com/build/ci/github-actions/multi-platform/).
