# Product contract

## Outcome

One private place to answer: What do I own? Where did it come from? How has it changed? What needs attention, why, and what evidence supports that advice?

Initial collection: one lemon, one lime, one mandarin, two separately tracked figs, strawberries, blackberries. Do not invent cultivars, purchase details, plant counts for berries, climate, or growing conditions. An opt-in starter creates five individual tree records and two group records with unknown details. Demo data must be labeled synthetic and confined to development/test environments.

## Default assumptions

- One owner, one workspace, one self-hosted installation. Private by default; authenticated access required even on a LAN. No public signup. Schema includes workspace ownership for consistent isolation, but household sharing UI and invitations are deferred.
- Responsive web application, optimized for desktop planning and phone capture. Browser install manifest; offline viewing/writes and background photo-library sync are deferred.
- Location, IANA timezone, cultivar, planting medium, container size, indoor/outdoor transitions, and approximate age are editable facts with unknown values allowed. America/Chicago is an onboarding suggestion, never inferred as the plant's actual climate from this conversation.
- External services are optional. Records, photos, receipts, manual care, deterministic scheduling, and source-backed guidance work with all AI/API keys absent.
- No autonomous watering, fertilizing, pruning, purchases, or treatment. Suggestions can create proposed tasks; only an explicit owner action accepts them.
- Plan now; do not begin feature implementation merely because tasks below are ready.

## Complete v1 scope

A staged release, not a promise to deliver every feature in a first scaffold:

**R1 — trusted journal:** owner setup/auth, workspace/site/plant records, provenance and purchases, receipt attachments, dated photo journal, comparison, activity and measurement logging, search/filter, settings, export, tested backup/restore. This is already useful for the collection.

**R2 — contextual care:** editable source-backed care profiles for the six plant categories, cultivar overrides, deterministic seasonal/interval reminders, snooze/skip/complete semantics, notification inbox and email, weather-aware checks, source provenance and uncertainty. No weather service is needed to complete manual tasks.

**R3 — evidence-based assistance:** receipt extraction drafts, identification candidates, optional vision observations and change summaries, text synthesis across records/photos/care/weather, reviewed suggestion acceptance, job status/retry/budget controls. Actual photo interpretation requires a configured and tested vision-capable adapter. R3 cannot be called complete with only mocked model responses; a real capability test and evaluated pilot are required, or it must ship visibly as unavailable.

**R4 — release hardening:** accessibility and visual review, realistic scale/performance, failure recovery, security checks, upgrade/restore rehearsal, docs. This is the production v1 gate, not optional polish.

## Core journeys and acceptance

| Journey | Observable acceptance |
|---|---|
| Add a tree | Create unknown cultivar, select location, acquire date/price/seller optionally; detail page opens immediately and survives reload. |
| Record purchase | One receipt can cover multiple plants and supplies; allocations reconcile exactly, original remains downloadable, per-plant spending is unambiguous. |
| Capture growth | Phone selects/captures multiple images, shows upload/processing states, asks when date is uncertain, then places images on dated plant timeline. |
| Compare | Choose any two photos, compare side by side or slider, dates and plant names remain visible, keyboard works, no layout jumps. |
| Log care | Water/feed/prune/repot/harvest/note with optional quantities; backdated entries sort correctly; corrections retain audit history. |
| Know what to do | Today shows due checks, outstanding work, and proposed advice separately; completion records an activity atomically. |
| Understand advice | Every suggestion exposes inputs, missing facts, sources, date, confidence/uncertainty, and proposed action; unknown context leads to a question. |
| Review health | Photos and timeline evidence lead to visible observations and hypotheses, with explicit limits; an owner can dismiss or accept a proposed follow-up check. |
| Recover | Restore the database and files onto a clean instance, log in, see the same photo hashes, purchases, events, and care state. |

## Explicit v1 exclusions

No microservices, Kubernetes, Kafka, vector database, generic workflow builder, plugin marketplace, native mobile apps, collaboration, public social sharing, IoT automation, arbitrary web crawling, photo-library scraping, growth measurement from uncalibrated photos, or fine-tuned disease classifier. No generic conversational agent with shell/database/browser tools. Optional S3 storage and read-only external album import are future adapters, not v1 dependencies.

## Missing facts and graceful behavior

- Cultivar unknown: show general guidance, mark cultivar-sensitive actions as needing confirmation.
- Location/weather unavailable: show manual care checks with stale/unavailable indicator; never assume rainfall reached a covered pot.
- No purchase price: show unknown, not zero. Unknown date remains unknown, not the creation date.
- No growth photos: explain how to take the first one; no fabricated growth trend.
- No vision model: allow journaling and text analysis of user-entered observations; clearly disable photo analysis.
- “All growth off the trees” is unresolved terminology. Onboarding explains separate harvest, pruning, fruit removal, and dormancy concepts; never encode one universal stripping/pruning deadline.

## Traceability

Requirement 1: T09–T12, T18, T33, T38.a/T38.final. Requirement 2: T13–T16, T19–T20, T32. Requirement 3: T21–T28, T34. Requirement 4: T29–T35. Shared quality: T00–T08, T36–T41.

## Definition of done

A feature includes a working user journey, typed contract, authoritative server validation, relevant unit/integration/UI tests, all empty/loading/error/permission states, audit behavior, and a local demo recipe. Generated code must reproduce exactly. No TODO stubs in reachable functionality, ungrounded AI placeholders, silent mock fallbacks, or tests disabled to get a green build.

Final release requires every task accepted and R1–R4 evidence archived. Independent planning grade is a plan review only; implementation requires its own later independent review and runtime evidence.
