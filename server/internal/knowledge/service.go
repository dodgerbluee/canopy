package knowledge

import (
	"canopy/server/internal/agentcontext"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Actor struct{ WorkspaceID, OwnerID, SessionID pgtype.UUID }
type Problem struct {
	Code, Field, Message string
	Revision             *int32
}

func (p *Problem) Error() string { return p.Message }
func invalid(field, message string) error {
	return &Problem{Code: "VALIDATION", Field: field, Message: message}
}
func uid(u pgtype.UUID) string { v, _ := u.Value(); s, _ := v.(string); return s }
func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func optional(s *string) pgtype.Text {
	if s == nil || strings.TrimSpace(*s) == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: strings.TrimSpace(*s), Valid: true}
}

type Service struct {
	GeocodingHTTP *http.Client
	Pool          *pgxpool.Pool
	Q             *dbgen.Queries
	Fetch         *Fetcher
	Model         Model
	ResolveModel  func(context.Context, pgtype.UUID) (Model, error)
	Sources       []Source
	chatMu        sync.Mutex
	chatUsage     map[string][]time.Time
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{Pool: pool, Q: dbgen.New(pool), Sources: Registry(), chatUsage: map[string][]time.Time{}}
}
func (s *Service) Settings(ctx context.Context, ws pgtype.UUID) (dbgen.KnowledgeSettingsRow, error) {
	return s.Q.KnowledgeSettings(ctx, ws)
}
func transaction[T any](s *Service, ctx context.Context, a Actor, op string, key *string, input any, fn func(*dbgen.Queries) (T, error)) (T, error) {
	var zero T
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	if err := agentcontext.Enter(ctx, tx); err != nil {
		return zero, err
	}
	cmid := ""
	if key != nil {
		cmid = strings.TrimSpace(*key)
	}
	if len(cmid) > 200 {
		return zero, invalid("clientMutationId", "Request identity is too long.")
	}
	if cmid != "" {
		if err = q.LockMutationKey(ctx, uid(a.WorkspaceID)+"|"+op+"|"+cmid); err != nil {
			return zero, err
		}
		receipt, err := q.GetMutationReceipt(ctx, dbgen.GetMutationReceiptParams{WorkspaceID: a.WorkspaceID, Operation: op, IdempotencyKey: cmid})
		if err == nil {
			if receipt.RequestHash != hash(input) {
				return zero, &Problem{Code: "IDEMPOTENCY_CONFLICT", Message: "This request identity was used for different values."}
			}
			if receipt.Gone {
				return zero, &Problem{Code: "RESOURCE_GONE", Message: "The stored response is no longer available."}
			}
			var replay T
			err = json.Unmarshal(receipt.Result, &replay)
			return replay, err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return zero, err
		}
	}
	result, err := fn(q)
	if err != nil {
		return zero, err
	}
	if cmid != "" {
		b, err := json.Marshal(result)
		if err != nil {
			return zero, err
		}
		_, err = q.InsertMutationReceipt(ctx, dbgen.InsertMutationReceiptParams{ID: platform.UUIDv4{}.New(), WorkspaceID: a.WorkspaceID, Operation: op, IdempotencyKey: cmid, RequestHash: hash(input), Result: b, ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true}})
		if err != nil {
			return zero, err
		}
	}
	if err = agentcontext.Complete(ctx, tx, result); err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return result, nil
}

type SettingsInput struct {
	Country, Region, Place, Timezone               string
	Latitude, Longitude, HardinessZone, ZoneSystem *string
	Enabled                                        bool
	ExpectedRevision                               int32
}

