// Commands (T17): typed care/observation activities and measurements.
//
// Rules enforced here (03 "Activities and measurements"):
//   - kind/unit compatibility is validated before the DB CHECK fires, so
//     problems quote exact expected/found values (WATER: ML|L; FERTILIZE:
//     G|KG|ML|L; other kinds carry no quantity/unit/product).
//   - quantity and unit travel together; quantity > 0.
//   - measurement unit must match quantityKind; value >= 0 except
//     TEMPERATURE (a cold reading is real data); uncertainty >= 0.
//   - backdating is first-class: occurredAt/observedAt are event times,
//     separate from createdAt.
//   - updates are revision-guarded; CONFLICT names currentRevision.
//   - deletes void rather than erase: activities get voided_at + a
//     tombstoned projection row; measurements have no tombstone shape in
//     the frozen contract, so their projection row is removed and the
//     trail lives in audit_revisions.
//   - every mutation projects into journal_entries in the same tx.
//
// CompletionPort is the seam T23 (care schedules) completes through.
package journal

import (
	"canopy/server/internal/agentcontext"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

// Problem codes (mirror the frozen ProblemCode enum).
const (
	CodeValidation          = "VALIDATION"
	CodeConflict            = "CONFLICT"
	CodeLinkedCompletion    = "LINKED_COMPLETION_USE_CORRECTION"
	CodeNotFound            = "NOT_FOUND"
	CodeIdempotencyConflict = "IDEMPOTENCY_CONFLICT"
	CodeResourceGone        = "RESOURCE_GONE"
)

// ReceiptRetention is how long idempotency receipts live: 30 days
// (03-contracts deletion matrix: op/key/input hash are retained until the
// original TTL even after the stored response becomes RESOURCE_GONE).
const ReceiptRetention = 30 * 24 * time.Hour

// Problem is a payload-level result (same shape as the other services).
type Problem struct {
	Code            string
	Field           string
	Message         string
	CurrentRevision *int32
}

func problem(code, field, msg string) Problem {
	return Problem{Code: code, Field: field, Message: msg}
}

func problemWithRevision(p Problem, rev int32) Problem {
	p.CurrentRevision = &rev
	return p
}

// Actor identifies who is performing a use case.
type Actor struct {
	WorkspaceID pgtype.UUID
	OwnerID     pgtype.UUID
	SessionID   pgtype.UUID
}

// CompletionPort is the seam T23 (care schedules) uses to record the
// activity a completed scheduled task produces; cmid keeps completions
// idempotent across retries.
type CompletionPort interface {
	RecordActivity(ctx context.Context, a Actor, in ActivityInput, cmid *string) (ActivityView, []Problem, error)
}

var _ CompletionPort = (*Service)(nil)

// ---- validation tables (mirrors of the frozen enums + DB CHECKs) ----------

var activityKinds = map[string]bool{
	"WATER": true, "FERTILIZE": true, "PRUNE": true, "REPOT": true,
	"HARVEST": true, "OBSERVE": true, "NOTE": true,
}

// kinds that carry quantity+unit, and the units each accepts
var kindUnits = map[string]map[string]bool{
	"WATER":     {"ML": true, "L": true},
	"FERTILIZE": {"G": true, "KG": true, "ML": true, "L": true},
}

var quantityKindUnits = map[string]map[string]bool{
	"VOLUME":      {"ML": true, "L": true},
	"MASS":        {"G": true, "KG": true},
	"LENGTH":      {"MM": true, "CM": true},
	"COUNT":       {"COUNT": true},
	"TEMPERATURE": {"C": true, "F": true},
}

var methods = map[string]bool{"MANUAL": true, "SENSOR": true, "IMAGE_ESTIMATE": true}

const (
	noteMax    = 20000 // DB CHECK
	productMax = 160   // DB CHECK
)

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// ---- decimal handling -------------------------------------------------------

var decimalRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// parseDecimal accepts canonical decimal text (the frozen Decimal scalar
// shape) and rejects NaN/Infinity.
func parseDecimal(s string) (pgtype.Numeric, bool) {
	if !decimalRe.MatchString(s) {
		return pgtype.Numeric{}, false
	}
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil || !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite {
		return pgtype.Numeric{}, false
	}
	return n, true
}

// numericString renders a stored numeric exactly (scale preserved), so a
// value read back from the DB matches what the owner typed.
func numericString(n pgtype.Numeric) string {
	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite {
		return ""
	}
	bi := new(big.Int)
	if n.Int != nil {
		bi.Set(n.Int)
	}
	neg := bi.Sign() < 0
	s := new(big.Int).Abs(bi).String()
	if n.Exp < 0 {
		d := int(-n.Exp)
		for len(s) <= d {
			s = "0" + s
		}
		s = s[:len(s)-d] + "." + s[len(s)-d:]
	} else if n.Exp > 0 {
		s += strings.Repeat("0", int(n.Exp))
	}
	if neg {
		s = "-" + s
	}
	return s
}

func decimalSign(s string) int {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0
	}
	return r.Sign()
}

