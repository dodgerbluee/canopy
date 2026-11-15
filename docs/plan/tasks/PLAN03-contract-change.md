# PLAN03 — Additive planning research contract

Accepted within the user-authorized third implementation step ("continue").
Existing plan/action inputs and user-selected dates retain PLAN01 semantics.

- Add refreshPlantingGuidance(input: RefreshPlantingGuidanceInput!,
  clientMutationId: String): PlantingPlanPayload!, with id and expectedRevision.
  Active plans only. An unchanged current PENDING/PROCESSING request is a no-op
  after revision validation; completed/failed requests create a new generation,
  increment the edit revision and audit the explicit refresh. Receipts retain
  existing conflict/gone/replay behavior.
- Saving a plan or changing relevant intent requests guidance when a provider is
  wired. No real plant is created. Disabled/missing-location/unsupported context
  creates its honest terminal state; it does not lose the saved manual plan.
  Enrichment never changes targetPeriod, actions, budgets or accepted provenance.
- Context includes workspace, crop identity/cultivar, method, placement/intended
  location, relevant notes/quantity, explicit geography and its revision, source
  registry/extraction version and an allowlisted model signature. Timezone and
  hardiness zone never supply a frost date or latitude. Publication extraction is
  year-independent; target/display year changes reuse the publication cache.
- Settings changes invalidate active plan generations in the same transaction.
  Old source snapshots remain immutable. Background context review can advance
  the guidance generation independently of the user edit revision; explicit
  commands retain their normal aggregate revision protocol. Cancellation fences
  pending research; restoration requests the current context again.
- A READY snapshot's facts/provenance/timestamps remain immutable. Migration 20
  adds nullable internal next_check_at scheduling metadata, which may advance
  without changing that immutable content or updatedAt. Null supports backups
  from before the column existed. This field is not a product/API field.
- The periodic worker reviews at most eight contexts and researches at most two
  plans per run, with one planning sweep across processes. It never waits on a
  provider while holding a plan row lock. A processing lease is twenty minutes;
  unchanged contexts are reviewed no more often than every fifteen minutes.
  Late results cannot attach after an identity/geography/generation change.
- Provider attempts are bounded to eight source-extraction calls per workspace
  per UTC hour and the existing 5,000-token response limit per call. Attempts,
  including failures, count. Cache hits do not consume this allowance. Failed
  requests require an explicit refresh; state/configuration changes can requeue.
- Public publication text and validated extraction are cached for thirty days,
  separately from private plan context. Only curated sources are fetched; model
  and user text cannot introduce fetch URLs. Extraction receives public crop/
  publication data only, with local filtering for cultivar/method/place. Public
  caches and operational usage counters are rebuildable and excluded from user
  exports/operator backups; snapshots retain the complete private provenance.
- Timing is derived from verified complete source phrases, never model-returned
  dates. Annual month/day/season, distinct windows and relative anchors retain
  their precision. Unknown offsets stay null. Negative or unsupported timing is
  an untimed condition; no guessed last frost, same-year fruit or water/fertilizer
  dose. Source applicability is explicit, with unsupported/manual fallback.
- Evidence matching normalizes typography and whitespace only. Unsupported
  model claims are discarded individually; a deterministic fallback may read
  explicit, complete planting/harvest clauses through the same validators.
  Its producer identity is recorded separately from model extraction. Missing
  phase windows or ambiguous mixed plant types remain unplottable. Reviewed
  multi-crop adapters exclude neighboring crop sections and fail closed when
  their section boundary disappears; provenance hashes the complete original.
- The registry now covers eight crop families and twelve publications, including
  reviewed tomato sources for Texas and Oregon and strawberry guidance for the
  United Kingdom. Country/region matching remains explicit; coverage is not
  inferred for unregistered regions.
- Use the configured GARDEN connection and existing OpenAI-compatible/Anthropic/
  Gemini transport. No new assistant connection, service or container is added.
  Source research creates no schedule, care task, reminder, spending or MCP tool.