func numeric(raw *string, min, max int64) (*pgtype.Numeric, error) {
	if raw == nil {
		return nil, nil
	}
	r, ok := new(big.Rat).SetString(*raw)
	if !ok || r.Cmp(big.NewRat(min, 1)) < 0 || r.Cmp(big.NewRat(max, 1)) > 0 {
		return nil, errors.New("coordinate is outside its valid range")
	}
	var n pgtype.Numeric
	if err := n.Scan(*raw); err != nil || !n.Valid || n.NaN || n.Exp < -6 {
		return nil, errors.New("coordinate must be a decimal with at most six fractional digits")
	}
	return &n, nil
}
func (s *Service) SaveSettings(ctx context.Context, a Actor, in SettingsInput, key *string) (dbgen.KnowledgeSettingsRow, error) {
	in.Country = strings.ToUpper(strings.TrimSpace(in.Country))
	in.Region = strings.TrimSpace(in.Region)
	in.Place = strings.TrimSpace(in.Place)
	if !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(in.Country) {
		return dbgen.KnowledgeSettingsRow{}, invalid("country", "Use a two-letter country code, such as US or GB.")
	}
	if len(in.Region) > 160 || len(in.Region) < 1 || len(in.Place) > 160 || len(in.Place) < 1 {
		return dbgen.KnowledgeSettingsRow{}, invalid("region", "Enter your garden's region and location name.")
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return dbgen.KnowledgeSettingsRow{}, invalid("timezone", "Choose a valid IANA timezone.")
	}
	if in.ExpectedRevision < 0 {
		return dbgen.KnowledgeSettingsRow{}, invalid("expectedRevision", "Invalid settings revision.")
	}
	if (in.Latitude == nil) != (in.Longitude == nil) {
		return dbgen.KnowledgeSettingsRow{}, invalid("latitude", "Latitude and longitude must be supplied together.")
	}
	lat, err := numeric(in.Latitude, -90, 90)
	if err != nil {
		return dbgen.KnowledgeSettingsRow{}, invalid("latitude", err.Error())
	}
	lng, err := numeric(in.Longitude, -180, 180)
	if err != nil {
		return dbgen.KnowledgeSettingsRow{}, invalid("longitude", err.Error())
	}
	if (in.HardinessZone == nil) != (in.ZoneSystem == nil) {
		return dbgen.KnowledgeSettingsRow{}, invalid("zoneSystem", "Choose a zone system when entering a hardiness zone.")
	}
	if in.HardinessZone != nil {
		if len(*in.HardinessZone) > 40 || len(*in.ZoneSystem) > 40 {
			return dbgen.KnowledgeSettingsRow{}, invalid("hardinessZone", "Zone labels must be at most 40 characters.")
		}
		if strings.EqualFold(*in.ZoneSystem, "USDA") && !regexp.MustCompile(`^(?:[1-9]|1[0-3])[ab]?$`).MatchString(strings.ToLower(*in.HardinessZone)) {
			return dbgen.KnowledgeSettingsRow{}, invalid("hardinessZone", "Enter a USDA zone from 1a to 13b.")
		}
	}
	return transaction(s, ctx, a, "saveGardenKnowledgeSettings", key, in, func(q *dbgen.Queries) (dbgen.KnowledgeSettingsRow, error) {
		if err := q.LockMutationKey(ctx, uid(a.WorkspaceID)+"|garden-settings"); err != nil {
			return dbgen.KnowledgeSettingsRow{}, err
		}
		cur, err := q.KnowledgeSettings(ctx, a.WorkspaceID)
		if err != nil {
			return cur, err
		}
		if cur.Revision != in.ExpectedRevision {
			return cur, &Problem{Code: "CONFLICT", Message: "Garden settings changed. Reload before saving.", Revision: &cur.Revision}
		}
		_, err = q.SaveKnowledgeSettings(ctx, dbgen.SaveKnowledgeSettingsParams{WorkspaceID: a.WorkspaceID, Country: in.Country, Region: in.Region, Place: in.Place, HardinessZone: optional(in.HardinessZone), ZoneSystem: optional(in.ZoneSystem), Enabled: in.Enabled})
		if err != nil {
			return cur, err
		}
		if err = q.SetKnowledgeGeography(ctx, dbgen.SetKnowledgeGeographyParams{ID: a.WorkspaceID, Latitude: lat, Longitude: lng, LocationLabel: pgtype.Text{String: in.Place, Valid: true}, Timezone: in.Timezone}); err != nil {
			return cur, err
		}
		if _, err = q.RefreshKnowledge(ctx, dbgen.RefreshKnowledgeParams{WorkspaceID: a.WorkspaceID}); err != nil {
			return cur, err
		}
		prior, _ := json.Marshal(cur)
		_, err = q.InsertAuditRevision(ctx, dbgen.InsertAuditRevisionParams{ID: platform.UUIDv4{}.New(), WorkspaceID: a.WorkspaceID, ActorID: a.OwnerID, Action: "UPDATE", AggregateType: "garden_knowledge_settings", AggregateID: a.WorkspaceID, Revision: cur.Revision + 1, RequestID: uid(a.SessionID), PriorValues: prior})
		if err != nil {
			return cur, err
		}
		return q.KnowledgeSettings(ctx, a.WorkspaceID)
	})
}
func (s *Service) Refresh(ctx context.Context, a Actor, plantID *string, key *string) (int, error) {
	return transaction(s, ctx, a, "refreshGardenKnowledge", key, plantID, func(q *dbgen.Queries) (int, error) {
		params := dbgen.RefreshKnowledgeParams{WorkspaceID: a.WorkspaceID}
		if plantID != nil {
			if err := params.PlantID.Scan(*plantID); err != nil {
				return 0, invalid("plantId", "Invalid plant ID.")
			}
			if _, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: params.PlantID, WorkspaceID: a.WorkspaceID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return 0, &Problem{Code: "NOT_FOUND", Message: "Plant not found."}
				}
				return 0, err
			}
		}
		count, err := q.RefreshKnowledge(ctx, params)
		return int(count), err
	})
}

