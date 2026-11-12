# Canopy — plant tracker

Canopy has an Angular frontend, a Go GraphQL API, and queued background workers. The documents below describe the implementation plan and its verification contracts.

Build a private, polished plant journal with an Angular frontend and a Go GraphQL backend. Reuse maintained infrastructure libraries; own the plant model, journey, contextual care, and evidence-based insights.

Read in this order:

1. [Product and delivery scope](docs/plan/01-product.md)
2. [Architecture and engineering decisions](docs/plan/02-architecture.md)
3. [Data, API, and behavior contracts](docs/plan/03-contracts.md)
4. [Visual and interaction specification](docs/plan/04-experience.md)
5. [Verification and operations](docs/plan/05-verification.md)
6. [Strata delegation protocol](docs/plan/06-delegation.md)
7. [Dependency-ordered implementation tasks](docs/plan/07-tasks.md)
8. [Sources and decision log](docs/plan/08-sources.md)
9. [Canonical task DAG](docs/plan/task-graph.json) and [plan validator](docs/plan/validate_plan.py)
10. [Independent reviews](docs/plan/reviews/)

Working name: **Canopy**. Naming is provisional and does not affect package names or require branding approval to begin.

The approved stack is Angular + Go + GraphQL. All remaining choices in these documents are concrete proposed defaults, with documented evidence and tests. Implementation begins with task T00, not with generating the entire app in one model session.

Validate planning links and dependency invariants with `python3 docs/plan/validate_plan.py`. This does not run or validate application code.

### Receipt image models

When a vision provider is configured, receipt images use that model's native
image input. The editable result is validated before review; extraction never
creates a purchase. PDF receipts retain the existing OCR path. Without a
vision provider, Tesseract reads text and the conservative local parser may
leave many fields blank.

Configure the API and worker with the same `CANOPY_VISION_BASE_URL` (origin,
without `/v1`), `CANOPY_VISION_MODEL`, and `CANOPY_VISION_API_KEY` environment
variables. For the current self-hosted Strata deployment the model ID is
`qwen3.8-flash-next-iq3_s`. Set `CANOPY_VISION_LOCAL=1` only when the endpoint is
part of the same self-hosted deployment; the default reports data as leaving
the deployment. Keys stay in process configuration and are never returned in
provider status. Restart compatible API and worker builds together.

An unread date, currency or amount remains unknown. Every extracted receipt
requires owner review, including when its arithmetic balances.

Receipt reading sends bounded enlarged views of long receipts. Local OCR
provides secondary product-name hints and checks enlarged date regions; an
unconfirmed or ambiguous date stays blank for manual entry. Original uploads
remain intact. Review checks read item totals plus read tax/shipping using exact
minor-unit arithmetic, independently of the model's request for owner review.
When currency is unprinted, the selected purchase currency is identified for
display; the extracted value stays unknown.

`CANOPY_VISION_REASONING_EFFORT` is optional and omitted by default. Supported
values are `none`, `minimal`, `low`, `medium`, `high`, and `xhigh`; use a value
supported by the configured endpoint. This local Strata deployment uses `none`
for its fast variant. Set the same value on API and worker.

### Regional knowledge and the garden year

`/calendar/year` shows annual plant tracks and source-backed garden questions.
Set the garden country/region in **Garden location** and explicitly enable
regional guidance. City/postal search uses Open-Meteo/GeoNames only on request;
manual location entry also works. Device coordinates are optional and rounded.
Hardiness zone is an optional owner-entered value with an explicit zone system;
it is never inferred from timezone. There is no automatic hardiness-map lookup.

Plant creation and identity edits write a durable enrichment intent in the same
transaction. The worker checks intents every 30 seconds, respects source robots
policies, reuses public document extraction and workspace profiles for 30 days,
and fences results against plant identity/location changes. A failed source/model does
not block plant creation. Guidance is AI-extracted and advisory; it never creates
activities or reviewed care plans. Unsupported types/regions remain unknown.

Source adapters are declared in `server/internal/knowledge/providers.json` with
country, region aliases, crop, hemisphere, publisher, exact guide URL and scope.
Initial adapters cover Texas fruit crops, California citrus, and Pacific
Northwest strawberries. Extend the registry and crop aliases with primary
regional sources and tests; do not assign one region's seasonal guidance to
another. Only bounded factual paraphrases and provenance are retained, not full
publication mirrors. Source access/download limits are enforced. PDF extraction
uses the existing resource-limited media sandbox.

An OpenAI-compatible chat model performs extraction and garden answers. By
default this reuses the configured vision endpoint. A separate text-only model
can use `CANOPY_KNOWLEDGE_BASE_URL` (origin, no `/v1` suffix),
`CANOPY_KNOWLEDGE_MODEL`, optional `CANOPY_KNOWLEDGE_API_KEY`, and optional
`CANOPY_KNOWLEDGE_REASONING_EFFORT`. Dedicated endpoints require HTTPS in prod.
The older `CANOPY_AI_BASE_URL` `/v1/analyze` adapter is a different protocol and
is not reused as a chat endpoint. Model credentials stay in runtime configuration. Garden chat is read-only, limited to 12 requests per
workspace/hour/process, and validates returned plant and citation references.
It uses plant identities, relevant source facts and actual yearly care/photo
counts; it does not diagnose photos or forecast exact yields. Season words map
to approximate windows using the provider's explicit hemisphere; early/mid/late
qualifiers narrow those windows.
Unsupported/relative timing stays off the timeline. Source-supported month
phrases are parsed deterministically, never supplied as guessed model months.

