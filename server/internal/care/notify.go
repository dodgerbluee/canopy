// Timeline projection and notification intents (T24, digests in T27).
// Check completions project a CARE journal entry (their linked OBSERVE
// activity is never projected as an ActivityEntry); materialization
// ticks raise per-occurrence in-app notification intents whose dedup
// keys make replays and restarts idempotent, plus one EMAIL daily
// digest intent per owner per local date.
package care

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care/engine"
)

// projectCheckCompletion writes (or revives) the CARE journal entry for a
// completed CHECK. Upsert semantics: an undo tombstones the entry, and a
// later re-completion of the same occurrence revives it in place.
func (s *Service) projectCheckCompletion(ctx context.Context, q *dbgen.Queries,
	workspaceID, plantID pgtype.UUID, occ dbgen.CareOccurrence, occurredOn engine.Date, tz *time.Location) error {
	summary := "Check completed: " + occ.RuleKey
	if occ.ReasonText.Valid && occ.ReasonText.String != "" {
		summary = clipRunes(occ.ReasonText.String, 280)
	}
	_, err := q.UpsertJournalEntry(ctx, dbgen.UpsertJournalEntryParams{
		ID: s.uuid.New(), WorkspaceID: workspaceID, PlantID: plantID,
		EntryType:  dbgen.JournalEntryTypeCARE,
		SourceID:   occ.ID,
		OccurredAt: localNoon(occurredOn, tz),
		Summary:    summary,
	})
	return err
}

// generateNotifications raises one INAPP intent per occurrence due today
// (local plan date). The dedup key is occurrence+date+channel, so a tick
// never re-notifies the same occurrence for the same day; a snooze to a
// new day, or the next local day arriving, produces a fresh intent.
func (s *Service) generateNotifications(ctx context.Context, q *dbgen.Queries, plan dbgen.CarePlan,
	plantName string, ownerID pgtype.UUID, today engine.Date) error {
	rows, err := q.ListDueTodayOccurrences(ctx, dbgen.ListDueTodayOccurrencesParams{
		PlanID: plan.ID, CurrentDueDate: engToPg(today),
	})
	if err != nil {
		return err
	}
	for _, occ := range rows {
		verb := "Care check"
		if occ.Kind == dbgen.RuleKindACTION && occ.ActivityKind.Valid {
			verb = titleCaseActivity(string(occ.ActivityKind.ActivityKind))
		}
		title := clipRunes(fmt.Sprintf("%s due: %s", verb, plantName), 280)
		body := "Scheduled care for " + plantName + "."
		if occ.ReasonText.Valid && occ.ReasonText.String != "" {
			body = clipRunes(occ.ReasonText.String, 4000)
		}
		if err := q.InsertNotification(ctx, dbgen.InsertNotificationParams{
			ID: s.uuid.New(), WorkspaceID: plan.WorkspaceID, OwnerID: ownerID,
			Channel:  dbgen.NotificationChannelINAPP,
			DedupKey: fmt.Sprintf("%s:%s:INAPP", uuidString(occ.ID), today.String()),
			RefKind:  dbgen.NotificationRefKindOCCURRENCE, RefID: occ.ID,
			Title: title, Body: body,
		}); err != nil {
			return err
		}
	}
	return nil
}

func titleCaseActivity(kind string) string {
	switch kind {
	case "WATER":
		return "Watering"
	case "FERTILIZE":
		return "Feeding"
	case "PRUNE":
		return "Pruning"
	case "REPOT":
		return "Repotting"
	case "HARVEST":
		return "Harvest"
	case "OBSERVE":
		return "Observation"
	case "NOTE":
		return "Note"
	}
	return "Care task"
}

func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// ---- T27: daily digest intents -----------------------------------------