// ---- summaries ---------------------------------------------------------------

var kindLabels = map[string]string{
	"WATER": "Watered", "FERTILIZE": "Fertilized", "PRUNE": "Pruned",
	"REPOT": "Repotted", "HARVEST": "Harvested", "OBSERVE": "Observed", "NOTE": "Note",
}

// ActivitySummary builds the timeline line for an activity entry: kind
// label plus the quantities the owner recorded; NOTE/OBSERVE lead with
// the owner's own note. Truncated rune-safe to the DB's 280-char CHECK.
func ActivitySummary(kind string, quantity *pgtype.Numeric, unit, product, note *string) string {
	var b strings.Builder
	if (kind == "NOTE" || kind == "OBSERVE") && note != nil && strings.TrimSpace(*note) != "" {
		return truncateRunes(strings.TrimSpace(*note), summaryMax)
	}
	b.WriteString(kindLabels[kind])
	if quantity != nil && quantity.Valid {
		b.WriteByte(' ')
		b.WriteString(numericString(*quantity))
		if unit != nil {
			b.WriteByte(' ')
			b.WriteString(*unit)
		}
	}
	if kind == "FERTILIZE" && product != nil && strings.TrimSpace(*product) != "" {
		b.WriteString(" (")
		b.WriteString(strings.TrimSpace(*product))
		b.WriteByte(')')
	}
	return truncateRunes(b.String(), summaryMax)
}

var quantityKindLabels = map[string]string{
	"VOLUME": "Volume", "MASS": "Mass", "LENGTH": "Length",
	"COUNT": "Count", "TEMPERATURE": "Temperature",
}

// MeasurementSummary: "Length 12 CM" — kind, exact value, unit.
func MeasurementSummary(quantityKind string, value pgtype.Numeric, unit string) string {
	return truncateRunes(quantityKindLabels[quantityKind]+" "+numericString(value)+" "+unit, summaryMax)
}

// ---- shared mutate protocol (same as collection/purchases/media) -----------

func mutate[T any](s *Service, ctx context.Context, a Actor, op string, cmid *string, input any,
	fn func(ctx context.Context, q *dbgen.Queries) (T, []Problem, bool),
) (T, []Problem, error) {
	var zero T
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	if err := agentcontext.Enter(ctx, tx); err != nil {
		return zero, nil, err
	}

	key := ""
	if cmid != nil {
		key = strings.TrimSpace(*cmid)
	}
	if key != "" {
		lockID := uuidString(a.WorkspaceID) + "|" + op + "|" + key
		if err := q.LockMutationKey(ctx, lockID); err != nil {
			return zero, nil, err
		}
		rec, err := q.GetMutationReceipt(ctx, dbgen.GetMutationReceiptParams{
			WorkspaceID: a.WorkspaceID, Operation: op, IdempotencyKey: key,
		})
		switch {
		case err == nil:
			if rec.RequestHash != requestHash(input) {
				return zero, []Problem{problem(CodeIdempotencyConflict, "clientMutationId",
					"clientMutationId was already used for a different request.")}, nil
			}
			if rec.Gone {
				return zero, []Problem{problem(CodeResourceGone, "clientMutationId",
					"the resource this stored response referred to has been purged.")}, nil
			}
			var replay T
			if err := json.Unmarshal(rec.Result, &replay); err != nil {
				return zero, nil, fmt.Errorf("receipt decode: %w", err)
			}
			return replay, nil, nil
		case !isNoRows(err):
			return zero, nil, err
		}
	}

	res, problems, keep := fn(ctx, q)
	if len(problems) > 0 {
		if keep {
			if err := tx.Commit(ctx); err != nil {
				return zero, nil, err
			}
		}
		return zero, problems, nil
	}

	if key != "" {
		resultJSON, err := json.Marshal(res)
		if err != nil {
			return zero, nil, err
		}
		if _, err := q.InsertMutationReceipt(ctx, dbgen.InsertMutationReceiptParams{
			ID:             s.uuid.New(),
			WorkspaceID:    a.WorkspaceID,
			Operation:      op,
			IdempotencyKey: key,
			RequestHash:    requestHash(input),
			Result:         resultJSON,
			ExpiresAt:      pgtype.Timestamptz{Time: s.clock.Now().Add(ReceiptRetention), Valid: true},
		}); err != nil {
			return zero, nil, err
		}
	}
	if err := agentcontext.Complete(ctx, tx, res); err != nil {
		return zero, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, nil, err
	}
	return res, nil, nil
}

func requestHash(input any) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(input)
	return hex.EncodeToString(h.Sum(nil))
}

