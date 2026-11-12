// Read paths for the T24 GraphQL surface: the agenda keyset page, the
// notification inbox, and hydrated plan/profile views. Mutations live in
// sources.go / profiles.go / occurrences.go; these reads never write.
package care

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	engine "canopy/server/internal/care/engine"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

// ---- agenda -----------------------------------------------------------------

// OccurrenceView is one agenda/payload occurrence with its plan's plant
// attached (the row itself is plan-keyed; the contract exposes plantId).
// Weather is the exact snapshot the row's advice was computed from
// (T26); nil when no snapshot backed it.
type OccurrenceView struct {
	Row     dbgen.CareOccurrence
	PlantID string
	Weather *dbgen.WeatherSnapshot
}

// AttachWeather hydrates each view's Weather from the snapshot id its
// row references — one batch query per page, workspace-scoped so a
// foreign snapshot can never attach.
func (s *Service) AttachWeather(ctx context.Context, workspaceID pgtype.UUID, items []OccurrenceView) error {
	var ids []pgtype.UUID
	seen := map[string]bool{}
	for _, it := range items {
		if !it.Row.WeatherSnapshotID.Valid {
			continue
		}
		k := uuidString(it.Row.WeatherSnapshotID)
		if !seen[k] {
			seen[k] = true
			ids = append(ids, it.Row.WeatherSnapshotID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.q().ListWeatherSnapshotsByIDs(ctx, dbgen.ListWeatherSnapshotsByIDsParams{
		WorkspaceID: workspaceID, Ids: ids,
	})
	if err != nil {
		return err
	}
	byID := make(map[string]dbgen.WeatherSnapshot, len(rows))
	for _, r := range rows {
		byID[uuidString(r.ID)] = r
	}
	for i := range items {
		if snap, ok := byID[uuidString(items[i].Row.WeatherSnapshotID)]; ok {
			s := snap
			items[i].Weather = &s
		}
	}
	return nil
}

type OccurrenceConnection struct {
	Items      []OccurrenceView
	NextCursor *string
}

// CareAgenda pages open (DUE) work in the [from, to] local-date window.
// Dates are compared as plan-local calendar dates, exactly as stored.
func (s *Service) CareAgenda(ctx context.Context, a Actor, from, to, plantID *string, first int32, after *string) (OccurrenceConnection, []Problem, error) {
	if first < 1 || first > 100 {
		return OccurrenceConnection{}, []Problem{problem(CodeValidation, "first", "expected 1..100")}, nil
	}
	fromD, probs := optDateArg(from, "from")
	if probs != nil {
		return OccurrenceConnection{}, probs, nil
	}
	toD, probs := optDateArg(to, "to")
	if probs != nil {
		return OccurrenceConnection{}, probs, nil
	}
	pid, probs := optIDArg(plantID, "filter.plantId")
	if probs != nil {
		return OccurrenceConnection{}, probs, nil
	}
	afterDue, afterID, probs := decodeDueCursor(after)
	if probs != nil {
		return OccurrenceConnection{}, probs, nil
	}
	rows, err := s.q().ListCareAgenda(ctx, dbgen.ListCareAgendaParams{
		WorkspaceID: a.WorkspaceID, FromDueDate: fromD, ToDueDate: toD, PlantID: pid,
		AfterDueDate: afterDue, AfterID: afterID, LimitCount: first + 1,
	})
	if err != nil {
		return OccurrenceConnection{}, nil, err
	}
	hasMore := len(rows) > int(first)
	if hasMore {
		rows = rows[:first]
	}
	out := OccurrenceConnection{Items: make([]OccurrenceView, 0, len(rows))}
	plantByPlan := map[string]string{}
	for _, r := range rows {
		key := uuidString(r.PlanID)
		pl, ok := plantByPlan[key]
		if !ok {
			plan, err := s.q().GetCarePlan(ctx, r.PlanID)
			if err != nil {
				return OccurrenceConnection{}, nil, err
			}
			pl = uuidString(plan.PlantID)
			plantByPlan[key] = pl
		}
		out.Items = append(out.Items, OccurrenceView{Row: r, PlantID: pl})
	}
	if err := s.AttachWeather(ctx, a.WorkspaceID, out.Items); err != nil {
		return OccurrenceConnection{}, nil, err
	}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		c := encodeDueCursor(last.CurrentDueDate, uuidString(last.ID))
		out.NextCursor = &c
	}
	return out, nil, nil
}

func optDateArg(s *string, field string) (pgtype.Date, []Problem) {
	if s == nil {
		return pgtype.Date{}, nil
	}
	d, err := engine.ParseDate(*s)
	if err != nil {
		return pgtype.Date{}, []Problem{problem(CodeValidation, field, "expected YYYY-MM-DD")}
	}
	return engToPg(d), nil
}

func optIDArg(s *string, field string) (pgtype.UUID, []Problem) {
	if s == nil {
		return pgtype.UUID{}, nil
	}
	id, ok := parseID(*s)
	if !ok {
		return pgtype.UUID{}, []Problem{problem(CodeValidation, field, "must be a UUID")}
	}
	return id, nil
}

func encodeDueCursor(due pgtype.Date, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(pgToEng(due).String() + "|" + id))
}

func decodeDueCursor(cur *string) (pgtype.Date, pgtype.UUID, []Problem) {
	if cur == nil || *cur == "" {
		return pgtype.Date{}, pgtype.UUID{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(*cur)
	if err != nil {
		return pgtype.Date{}, pgtype.UUID{}, []Problem{problem(CodeValidation, "after", "malformed cursor")}
	}
	datePart, idPart, ok := strings.Cut(string(raw), "|")
	if !ok {
		return pgtype.Date{}, pgtype.UUID{}, []Problem{problem(CodeValidation, "after", "malformed cursor")}
	}
	d, err := engine.ParseDate(datePart)
	if err != nil {
		return pgtype.Date{}, pgtype.UUID{}, []Problem{problem(CodeValidation, "after", "malformed cursor")}
	}
	id, ok := parseID(idPart)
	if !ok {
		return pgtype.Date{}, pgtype.UUID{}, []Problem{problem(CodeValidation, "after", "malformed cursor")}
	}
	return engToPg(d), id, nil
}

// ---- notifications ------------------------------------------------------------

type NotificationView struct {
	ID         string
	Channel    string
	DedupKey   string
	RefKind    string
	RefID      string
	Title      string
	Body       string
	DigestDate *string
	CreatedAt  string
	ReadAt     *string
}

type NotificationConnection struct {
	Items      []NotificationView
	NextCursor *string
}

func (s *Service) ListNotifications(ctx context.Context, a Actor, unreadOnly bool, first int32, after *string) (NotificationConnection, []Problem, error) {
	if first < 1 || first > 100 {
		return NotificationConnection{}, []Problem{problem(CodeValidation, "first", "expected 1..100")}, nil
	}
	afterTS, afterID, probs := decodeDateCursor(after)
	if len(probs) > 0 {
		return NotificationConnection{}, probs, nil
	}
	rows, err := s.q().ListNotifications(ctx, dbgen.ListNotificationsParams{
		WorkspaceID: a.WorkspaceID, UnreadOnly: unreadOnly,
		AfterCreatedAt: afterTS, AfterID: afterID, LimitCount: first + 1,
	})
	if err != nil {
		return NotificationConnection{}, nil, err
	}
	hasMore := len(rows) > int(first)
	if hasMore {
		rows = rows[:first]
	}
	out := NotificationConnection{Items: make([]NotificationView, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, notificationView(r))
	}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		c := encodeDateCursor(last.CreatedAt, uuidString(last.ID))
		out.NextCursor = &c
	}
	return out, nil, nil
}

func notificationView(r dbgen.Notification) NotificationView {
	return NotificationView{
		ID: uuidString(r.ID), Channel: string(r.Channel), DedupKey: r.DedupKey,
		RefKind: string(r.RefKind), RefID: uuidString(r.RefID),
		Title: r.Title, Body: r.Body,
		DigestDate: optDate(r.DigestDate),
		CreatedAt:  stamp(r.CreatedAt), ReadAt: optTS(r.ReadAt),
	}
}

type MarkNotificationReadInput struct {
	NotificationID string
}

// MarkNotificationRead is idempotent: re-marking a read notification
// returns the same row, never a conflict.
func (s *Service) MarkNotificationRead(ctx context.Context, a Actor, in MarkNotificationReadInput, cmid *string) (NotificationView, []Problem, error) {
	var zero NotificationView
	id, ok := parseID(in.NotificationID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.notificationId", "must be a UUID")}, nil
	}
	return mutate(s, ctx, a, "markNotificationRead", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (NotificationView, []Problem) {
			row, err := q.GetNotification(ctx, dbgen.GetNotificationParams{ID: id, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if isNoRows(err) {
					return zero, []Problem{problem(CodeNotFound, "input.notificationId", "notification not found")}
				}
				return zero, []Problem{dbProblem("input", err)}
			}
			if row.ReadAt.Valid {
				return notificationView(row), nil
			}
			updated, err := q.MarkNotificationReadRow(ctx, dbgen.MarkNotificationReadRowParams{ID: id, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if isNoRows(err) { // raced with another mark: re-read
					if row2, err2 := q.GetNotification(ctx, dbgen.GetNotificationParams{ID: id, WorkspaceID: a.WorkspaceID}); err2 == nil {
						return notificationView(row2), nil
					}
				}
				return zero, []Problem{dbProblem("input", err)}
			}
			return notificationView(updated), nil
		})
}

// ---- plan views ---------------------------------------------------------------

type PlanOverrideView struct {
	RuleKey            string
	Enabled            bool
	RecurrenceOverride json.RawMessage // nil = profile cadence
}

type PlanView struct {
	Row            dbgen.CarePlan
	ProfileVersion *ProfileVersionView // nil when owner-defined plan only
	Overrides      []PlanOverrideView
}

// PlanViewOf hydrates a plan (pinned version with rules and citations,
// overrides) for the GraphQL CarePlan shape.
func (s *Service) PlanViewOf(ctx context.Context, a Actor, plan dbgen.CarePlan) (PlanView, []Problem, error) {
	pv, err := s.planViewTx(ctx, s.q(), a, plan)
	if err != nil {
		return PlanView{}, nil, err
	}
	return pv, nil, nil
}

func (s *Service) planViewTx(ctx context.Context, q *dbgen.Queries, a Actor, plan dbgen.CarePlan) (PlanView, error) {
	pv := PlanView{Row: plan, Overrides: []PlanOverrideView{}}
	if plan.ProfileVersionID.Valid {
		ver, err := q.GetCareProfileVersion(ctx, dbgen.GetCareProfileVersionParams{
			ID: plan.ProfileVersionID, WorkspaceID: plan.WorkspaceID})
		if err != nil {
			return PlanView{}, err
		}
		vv, err := s.versionViewTx(ctx, q, ver)
		if err != nil {
			return PlanView{}, err
		}
		pv.ProfileVersion = &vv
	}
	ovs, err := q.ListCarePlanOverrides(ctx, plan.ID)
	if err != nil {
		return PlanView{}, err
	}
	for _, o := range ovs {
		ov := PlanOverrideView{RuleKey: o.RuleKey, Enabled: o.Enabled}
		if len(o.RecurrenceOverride) > 0 {
			ov.RecurrenceOverride = o.RecurrenceOverride
		}
		pv.Overrides = append(pv.Overrides, ov)
	}
	return pv, nil
}

// versionViewTx hydrates one profile version (environment, rules with
// pinned citations) without walking the whole aggregate.
func (s *Service) versionViewTx(ctx context.Context, q *dbgen.Queries, ver dbgen.CareProfileVersion) (ProfileVersionView, error) {
	env := EnvironmentView{Situations: []string{}, Hemispheres: []string{}}
	var raw struct {
		Situations  []string `json:"situations"`
		Hemispheres []string `json:"hemispheres"`
	}
	if len(ver.Environment) > 0 {
		if err := json.Unmarshal(ver.Environment, &raw); err == nil {
			if raw.Situations != nil {
				env.Situations = raw.Situations
			}
			if raw.Hemispheres != nil {
				env.Hemispheres = raw.Hemispheres
			}
		}
	}
	rules, err := q.ListCareRules(ctx, ver.ID)
	if err != nil {
		return ProfileVersionView{}, err
	}
	srcs, err := q.ListRuleSourcesForVersion(ctx, ver.ID)
	if err != nil {
		return ProfileVersionView{}, err
	}
	byRule := map[string][]SourceVersionView{}
	for _, row := range srcs {
		byRule[uuidString(row.RuleID)] = append(byRule[uuidString(row.RuleID)], SourceVersionView{
			ID: uuidString(row.ID), SourceID: uuidString(row.SourceID), Version: row.Version,
			Citation: row.Citation, Summary: row.Summary,
			RetrievedOn:   optDate(row.RetrievedOn),
			ContentSha256: optText(row.ContentSha256),
			CreatedAt:     stamp(row.CreatedAt),
		})
	}
	pv := ProfileVersionView{
		ID: uuidString(ver.ID), ProfileID: uuidString(ver.ProfileID),
		Version: ver.Version, Status: string(ver.Status), Environment: env,
		Rules:         make([]RuleView, 0, len(rules)),
		ReviewerLabel: optText(ver.ReviewerLabel),
		ReviewedOn:    optDate(ver.ReviewedOn),
		PublishedAt:   optTS(ver.PublishedAt), RetiredAt: optTS(ver.RetiredAt),
		CreatedAt: stamp(ver.CreatedAt),
	}
	for _, r := range rules {
		rv := RuleView{
			RuleKey: r.RuleKey, Kind: string(r.Kind),
			Recurrence:         r.Recurrence,
			RequiredFacts:      r.RequiredFacts,
			WeatherSensitivity: string(r.WeatherSensitivity),
			GuidanceText:       r.GuidanceText,
			SourceVersions:     byRule[uuidString(r.ID)],
		}
		if rv.SourceVersions == nil {
			rv.SourceVersions = []SourceVersionView{}
		}
		if r.ActivityKind.Valid {
			ak := string(r.ActivityKind.ActivityKind)
			rv.ActivityKind = &ak
		}
		pv.Rules = append(pv.Rules, rv)
	}
	return pv, nil
}

// NextOpenOccurrence returns the earliest still-open slot for a plan rule
// (the payload's nextOccurrence).
func (s *Service) NextOpenOccurrence(ctx context.Context, planID pgtype.UUID, ruleKey string) (OccurrenceView, bool, error) {
	row, err := s.q().NextOpenOccurrenceForRule(ctx, dbgen.NextOpenOccurrenceForRuleParams{
		PlanID: planID, RuleKey: ruleKey})
	if err != nil {
		if isNoRows(err) {
			return OccurrenceView{}, false, nil
		}
		return OccurrenceView{}, false, err
	}
	plan, err := s.q().GetCarePlan(ctx, planID)
	if err != nil {
		return OccurrenceView{}, false, err
	}
	views := []OccurrenceView{{Row: row, PlantID: uuidString(plan.PlantID)}}
	if err := s.AttachWeather(ctx, plan.WorkspaceID, views); err != nil {
		return OccurrenceView{}, false, err
	}
	return views[0], true, nil
}