Knowledge settings and extracted profiles are exported and backed up. Public
source caches and processing intents are regenerable; restores recreate intents
from plant rows. Owner plant photos and purchase records are not altered by
enrichment. Knowledge opts out by default, including after installation.

HTML extraction retains paragraph, list, heading and table boundaries so
neighboring climate and care sections do not contaminate each other.
Timing extraction validates complete supporting sentences against both the
summary and the selected timing phrase. Compound windows retain every
supported clause; explicit ranges and discrete months remain distinct.
An independent audit checks explicit dated harvest statements for omitted crop
windows, including windows in separate sentences. Invalid facts and missing
harvest windows receive one targeted repair attempt without replacing already
validated facts. Timing that remains ambiguous stays untimed rather than
creating a partial calendar band. Bounded verbatim timing phrases (up to 500
characters per fact) and an extraction version are retained for provenance;
full publications are not stored. Updating the extraction version bypasses
old document/profile caches and automatically requeues active opted-in plants.
Old-version guidance stays hidden while background validation runs.

### Bring your own AI

Open **Settings → AI connections** to add a named OpenAI-compatible service
(Strata, OpenAI, Ollama, LM Studio or another compatible gateway), Anthropic
Messages, or Google Gemini GenerateContent connection. Supply its API base URL,
exact model identifier and optional API key. Mark image support only when the
selected model supports it. Local HTTP requires the deployment-local option and
an address resolving to a private or loopback network. Hosted connections require
explicit consent before garden data can be sent.

Choose connections separately for garden questions/research, plant photo
analysis and receipt image extraction. Each feature can use the deployment
default, a saved connection, or be off. Editing applies without restarting the
API or worker. Deleting a connection turns its bound features off; it does not
silently send data to another provider. Existing environment defaults, local OCR
and separately configured PlantNet remain available. Native providers are not a
connection to a consumer ChatGPT/Claude subscription or a personal assistant's
existing conversation history: they use the provider's API credentials.

**Test connection** sends only synthetic text and an optional sample image.
It verifies accepted requests and a structured reply, not the accuracy of plant
advice. Tests are limited to 20 per workspace/hour. Provider-specific model
availability, access and usage costs depend on the supplied account. The OpenAI
compatibility section offers reasoning effort and `max_tokens` versus
`max_completion_tokens` selection for different services. Use API roots such as
`https://api.openai.com/v1`, `https://api.anthropic.com/v1`, or
`https://generativelanguage.googleapis.com/v1beta`; do not append the completion
method itself. A deployment default can be copied into an editable connection
without returning its key to the browser; copying credentials is restricted to
the identical provider protocol and endpoint.

Saved API keys are write-only, encrypted with AES-256-GCM and bound to their
workspace/connection identity. The API and worker must share the same private
32-byte key file, set through `CANOPY_AI_CREDENTIALS_KEY_FILE` or defaulting to
`<CANOPY_STORAGE_ROOT>/.private/ai-credentials.key`. The first process creates it
atomically with owner-only permissions. Preserve this file across container
recreation and **back it up separately** from the database backup. Database
backups carry encrypted connection rows and feature bindings; user exports carry
only connection metadata and bindings. Neither export nor backup embeds the
credential encryption key. Restoring with a different key requires re-entering
saved API keys. Replacing a connection's endpoint/protocol requires a new key or
explicitly clearing the old one, so saved credentials are not forwarded to a
new service by accident.


## MCP assistant connections

Canopy exposes named MCP tools for garden records, photos, care, knowledge, purchases, AI runs, notifications and reviewed changes. The endpoint is disabled by default. Enable it locally with these API process environment settings:

```sh
CANOPY_MCP_ENABLED=true
CANOPY_MCP_PUBLIC_URL=http://localhost:8080/mcp
CANOPY_MCP_ALLOWED_HOSTS=localhost:8080,127.0.0.1:8080,host.docker.internal:8080
```

Create a connection in **Settings → Agent connections**. Choose permissions, optional plant restrictions and an expiry. Tokens are shown once, stored only as hashes on the server, and independently revocable. Agent credentials are separate from the app password and Strata/provider keys. Normal record reads and logging do not invoke the configured AI model.

Direct watering/rain/observation logging uses saved permissions. Identity, historical, financial, care-schedule and export changes create immutable proposals for owner approval. Agents cannot approve themselves. Retries reuse `operation_id`; authorization and record revisions are checked again when a proposal is applied. Rain is area context, unmeasured watering stays unmeasured, and photo attachment prefers original capture metadata.

See [assistant setup and tested compatibility](examples/agents/README.md) for Hermes HTTP/stdio, Grok Bot considerations, the client-host photo upload helper and limitations. Native Hermes/Grok account onboarding has not been exercised; pinned SDK HTTP/stdio paths, the actual queued worker and local owner UI are tested. The prepared [HTTPS proxy example](examples/agents/Caddyfile.example) exposes only agent routes. Production requires an HTTPS public URL and explicit host allowlist. No public tunnel or external account is enabled automatically.

Authenticated byte routes are `PUT /agent/uploads/{assetID}` and `GET /agent/exports/{id}/download`; they use the agent bearer credential. Backups preserve grant history but revoke copied credentials on restore. User exports exclude credential verifiers and operation receipts. `contracts/mcp/v1/tools.json` is authoritative; `make gen` regenerates its embedded catalog.

## Publishing and self-hosted deployment

GitHub Actions now support Docked-style development images, versioned releases
and manual promotion of an existing stable image set to `latest`. The published
server image contains matching API/worker/operator binaries; separate web and
media images complete the stack. See [publishing and deployment](docs/deployment.md)
for GHCR setup, the Compose bundle, first-owner setup, remote MCP and upgrades.
