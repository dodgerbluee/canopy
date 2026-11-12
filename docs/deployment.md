# Deploy Canopy

The application is one image: `ghcr.io/dodgerbluee/canopy`. It includes the
frontend, API, worker, migrations and existing photo/receipt tools. Compose runs
two services: Canopy and PostgreSQL. No application features were redesigned.

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

The package is private until its owner changes GitHub Packages visibility.
Configure GHCR credentials with permission to read the package in Portainer,
or run `docker login ghcr.io` before pulling with Compose.

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

The existing Docker-based sandbox is retained. The app launches short-lived,
network-disabled codec jobs using its own already-pulled image. These are not
additional Compose services. The Docker socket and work-directory mounts in the
Compose file support that existing behavior. The app detects the socket group
and image name automatically. It does not need registry credentials inside it.
The work directory must retain the same absolute path inside the app and on the
Docker host; the published Compose file already does this.

The worker has Docker-host authority through that socket, as before. The API
runs as UID 10001 without the worker's socket group. Codec jobs receive only
their temporary input/output directory, never the application's data volume.

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
