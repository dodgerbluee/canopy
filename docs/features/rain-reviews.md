# Garden rain reviews

Today shows a **Rain watch** panel. When recent weather estimates liquid rain
near the garden, it asks **Did your garden get rain?** Choose **Yes, it rained**,
optionally enter your own amount in inches or millimeters, then save. Leave the
amount blank if unknown. **No rain here** dismisses that day across devices.
The Calendar's existing **Add rain** remains available for a missed local shower.

Rain is area history between waterings. Confirming never creates watering
activities, completes care tasks, or claims any plant received enough water.
Provider estimates are displayed for context and never filled into the amount
field or silently saved as a measurement.

## Location and checks

The hourly `rain.check` River job uses the **garden coordinates in Settings**.
It requires neither individual plant-location coordinates nor care profiles.
Remove the garden coordinates to stop checks. Coordinates are sent to the
configured Open-Meteo endpoint; no location is inferred from timezone or IP.
The existing `weather.fetch` job still supplies separate plant-location forecasts.

Only elapsed hourly `rain` and `showers` intervals contribute. Future forecast
intervals and snow do not become rain prompts. The source is modelled weather,
not a rain gauge; nearby storms can be missed. Requests use absolute Unix times
and are grouped into the garden's IANA timezone, including DST and fractional
offsets. An interval crossing midnight is apportioned by duration. This follows
the [Open-Meteo hourly variable/time documentation](https://open-meteo.com/en/docs).

At most today and the previous two garden days can appear, with an estimate of
at least 0.1 mm. Checks are cached for one hour. The panel distinguishes an
unconfigured garden, no completed check, current results, and checks older than
three hours. A provider outage retains the previous successful check; it does
not report a dry garden. Unanswered estimates older than three hours cannot be
confirmed through a prompt; add rain manually instead. The page reads the cache
when loaded/refreshed; it does not make a weather request in the browser.

## Persistence and corrections

`rain_reviews` keeps weather context and explicit response per local day and
exact coordinate/timezone pair. Repeated checks preserve answers. A changed
location hides old prompts and fences a request already in flight. Cache and
responses expire after 14 days; actual rain events remain in `rain_events`.
Operator backups preserve these responses and confirmed-event references.
User exports retain confirmed rainfall in the existing rain dataset.

Review confirmation and rainfall creation happen in one transaction. Retried
and simultaneous confirmations return the same event. A manual rain record
already on that day suppresses the prompt; a stale open form returns that record
instead of inserting another. Removing the recorded rain in Calendar stays an
explicit correction and does not turn a previously answered review back on.

## Deployment

Migration 24 and matching API/worker builds are required. After restart,
`rain.check` runs at startup and hourly. Check `river_job` for its completion and
`rain_reviews.checked_at` for a successful provider result: a completed job can
still have skipped a garden during a provider outage (logged as
`garden rain check unavailable`). There is no separate weather API key or
plant care setup step.
