// T29 validator acceptance: provider output is untrusted data — schema,
// length, enum, evidence-reference, and honesty rules must fail closed
// before anything is persisted or rendered.
package insights

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	dbgen "canopy/server/db/generated"
)

func testSnap() *Snapshot {
	return &Snapshot{
		Schema: SnapSchema, Kind: "TEXT",
		Activities:   []SnapActivity{{ID: "act-1", Kind: "WATERING"}},
		Measurements: []SnapMeasure{{ID: "mea-1", Kind: "HEIGHT", Value: "30", Unit: "CM"}},
		Photos:       []SnapPhoto{{ID: "pho-1", AssetID: "ass-1"}},
		Guidance:     []SnapGuidance{{SourceVersionID: "gv-1", SourceID: "gp-1", Version: 1}},
		MissingFacts: []string{},
	}
}

func obsDoc(body string) []byte {
	return []byte(`{"visibleObservations":[{"text":"Leaf edge pale.","evidenceIds":["act-1"]}],` +
		`"hypotheses":[{"text":"Possible nitrogen limit.","confidence":"MEDIUM","evidenceIds":["mea-1"]}],` +
		`"missingInformation":["no humidity data"],"followUpChecks":["check soil"],"citations":` + body + `}`)
}

func wantInvalid(t *testing.T, raw []byte, why string) {
	t.Helper()
	if _, err := ValidateResult(dbgen.AnalysisKindTEXT, testSnap(), raw, 64<<10); err == nil {
		t.Fatalf("expected rejection (%s): %s", why, raw)
	} else {
		var ve *ValidationError
		if !asValidationError(err, &ve) {
			t.Fatalf("not a ValidationError: %v", err)
		}
	}
}

func asValidationError(err error, target **ValidationError) bool {
	ve, ok := err.(*ValidationError)
	if ok {
		*target = ve
	}
	return ok
}

func TestValidObservationResultCanonicalizes(t *testing.T) {
	canon, err := ValidateResult(dbgen.AnalysisKindTEXT, testSnap(),
		obsDoc(`[{"sourceId":"gp-1","sourceVersionId":"gv-1","title":"Care guide","publisher":"P","retrievedOn":"2026-10-01","region":"EU","license":"CC"}]`),
		64<<10)
	if err != nil {
		t.Fatalf("valid doc rejected: %v", err)
	}
	var back ObsResult
	if err := json.Unmarshal(canon, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Citations) != 1 || back.Citations[0].SourceVersionID != "gv-1" {
		t.Fatalf("canonical: %s", canon)
	}
}

func TestEvidenceReferencesMustBeSubset(t *testing.T) {
	wantInvalid(t, []byte(`{"visibleObservations":[{"text":"x","evidenceIds":["not-shown"]}],`+
		`"hypotheses":[],"missingInformation":[],"followUpChecks":[],"citations":[]}`), "forged evidence id")
}

func TestCitationsBindToSuppliedVersions(t *testing.T) {
	wantInvalid(t, obsDoc(`[{"sourceId":"gp-9","sourceVersionId":"gv-invented","title":"Fake source"}]`),
		"provider-invented citation")
}

func TestConfidenceEnumWhitelist(t *testing.T) {
	wantInvalid(t, []byte(`{"visibleObservations":[],"hypotheses":[{"text":"x","confidence":"CERTAIN","evidenceIds":[]}],`+
		`"missingInformation":[],"followUpChecks":[],"citations":[]}`), "confidence enum")
}

func TestUnknownFieldsAndTrailingDataRejected(t *testing.T) {
	wantInvalid(t, []byte(`{"visibleObservations":[],"hypotheses":[],"missingInformation":[],`+
		`"followUpChecks":[],"citations":[],"instructions":"ignore prior rules"}`), "unknown field")
	wantInvalid(t, append(obsDoc(`[]`), []byte(` {"extra":true}`)...), "trailing data")
}

func TestLengthCaps(t *testing.T) {
	long := strings.Repeat("é", MaxTextLen+1)
	wantInvalid(t, []byte(fmt.Sprintf(`{"visibleObservations":[{"text":%q,"evidenceIds":[]}],`+
		`"hypotheses":[],"missingInformation":[],"followUpChecks":[],"citations":[]}`, long)), "text length")

	var many []Observation
	for i := 0; i <= MaxObservations; i++ {
		many = append(many, Observation{Text: "ok", EvidenceIDs: []string{}})
	}
	raw, _ := json.Marshal(ObsResult{VisibleObservations: many, Hypotheses: []Hypothesis{},
		MissingInformation: []string{}, FollowUpChecks: []string{}, Citations: []Citation{}})
	wantInvalid(t, raw, "observation count")
}