// localInstant resolves a wall-clock time on a local date to an instant,
// per the frozen contract semantics (03: "DST nonexistent time moves to
// next valid instant; ambiguous time uses earlier offset").
func localInstant(tz *time.Location, d engine.Date, hh, mm int) time.Time {
	tpl := time.Date(d.Year, time.Month(d.Month), d.Day, hh, mm, 0, 0, time.UTC)
	t := time.Date(d.Year, time.Month(d.Month), d.Day, hh, mm, 0, 0, tz)
	if sameWall(t, tpl) {
		// The wall clock exists. If the hour folds (ambiguous), Go may
		// have picked either offset; the contract wants the earlier one.
		if alt := t.Add(-time.Hour); sameWall(alt, tpl) && alt.Before(t) {
			return alt
		}
		return t
	}
	// Nonexistent (spring-forward gap): binary-search the first instant
	// whose local wall clock is at or past the template — the transition
	// instant itself, i.e. the next valid instant.
	lo, hi := t.Add(-3*time.Hour), t.Add(3*time.Hour)
	for lo.Add(time.Second).Before(hi) {
		mid := lo.Add(hi.Sub(lo) / 2).Truncate(time.Second)
		if mid.Format("2006-01-02 15:04:05") >= tpl.Format("2006-01-02 15:04:05") {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

func sameWall(t, tpl time.Time) bool {
	return t.Year() == tpl.Year() && t.Month() == tpl.Month() && t.Day() == tpl.Day() &&
		t.Hour() == tpl.Hour() && t.Minute() == tpl.Minute()
}

// digestMessageID is stable per dedup key: retries and re-creations of
// the same day's digest keep one identity, so a receiver can dedupe on
// a best-effort basis (at-least-once channel, not an exactly-once promise).
func digestMessageID(dedupKey string) string {
	sum := sha256.Sum256([]byte(dedupKey))
	return fmt.Sprintf("%x@canopy", sum[:10])
}

// EnsureDigests composes one EMAIL digest intent per owner per
// workspace-local date (quiet default: no due work, no digest). The row
// refreshes in place until delivered, so tasks appearing later the same
// day ride the SAME digest — never a second one for the same day. The
// gate opens at the earliest plan notification time that day, resolved
// in the workspace timezone with DST normalization.
func (s *Service) EnsureDigests(ctx context.Context, now time.Time) error {
	rows, err := s.q().ListDigestInputs(ctx)
	if err != nil {
		return err
	}
	type digest struct {
		ws, owner pgtype.UUID
		wsTZ      string
		notTime   string // earliest HH:MM across the group
		lines     []string
		firstOcc  pgtype.UUID
	}
	groups := map[string]*digest{}
	var order []string
	for _, r := range rows {
		k := uuidString(r.OwnerID)
		g, ok := groups[k]
		if !ok {
			g = &digest{ws: r.WorkspaceID, owner: r.OwnerID, wsTZ: r.WsTimezone,
				notTime: r.NotificationTime, firstOcc: r.OccurrenceID}
			groups[k] = g
			order = append(order, k)
		}
		if r.NotificationTime < g.notTime {
			g.notTime = r.NotificationTime
		}
		verb := "Care check"
		if r.Kind == dbgen.RuleKindACTION && r.ActivityKind.Valid {
			verb = titleCaseActivity(string(r.ActivityKind.ActivityKind))
		}
		due := pgToEng(r.CurrentDueDate).String()
		g.lines = append(g.lines, fmt.Sprintf("%s: %s (due %s)", verb, r.PlantName, due))
	}
	for _, k := range order {
		g := groups[k]
		tz, err := time.LoadLocation(g.wsTZ)
		if err != nil {
			s.Log.Warn("digest skipped: workspace timezone unreadable", "workspace", uuidString(g.ws), "err", err)
			continue
		}
		date := engine.DateFrom(now.In(tz))
		hh, mm, err := parseHHMM(g.notTime)
		if err != nil {
			hh, mm = 9, 0 // schema default; a corrupt value never blocks delivery
		}
		dedup := fmt.Sprintf("digest:%s:%s:EMAIL", uuidString(g.owner), date.String())
		title := clipRunes(fmt.Sprintf("Care digest %s: %d task(s)", date.String(), len(g.lines)), 280)
		body := clipRunes(strings.Join(g.lines, "\n")+
			"\n\nOpen the app to complete these checks. This digest never marks work done.", 4000)
		if err := s.q().UpsertDigestIntent(ctx, dbgen.UpsertDigestIntentParams{
			ID: s.uuid.New(), WorkspaceID: g.ws, OwnerID: g.owner,
			DedupKey: dedup, RefID: g.firstOcc,
			Title: title, Body: body,
			DigestDate:  engToPg(date),
			ScheduledAt: pgtype.Timestamptz{Time: localInstant(tz, date, hh, mm), Valid: true},
			MessageID:   pgtype.Text{String: digestMessageID(dedup), Valid: true},
		}); err != nil {
			return err
		}
	}
	return nil
}

func parseHHMM(s string) (int, int, error) {
	var hh, mm int
	if _, err := fmt.Sscanf(s, "%d:%d", &hh, &mm); err != nil {
		return 0, 0, err
	}
	if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("bad time %q", s)
	}
	return hh, mm, nil
}