// audit writes one audit_revisions row inside the mutation's tx, mirroring
// the collection/purchases/media services (bounded prior values, no secrets).
func (s *Service) audit(ctx context.Context, q *dbgen.Queries, a Actor, action, aggType string,
	aggID pgtype.UUID, revision int32, prior any) error {
	var priorJSON []byte
	if prior != nil {
		b, err := json.Marshal(prior)
		if err != nil {
			return err
		}
		priorJSON = b
	}
	_, err := q.InsertAuditRevision(ctx, dbgen.InsertAuditRevisionParams{
		ID:            s.uuid.New(),
		WorkspaceID:   a.WorkspaceID,
		ActorID:       a.OwnerID,
		Action:        action,
		AggregateType: aggType,
		AggregateID:   aggID,
		Revision:      revision,
		RequestID:     uuidString(a.SessionID),
		PriorValues:   priorJSON,
	})
	return err
}

func (s *Service) internalProblem(err error) Problem {
	return problem("INTERNAL", "", "internal error")
}

// ---- projection helpers (run inside the mutation's tx) ----------------------

func (s *Service) projectActivity(ctx context.Context, q *dbgen.Queries, a Actor, r dbgen.Activity) error {
	return ProjectActivity(ctx, q, s.uuid.New(), r)
}

func (s *Service) projectMeasurement(ctx context.Context, q *dbgen.Queries, a Actor, r dbgen.Measurement) error {
	_, err := q.UpsertJournalEntry(ctx, dbgen.UpsertJournalEntryParams{
		ID: s.uuid.New(), WorkspaceID: a.WorkspaceID, PlantID: r.PlantID,
		EntryType:  dbgen.JournalEntryTypeMEASUREMENT,
		SourceID:   r.ID,
		OccurredAt: r.ObservedAt,
		Summary:    MeasurementSummary(string(r.QuantityKind), r.Value, string(r.Unit)),
	})
	return err
}

func (s *Service) tombstoneActivity(ctx context.Context, q *dbgen.Queries, a Actor, activityID pgtype.UUID) error {
	return TombstoneActivity(ctx, q, a.WorkspaceID, activityID)
}

func (s *Service) removeMeasurementEntry(ctx context.Context, q *dbgen.Queries, a Actor, id pgtype.UUID) error {
	return q.DeleteJournalEntry(ctx, dbgen.DeleteJournalEntryParams{
		WorkspaceID: a.WorkspaceID, EntryType: dbgen.JournalEntryTypeMEASUREMENT, SourceID: id,
	})
}

// ---- views ------------------------------------------------------------------