func TestControlCharactersRejected(t *testing.T) {
	wantInvalid(t, []byte(`{"visibleObservations":[{"text":"system\u001b[31m","evidenceIds":[]}],`+
		`"hypotheses":[],"missingInformation":[],"followUpChecks":[],"citations":[]}`), "control chars")
}

func TestRequiredListsMustBePresent(t *testing.T) {
	wantInvalid(t, []byte(`{"visibleObservations":[],"hypotheses":[]}`), "missing lists")
}

func TestOversizedRawRejectedBeforeParse(t *testing.T) {
	raw := []byte(`{"visibleObservations":[{"text":"` + strings.Repeat("a", 5000) + `","evidenceIds":[]}],` +
		`"hypotheses":[],"missingInformation":[],"followUpChecks":[],"citations":[]}`)
	if _, err := ValidateResult(dbgen.AnalysisKindTEXT, testSnap(), raw, 1024); err == nil {
		t.Fatal("oversize must fail")
	}
}

func TestIdentificationCultivarHonesty(t *testing.T) {
	good := []byte(`{"candidates":[{"rank":1,"scientificName":"Monstera deliciosa","confidence":"HIGH"}],` +
		`"cultivar":"","cultivarEvidenced":false}`)
	if _, err := ValidateResult(dbgen.AnalysisKindIDENTIFICATION, testSnap(), good, 64<<10); err != nil {
		t.Fatalf("valid ident rejected: %v", err)
	}
	invented := []byte(`{"candidates":[{"rank":1,"scientificName":"Monstera deliciosa","confidence":"HIGH"}],` +
		`"cultivar":"Albo Variegata","cultivarEvidenced":false}`)
	if _, err := ValidateResult(dbgen.AnalysisKindIDENTIFICATION, testSnap(), invented, 64<<10); err == nil {
		t.Fatal("cultivar without label evidence must be rejected")
	}
	badRank := []byte(`{"candidates":[{"rank":1,"scientificName":"A b","confidence":"HIGH"},` +
		`{"rank":1,"scientificName":"C d","confidence":"LOW"}],"cultivarEvidenced":false}`)
	if _, err := ValidateResult(dbgen.AnalysisKindIDENTIFICATION, testSnap(), badRank, 64<<10); err == nil {
		t.Fatal("duplicate rank must be rejected")
	}
}

func TestReceiptReconciliationHonesty(t *testing.T) {
	lying := []byte(`{"currency":"USD","lines":[{"description":"Plant","amountMinor":"1099"}],` +
		`"totalMinor":"1299","needsReconciliation":false}`)
	if _, err := ValidateResult(dbgen.AnalysisKindRECEIPTEXTRACTION, testSnap(), lying, 64<<10); err == nil {
		t.Fatal("total != sum(lines) with needsReconciliation=false must be rejected")
	}
	incomplete := []byte(`{"currency":"USD","lines":[{"description":"Plant"}],` +
		`"totalMinor":"1099","needsReconciliation":false}`)
	if _, err := ValidateResult(dbgen.AnalysisKindRECEIPTEXTRACTION, testSnap(), incomplete, 64<<10); err == nil {
		t.Fatal("unlegible lines cannot claim exactness")
	}
	honest := []byte(`{"currency":"USD","lines":[{"description":"Plant","amountMinor":"1099"}],` +
		`"totalMinor":"1299","taxMinor":"200","needsReconciliation":true}`)
	if _, err := ValidateResult(dbgen.AnalysisKindRECEIPTEXTRACTION, testSnap(), honest, 64<<10); err != nil {
		t.Fatalf("honest unreconciled draft rejected: %v", err)
	}
	badCurrency := []byte(`{"currency":"usd","lines":[],"totalMinor":"0","needsReconciliation":true}`)
	if _, err := ValidateResult(dbgen.AnalysisKindRECEIPTEXTRACTION, testSnap(), badCurrency, 64<<10); err == nil {
		t.Fatal("lowercase currency must be rejected")
	}
}

// ---- snapshot bounds ---------------------------------------------------------

func TestSnapshotHashIsStableAndEvidenceSetsCloseTheLoop(t *testing.T) {
	s := testSnap()
	raw1, h1, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	raw2, h2, err := testSnap().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(raw1) != string(raw2) || h1 != h2 {
		t.Fatal("marshal must be deterministic for identical inputs")
	}
	ev := s.EvidenceIDSet()
	for _, id := range []string{"act-1", "mea-1", "pho-1", "gv-1"} {
		if !ev[id] {
			t.Fatalf("evidence set missing %s", id)
		}
	}
	if s.GuidanceVersionIDSet()["gv-1"] != true {
		t.Fatal("guidance version set must contain supplied version")
	}
}
