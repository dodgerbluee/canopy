package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/planning"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// PlanningProvider adapts crop/region research to planning's narrow port. It does
// not need an owned plant, and it never sends private plan context to a model.
type PlanningProvider struct{ Service *Service }
type planningEnvironment struct {
	context  planning.GuidanceContext
	settings dbgen.KnowledgeSettingsRow
	sources  []Source
	model    PlantingModel
	crop     string
}

func (p PlanningProvider) Context(ctx context.Context, ws pgtype.UUID, subject planning.GuidanceSubject) (planning.GuidanceContext, error) {
	env, err := p.environment(ctx, ws, subject)
	return env.context, err
}
func (p PlanningProvider) environment(ctx context.Context, ws pgtype.UUID, subject planning.GuidanceSubject) (planningEnvironment, error) {
	var env planningEnvironment
	s := p.Service
	settings, err := s.Settings(ctx, ws)
	if err != nil {
		return env, err
	}
	env.settings = settings
	env.crop = cropFor(subject.Name, subject.ScientificName, subject.Category)
	env.sources = Applicable(s.Sources, settings.Country, settings.Region, env.crop)
	env.context = planning.GuidanceContext{State: "PENDING", Message: "Waiting for regional planting guidance.", Assumptions: []string{}}
	locRevision := int32(0)
	locSituation := ""
	var locLat, locLon *pgtype.Numeric
	if subject.LocationID != "" {
		var id pgtype.UUID
		if id.Scan(subject.LocationID) != nil {
			return env, errors.New("invalid stored planning location")
		}
		location, e := s.Q.GetLocation(ctx, dbgen.GetLocationParams{ID: id, WorkspaceID: ws})
		if errors.Is(e, pgx.ErrNoRows) {
			env.context.State = "NEEDS_LOCATION"
			env.context.Message = "The intended location changed. Review the plan location."
		} else if e != nil {
			return env, e
		} else {
			locRevision = location.Revision
			locSituation = string(location.Situation)
			locLat = location.Latitude
			locLon = location.Longitude
		}
	}
	modelIdentity := ""
	switch {
	case !settings.Enabled:
		env.context.State = "DISABLED"
		env.context.Message = "Enable regional knowledge in garden settings to research this plan."
	case settings.Country == "" || settings.Region == "":
		env.context.State = "NEEDS_LOCATION"
		env.context.Message = "Set a country and region for source matching. Timezone is not a climate location."
	case len(env.sources) == 0:
		env.context.State = "UNSUPPORTED"
		env.context.Message = "No registered regional publication covers this crop here yet. You can plan it manually."
	case len(env.sources) > 3:
		env.context.State = "FAILED"
		env.context.Message = "The source registry exceeds the per-plan research bound."
	case env.context.State == "PENDING":
		selected, e := s.selectedModel(ctx, ws)
		if e != nil {
			env.context.State = "FAILED"
			env.context.Message = "The garden AI connection is unavailable. Review AI connections in Settings."
		} else if selected == nil {
			env.context.State = "DISABLED"
			env.context.Message = "Choose an enabled garden AI connection in Settings to research this plan."
		} else if model, ok := selected.(PlantingModel); ok {
			env.model = model
			modelIdentity = model.PlanningIdentity()
		} else {
			env.context.State = "FAILED"
			env.context.Message = "The selected garden model adapter does not support planting extraction."
		}
	}
	if subject.Method == "" {
		env.context.Assumptions = append(env.context.Assumptions, "Choose a starting method to narrow planting advice.")
	}
	if subject.Placement == "" {
		env.context.Assumptions = append(env.context.Assumptions, "Container or ground conditions are not confirmed.")
	}
	env.context.Assumptions = append(env.context.Assumptions, "Guidance uses the saved garden region; confirm it applies to the intended site.", "Frost dates are unknown until supplied explicitly; zone and timezone do not provide them.")
	env.context.Fingerprint = hash([]any{PlanningExtractionVersion, uid(ws), subject, settings.Country, normalize(settings.Region), settings.Place, settings.Revision, settings.Timezone, settings.Latitude, settings.Longitude, settings.HardinessZone.String, settings.ZoneSystem.String, settings.Enabled, locRevision, locSituation, locLat, locLon, env.sources, modelIdentity, env.context.State})
	return env, nil
}
func (p PlanningProvider) Research(ctx context.Context, ws pgtype.UUID, subject planning.GuidanceSubject, expected planning.GuidanceContext) (planning.GuidanceResult, error) {
	env, err := p.environment(ctx, ws, subject)
	if err != nil {
		return planning.GuidanceResult{}, err
	}
	if env.context.Fingerprint != expected.Fingerprint {
		return planning.GuidanceResult{}, planning.ErrGuidanceContextChanged
	}
	out := planning.GuidanceResult{State: env.context.State, Message: env.context.Message, Facts: []planning.GuidanceFact{}, Assumptions: append([]string{}, env.context.Assumptions...)}
	if env.context.State != "PENDING" {
		return out, nil
	}
	identity := subject.Name + " " + subject.ScientificName + " " + subject.Cultivar + " " + env.crop
	localIdentity := env.settings.Country + " " + env.settings.Region + " " + env.settings.Place
	for _, source := range env.sources {
		publication, err := p.Service.publication(ctx, source)
		if err != nil {
			out.State = "FAILED"
			out.Message = "A regional publication could not be read. Your plan is saved; retry when the source is available."
			return out, nil
		}
		facts, err := p.sourceFacts(ctx, ws, source, publication, env.model)
		if err != nil {
			if errors.Is(err, errPlanningQuota) {
				out.State = "FAILED"
				out.Message = "The hourly planting research limit was reached. Refresh after the next UTC hour."
			} else if errors.Is(err, planning.ErrGuidanceContextChanged) {
				return out, err
			} else {
				out.State = "FAILED"
				out.Message = "The publication could not produce verified planting guidance. Your plan is saved; review the source or retry."
			}
			return out, nil
		}
		for _, fact := range facts {
			if !matchesRestriction(identity, fact.Subjects) || !matchesRestriction(localIdentity, fact.Localities) {
				continue
			}
			if len(fact.Methods) > 0 && (subject.Method == "" || !stringIn(subject.Method, fact.Methods)) {
				continue
			}
			if len(fact.Placements) > 0 && (subject.Placement == "" || !stringIn(subject.Placement, fact.Placements)) {
				continue
			}
			out.Facts = append(out.Facts, fact.Fact)
		}
	}
	if len(out.Facts) == 0 {
		out.State = "UNSUPPORTED"
		out.Message = "The registered publication has no guidance matching the known method, type and site. Keep planning manually or refine those details."
		return out, nil
	}
	if err = planning.ValidateGuidanceFacts(out.Facts); err != nil {
		out.State = "FAILED"
		out.Message = "The compatible guidance exceeded its validated bounds."
		out.Facts = []planning.GuidanceFact{}
		return out, nil
	}
	out.State = "READY"
	out.Message = "Source-supported planting guidance. Review its conditions before choosing dates."
	out.Assumptions = append(out.Assumptions, "Source windows are typical guidance, not biological deadlines or a harvest guarantee.", "Seasonal harvest windows alone do not establish when a new planting will first produce.")
	return out, nil
}
func stringIn(value string, values []string) bool {
	for _, v := range values {
		if value == v {
			return true
		}
	}
	return false
}
func matchesRestriction(identity string, restrictions []string) bool {
	if len(restrictions) == 0 {
		return true
	}
	for _, v := range restrictions {
		if containsWord(identity, v) {
			return true
		}
	}
	return false
}