type ActivityView struct {
	ID         string     `json:"id"`
	PlantID    string     `json:"plantId"`
	Kind       string     `json:"kind"`
	OccurredAt time.Time  `json:"occurredAt"`
	Timezone   string     `json:"timezone"`
	Note       *string    `json:"note,omitempty"`
	Quantity   *string    `json:"quantity,omitempty"`
	Unit       *string    `json:"unit,omitempty"`
	Product    *string    `json:"product,omitempty"`
	Revision   int32      `json:"revision"`
	VoidedAt   *time.Time `json:"voidedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type MeasurementView struct {
	ID           string    `json:"id"`
	PlantID      string    `json:"plantId"`
	QuantityKind string    `json:"quantityKind"`
	Value        string    `json:"value"`
	Unit         string    `json:"unit"`
	ObservedAt   time.Time `json:"observedAt"`
	Method       string    `json:"method"`
	Uncertainty  *string   `json:"uncertainty,omitempty"`
	ActivityID   *string   `json:"activityId,omitempty"`
	Revision     int32     `json:"revision"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func activityViewOf(r dbgen.Activity) ActivityView {
	v := ActivityView{
		ID: uuidString(r.ID), PlantID: uuidString(r.PlantID), Kind: string(r.Kind),
		OccurredAt: r.OccurredAt.Time, Timezone: r.Timezone,
		Revision: r.Revision, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
	}
	if r.Note.Valid {
		v.Note = &r.Note.String
	}
	if r.Quantity != nil {
		q := numericString(*r.Quantity)
		v.Quantity = &q
	}
	if r.Unit.Valid {
		u := string(r.Unit.MeasurementUnit)
		v.Unit = &u
	}
	if r.Product.Valid {
		v.Product = &r.Product.String
	}
	if r.VoidedAt.Valid {
		t := r.VoidedAt.Time
		v.VoidedAt = &t
	}
	return v
}

func measurementViewOf(r dbgen.Measurement) MeasurementView {
	v := MeasurementView{
		ID: uuidString(r.ID), PlantID: uuidString(r.PlantID),
		QuantityKind: string(r.QuantityKind), Value: numericString(r.Value),
		Unit: string(r.Unit), ObservedAt: r.ObservedAt.Time, Method: string(r.Method),
		Revision: r.Revision, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
	}
	if r.Uncertainty != nil {
		u := numericString(*r.Uncertainty)
		v.Uncertainty = &u
	}
	if r.ActivityID.Valid {
		a := uuidString(r.ActivityID)
		v.ActivityID = &a
	}
	return v
}

// ---- shared input helpers -----------------------------------------------------

func nullText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func nullUnit(u *string) dbgen.NullMeasurementUnit {
	if u == nil {
		return dbgen.NullMeasurementUnit{}
	}
	return dbgen.NullMeasurementUnit{MeasurementUnit: dbgen.MeasurementUnit(*u), Valid: true}
}

// resolveTimezone validates the IANA zone (rejecting fabricated ones) and
// falls back to the workspace default.
func resolveTimezone(ctx context.Context, q *dbgen.Queries, a Actor, in *string) (string, *Problem) {
	tz := ""
	if in != nil {
		tz = strings.TrimSpace(*in)
	}
	if tz == "" {
		ws, err := q.GetWorkspaceByID(ctx, a.WorkspaceID)
		if err != nil {
			return "", nil
		}
		tz = ws.Timezone
	}
	if _, err := time.LoadLocation(tz); err != nil {
		p := problem(CodeValidation, "input.timezone", "timezone must be an IANA zone")
		return "", &p
	}
	return tz, nil
}

// validateActivityQuantity enforces the kind/unit pairing before the DB
// CHECK can fire, quoting exact values.
func validateActivityQuantity(kind string, quantity, unit *string) (pgtype.Numeric, *string, *Problem) {
	allowed, carries := kindUnits[kind]
	if quantity == nil && unit == nil {
		if carries && kind != "WATER" {
			p := problem(CodeValidation, "input.quantity",
				fmt.Sprintf("%s requires quantity and unit", kind))
			return pgtype.Numeric{}, nil, &p
		}
		return pgtype.Numeric{}, nil, nil
	}
	if !carries {
		p := problem(CodeValidation, "input.unit",
			fmt.Sprintf("%s carries no quantity or unit", kind))
		return pgtype.Numeric{}, nil, &p
	}
	if quantity == nil {
		p := problem(CodeValidation, "input.quantity", "quantity and unit travel together")
		return pgtype.Numeric{}, nil, &p
	}
	if unit == nil {
		p := problem(CodeValidation, "input.unit", "quantity and unit travel together")
		return pgtype.Numeric{}, nil, &p
	}
	if !allowed[*unit] {
		p := problem(CodeValidation, "input.unit",
			fmt.Sprintf("%s accepts unit %s, got %s", kind,
				strings.Join(sortedKeys(allowed), " or "), *unit))
		return pgtype.Numeric{}, nil, &p
	}
	n, ok := parseDecimal(*quantity)
	if !ok {
		p := problem(CodeValidation, "input.quantity", "quantity must be a decimal number")
		return pgtype.Numeric{}, nil, &p
	}
	if decimalSign(*quantity) <= 0 {
		p := problem(CodeValidation, "input.quantity", "quantity must be greater than zero")
		return pgtype.Numeric{}, nil, &p
	}
	return n, unit, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- recordActivity -----------------------------------------------------------

type ActivityInput struct {
	PlantID    string
	Kind       string
	OccurredAt time.Time
	Timezone   *string
	Note       *string
	Quantity   *string
	Unit       *string
	Product    *string
}

func (s *Service) RecordActivity(ctx context.Context, a Actor, in ActivityInput, cmid *string) (ActivityView, []Problem, error) {
	var zero ActivityView
	plantID, ok := parseUUID(in.PlantID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.plantId", "plantId must be a UUID")}, nil
	}
	if !activityKinds[in.Kind] {
		return zero, []Problem{problem(CodeValidation, "input.kind", "unknown activity kind")}, nil
	}
	if in.Note != nil && len([]rune(*in.Note)) > noteMax {
		return zero, []Problem{problem(CodeValidation, "input.note", "note too long")}, nil
	}
	if in.Product != nil {
		if len([]rune(*in.Product)) > productMax {
			return zero, []Problem{problem(CodeValidation, "input.product", "product too long")}, nil
		}
		if in.Kind != "FERTILIZE" {
			return zero, []Problem{problem(CodeValidation, "input.product",
				fmt.Sprintf("%s carries no product", in.Kind))}, nil
		}
	}

	out, problems, err := mutate(s, ctx, a, "recordActivity", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (ActivityView, []Problem, bool) {
			if _, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: plantID, WorkspaceID: a.WorkspaceID}); err != nil {
				if isNoRows(err) {
					return zero, []Problem{problem(CodeNotFound, "input.plantId", "plant not found")}, false
				}
				return zero, []Problem{s.internalProblem(err)}, false
			}
			tz, p := resolveTimezone(ctx, q, a, in.Timezone)
			if p != nil {
				return zero, []Problem{*p}, false
			}
			qty, unit, p := validateActivityQuantity(in.Kind, in.Quantity, in.Unit)
			if p != nil {
				return zero, []Problem{*p}, false
			}
			var qtyN *pgtype.Numeric
			if in.Quantity != nil {
				qtyN = &qty
			}
			row, err := q.InsertActivity(ctx, dbgen.InsertActivityParams{
				ID: s.uuid.New(), WorkspaceID: a.WorkspaceID, PlantID: plantID,
				Kind:       dbgen.ActivityKind(in.Kind),
				OccurredAt: pgtype.Timestamptz{Time: in.OccurredAt, Valid: true},
				Timezone:   tz,
				Note:       nullText(in.Note),
				Quantity:   qtyN,
				Unit:       nullUnit(unit),
				Product:    nullText(in.Product),
			})
			if err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			if err := s.projectActivity(ctx, q, a, row); err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			if err := s.audit(ctx, q, a, "recordActivity", "activity", row.ID, row.Revision, nil); err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			return activityViewOf(row), nil, false
		})
	if err != nil {
		return zero, nil, err
	}
	return out, problems, nil
}

