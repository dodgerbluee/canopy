package insights

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	dbgen "canopy/server/db/generated"
)

// Validator caps (03: "length/schema/enum/evidence-reference validation
// before persistence or rendering"). Provider output is untrusted data:
// anything outside these bounds fails the run (or its single bounded
// repair), never the database or the UI.
const (
	MaxObservations   = 20
	MaxHypotheses     = 10
	MaxInfoStrings    = 10
	MaxCitations      = 10
	MaxCandidates     = 5
	MaxReceiptLines   = 50
	MaxEvidencePerRef = 32
	MaxTextLen        = 1000 // observation/hypothesis text
	MaxShortLen       = 300  // missing info, follow-ups, titles, descriptions
	MaxNameLen        = 200  // publisher/region/license/common name/cultivar/seller
	MaxNoteLen        = 500  // evidence note
)

// causalClaimRe catches asserted causation in SYNTHESIS hypotheses
// (T33: correlations stay qualified; a diagnosis asserted from
// correlation is invalid output, not advice). Connectives only — the
// qualified alternatives ("may", "appears", "correlates with") pass.
var causalClaimRe = regexp.MustCompile(`(?i)\b(caused|because of|due to|led to|resulted from|responsible for)\b`)

// ValidationError marks provider output that failed schema/length/enum/
// evidence validation. The worker treats it as INVALID_OUTPUT and takes
// the single bounded repair path.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "insights: invalid provider output: " + e.Msg }

// ---------- result shapes (mirror the frozen SDL result types) ----------

type ObsResult struct {
	VisibleObservations []Observation `json:"visibleObservations"`
	Hypotheses          []Hypothesis  `json:"hypotheses"`
	MissingInformation  []string      `json:"missingInformation"`
	FollowUpChecks      []string      `json:"followUpChecks"`
	Citations           []Citation    `json:"citations"`
}

type Observation struct {
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidenceIds"`
}

type Hypothesis struct {
	Text        string   `json:"text"`
	Confidence  string   `json:"confidence"`
	EvidenceIDs []string `json:"evidenceIds"`
}

type Citation struct {
	SourceID        string `json:"sourceId"`
	SourceVersionID string `json:"sourceVersionId"`
	Title           string `json:"title"`
	Publisher       string `json:"publisher"`
	RetrievedOn     string `json:"retrievedOn"`
	Region          string `json:"region"`
	License         string `json:"license"`
}

type IdentResult struct {
	Candidates        []IdentCandidate `json:"candidates"`
	Cultivar          string           `json:"cultivar"`
	CultivarEvidenced bool             `json:"cultivarEvidenced"`
}

type IdentCandidate struct {
	Rank           int    `json:"rank"`
	ScientificName string `json:"scientificName"`
	CommonName     string `json:"commonName"`
	Confidence     string `json:"confidence"`
	EvidenceNote   string `json:"evidenceNote"`
}

type ReceiptResult struct {
	Currency            string        `json:"currency"`
	Lines               []ReceiptLine `json:"lines"`
	TotalMinor          string        `json:"totalMinor"`
	ShippingMinor       string        `json:"shippingMinor"`
	TaxMinor            string        `json:"taxMinor"`
	PurchasedOn         string        `json:"purchasedOn"`
	Seller              string        `json:"seller"`
	NeedsReconciliation bool          `json:"needsReconciliation"`
}

type ReceiptLine struct {
	Description string `json:"description"`
	AmountMinor string `json:"amountMinor"`
}

// ValidateResult checks raw provider output against the pinned result
// schema for the run kind and the snapshot's closed evidence sets, and
// returns the canonical (re-marshalled) JSON to persist. Optional
// strings use "" for absent; required non-null lists must be present.
func ValidateResult(kind dbgen.AnalysisKind, snap *Snapshot, raw []byte, maxBytes int64) (json.RawMessage, error) {
	if int64(len(raw)) > maxBytes {
		return nil, &ValidationError{Msg: fmt.Sprintf("result exceeds %d bytes", maxBytes)}
	}
	switch kind {
	case dbgen.AnalysisKindTEXT, dbgen.AnalysisKindVISION, dbgen.AnalysisKindSYNTHESIS:
		var v ObsResult
		if err := strictUnmarshal(raw, &v); err != nil {
			return nil, err
		}
		if err := validateObs(&v, snap, kind); err != nil {
			return nil, asInvalid(err)
		}
		return canonical(v)
	case dbgen.AnalysisKindIDENTIFICATION:
		var v IdentResult
		if err := strictUnmarshal(raw, &v); err != nil {
			return nil, err
		}
		if err := validateIdent(&v); err != nil {
			return nil, asInvalid(err)
		}
		return canonical(v)
	case dbgen.AnalysisKindRECEIPTEXTRACTION:
		var v ReceiptResult
		if err := strictUnmarshal(raw, &v); err != nil {
			return nil, err
		}
		if err := validateReceipt(&v); err != nil {
			return nil, asInvalid(err)
		}
		return canonical(v)
	default:
		return nil, &ValidationError{Msg: "unknown analysis kind"}
	}
}