var errPlanningQuota = errors.New("planting provider quota exhausted")

func (p PlanningProvider) sourceFacts(ctx context.Context, ws pgtype.UUID, source Source, publication dbgen.KnowledgePublication, model PlantingModel) ([]plantingSourceFact, error) {
	key := hash([]any{PlanningExtractionVersion, source})
	conn, err := p.Service.Pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	lockKey := "planning-source|" + source.URL
	locked := false
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if locked {
			var released bool
			if err := conn.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, lockKey).Scan(&released); err != nil || !released {
				_ = conn.Conn().Close(cleanup)
			}
		}
		conn.Release()
	}()
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, lockKey); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Conn().Close(cleanup)
		return nil, err
	}
	locked = true
	q := dbgen.New(conn)
	latest, err := q.GetPublication(ctx, source.URL)
	if err != nil {
		return nil, err
	}
	publication = latest
	if publication.PlanningKey.Valid && publication.PlanningKey.String == key {
		var cached []plantingSourceFact
		if err = json.Unmarshal(publication.PlanningFacts, &cached); err == nil && len(cached) > 0 && len(cached) <= 12 {
			valid := true
			for _, fact := range cached {
				if fact.Fact.ExtractionVersion != PlanningExtractionVersion || fact.Fact.SourceURL != source.URL || fact.Fact.ContentSHA256 != publication.ContentSha256 || fact.Fact.Evidence == "" || !strings.Contains(strings.Join(strings.Fields(canonicalPlantingText(publication.SourceText)), " "), fact.Fact.Evidence) || planning.ValidateGuidanceFacts([]planning.GuidanceFact{fact.Fact}) != nil {
					valid = false
				}
			}
			if valid {
				return cached, nil
			}
		}
	}
	sourceText := planningSourceText(source, publication.SourceText)
	if strings.TrimSpace(sourceText) == "" {
		return nil, errors.New("reviewed crop section is unavailable")
	}
	// The allowance commits on this connection before the provider call, so
	// failed attempts count without requiring another pool connection.
	reserved, err := q.ReservePlanningProviderRequest(ctx, ws)
	if err != nil {
		return nil, err
	}
	if reserved == 0 {
		return nil, errPlanningQuota
	}
	drafts, err := model.ExtractPlanting(ctx, source, sourceText)
	if err != nil {
		return nil, err
	}
	facts, err := usablePlantingSource(source, sourceText, drafts, time.Now().UTC(), model.PlanningIdentity())
	if err != nil {
		return nil, err
	}
	for i := range facts {
		facts[i].Fact.ContentSHA256 = publication.ContentSha256
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return nil, err
	}
	written, err := q.PutPlanningPublicationFacts(ctx, dbgen.PutPlanningPublicationFactsParams{Url: source.URL, PlanningKey: pgtype.Text{String: key, Valid: true}, PlanningFacts: raw, ContentSha256: publication.ContentSha256})
	if err != nil {
		return nil, err
	}
	if written == 0 {
		return nil, planning.ErrGuidanceContextChanged
	}
	return facts, nil
}
