package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media/processor"
	platformmedia "canopy/server/internal/platform/media"
)

// PlantNet is the optional configured identification port (T30). It is
// off unless the operator pins an API key; nothing is ever fetched at
// default. The wire shape follows the public v2 identify contract
// (POST {base}/v2/identify/all, multipart images, jpeg/png only) and
// is exercised against a fake in tests — no live-provider behavior is
// assumed beyond that documented contract.
//
// Identification output is candidates, never facts: names arrive
// ranked with an honest confidence label, cultivar is never set
// (identify results carry no label evidence), and low-score answers
// abstain to an empty candidate list instead of guessing.
type PlantNet struct {
	BaseURL  string // pinned origin (default https://my-api.plantnet.org)
	APIKey   string // env-only; never logged, never returned
	HTTP     *http.Client
	Q        *dbgen.Queries
	Store    platformmedia.Store
	SB       processor.Sandbox // transcodes non-jpeg/png originals
	MaxBytes int64             // per-photo bound
}

// pnMinScore drops below-threshold guesses; pnHigh/pnMed are the
// confidence-label thresholds.
const (
	pnMinScore = 0.35
	pnHigh     = 0.80
	pnMed      = 0.55
)

func (a *PlantNet) enabled() bool {
	return a.APIKey != "" && a.BaseURL != "" && a.Q != nil && a.Store != nil
}

func (a *PlantNet) Capabilities(context.Context) map[Capability]CapabilityStatus {
	if !a.enabled() {
		return map[Capability]CapabilityStatus{
			CapPlantIdentifier: {ExternalData: true,
				Reason: "No identification provider is configured."},
		}
	}
	return map[Capability]CapabilityStatus{
		CapPlantIdentifier: {Available: true, Model: "plantnet-identify-v2", ExternalData: true},
	}
}

func (a *PlantNet) Analyze(ctx context.Context, in AnalyzeInput) (AnalyzeOutput, error) {
	if !a.enabled() {
		return AnalyzeOutput{}, &ProviderError{Class: ClassUnavailable,
			Err: errors.New("identification provider not configured")}
	}
	if len(in.PhotoAssetIDs) == 0 {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent,
			Err: errors.New("run carries no photo assets")}
	}
	wsID, err := parseUUID(in.WorkspaceID)
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
	}

	workDir, err := os.MkdirTemp("", "canopy-pn-")
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassRetryable, Err: err}
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	var imgs [][]byte
	for _, idStr := range in.PhotoAssetIDs {
		if len(imgs) >= 2 { // our pair bound; PlantNet allows up to 5
			break
		}
		id, err := parseUUID(idStr)
		if err != nil {
			return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
		}
		asset, perr := resolveReadyPhotoAsset(ctx, a.Q, id, wsID)
		if perr != nil {
			return AnalyzeOutput{}, perr
		}
		data, perr := a.photoBytes(ctx, workDir, len(imgs), asset)
		if perr != nil {
			return AnalyzeOutput{}, perr
		}
		imgs = append(imgs, data)
	}
	if len(imgs) == 0 {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: errors.New("no usable photos")}
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for i, b := range imgs {
		fw, err := mw.CreateFormFile("images", fmt.Sprintf("photo-%d.jpg", i+1))
		if err != nil {
			return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
		}
		if _, err := fw.Write(b); err != nil {
			return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
		}
	}
	if err := mw.Close(); err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
	}

	// The key rides the query string (v2 contract); the URL is never
	// logged and ProviderError never carries the response body.
	u := fmt.Sprintf("%s/v2/identify/all?api-key=%s&lang=en&nb-results=%d",
		strings.TrimSuffix(a.BaseURL, "/"), a.APIKey, MaxCandidates)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, &body)
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := a.httpClient().Do(req)
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassTimeout, Err: err}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()
	switch {
	case resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		return AnalyzeOutput{}, &ProviderError{Class: ClassRetryable, Status: resp.StatusCode,
			Err: fmt.Errorf("plantnet status %d", resp.StatusCode)}
	case resp.StatusCode != http.StatusOK:
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Status: resp.StatusCode,
			Err: fmt.Errorf("plantnet status %d", resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassRetryable, Status: resp.StatusCode, Err: err}
	}
	if len(raw) > 1<<20 {
		return AnalyzeOutput{}, &ProviderError{Class: ClassInvalidOutput,
			Err: errors.New("plantnet response oversized")}
	}
	var pr pnResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassInvalidOutput,
			Err: errors.New("plantnet response is not valid JSON")}
	}

	res := IdentResult{Candidates: []IdentCandidate{}}
	for _, r := range pr.Results {
		if len(res.Candidates) >= MaxCandidates || r.Score < pnMinScore {
			continue // below-threshold guesses are dropped: abstention is honest
		}
		name := strings.TrimSpace(r.Species.SciNoAuthor)
		if name == "" {
			continue
		}
		note := fmt.Sprintf("Pl@ntNet identify score %.2f against the supplied photo.", r.Score)
		c := IdentCandidate{
			Rank:           len(res.Candidates) + 1,
			ScientificName: name,
			Confidence:     pnConfidence(r.Score),
			EvidenceNote:   note,
		}
		if len(r.Species.CommonNames) > 0 {
			c.CommonName = strings.TrimSpace(r.Species.CommonNames[0])
		}
		res.Candidates = append(res.Candidates, c)
	}
	// Cultivar is never set from identify output: the service returns
	// no label evidence, and the SDL forbids inventing it.
	canon, verr := ValidateResult(dbgen.AnalysisKindIDENTIFICATION, &Snapshot{}, mustJSON(res), 64<<10)
	if verr != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassInvalidOutput,
			Err: fmt.Errorf("plantnet mapping invalid: %v", verr)}
	}
	model := "plantnet-identify-v2"
	if pr.Version != "" && len(pr.Version) <= 100 {
		model = "plantnet " + pr.Version
	}
	// identify is quota-metered, not USD-metered: the marginal external
	// cost of one call is zero, so settle honestly at 0 instead of
	// leaving the reservation encumbered on an unknown.
	zero := int64(0)
	return AnalyzeOutput{Raw: canon, Provider: "plantnet", Model: model,
		UsageCostMicroUSD: &zero}, nil
}

// photoBytes returns jpeg/png bytes for one photo asset; the shared
// adapterPhotoBytes helper (photos.go) owns the variant/transcode logic.
func (a *PlantNet) photoBytes(ctx context.Context, workDir string, idx int, asset dbgen.GetReadyAssetForAdapterRow) ([]byte, *ProviderError) {
	data, _, perr := adapterPhotoBytes(ctx, a.Q, a.Store, a.SB, workDir, idx, asset, a.MaxBytes)
	return data, perr
}

func (a *PlantNet) httpClient() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func pnConfidence(score float64) string {
	switch {
	case score >= pnHigh:
		return "HIGH"
	case score >= pnMed:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

type pnResponse struct {
	Results []struct {
		Score   float64 `json:"score"`
		Species struct {
			SciNoAuthor string   `json:"scientificNameWithoutAuthor"`
			CommonNames []string `json:"commonNames"`
		} `json:"species"`
	} `json:"results"`
	Version string `json:"version"`
}