// ---- updateActivity -----------------------------------------------------------

type ActivityUpdate struct {
	ID               string
	ExpectedRevision int32
	OccurredAt       *time.Time
	Timezone         *string
	Note             *string
	Quantity         *string
	Unit             *string
	Product          *string
}

func (s *Service) UpdateActivity(ctx context.Context, a Actor, in ActivityUpdate, cmid *string) (ActivityView, []Problem, error) {
	var zero ActivityView
	id, ok := parseUUID(in.ID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.id", "id must be a UUID")}, nil
	}
	if in.Note != nil && len([]rune(*in.Note)) > noteMax {
		return zero, []Problem{problem(CodeValidation, "input.note", "note too long")}, nil
	}
	if in.Product != nil && len([]rune(*in.Product)) > productMax {
		return zero, []Problem{problem(CodeValidation, "input.product", "product too long")}, nil
	}

	out, problems, err := mutate(s, ctx, a, "updateActivity", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (ActivityView, []Problem, bool) {
			cur, err := q.GetActivity(ctx, dbgen.GetActivityParams{ID: id, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if isNoRows(err) {
					return zero, []Problem{problem(CodeNotFound, "input.id", "activity not found")}, false
				}
				return zero, []Problem{s.internalProblem(err)}, false
			}
			if cur.VoidedAt.Valid {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.id", "activity is voided"), cur.Revision)}, false
			}
			// Linked care completions are edited/undone only through
			// correctOccurrenceCompletion (03: completion integration port).
			if occ, err := q.GetLinkedCompletionOccurrence(ctx, id); err == nil {
				return zero, []Problem{problem(CodeLinkedCompletion, "input.id",
					fmt.Sprintf("activity is a linked care completion (occurrence %s); use correctOccurrenceCompletion",
						uuidString(occ.ID)))}, false
			} else if !isNoRows(err) {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			// product only for FERTILIZE; quantity/unit only for kinds that carry them
			if in.Product != nil && cur.Kind != dbgen.ActivityKindFERTILIZE {
				return zero, []Problem{problem(CodeValidation, "input.product",
					fmt.Sprintf("%s carries no product", cur.Kind))}, false
			}
			var qtyN *pgtype.Numeric
			var unit *string
			setQty := in.Quantity != nil || in.Unit != nil
			if setQty {
				qv, u, p := validateActivityQuantity(string(cur.Kind), in.Quantity, in.Unit)
				if p != nil {
					return zero, []Problem{*p}, false
				}
				qtyN, unit = &qv, u
			}
			if in.Timezone != nil {
				if _, p := resolveTimezone(ctx, q, a, in.Timezone); p != nil {
					return zero, []Problem{*p}, false
				}
			}
			upd := dbgen.UpdateActivityParams{
				ID: id, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
				Timezone: nullText(in.Timezone),
				Note:     nullText(in.Note),
				Product:  nullText(in.Product),
			}
			if in.OccurredAt != nil {
				upd.OccurredAt = pgtype.Timestamptz{Time: *in.OccurredAt, Valid: true}
			}
			if setQty {
				upd.Quantity = qtyN
				upd.Unit = nullUnit(unit)
			}
			row, err := q.UpdateActivity(ctx, upd)
			if err != nil {
				if isNoRows(err) {
					latest, lerr := q.GetActivity(ctx, dbgen.GetActivityParams{ID: id, WorkspaceID: a.WorkspaceID})
					if lerr == nil {
						return zero, []Problem{problemWithRevision(
							problem(CodeConflict, "input.expectedRevision", "revision conflict"),
							latest.Revision)}, false
					}
					return zero, []Problem{problem(CodeNotFound, "input.id", "activity not found")}, false
				}
				return zero, []Problem{s.internalProblem(err)}, false
			}
			if err := s.projectActivity(ctx, q, a, row); err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			if err := s.audit(ctx, q, a, "updateActivity", "activity", row.ID, row.Revision,
				activityAuditPrior(cur)); err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			return activityViewOf(row), nil, false
		})
	if err != nil {
		return zero, nil, err
	}
	return out, problems, nil
}

// activityAuditPrior captures the pre-image for the corrections audit.
func activityAuditPrior(r dbgen.Activity) map[string]any {
	p := map[string]any{"occurredAt": r.OccurredAt.Time, "timezone": r.Timezone}
	if r.Note.Valid {
		p["note"] = r.Note.String
	}
	if r.Quantity != nil {
		p["quantity"] = numericString(*r.Quantity)
	}
	if r.Unit.Valid {
		p["unit"] = string(r.Unit.MeasurementUnit)
	}
	if r.Product.Valid {
		p["product"] = r.Product.String
	}
	return p
}

// ---- voidActivity --------------------------------------------------------------

func (s *Service) VoidActivity(ctx context.Context, a Actor, id string, expectedRevision int32, cmid *string) (ActivityView, []Problem, error) {
	var zero ActivityView
	aid, ok := parseUUID(id)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.id", "id must be a UUID")}, nil
	}
	out, problems, err := mutate(s, ctx, a, "voidActivity", cmid,
		map[string]any{"id": id, "expectedRevision": expectedRevision},
		func(ctx context.Context, q *dbgen.Queries) (ActivityView, []Problem, bool) {
			cur, err := q.GetActivity(ctx, dbgen.GetActivityParams{ID: aid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if isNoRows(err) {
					return zero, []Problem{problem(CodeNotFound, "input.id", "activity not found")}, false
				}
				return zero, []Problem{s.internalProblem(err)}, false
			}
			if cur.VoidedAt.Valid {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.id", "activity is already voided"), cur.Revision)}, false
			}
			// Linked care completions are voided only through the care
			// correction flow (undo), never through the general command.
			if occ, err := q.GetLinkedCompletionOccurrence(ctx, aid); err == nil {
				return zero, []Problem{problem(CodeLinkedCompletion, "input.id",
					fmt.Sprintf("activity is a linked care completion (occurrence %s); use correctOccurrenceCompletion",
						uuidString(occ.ID)))}, false
			} else if !isNoRows(err) {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			row, err := q.VoidActivity(ctx, dbgen.VoidActivityParams{
				ID: aid, WorkspaceID: a.WorkspaceID, Revision: expectedRevision,
			})
			if err != nil {
				if isNoRows(err) {
					latest, lerr := q.GetActivity(ctx, dbgen.GetActivityParams{ID: aid, WorkspaceID: a.WorkspaceID})
					if lerr == nil {
						return zero, []Problem{problemWithRevision(
							problem(CodeConflict, "input.expectedRevision", "revision conflict"),
							latest.Revision)}, false
					}
					return zero, []Problem{problem(CodeNotFound, "input.id", "activity not found")}, false
				}
				return zero, []Problem{s.internalProblem(err)}, false
			}
			// voided events leave active timelines but stay attributable
			if err := s.tombstoneActivity(ctx, q, a, aid); err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			if err := s.audit(ctx, q, a, "voidActivity", "activity", row.ID, row.Revision,
				activityAuditPrior(cur)); err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			return activityViewOf(row), nil, false
		})
	if err != nil {
		return zero, nil, err
	}
	return out, problems, nil
}

// ---- recordMeasurement -----------------------------------------------------------

type MeasurementInput struct {
	PlantID      string
	QuantityKind string
	Value        string
	Unit         string
	ObservedAt   time.Time
	Method       string
	Uncertainty  *string
	ActivityID   *string
}

// validateMeasurementUnit mirrors the DB CHECK with quotable problems.
func validateMeasurementUnit(quantityKind, unit string) *Problem {
	allowed, ok := quantityKindUnits[quantityKind]
	if !ok {
		p := problem(CodeValidation, "input.quantityKind", "unknown quantity kind")
		return &p
	}
	if !allowed[unit] {
		p := problem(CodeValidation, "input.unit",
			fmt.Sprintf("%s accepts unit %s, got %s", quantityKind,
				strings.Join(sortedKeys(allowed), " or "), unit))
		return &p
	}
	return nil
}

func (s *Service) RecordMeasurement(ctx context.Context, a Actor, in MeasurementInput, cmid *string) (MeasurementView, []Problem, error) {
	var zero MeasurementView
	plantID, ok := parseUUID(in.PlantID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.plantId", "plantId must be a UUID")}, nil
	}
	if p := validateMeasurementUnit(in.QuantityKind, in.Unit); p != nil {
		return zero, []Problem{*p}, nil
	}
	if !methods[in.Method] {
		return zero, []Problem{problem(CodeValidation, "input.method",
			"method must be MANUAL, SENSOR or IMAGE_ESTIMATE")}, nil
	}
	val, ok := parseDecimal(in.Value)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.value", "value must be a decimal number")}, nil
	}
	// a cold reading is real data; everything else has no negative meaning
	if in.QuantityKind != "TEMPERATURE" && decimalSign(in.Value) < 0 {
		return zero, []Problem{problem(CodeValidation, "input.value", "value must be zero or greater")}, nil
	}
	var unc *pgtype.Numeric
	if in.Uncertainty != nil {
		u, ok := parseDecimal(*in.Uncertainty)
		if !ok {
			return zero, []Problem{problem(CodeValidation, "input.uncertainty",
				"uncertainty must be a decimal number")}, nil
		}
		if decimalSign(*in.Uncertainty) < 0 {
			return zero, []Problem{problem(CodeValidation, "input.uncertainty",
				"uncertainty must be zero or greater")}, nil
		}
		unc = &u
	}
	var actID pgtype.UUID
	if in.ActivityID != nil {
		id, ok := parseUUID(*in.ActivityID)
		if !ok {
			return zero, []Problem{problem(CodeValidation, "input.activityId",
				"activityId must be a UUID")}, nil
		}
		actID = id
	}

	out, problems, err := mutate(s, ctx, a, "recordMeasurement", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (MeasurementView, []Problem, bool) {
			if _, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: plantID, WorkspaceID: a.WorkspaceID}); err != nil {
				if isNoRows(err) {
					return zero, []Problem{problem(CodeNotFound, "input.plantId", "plant not found")}, false
				}
				return zero, []Problem{s.internalProblem(err)}, false
			}
			if actID.Valid {
				if _, err := q.GetActivity(ctx, dbgen.GetActivityParams{
					ID: actID, WorkspaceID: a.WorkspaceID}); err != nil {
					if isNoRows(err) {
						return zero, []Problem{problem(CodeNotFound, "input.activityId",
							"activity not found")}, false
					}
					return zero, []Problem{s.internalProblem(err)}, false
				}
			}
			row, err := q.InsertMeasurement(ctx, dbgen.InsertMeasurementParams{
				ID: s.uuid.New(), WorkspaceID: a.WorkspaceID, PlantID: plantID,
				QuantityKind: dbgen.QuantityKind(in.QuantityKind),
				Value:        val,
				Unit:         dbgen.MeasurementUnit(in.Unit),
				ObservedAt:   pgtype.Timestamptz{Time: in.ObservedAt, Valid: true},
				Method:       dbgen.MeasurementMethod(in.Method),
				Uncertainty:  unc,
				ActivityID:   actID,
			})
			if err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			if err := s.projectMeasurement(ctx, q, a, row); err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			if err := s.audit(ctx, q, a, "recordMeasurement", "measurement", row.ID, row.Revision, nil); err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			return measurementViewOf(row), nil, false
		})
	if err != nil {
		return zero, nil, err
	}
	return out, problems, nil
}

