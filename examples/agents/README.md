# Connect an assistant to Canopy

Create a dedicated connection in **Settings → Agent connections**. Start with read access and selected plants. Save the one-time token in the agent host's secret/environment mechanism as `CANOPY_AGENT_TOKEN`. This is a Canopy agent credential, separate from the login password and AI provider key. Expiry, permissions, rotation, revocation and last successful tool call are managed in Canopy.

The server must enable `CANOPY_MCP_ENABLED=true`. HTTP MCP is at `/mcp`; uploads and exports use `/agent/uploads/{assetID}` and `/agent/exports/{id}/download`. All require the connection bearer credential. A local browser preview URL does not necessarily proxy these routes. Loopback refers to the machine running the assistant. Remote clients require a reachable HTTPS endpoint; no public tunnel is created by this implementation.

## Hermes

Merge `hermes-http.yaml` into the Hermes configuration, adjust the endpoint, and restart Hermes. Its environment resolves `${CANOPY_AGENT_TOKEN}`. The include list narrows the server grant and never expands it. Add write tools deliberately when granting logging or reviewed changes.

For command-based MCP, build the adapter using Go 1.26:

```sh
cd server
go build -o canopy-mcp ./cmd/canopy-mcp
```

Install that binary on the agent's computer and use `hermes-stdio.yaml`. The adapter inherits `CANOPY_AGENT_TOKEN` from its environment. It forwards named tools and image/structured results over MCP stdio, has no database credentials, and writes no diagnostics to protocol stdout. Restart the adapter after changing permissions to refresh discovery; calls always recheck authorization on the server.

## Grok Bot and other clients

Use the client's custom MCP URL connection and its supported bearer-secret mechanism. If command-based connections are supported, install the adapter on that client's computer; a path on this development Mac cannot be used by a hosted Bot. If the chosen connector requires OAuth instead of bearer or command support, this release cannot authenticate it: OAuth is a separate extension. Do not present an example configuration as a connected Bot.

`xai-remote-tool.json` is an API caller template for the xAI remote MCP tool, distinct from installing a Grok Bot connector. Inject the dedicated token at runtime. Actual external accounts, hosted reachability and client-specific auth flows must be verified in the intended account.

## Upload original photos from the agent host

The helper reads only the file explicitly passed by its local caller. The Canopy server never reads an agent-provided local path or fetches a photo URL.

```sh
canopy-mcp --url http://127.0.0.1:8080/mcp \
  --upload /path/on/agent/host/original.jpg \
  --plant 22222222-2222-4222-8222-222222222222 \
  --operation camera-import-2026-11-06-file1 \
  --timezone America/Chicago
```

Grant `media:write` and the selected plant. Original bytes are checksummed and transferred unchanged. The existing worker processes the upload; attachment prefers the original camera capture timestamp, interpreted using the specified timezone. If metadata is absent, the app reports upload fallback rather than inventing a capture date. The helper returns JSON after attachment. Retry the same file with the same operation identity after an uncertain response; choose a new identity for a different file. Processing timeout is explicit and may be retried. The helper rejects redirects and sending the credential to a different origin. HTTP is allowed only for loopback; remote connections use HTTPS.

## Writes, proposals and schedules

Watering without a measured amount remains unmeasured. Rain records an area event and does not complete each plant's watering. Direct logging uses saved connection permissions. Identity edits, historical corrections, spending, care schedules, suggestions and exports create immutable proposals: the owner reviews and approves them in Settings. Agents cannot approve their own proposals. Reuse `operation_id` for retries and query `operation_status` or `proposal_status` after an uncertain reply.

Seasonal windows are source-backed estimates, not ripening deadlines or scheduled care. Unlogged work is unknown. Schedule periodic reviews in the assistant's scheduler; Canopy does not add a second agent scheduler.

## Compatibility evidence

Pinned Go MCP SDK: `github.com/modelcontextprotocol/go-sdk v1.8.0`. Tests execute a real stdio child process against its Streamable HTTP server, discover tools, forward image/structured results, reject post-revocation calls, and transfer original Android photo bytes with stable retry identities. The application's separate integration suite tests the real PostgreSQL permissions and worker path.

Native Hermes and Grok Bot account onboarding remains **unverified**: neither client/account was available in this environment. SDK protocol interoperability is tested; it is not a substitute for those native-client checks. No external account was configured and no public endpoint was opened.

Upstream setup references checked November 6, 2026: [Hermes MCP](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp), [Grok Bot computer and apps](https://docs.x.ai/grok-bot/computer-and-apps), [xAI remote MCP](https://docs.x.ai/developers/tools/remote-mcp). Grok Bot account-wide computers and credentials are shared across its Bots; use separate Canopy connections for independently revocable clients.