type Plant struct {
	PlantID, Name, Category string
	Cultivar, PhotoAssetID  *string
	State, Message          string
	Facts                   []Fact
	UpdatedAt               time.Time
}
type Garden struct {
	Settings dbgen.KnowledgeSettingsRow
	Plants   []Plant
	Records  []dbgen.KnowledgeYearRecordsRow
}

func (s *Service) Garden(ctx context.Context, ws pgtype.UUID, year int) (Garden, error) {
	var out Garden
	if year < 1900 || year > 2200 {
		return out, invalid("year", "Choose a year between 1900 and 2200.")
	}
	settings, err := s.Settings(ctx, ws)
	if err != nil {
		return out, err
	}
	out.Settings = settings
	out.Plants = []Plant{}
	rows, err := s.Q.KnowledgeGarden(ctx, ws)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		p := Plant{PlantID: uid(r.ID), Name: r.Name, Category: string(r.Category), State: r.State, Message: r.Message, Facts: []Fact{}, UpdatedAt: r.UpdatedAt.Time}
		if r.Cultivar.Valid {
			p.Cultivar = &r.Cultivar.String
		}
		if r.PhotoAssetID.Valid {
			id := uid(r.PhotoAssetID)
			p.PhotoAssetID = &id
		}
		if r.State == "READY" && settings.Enabled {
			if err = json.Unmarshal(r.Facts, &p.Facts); err != nil {
				return out, err
			}
			if !currentFacts(p.Facts) {
				p.Facts = []Fact{}
				p.State = "PENDING"
				p.Message = "Updating source timing with the corrected extraction process."
			}
		}
		if !settings.Enabled {
			p.State = "DISABLED"
			p.Message = "Enable regional knowledge in garden settings."
		}
		out.Plants = append(out.Plants, p)
	}
	out.Records, err = s.Q.KnowledgeYearRecords(ctx, dbgen.KnowledgeYearRecordsParams{WorkspaceID: ws, Timezone: settings.Timezone, Year: int32(year)})
	return out, err
}
func (s *Service) Ask(ctx context.Context, ws pgtype.UUID, question string, year int) (AnswerDraft, []Fact, error) {
	question = strings.TrimSpace(question)
	if len(question) < 3 || len(question) > 2000 {
		return AnswerDraft{}, nil, invalid("question", "Ask a question between 3 and 2000 characters.")
	}
	model, modelErr := s.selectedModel(ctx, ws)
	if modelErr != nil {
		return AnswerDraft{}, nil, modelErr
	}
	if model == nil {
		return AnswerDraft{}, nil, errors.New("garden chat requires a configured AI model")
	}
	s.chatMu.Lock()
	now := time.Now()
	key := uid(ws)
	recent := []time.Time{}
	for _, t := range s.chatUsage[key] {
		if t.After(now.Add(-time.Hour)) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 12 {
		s.chatMu.Unlock()
		return AnswerDraft{}, nil, &Problem{Code: "RATE_LIMITED", Message: "Please wait before asking more garden questions."}
	}
	s.chatUsage[key] = append(recent, now)
	s.chatMu.Unlock()
	garden, err := s.Garden(ctx, ws, year)
	if err != nil {
		return AnswerDraft{}, nil, errors.New("Your garden could not be loaded.")
	}
	if !garden.Settings.Enabled {
		return AnswerDraft{}, nil, errors.New("enable regional knowledge in garden settings before using garden chat")
	}
	facts := map[string]Fact{}
	plants := map[string]bool{}
	type chatPlant struct {
		ID, Name, State string
		Cultivar        *string
		Facts           []Fact
	}
	contextPlants := []chatPlant{}
	for _, p := range garden.Plants {
		plants[p.PlantID] = true
		contextPlants = append(contextPlants, chatPlant{ID: p.PlantID, Name: p.Name, State: p.State, Cultivar: p.Cultivar, Facts: p.Facts})
		for _, f := range p.Facts {
			facts[f.ID] = f
		}
	}
	// Without source-matched guidance, don't ask a model to improvise care.
	// The fallback reports only facts we can calculate from stored records.
	if len(facts) == 0 {
		var water, photos, harvests, feeds int64
		for _, record := range garden.Records {
			water += record.Waterings
			photos += record.Photos
			harvests += record.Harvests
			feeds += record.Fertilizations
		}
		return AnswerDraft{Answer: fmt.Sprintf("There is no source-matched guidance available for your garden yet, so bloom, harvest and care timing remain unknown. For %d, the records contain %d waterings, %d photos, %d harvest logs and %d fertilizations across the garden. Unlogged work is unknown. Check Garden location and the guidance status of each plant, then refresh or retry where needed.", year, water, photos, harvests, feeds), FactIDs: []string{}, PlantIDs: []string{}}, []Fact{}, nil
	}
	location, err := time.LoadLocation(garden.Settings.Timezone)
	if err != nil {
		return AnswerDraft{}, nil, errors.New("Garden timezone is invalid.")
	}
	payload, _ := json.Marshal(map[string]any{"question": question, "asOf": time.Now().In(location).Format(time.DateOnly), "timezone": garden.Settings.Timezone, "year": year, "country": garden.Settings.Country, "region": garden.Settings.Region, "zone": garden.Settings.HardinessZone.String, "plants": contextPlants, "actualRecordedCounts": garden.Records, "note": "Seasonal knowledge is general guidance, not a weather-adjusted prediction. Unrecorded work is unknown, not proof it did not happen."})
	if len(payload) > 180000 {
		return AnswerDraft{}, nil, errors.New("garden is too large for one answer; filter or reduce the scope")
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	answer, err := model.Answer(ctx, string(payload))
	if err != nil {
		return answer, nil, err
	}
	if len(answer.Answer) < 1 || len(answer.Answer) > 6000 || len(answer.FactIDs) > 40 || len(answer.PlantIDs) > 200 {
		return AnswerDraft{}, nil, errors.New("garden answer did not pass validation")
	}
	cited := []Fact{}
	seen := map[string]bool{}
	for _, id := range answer.FactIDs {
		f, ok := facts[id]
		if !ok {
			return AnswerDraft{}, nil, errors.New("garden answer cited unsupported guidance")
		}
		if !seen[id] {
			cited = append(cited, f)
			seen[id] = true
		}
	}
	for _, id := range answer.PlantIDs {
		if !plants[id] {
			return AnswerDraft{}, nil, errors.New("garden answer referred to an unknown plant")
		}
	}
	if len(facts) > 0 && len(cited) == 0 {
		return AnswerDraft{}, nil, errors.New("garden answer did not supply supporting references; try a more specific question")
	}
	return answer, cited, nil
}