// asInvalid normalizes any field-level failure into a ValidationError so
// the worker's classification (invalid output -> one bounded repair) can
// never mistake untrusted output for a transport fault.
func asInvalid(err error) error {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve
	}
	return &ValidationError{Msg: err.Error()}
}

func strictUnmarshal(raw []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return &ValidationError{Msg: err.Error()}
	}
	if dec.More() {
		return &ValidationError{Msg: "trailing data after JSON value"}
	}
	return nil
}

func canonical(v any) (json.RawMessage, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("insights: canonicalize: %w", err)
	}
	return out, nil
}

func validateObs(v *ObsResult, snap *Snapshot, kind dbgen.AnalysisKind) error {
	evidence := snap.EvidenceIDSet()
	guidance := snap.GuidanceVersionIDSet()
	if v.VisibleObservations == nil {
		return &ValidationError{Msg: "visibleObservations missing"}
	}
	if len(v.VisibleObservations) > MaxObservations {
		return &ValidationError{Msg: "too many visibleObservations"}
	}
	for i, o := range v.VisibleObservations {
		if err := plainText(o.Text, MaxTextLen, true); err != nil {
			return fmt.Errorf("visibleObservations[%d].text: %w", i, err)
		}
		if err := evidenceRefs(o.EvidenceIDs, evidence); err != nil {
			return fmt.Errorf("visibleObservations[%d].evidenceIds: %w", i, err)
		}
	}
	if v.Hypotheses == nil {
		return &ValidationError{Msg: "hypotheses missing"}
	}
	if len(v.Hypotheses) > MaxHypotheses {
		return &ValidationError{Msg: "too many hypotheses"}
	}
	for i, h := range v.Hypotheses {
		if err := plainText(h.Text, MaxTextLen, true); err != nil {
			return fmt.Errorf("hypotheses[%d].text: %w", i, err)
		}
		if !validConfidence(h.Confidence) {
			return &ValidationError{Msg: fmt.Sprintf("hypotheses[%d].confidence invalid", i)}
		}
		if err := evidenceRefs(h.EvidenceIDs, evidence); err != nil {
			return fmt.Errorf("hypotheses[%d].evidenceIds: %w", i, err)
		}
	}
	// T33: synthesis explains correlations across records; a stored
	// hypothesis must stay qualified — an asserted causal diagnosis from
	// correlation is rejected as invalid output, never quietly persisted.
	if kind == dbgen.AnalysisKindSYNTHESIS {
		for i, h := range v.Hypotheses {
			if causalClaimRe.MatchString(h.Text) {
				return &ValidationError{Msg: fmt.Sprintf(
					"hypotheses[%d] asserts causation; synthesis correlations must stay qualified", i)}
			}
		}
	}
	if v.MissingInformation == nil || v.FollowUpChecks == nil {
		return &ValidationError{Msg: "missingInformation/followUpChecks missing"}
	}
	if err := shortStrings(v.MissingInformation, "missingInformation"); err != nil {
		return err
	}
	if err := shortStrings(v.FollowUpChecks, "followUpChecks"); err != nil {
		return err
	}
	if v.Citations == nil {
		return &ValidationError{Msg: "citations missing"}
	}
	if len(v.Citations) > MaxCitations {
		return &ValidationError{Msg: "too many citations"}
	}
	for i, c := range v.Citations {
		// Citations bind to the exact supplied source versions only: a
		// provider-invented "source" never becomes a stored citation.
		if !guidance[c.SourceVersionID] {
			return &ValidationError{Msg: fmt.Sprintf("citations[%d].sourceVersionId not a supplied source", i)}
		}
		if c.SourceID == "" || len(c.SourceID) > 64 {
			return &ValidationError{Msg: fmt.Sprintf("citations[%d].sourceId invalid", i)}
		}
		if err := plainText(c.Title, MaxShortLen, true); err != nil {
			return fmt.Errorf("citations[%d].title: %w", i, err)
		}
		for _, s := range []struct {
			name string
			val  string
		}{{"publisher", c.Publisher}, {"region", c.Region}, {"license", c.License}} {
			if err := plainText(s.val, MaxNameLen, false); err != nil {
				return fmt.Errorf("citations[%d].%s: %w", i, s.name, err)
			}
		}
		if c.RetrievedOn != "" {
			if _, err := time.Parse("2006-01-02", c.RetrievedOn); err != nil {
				return &ValidationError{Msg: fmt.Sprintf("citations[%d].retrievedOn not an ISO date", i)}
			}
		}
	}
	return nil
}