// ---- updateMeasurement -----------------------------------------------------------

type MeasurementUpdate struct {
	ID               string
	ExpectedRevision int32
	Value            *string
	Uncertainty      *string
	ObservedAt       *time.Time
}

func (s *Service) UpdateMeasurement(ctx context.Context, a Actor, in MeasurementUpdate, cmid *string) (MeasurementView, []Problem, error) {
	var zero MeasurementView
	id, ok := parseUUID(in.ID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.id", "id must be a UUID")}, nil
	}
	var val, unc *pgtype.Numeric
	if in.Value != nil {
		v, ok := parseDecimal(*in.Value)
		if !ok {
			return zero, []Problem{problem(CodeValidation, "input.value", "value must be a decimal number")}, nil
		}
		val = &v
	}
	if in.Uncertainty != nil {
		u, ok := parseDecimal(*in.Uncertainty)
		if !ok {
			return zero, []Problem{problem(CodeValidation, "input.uncertainty",
				"uncertainty must be a decimal number")}, nil
		}
		if decimalSign(*in.Uncertainty) < 0 {
			return zero, []Problem{problem(CodeValidation, "input.uncertainty",
				"uncertainty must be zero or greater")}, nil
		}
		unc = &u
	}

	out, problems, err := mutate(s, ctx, a, "updateMeasurement", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (MeasurementView, []Problem, bool) {
			cur, err := q.GetMeasurement(ctx, dbgen.GetMeasurementParams{ID: id, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if isNoRows(err) {
					return zero, []Problem{problem(CodeNotFound, "input.id", "measurement not found")}, false
				}
				return zero, []Problem{s.internalProblem(err)}, false
			}
			// negative values only make sense for temperature (kind is immutable)
			if in.Value != nil && cur.QuantityKind != dbgen.QuantityKindTEMPERATURE &&
				decimalSign(*in.Value) < 0 {
				return zero, []Problem{problem(CodeValidation, "input.value",
					"value must be zero or greater")}, false
			}
			upd := dbgen.UpdateMeasurementParams{
				ID: id, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
				Value: val, Uncertainty: unc,
			}
			if in.ObservedAt != nil {
				upd.ObservedAt = pgtype.Timestamptz{Time: *in.ObservedAt, Valid: true}
			}
			row, err := q.UpdateMeasurement(ctx, upd)
			if err != nil {
				if isNoRows(err) {
					latest, lerr := q.GetMeasurement(ctx, dbgen.GetMeasurementParams{ID: id, WorkspaceID: a.WorkspaceID})
					if lerr == nil {
						return zero, []Problem{problemWithRevision(
							problem(CodeConflict, "input.expectedRevision", "revision conflict"),
							latest.Revision)}, false
					}
					return zero, []Problem{problem(CodeNotFound, "input.id", "measurement not found")}, false
				}
				return zero, []Problem{s.internalProblem(err)}, false
			}
			// original quantityKind/unit/value inputs are retained in audit revisions
			if err := s.projectMeasurement(ctx, q, a, row); err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			if err := s.audit(ctx, q, a, "updateMeasurement", "measurement", row.ID, row.Revision,
				measurementAuditPrior(cur)); err != nil {
				return zero, []Problem{s.internalProblem(err)}, false
			}
			return measurementViewOf(row), nil, false
		})
	if err != nil {
		return zero, nil, err
	}
	return out, problems, nil
}

func measurementAuditPrior(r dbgen.Measurement) map[string]any {
	p := map[string]any{
		"quantityKind": string(r.QuantityKind),
		"value":        numericString(r.Value),
		"unit":         string(r.Unit),
		"observedAt":   r.ObservedAt.Time,
	}
	if r.Uncertainty != nil {
		p["uncertainty"] = numericString(*r.Uncertainty)
	}
	return p
}

// ---- deleteMeasurement -----------------------------------------------------------

func (s *Service) DeleteMeasurement(ctx context.Context, a Actor, id string, expectedRevision int32, cmid *string) (string, []Problem, error) {
	mid, ok := parseUUID(id)
	if !ok {
		return "", []Problem{problem(CodeValidation, "input.id", "id must be a UUID")}, nil
	}
	deleted, problems, err := mutate(s, ctx, a, "deleteMeasurement", cmid,
		map[string]any{"id": id, "expectedRevision": expectedRevision},
		func(ctx context.Context, q *dbgen.Queries) (string, []Problem, bool) {
			cur, err := q.GetMeasurement(ctx, dbgen.GetMeasurementParams{ID: mid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if isNoRows(err) {
					return "", []Problem{problem(CodeNotFound, "input.id", "measurement not found")}, false
				}
				return "", []Problem{s.internalProblem(err)}, false
			}
			if _, err := q.DeleteMeasurement(ctx, dbgen.DeleteMeasurementParams{
				ID: mid, WorkspaceID: a.WorkspaceID, ExpectedRevision: expectedRevision}); err != nil {
				if isNoRows(err) {
					latest, lerr := q.GetMeasurement(ctx, dbgen.GetMeasurementParams{ID: mid, WorkspaceID: a.WorkspaceID})
					if lerr == nil {
						return "", []Problem{problemWithRevision(
							problem(CodeConflict, "input.expectedRevision", "revision conflict"),
							latest.Revision)}, false
					}
					return "", []Problem{problem(CodeNotFound, "input.id", "measurement not found")}, false
				}
				return "", []Problem{s.internalProblem(err)}, false
			}
			// Measurement has no tombstone shape in the frozen contract: the
			// entry leaves every timeline view; audit_revisions keeps the trail.
			if err := s.removeMeasurementEntry(ctx, q, a, mid); err != nil {
				return "", []Problem{s.internalProblem(err)}, false
			}
			if err := s.audit(ctx, q, a, "deleteMeasurement", "measurement", mid, cur.Revision,
				measurementAuditPrior(cur)); err != nil {
				return "", []Problem{s.internalProblem(err)}, false
			}
			return uuidString(mid), nil, false
		})
	if err != nil {
		return "", nil, err
	}
	return deleted, problems, nil
}

// ---- batch hydration for the journal read model -----------------------------------

func (s *Service) ActivitiesByIDs(ctx context.Context, ws pgtype.UUID, ids []pgtype.UUID) (map[string]ActivityView, error) {
	out := map[string]ActivityView{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.ListActivitiesByIDs(ctx, dbgen.ListActivitiesByIDsParams{WorkspaceID: ws, Ids: ids})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out[uuidString(r.ID)] = activityViewOf(r)
	}
	return out, nil
}

func (s *Service) MeasurementsByIDs(ctx context.Context, ws pgtype.UUID, ids []pgtype.UUID) (map[string]MeasurementView, error) {
	out := map[string]MeasurementView{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.ListMeasurementsByIDs(ctx, dbgen.ListMeasurementsByIDsParams{WorkspaceID: ws, Ids: ids})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out[uuidString(r.ID)] = measurementViewOf(r)
	}
	return out, nil
}
