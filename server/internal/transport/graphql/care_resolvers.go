// Care GraphQL surface (T24): agenda/profile/source/notification reads and
// the full lifecycle mutation set. Service calls mirror the journal
// pattern: domain Problems map into payload.problems; unexpected errors
// become INTERNAL GraphQL errors. Nested contract structs are pre-hydrated
// here (no per-field resolvers).
package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vektah/gqlparser/v2/gqlerror"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care"
	"canopy/server/internal/care/engine"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	httpx "canopy/server/internal/transport/http"
)

func careActor(p httpx.Principal) care.Actor {
	return care.Actor{WorkspaceID: p.WorkspaceID, OwnerID: p.OwnerID, SessionID: p.SessionID}
}

func mapCareProblems(ps []care.Problem) []*gql.MutationProblem {
	out := make([]*gql.MutationProblem, 0, len(ps))
	for _, p := range ps {
		mp := &gql.MutationProblem{Code: gql.ProblemCode(p.Code), Message: p.Message}
		if p.Field != "" {
			f := p.Field
			mp.Field = &f
		}
		if p.CurrentRevision != nil {
			rev := int(*p.CurrentRevision)
			mp.CurrentRevision = &rev
		}
		out = append(out, mp)
	}
	return out
}

// careProblemsError surfaces read-path validation as a GraphQL error
// (reads have no payload to carry problems).
func careProblemsError(probs []care.Problem) error {
	p := probs[0]
	msg := p.Message
	if p.Field != "" {
		msg = p.Field + ": " + msg
	}
	e := gqlerror.Errorf("%s", msg)
	// Errorf leaves Extensions nil; assigning into it would panic. Every
	// other resolver builds the map explicitly — do the same here.
	if e.Extensions == nil {
		e.Extensions = map[string]any{}
	}
	e.Extensions["code"] = p.Code
	return e
}

func (m mutationResolver) requireCare() error {
	if m.r.Care == nil {
		return capabilityUnavailable("care")
	}
	return nil
}

// ---- scalar helpers --------------------------------------------------------

func dtOf(s string) scalars.DateTime {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return scalars.DateTime{}
	}
	return scalars.DateTime(t)
}

func optDTS(s *string) *scalars.DateTime {
	if s == nil {
		return nil
	}
	d := dtOf(*s)
	return &d
}

func optD(s *string) *scalars.Date {
	if s == nil {
		return nil
	}
	d := scalars.Date(*s)
	return &d
}

func tsOf(t pgtype.Timestamptz) scalars.DateTime {
	if !t.Valid {
		return scalars.DateTime{}
	}
	return scalars.DateTime(t.Time)
}

func optTS(t pgtype.Timestamptz) *scalars.DateTime {
	if !t.Valid {
		return nil
	}
	d := scalars.DateTime(t.Time)
	return &d
}

func dateOf(d pgtype.Date) scalars.Date {
	if !d.Valid {
		return scalars.Date("")
	}
	return scalars.Date(d.Time.Format("2006-01-02"))
}

func optDateT(d pgtype.Date) *scalars.Date {
	if !d.Valid {
		return nil
	}
	v := scalars.Date(d.Time.Format("2006-01-02"))
	return &v
}

func dateStrPtr(d *scalars.Date) *string {
	if d == nil {
		return nil
	}
	s := string(*d)
	return &s
}

func pageFirst(first *int) int32 {
	if first == nil {
		return 25
	}
	return int32(*first)
}

func carePageInfo(endCursor *string) *gql.PageInfo {
	return &gql.PageInfo{EndCursor: endCursor, HasNextPage: endCursor != nil}
}

// ---- output mappers ---------------------------------------------------------

func sourceVersionOut(v care.SourceVersionView) *gql.SourceVersion {
	return &gql.SourceVersion{
		ID: v.ID, SourceID: v.SourceID, Citation: v.Citation, Summary: v.Summary,
		RetrievedOn: optD(v.RetrievedOn), ContentSha256: v.ContentSha256,
		CreatedAt: dtOf(v.CreatedAt),
	}
}

func sourceOut(v care.SourceView) *gql.Source {
	out := &gql.Source{
		ID: v.ID, Kind: gql.SourceKind(v.Kind), Title: v.Title,
		Publisher: v.Publisher, URL: v.URL, Region: v.Region, License: v.License,
		RetiredAt: optDTS(v.RetiredAt), Revision: int(v.Revision),
		CreatedAt: dtOf(v.CreatedAt), UpdatedAt: dtOf(v.UpdatedAt),
		Versions: make([]*gql.SourceVersion, 0, len(v.Versions)),
	}
	for i, sv := range v.Versions {
		out.Versions = append(out.Versions, sourceVersionOut(sv))
		if i == 0 {
			out.CurrentVersion = sourceVersionOut(sv) // versions newest first
		}
	}
	return out
}

func profileEnvironmentOut(e care.EnvironmentView) *gql.ProfileEnvironment {
	env := &gql.ProfileEnvironment{
		Situations:  make([]gql.LocationSituation, 0, len(e.Situations)),
		Hemispheres: make([]gql.SeasonHemisphere, 0, len(e.Hemispheres)),
	}
	for _, s := range e.Situations {
		env.Situations = append(env.Situations, gql.LocationSituation(s))
	}
	for _, h := range e.Hemispheres {
		env.Hemispheres = append(env.Hemispheres, gql.SeasonHemisphere(h))
	}
	return env
}