func validateIdent(v *IdentResult) error {
	if v.Candidates == nil {
		return &ValidationError{Msg: "candidates missing"}
	}
	if len(v.Candidates) > MaxCandidates {
		return &ValidationError{Msg: "too many candidates"}
	}
	seen := map[int]bool{}
	for i, c := range v.Candidates {
		if c.Rank < 1 || c.Rank > len(v.Candidates) || seen[c.Rank] {
			return &ValidationError{Msg: fmt.Sprintf("candidates[%d].rank must be unique 1..n", i)}
		}
		seen[c.Rank] = true
		if err := plainText(c.ScientificName, MaxNameLen, true); err != nil {
			return fmt.Errorf("candidates[%d].scientificName: %w", i, err)
		}
		if err := plainText(c.CommonName, MaxNameLen, false); err != nil {
			return fmt.Errorf("candidates[%d].commonName: %w", i, err)
		}
		if !validConfidence(c.Confidence) {
			return &ValidationError{Msg: fmt.Sprintf("candidates[%d].confidence invalid", i)}
		}
		if err := plainText(c.EvidenceNote, MaxNoteLen, false); err != nil {
			return fmt.Errorf("candidates[%d].evidenceNote: %w", i, err)
		}
	}
	if err := plainText(v.Cultivar, MaxNameLen, false); err != nil {
		return fmt.Errorf("cultivar: %w", err)
	}
	// 03: cultivar is null unless a supplied label evidences it — never
	// invented from species priors.
	if v.Cultivar != "" && !v.CultivarEvidenced {
		return &ValidationError{Msg: "cultivar set without label evidence"}
	}
	return nil
}

func validateReceipt(v *ReceiptResult) error {
	if v.Currency != "" && !isISO4217(v.Currency) {
		return &ValidationError{Msg: "currency must be a 3-letter ISO code"}
	}
	if v.Lines == nil {
		return &ValidationError{Msg: "lines missing"}
	}
	if len(v.Lines) > MaxReceiptLines {
		return &ValidationError{Msg: "too many lines"}
	}
	sum, complete := int64(0), true
	for i, l := range v.Lines {
		if err := plainText(l.Description, MaxShortLen, true); err != nil {
			return fmt.Errorf("lines[%d].description: %w", i, err)
		}
		if l.AmountMinor != "" {
			n, err := parseMinor(l.AmountMinor)
			if err != nil {
				return fmt.Errorf("lines[%d].amountMinor: %w", i, err)
			}
			sum += n
		} else {
			complete = false
		}
	}
	for _, f := range []struct {
		name string
		val  string
	}{{"totalMinor", v.TotalMinor}, {"shippingMinor", v.ShippingMinor}, {"taxMinor", v.TaxMinor}} {
		if f.val != "" {
			if _, err := parseMinor(f.val); err != nil {
				return fmt.Errorf("%s: %w", f.name, err)
			}
		}
	}
	if err := plainText(v.Seller, MaxNameLen, false); err != nil {
		return fmt.Errorf("seller: %w", err)
	}
	if v.PurchasedOn != "" {
		if _, err := time.Parse("2006-01-02", v.PurchasedOn); err != nil {
			return &ValidationError{Msg: "purchasedOn not an ISO date"}
		}
	}
	// Reconciliation honesty: claims of exactness are only accepted when
	// the arithmetic actually closes over legible lines.
	if !v.NeedsReconciliation {
		if v.TotalMinor == "" || !complete {
			return &ValidationError{Msg: "needsReconciliation false but totals/lines incomplete"}
		}
		total, _ := parseMinor(v.TotalMinor)
		if total != sum {
			return &ValidationError{Msg: "needsReconciliation false but total != sum(lines)"}
		}
	}
	return nil
}

func validConfidence(s string) bool {
	switch s {
	case "LOW", "MEDIUM", "HIGH":
		return true
	}
	return false
}

func evidenceRefs(ids []string, allowed map[string]bool) error {
	if ids == nil {
		return errors.New("missing")
	}
	if len(ids) > MaxEvidencePerRef {
		return errors.New("too many references")
	}
	for _, id := range ids {
		if !allowed[id] {
			return fmt.Errorf("reference %q not in supplied evidence", id)
		}
	}
	return nil
}

func shortStrings(ss []string, field string) error {
	if len(ss) > MaxInfoStrings {
		return &ValidationError{Msg: "too many " + field}
	}
	for i, s := range ss {
		if err := plainText(s, MaxShortLen, true); err != nil {
			return fmt.Errorf("%s[%d]: %w", field, i, err)
		}
	}
	return nil
}

// plainText enforces non-empty (when required), rune-bounded length, and
// rejects control characters — the injection guard: provider text is
// data for display, never instructions or markup payloads.
func plainText(s string, max int, required bool) error {
	if s == "" {
		if required {
			return errors.New("must not be empty")
		}
		return nil
	}
	if utf8.RuneCountInString(s) > max {
		return fmt.Errorf("longer than %d characters", max)
	}
	if strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r'
	}) {
		return errors.New("contains control characters")
	}
	return nil
}

func parseMinor(s string) (int64, error) {
	if len(s) > 16 {
		return 0, errors.New("amount too long")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, errors.New("must be an integer minor-unit string")
	}
	return n, nil
}

func isISO4217(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