// recurrenceOut renders the frozen ADR-0002 jsonb form as the contract union.
func recurrenceOut(raw json.RawMessage) (gql.RecurrenceSpec, error) {
	var probe struct {
		Form string `json:"form"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("recurrence json: %w", err)
	}
	switch probe.Form {
	case "EVERY_N_DAYS":
		var j struct {
			Days   int    `json:"days"`
			Anchor string `json:"anchor"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			return nil, err
		}
		return &gql.EveryNDaysRecurrence{Days: j.Days, Anchor: gql.AnchorPolicy(j.Anchor)}, nil
	case "WEEKLY_DAYS":
		var j struct {
			Weekdays []string `json:"weekdays"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			return nil, err
		}
		wd := make([]gql.Weekday, 0, len(j.Weekdays))
		for _, d := range j.Weekdays {
			wd = append(wd, gql.Weekday(d))
		}
		return &gql.WeeklyDaysRecurrence{Weekdays: wd}, nil
	case "ANNUAL_WINDOW":
		var j struct {
			Month      int `json:"month"`
			Day        int `json:"day"`
			WindowDays int `json:"window_days"`
			EveryYears int `json:"every_years"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			return nil, err
		}
		return &gql.AnnualWindowRecurrence{
			Month: j.Month, Day: j.Day, WindowDays: j.WindowDays, EveryYears: j.EveryYears,
		}, nil
	case "ONE_TIME":
		var j struct {
			Date string `json:"date"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			return nil, err
		}
		return &gql.OneTimeRecurrence{Date: scalars.Date(j.Date)}, nil
	}
	return nil, fmt.Errorf("unknown recurrence form %q", probe.Form)
}

func ruleOut(v care.RuleView) (*gql.CareRule, error) {
	rec, err := recurrenceOut(v.Recurrence)
	if err != nil {
		return nil, err
	}
	out := &gql.CareRule{
		RuleKey: v.RuleKey, Kind: gql.RuleKind(v.Kind),
		Recurrence:         rec,
		RequiredFacts:      make([]gql.ContextFact, 0, len(v.RequiredFacts)),
		WeatherSensitivity: gql.WeatherSensitivity(v.WeatherSensitivity),
		GuidanceText:       v.GuidanceText,
		SourceVersions:     make([]*gql.SourceVersion, 0, len(v.SourceVersions)),
	}
	for _, f := range v.RequiredFacts {
		out.RequiredFacts = append(out.RequiredFacts, gql.ContextFact(f))
	}
	for _, sv := range v.SourceVersions {
		out.SourceVersions = append(out.SourceVersions, sourceVersionOut(sv))
	}
	if v.ActivityKind != nil {
		ak := gql.ActivityKind(*v.ActivityKind)
		out.ActivityKind = &ak
	}
	return out, nil
}

func profileVersionOut(v care.ProfileVersionView) (*gql.CareProfileVersion, error) {
	out := &gql.CareProfileVersion{
		ID: v.ID, ProfileID: v.ProfileID, Version: int(v.Version),
		Status: gql.ProfileStatus(v.Status), Environment: profileEnvironmentOut(v.Environment),
		Rules:         make([]*gql.CareRule, 0, len(v.Rules)),
		ReviewerLabel: v.ReviewerLabel, ReviewedOn: optD(v.ReviewedOn),
		PublishedAt: optDTS(v.PublishedAt), RetiredAt: optDTS(v.RetiredAt),
		CreatedAt: dtOf(v.CreatedAt),
	}
	for _, r := range v.Rules {
		ro, err := ruleOut(r)
		if err != nil {
			return nil, err
		}
		out.Rules = append(out.Rules, ro)
	}
	return out, nil
}

func profileOut(v care.ProfileView) (*gql.CareProfile, error) {
	out := &gql.CareProfile{
		ID: v.ID, Category: gql.PlantCategory(v.Category),
		Species: v.Species, Cultivar: v.Cultivar,
		Status: gql.ProfileStatus(v.Status), Revision: int(v.Revision),
		CreatedAt: dtOf(v.CreatedAt), UpdatedAt: dtOf(v.UpdatedAt),
		Versions: make([]*gql.CareProfileVersion, 0, len(v.Versions)),
	}
	for _, pv := range v.Versions {
		pvo, err := profileVersionOut(pv)
		if err != nil {
			return nil, err
		}
		out.Versions = append(out.Versions, pvo)
		// currentVersion: highest REVIEWED, non-retired (versions newest first)
		if out.CurrentVersion == nil && pv.Status == "REVIEWED" && pv.RetiredAt == nil {
			out.CurrentVersion = pvo
		}
	}
	return out, nil
}

func planOut(pv care.PlanView) (*gql.CarePlan, error) {
	out := &gql.CarePlan{
		ID: uuidString(pv.Row.ID), PlantID: uuidString(pv.Row.PlantID),
		Timezone: pv.Row.Timezone, Enabled: pv.Row.Enabled,
		NotificationTime: pv.Row.NotificationTime,
		Overrides:        make([]*gql.PlanRuleOverride, 0, len(pv.Overrides)),
		Revision:         int(pv.Row.Revision),
		CreatedAt:        tsOf(pv.Row.CreatedAt), UpdatedAt: tsOf(pv.Row.UpdatedAt),
	}
	if pv.ProfileVersion != nil {
		pvo, err := profileVersionOut(*pv.ProfileVersion)
		if err != nil {
			return nil, err
		}
		out.ProfileVersion = pvo
	}
	for _, o := range pv.Overrides {
		ov := &gql.PlanRuleOverride{RuleKey: o.RuleKey, Enabled: o.Enabled}
		if len(o.RecurrenceOverride) > 0 {
			rec, err := recurrenceOut(o.RecurrenceOverride)
			if err != nil {
				return nil, err
			}
			ov.RecurrenceOverride = rec
		}
		out.Overrides = append(out.Overrides, ov)
	}
	return out, nil
}

// occurrenceReasonOut builds the typed reason from the stored occurrence
// columns. Weather evidence is the EXACT snapshot the advice was computed
// from (T26): the row's weather_snapshot_id, hydrated by the read path;
// freshness is computed against now per the 03 bands, so a snapshot that
// aged out reads STALE/UNAVAILABLE rather than silently fresh.
func occurrenceReasonOut(row dbgen.CareOccurrence, snap *dbgen.WeatherSnapshot) *gql.OccurrenceReason {
	if !row.ReasonText.Valid && len(row.EvidenceNotes) == 0 && len(row.MissingFacts) == 0 {
		return nil
	}
	out := &gql.OccurrenceReason{
		Text:         row.ReasonText.String,
		Evidence:     make([]*gql.ReasonEvidence, 0, len(row.EvidenceNotes)+1),
		MissingFacts: make([]gql.ContextFact, 0, len(row.MissingFacts)),
	}
	if snap != nil {
		ws, err := weatherSnapshotOut(*snap)
		if err == nil {
			out.Evidence = append(out.Evidence, &gql.ReasonEvidence{WeatherSnapshot: ws})
		}
		// A snapshot that fails to map (bad numeric) is omitted rather
		// than half-rendered; the typed reason text still stands.
	}
	for _, n := range row.EvidenceNotes {
		note := n
		out.Evidence = append(out.Evidence, &gql.ReasonEvidence{Note: &note})
	}
	for _, f := range row.MissingFacts {
		out.MissingFacts = append(out.MissingFacts, gql.ContextFact(f))
	}
	return out
}

// weatherSnapshotOut maps a stored snapshot row onto the contract type,
// provenance included, freshness computed against the current clock.
func weatherSnapshotOut(snap dbgen.WeatherSnapshot) (*gql.WeatherSnapshot, error) {
	high, err := decimalOut(snap.HighTempC)
	if err != nil {
		return nil, err
	}
	low, err := decimalOut(snap.LowTempC)
	if err != nil {
		return nil, err
	}
	rain, err := decimalOut(snap.RainfallMm)
	if err != nil {
		return nil, err
	}
	et0, err := decimalOut(snap.Et0Mm)
	if err != nil {
		return nil, err
	}
	out := &gql.WeatherSnapshot{
		ID: uuidString(snap.ID), LocationID: uuidString(snap.LocationID),
		Provider:  snap.Provider,
		FetchedAt: scalars.DateTime(snap.FetchedAt.Time), ValidFrom: scalars.DateTime(snap.ValidFrom.Time),
		ValidUntil: scalars.DateTime(snap.ValidUntil.Time),
		HighTempC:  high, LowTempC: low, RainfallMm: rain, Et0Mm: et0,
		Freshness: gql.SnapshotFreshness((&engine.Weather{FetchedAt: snap.FetchedAt.Time}).Freshness(time.Now())),
	}
	if snap.License.Valid {
		l := snap.License.String
		out.License = &l
	}
	return out, nil
}

func occurrenceOut(v care.OccurrenceView) *gql.CareOccurrence {
	row := v.Row
	out := &gql.CareOccurrence{
		ID: uuidString(row.ID), PlanID: uuidString(row.PlanID), PlantID: v.PlantID,
		RuleKey: row.RuleKey, Kind: gql.RuleKind(row.Kind),
		OccurrenceKey:   row.OccurrenceKey,
		OriginalDueDate: dateOf(row.OriginalDueDate), CurrentDueDate: dateOf(row.CurrentDueDate),
		SnoozedUntil: optDateT(row.SnoozedUntil), Status: gql.OccurrenceStatus(row.Status),
		ResolvedAt: optTS(row.ResolvedAt), PlanTimezone: row.PlanTimezone,
		Reason: occurrenceReasonOut(row, v.Weather),
	}
	if row.ActivityKind.Valid {
		ak := gql.ActivityKind(row.ActivityKind.ActivityKind)
		out.ActivityKind = &ak
	}
	if row.CompletedActivityID.Valid {
		id := uuidString(row.CompletedActivityID)
		out.CompletedActivityID = &id
	}
	return out
}

func notificationOut(v care.NotificationView) *gql.Notification {
	return &gql.Notification{
		ID: v.ID, Channel: gql.NotificationChannel(v.Channel), DedupKey: v.DedupKey,
		RefKind: gql.NotificationRefKind(v.RefKind), RefID: v.RefID,
		Title: v.Title, Body: v.Body,
		DigestDate: optD(v.DigestDate), CreatedAt: dtOf(v.CreatedAt), ReadAt: optDTS(v.ReadAt),
	}
}

// ---- input mappers -----------------------------------------------------------

func recSpecIn(in *gql.RecurrenceSpecInput) care.RecurrenceSpecInput {
	var out care.RecurrenceSpecInput
	if in == nil {
		return out
	}
	if e := in.EveryNDays; e != nil {
		out.EveryNDays = &care.EveryNDaysInput{Days: int32(e.Days), Anchor: string(e.Anchor)}
	}
	if w := in.WeeklyDays; w != nil {
		days := make([]string, 0, len(w.Weekdays))
		for _, d := range w.Weekdays {
			days = append(days, string(d))
		}
		out.WeeklyDays = &care.WeeklyDaysInput{Weekdays: days}
	}
	if a := in.AnnualWindow; a != nil {
		ai := &care.AnnualWindowInput{Month: int32(a.Month), Day: int32(a.Day)}
		if a.WindowDays != nil {
			wd := int32(*a.WindowDays)
			ai.WindowDays = &wd
		}
		if a.EveryYears != nil {
			ey := int32(*a.EveryYears)
			ai.EveryYears = &ey
		}
		out.AnnualWindow = ai
	}
	if o := in.OneTime; o != nil {
		out.OneTime = &care.OneTimeInput{Date: string(o.Date)}
	}
	return out
}

// careOverridesIn converts contract overrides into service inputs,
// canonicalizing recurrence overrides through the care package validator;
// problems short-circuit (nil slice + non-empty problem list).
func careOverridesIn(ovs []*gql.PlanRuleOverrideInput) ([]care.PlanOverrideInput, []*gql.MutationProblem) {
	out := make([]care.PlanOverrideInput, 0, len(ovs))
	for i, o := range ovs {
		in := care.PlanOverrideInput{RuleKey: o.RuleKey, Enabled: o.Enabled}
		if o.RecurrenceOverride != nil {
			raw, probs := care.CanonicalRecurrence(
				fmt.Sprintf("input.overrides[%d].recurrenceOverride", i),
				recSpecIn(o.RecurrenceOverride))
			if len(probs) > 0 {
				return nil, mapCareProblems(probs)
			}
			in.RecurrenceOverride = &raw
		}
		out = append(out, in)
	}
	return out, nil
}

func careRuleIns(rules []*gql.CareRuleInput) []care.RuleInput {
	out := make([]care.RuleInput, 0, len(rules))
	for _, r := range rules {
		in := care.RuleInput{
			RuleKey: r.RuleKey, Kind: string(r.Kind),
			Recurrence: recSpecIn(r.Recurrence), GuidanceText: r.GuidanceText,
		}
		in.SourceVersionIDs = append(in.SourceVersionIDs, r.SourceVersionIds...)
		for _, f := range r.RequiredFacts {
			in.RequiredFacts = append(in.RequiredFacts, string(f))
		}
		if r.ActivityKind != nil {
			ak := string(*r.ActivityKind)
			in.ActivityKind = &ak
		}
		if r.WeatherSensitivity != nil {
			w := string(*r.WeatherSensitivity)
			in.WeatherSensitivity = &w
		}
		out = append(out, in)
	}
	return out
}

func careEnvIn(e *gql.ProfileEnvironmentInput) *care.EnvironmentInput {
	if e == nil {
		return nil
	}
	out := &care.EnvironmentInput{}
	for _, s := range e.Situations {
		out.Situations = append(out.Situations, string(s))
	}
	for _, h := range e.Hemispheres {
		out.Hemispheres = append(out.Hemispheres, string(h))
	}
	return out
}

// ---- payload helpers ----------------------------------------------------------

// occurrenceView attaches the plan's plant to a plan-keyed occurrence row,
// plus the exact weather snapshot its advice references (T26).
func (m mutationResolver) occurrenceView(ctx context.Context, row dbgen.CareOccurrence) (care.OccurrenceView, error) {
	plan, err := m.r.DB.GetCarePlan(ctx, row.PlanID)
	if err != nil {
		return care.OccurrenceView{}, err
	}
	views := []care.OccurrenceView{{Row: row, PlantID: uuidString(plan.PlantID)}}
	if err := m.r.Care.AttachWeather(ctx, plan.WorkspaceID, views); err != nil {
		return care.OccurrenceView{}, err
	}
	return views[0], nil
}

// nextOccurrenceOut loads the earliest open work for the rule; skipID
// suppresses echoing the payload occurrence itself (undo reopens it).
func (m mutationResolver) nextOccurrenceOut(ctx context.Context, planID pgtype.UUID, ruleKey, skipID string) (*gql.CareOccurrence, error) {
	v, ok, err := m.r.Care.NextOpenOccurrence(ctx, planID, ruleKey)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if !ok || uuidString(v.Row.ID) == skipID {
		return nil, nil
	}
	return occurrenceOut(v), nil
}

// activityOutFor hydrates the completion-linked activity (voided
// activities come back with voidedAt set by the journal read).
func (m mutationResolver) activityOutFor(ctx context.Context, row dbgen.CareOccurrence) (*gql.Activity, error) {
	if !row.CompletedActivityID.Valid {
		return nil, nil
	}
	acts, err := m.r.Journal.ActivitiesByIDs(ctx, row.WorkspaceID, []pgtype.UUID{row.CompletedActivityID})
	if err != nil {
		return nil, internalError(ctx, err)
	}
	v, ok := acts[uuidString(row.CompletedActivityID)]
	if !ok {
		return nil, nil
	}
	return activityOut(v), nil
}

// ---- Query resolvers ----------------------------------------------------------

func (q queryResolver) CareAgenda(ctx context.Context, from, to *scalars.Date, plantID *string, first *int, after *string) (*gql.CareOccurrenceConnection, error) {
	if q.r.Care == nil {
		return nil, capabilityUnavailable("careAgenda")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	fromS, toS := dateStrPtr(from), dateStrPtr(to)
	if fromS == nil || toS == nil {
		// SDL default: today..+60d in the workspace timezone.
		ws, err := q.r.DB.GetWorkspace(ctx, p.OwnerID)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		loc, err := time.LoadLocation(ws.Timezone)
		if err != nil {
			loc = time.UTC
		}
		today := time.Now().In(loc)
		if fromS == nil {
			f := today.Format("2006-01-02")
			fromS = &f
		}
		if toS == nil {
			t := today.AddDate(0, 0, 60).Format("2006-01-02")
			toS = &t
		}
	}
	page, probs, err := q.r.Care.CareAgenda(ctx, careActor(p), fromS, toS, plantID, pageFirst(first), after)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(probs) > 0 {
		return nil, careProblemsError(probs)
	}
	occ := make([]*gql.CareOccurrence, 0, len(page.Items))
	for _, v := range page.Items {
		occ = append(occ, occurrenceOut(v))
	}
	return &gql.CareOccurrenceConnection{Items: occ, PageInfo: carePageInfo(page.NextCursor)}, nil
}

func (q queryResolver) CareProfiles(ctx context.Context, filter *gql.CareProfileFilter, first *int, after *string) (*gql.CareProfileConnection, error) {
	if q.r.Care == nil {
		return nil, capabilityUnavailable("careProfiles")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	f := care.ProfileFilter{}
	if filter != nil {
		if filter.Category != nil {
			c := string(*filter.Category)
			f.Category = &c
		}
		f.Query = filter.Query
		f.IncludeDrafts = filter.IncludeDrafts
	}
	page, probs, err := q.r.Care.ListCareProfiles(ctx, careActor(p), f, pageFirst(first), after)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(probs) > 0 {
		return nil, careProblemsError(probs)
	}
	items := make([]*gql.CareProfile, 0, len(page.Items))
	for _, v := range page.Items {
		po, err := profileOut(v)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		items = append(items, po)
	}
	return &gql.CareProfileConnection{Items: items, PageInfo: carePageInfo(page.NextCursor)}, nil
}

func (q queryResolver) CareProfile(ctx context.Context, id string) (*gql.CareProfile, error) {
	if q.r.Care == nil {
		return nil, capabilityUnavailable("careProfile")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	view, probs, err := q.r.Care.GetCareProfile(ctx, careActor(p), id)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(probs) > 0 {
		return nil, careProblemsError(probs)
	}
	return profileOut(view)
}

func (q queryResolver) Sources(ctx context.Context, filter *gql.SourceFilter, first *int, after *string) (*gql.SourceConnection, error) {
	if q.r.Care == nil {
		return nil, capabilityUnavailable("sources")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	f := care.SourceFilter{}
	if filter != nil {
		if filter.Kind != nil {
			k := string(*filter.Kind)
			f.Kind = &k
		}
		f.IncludeRetired = filter.IncludeRetired
	}
	page, probs, err := q.r.Care.ListSources(ctx, careActor(p), f, pageFirst(first), after)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(probs) > 0 {
		return nil, careProblemsError(probs)
	}
	items := make([]*gql.Source, 0, len(page.Items))
	for _, v := range page.Items {
		items = append(items, sourceOut(v))
	}
	return &gql.SourceConnection{Items: items, PageInfo: carePageInfo(page.NextCursor)}, nil
}

func (q queryResolver) Notifications(ctx context.Context, unreadOnly *bool, first *int, after *string) (*gql.NotificationConnection, error) {
	if q.r.Care == nil {
		return nil, capabilityUnavailable("notifications")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	page, probs, err := q.r.Care.ListNotifications(ctx, careActor(p),
		unreadOnly != nil && *unreadOnly, pageFirst(first), after)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(probs) > 0 {
		return nil, careProblemsError(probs)
	}
	items := make([]*gql.Notification, 0, len(page.Items))
	for _, v := range page.Items {
		items = append(items, notificationOut(v))
	}
	return &gql.NotificationConnection{Items: items, PageInfo: carePageInfo(page.NextCursor)}, nil
}

func (q queryResolver) PreviewCarePlan(ctx context.Context, in gql.PreviewCarePlanInput) (*gql.CarePlanPreviewPayload, error) {
	if q.r.Care == nil {
		return nil, capabilityUnavailable("previewCarePlan")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ovs, bad := careOverridesIn(in.Overrides)
	if len(bad) > 0 {
		return &gql.CarePlanPreviewPayload{Problems: bad}, nil
	}
	prev, probs, err := q.r.Care.PreviewCarePlan(ctx, careActor(p), care.PreviewPlanInput{
		PlantID: in.PlantID, ProfileVersionID: in.ProfileVersionID,
		Timezone: in.Timezone, NotificationTime: in.NotificationTime,
		Overrides: ovs, IncludeWouldCancel: in.IncludeWouldCancel,
	})
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.CarePlanPreviewPayload{Problems: mapCareProblems(probs)}
	if len(probs) > 0 {
		return payload, nil
	}
	out := &gql.CarePlanPreview{
		Occurrences:  make([]*gql.PreviewOccurrence, 0, len(prev.Occurrences)),
		WouldCancel:  make([]*gql.CareOccurrence, 0, len(prev.WouldCancel)),
		MissingFacts: make([]gql.ContextFact, 0, len(prev.MissingFacts)),
	}
	for _, v := range prev.Occurrences {
		po := &gql.PreviewOccurrence{
			RuleKey: v.RuleKey, Kind: gql.RuleKind(v.Kind), DueDate: scalars.Date(v.DueDate),
		}
		if v.ActivityKind != "" {
			ak := gql.ActivityKind(v.ActivityKind)
			po.ActivityKind = &ak
		}
		row := dbgen.CareOccurrence{ReasonText: pgtype.Text{String: v.ReasonText, Valid: v.ReasonText != ""},
			MissingFacts: v.MissingFacts, EvidenceNotes: v.EvidenceNotes}
		po.Reason = occurrenceReasonOut(row, v.Weather)
		out.Occurrences = append(out.Occurrences, po)
	}
	for _, r := range prev.WouldCancel {
		v, err := q.occurrenceViewFromPlan(ctx, r)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		out.WouldCancel = append(out.WouldCancel, occurrenceOut(v))
	}
	for _, f := range prev.MissingFacts {
		out.MissingFacts = append(out.MissingFacts, gql.ContextFact(f))
	}
	payload.Preview = out
	return payload, nil
}

// occurrenceViewFromPlan attaches the plant id for a would-cancel row
// (query-side twin of mutationResolver.occurrenceView).
func (q queryResolver) occurrenceViewFromPlan(ctx context.Context, row dbgen.CareOccurrence) (care.OccurrenceView, error) {
	plan, err := q.r.DB.GetCarePlan(ctx, row.PlanID)
	if err != nil {
		return care.OccurrenceView{}, err
	}
	views := []care.OccurrenceView{{Row: row, PlantID: uuidString(plan.PlantID)}}
	if err := q.r.Care.AttachWeather(ctx, plan.WorkspaceID, views); err != nil {
		return care.OccurrenceView{}, err
	}
	return views[0], nil
}

// ---- Mutation resolvers --------------------------------------------------------

func (m mutationResolver) CreateSource(ctx context.Context, in gql.CreateSourceInput, cmid *string) (*gql.SourcePayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, probs, err := m.r.Care.CreateSource(ctx, careActor(p), care.CreateSourceInput{
		Kind: string(in.Kind), Title: in.Title, Publisher: in.Publisher, URL: in.URL,
		Region: in.Region, License: in.License, Citation: in.Citation, Summary: in.Summary,
		RetrievedOn: dateStrPtr(in.RetrievedOn), ContentSha256: in.ContentSha256,
	}, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.SourcePayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) == 0 {
		payload.Source = sourceOut(v)
	}
	return payload, nil
}

func (m mutationResolver) UpdateSource(ctx context.Context, in gql.UpdateSourceInput, cmid *string) (*gql.SourcePayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, probs, err := m.r.Care.UpdateSource(ctx, careActor(p), care.UpdateSourceInput{
		ID: in.ID, ExpectedRevision: int32(in.ExpectedRevision),
		Title: in.Title, Publisher: in.Publisher, URL: in.URL,
		Region: in.Region, License: in.License, Citation: in.Citation, Summary: in.Summary,
		RetrievedOn: dateStrPtr(in.RetrievedOn), ContentSha256: in.ContentSha256,
	}, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.SourcePayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) == 0 {
		payload.Source = sourceOut(v)
	}
	return payload, nil
}

func (m mutationResolver) RetireSource(ctx context.Context, in gql.RetireSourceInput, cmid *string) (*gql.SourcePayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, probs, err := m.r.Care.RetireSource(ctx, careActor(p), in.ID, int32(in.ExpectedRevision), cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.SourcePayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) == 0 {
		payload.Source = sourceOut(v)
	}
	return payload, nil
}

func (m mutationResolver) CreateCareProfileDraft(ctx context.Context, in gql.CreateCareProfileDraftInput, cmid *string) (*gql.CareProfilePayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, probs, err := m.r.Care.CreateCareProfileDraft(ctx, careActor(p), care.CreateCareProfileDraftInput{
		Category: string(in.Category), Species: in.Species, Cultivar: in.Cultivar,
		Environment: careEnvIn(in.Environment), Rules: careRuleIns(in.Rules),
	}, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.CareProfilePayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) == 0 {
		po, err := profileOut(v)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		payload.Profile = po
	}
	return payload, nil
}

func (m mutationResolver) UpdateCareProfileDraft(ctx context.Context, in gql.UpdateCareProfileDraftInput, cmid *string) (*gql.CareProfilePayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	svcIn := care.UpdateCareProfileDraftInput{
		ID: in.ID, ExpectedRevision: int32(in.ExpectedRevision),
		Species: in.Species, Cultivar: in.Cultivar,
		Environment: careEnvIn(in.Environment),
	}
	if in.Rules != nil {
		svcIn.Rules = careRuleIns(in.Rules)
	}
	v, probs, err := m.r.Care.UpdateCareProfileDraft(ctx, careActor(p), svcIn, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.CareProfilePayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) == 0 {
		po, err := profileOut(v)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		payload.Profile = po
	}
	return payload, nil
}

func (m mutationResolver) DeleteCareProfileDraft(ctx context.Context, in gql.DeleteCareProfileDraftInput, cmid *string) (*gql.DeleteCareProfileDraftPayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	id, probs, err := m.r.Care.DeleteCareProfileDraft(ctx, careActor(p), in.ID, int32(in.ExpectedRevision), cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.DeleteCareProfileDraftPayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) == 0 {
		payload.DeletedProfileID = &id
	}
	return payload, nil
}

func (m mutationResolver) PublishCareProfile(ctx context.Context, in gql.PublishCareProfileInput, cmid *string) (*gql.CareProfilePayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, probs, err := m.r.Care.PublishCareProfile(ctx, careActor(p), care.PublishCareProfileInput{
		ProfileID: in.ProfileID, ExpectedRevision: int32(in.ExpectedRevision),
		ReviewerLabel: in.ReviewerLabel, ReviewedOn: string(in.ReviewedOn),
	}, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.CareProfilePayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) == 0 {
		po, err := profileOut(v)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		payload.Profile = po
	}
	return payload, nil
}

func (m mutationResolver) RetireCareProfileVersion(ctx context.Context, in gql.RetireCareProfileVersionInput, cmid *string) (*gql.CareProfilePayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, probs, err := m.r.Care.RetireCareProfileVersion(ctx, careActor(p),
		in.ProfileID, in.ProfileVersionID, int32(in.ExpectedRevision), cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.CareProfilePayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) == 0 {
		po, err := profileOut(v)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		payload.Profile = po
	}
	return payload, nil
}

func (m mutationResolver) CreateCarePlan(ctx context.Context, in gql.CreateCarePlanInput, cmid *string) (*gql.CarePlanPayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ovs, bad := careOverridesIn(in.Overrides)
	if len(bad) > 0 {
		return &gql.CarePlanPayload{Problems: bad, ClientMutationID: cmid}, nil
	}
	row, probs, err := m.r.Care.CreateCarePlan(ctx, careActor(p), care.CreatePlanInput{
		PlantID: in.PlantID, ProfileVersionID: in.ProfileVersionID,
		Timezone: in.Timezone, NotificationTime: in.NotificationTime,
		Enabled: in.Enabled, Overrides: ovs,
	}, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.CarePlanPayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) > 0 {
		return payload, nil
	}
	pv, probs, err := m.r.Care.PlanViewOf(ctx, careActor(p), row)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(probs) > 0 {
		return payload, nil
	}
	plan, err := planOut(pv)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload.Plan = plan
	return payload, nil
}

func (m mutationResolver) UpdateCarePlan(ctx context.Context, in gql.UpdateCarePlanInput, cmid *string) (*gql.CarePlanPayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ovs, bad := careOverridesIn(in.Overrides)
	if len(bad) > 0 {
		return &gql.CarePlanPayload{Problems: bad, ClientMutationID: cmid}, nil
	}
	svcIn := care.UpdatePlanInput{
		PlanID: in.ID, ExpectedRevision: int32(in.ExpectedRevision),
		ProfileVersionID:    in.ProfileVersionID,
		ClearProfileVersion: in.ClearProfileVersion != nil && *in.ClearProfileVersion,
		Timezone:            in.Timezone, NotificationTime: in.NotificationTime,
		Enabled: in.Enabled, Overrides: ovs,
		ConfirmRecalculation: in.ConfirmRecalculation != nil && *in.ConfirmRecalculation,
	}
	row, probs, err := m.r.Care.UpdateCarePlan(ctx, careActor(p), svcIn, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.CarePlanPayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) > 0 {
		return payload, nil
	}
	pv, probs, err := m.r.Care.PlanViewOf(ctx, careActor(p), row)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(probs) > 0 {
		return payload, nil
	}
	plan, err := planOut(pv)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload.Plan = plan
	return payload, nil
}

func (m mutationResolver) CompleteOccurrence(ctx context.Context, in gql.CompleteOccurrenceInput, cmid *string) (*gql.CompleteOccurrencePayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	row, probs, err := m.r.Care.CompleteOccurrence(ctx, careActor(p), care.CompleteOccurrenceInput{
		OccurrenceID: in.OccurrenceID, ExpectedRevision: int32(in.ExpectedRevision),
		OccurredOn: dateStrPtr(in.OccurredOn),
		Note:       in.Note, Quantity: decPtr(in.Quantity), Unit: unitPtr(in.Unit),
		Product: in.Product,
	}, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.CompleteOccurrencePayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) > 0 {
		return payload, nil
	}
	ov, err := m.occurrenceView(ctx, row)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload.Occurrence = occurrenceOut(ov)
	act, err := m.activityOutFor(ctx, row)
	if err != nil {
		return nil, err
	}
	payload.Activity = act
	next, err := m.nextOccurrenceOut(ctx, row.PlanID, row.RuleKey, "")
	if err != nil {
		return nil, err
	}
	payload.NextOccurrence = next
	return payload, nil
}

func (m mutationResolver) SkipOccurrence(ctx context.Context, in gql.SkipOccurrenceInput, cmid *string) (*gql.OccurrencePayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	row, probs, err := m.r.Care.SkipOccurrence(ctx, careActor(p), care.SkipOccurrenceInput{
		OccurrenceID: in.OccurrenceID, ExpectedRevision: int32(in.ExpectedRevision),
	}, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.OccurrencePayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) > 0 {
		return payload, nil
	}
	ov, err := m.occurrenceView(ctx, row)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload.Occurrence = occurrenceOut(ov)
	next, err := m.nextOccurrenceOut(ctx, row.PlanID, row.RuleKey, "")
	if err != nil {
		return nil, err
	}
	payload.NextOccurrence = next
	return payload, nil
}

func (m mutationResolver) SnoozeOccurrence(ctx context.Context, in gql.SnoozeOccurrenceInput, cmid *string) (*gql.OccurrencePayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	row, probs, err := m.r.Care.SnoozeOccurrence(ctx, careActor(p), care.SnoozeOccurrenceInput{
		OccurrenceID: in.OccurrenceID, ExpectedRevision: int32(in.ExpectedRevision),
		Until: string(in.Until),
	}, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.OccurrencePayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) > 0 {
		return payload, nil
	}
	ov, err := m.occurrenceView(ctx, row)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload.Occurrence = occurrenceOut(ov)
	next, err := m.nextOccurrenceOut(ctx, row.PlanID, row.RuleKey, "")
	if err != nil {
		return nil, err
	}
	payload.NextOccurrence = next
	return payload, nil
}

func (m mutationResolver) CorrectOccurrenceCompletion(ctx context.Context, in gql.CorrectOccurrenceCompletionInput, cmid *string) (*gql.CorrectOccurrenceCompletionPayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	svcIn := care.CorrectCompletionInput{
		OccurrenceID: in.OccurrenceID, ExpectedRevision: int32(in.ExpectedRevision),
		Undo: in.Undo != nil && *in.Undo,
		Note: in.Note, Quantity: decPtr(in.Quantity), Unit: unitPtr(in.Unit), Product: in.Product,
	}
	if in.OccurredAt != nil {
		// Contract gives an instant + entry zone; the service anchors on the
		// plan-local calendar date of that instant.
		occ, err := m.r.DB.GetCareOccurrence(ctx, mustUUID(in.OccurrenceID))
		if err != nil {
			return nil, internalError(ctx, err)
		}
		zone := occ.PlanTimezone
		if in.Timezone != nil {
			zone = *in.Timezone
		}
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return &gql.CorrectOccurrenceCompletionPayload{ClientMutationID: cmid,
				Problems: []*gql.MutationProblem{{Code: gql.ProblemCodeValidation,
					Field: strPtr("input.timezone"), Message: "unknown IANA timezone"}}}, nil
		}
		d := time.Time(*in.OccurredAt).In(loc).Format("2006-01-02")
		svcIn.OccurredOn = &d
	}
	row, probs, err := m.r.Care.CorrectCompletion(ctx, careActor(p), svcIn, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.CorrectOccurrenceCompletionPayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) > 0 {
		return payload, nil
	}
	ov, err := m.occurrenceView(ctx, row)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload.Occurrence = occurrenceOut(ov)
	if row.Status != dbgen.OccurrenceStatusCOMPLETIONRETRACTED {
		// Undo voids the linked activity; the contract reports null then.
		if !(svcIn.Undo && row.Status == dbgen.OccurrenceStatusDUE) {
			act, err := m.activityOutFor(ctx, row)
			if err != nil {
				return nil, err
			}
			payload.Activity = act
		}
	}
	retracted := row.Status == dbgen.OccurrenceStatusCOMPLETIONRETRACTED
	payload.Retracted = &retracted
	next, err := m.nextOccurrenceOut(ctx, row.PlanID, row.RuleKey, uuidString(row.ID))
	if err != nil {
		return nil, err
	}
	payload.NextOccurrence = next
	return payload, nil
}

func (m mutationResolver) MarkNotificationRead(ctx context.Context, in gql.MarkNotificationReadInput, cmid *string) (*gql.NotificationPayload, error) {
	if err := m.requireCare(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, probs, err := m.r.Care.MarkNotificationRead(ctx, careActor(p),
		care.MarkNotificationReadInput{NotificationID: in.NotificationID}, cmid)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	payload := &gql.NotificationPayload{Problems: mapCareProblems(probs), ClientMutationID: cmid}
	if len(probs) == 0 {
		payload.Notification = notificationOut(v)
	}
	return payload, nil
}
