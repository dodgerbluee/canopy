package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Capability names the four runtime provider ports (03). The DB CHECK
// on analysis_runs.capability pins exactly these strings.
type Capability string

const (
	CapTextAnalyzer     Capability = "TEXT_ANALYZER"
	CapVisionAnalyzer   Capability = "VISION_ANALYZER"
	CapPlantIdentifier  Capability = "PLANT_IDENTIFIER"
	CapReceiptExtractor Capability = "RECEIPT_EXTRACTOR"
)

// CapabilityStatus is what the settings surface exposes: availability,
// model name, whether data leaves the deployment, and an owner-language
// reason. Endpoints, keys and credentials are never exposed.
type CapabilityStatus struct {
	Available    bool
	Model        string
	ExternalData bool
	Reason       string // why unavailable; "" when available
}

// AnalyzeInput is one provider attempt. RepairOf carries the previous
// raw output when this is the single bounded schema-repair request.
type AnalyzeInput struct {
	RunID         string
	WorkspaceID   string
	Capability    Capability
	Snapshot      json.RawMessage
	PromptVersion string
	RepairOf      string

	// File-target ids from the claimed run row (T30): adapters that need
	// bytes (PlantNet photos, receipt OCR) resolve these asset ids
	// through the DB + object store. photoAssetIDs are the run's
	// photo_id_a/b columns, which reference assets directly.
	ReceiptAssetID string
	PhotoAssetIDs  []string
}

// AnalyzeOutput is the raw (still untrusted) provider answer plus
// provenance. UsageCostMicroUSD is authoritative usage when the provider
// reports it; nil means unknown (never zero).
type AnalyzeOutput struct {
	Raw               json.RawMessage
	Provider          string
	Model             string
	UsageCostMicroUSD *int64
}

// ErrorClass buckets provider failures for the retry policy
// (03: retry only timeout/429/5xx up to 3 attempts).
type ErrorClass int

const (
	ClassTimeout       ErrorClass = iota // client/per-request timeout
	ClassRetryable                       // 429 / 5xx
	ClassPermanent                       // 4xx, protocol errors
	ClassUnavailable                     // no provider configured
	ClassInvalidOutput                   // envelope-level malformation/oversize: repairable once
)

// ProviderError classifies an adapter failure; the raw provider body is
// never carried (it is untrusted and may contain secrets).
type ProviderError struct {
	Class  ErrorClass
	Status int
	Err    error
}

func (e *ProviderError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("provider %d: %v", e.Status, e.Err)
	}
	return fmt.Sprintf("provider status %d", e.Status)
}

func (e *ProviderError) Unwrap() error { return e.Err }

// Port is the outbound provider seam.
type Port interface {
	Capabilities(ctx context.Context) map[Capability]CapabilityStatus
	Analyze(ctx context.Context, in AnalyzeInput) (AnalyzeOutput, error)
}

// NoProvider is no-provider mode: every capability is honestly
// unavailable and nothing is ever fetched.
type NoProvider struct{}

func (NoProvider) Capabilities(context.Context) map[Capability]CapabilityStatus {
	reason := "No AI provider is configured; ask the operator to enable one in settings."
	return map[Capability]CapabilityStatus{
		CapTextAnalyzer:     {Reason: reason},
		CapVisionAnalyzer:   {Reason: "No vision provider is configured; ask the operator to enable one in settings."},
		CapPlantIdentifier:  {Reason: "No identification provider is configured."},
		CapReceiptExtractor: {Reason: "No receipt-extraction provider is configured."},
	}
}

func (NoProvider) Analyze(context.Context, AnalyzeInput) (AnalyzeOutput, error) {
	return AnalyzeOutput{}, &ProviderError{Class: ClassUnavailable, Err: errors.New("no provider configured")}
}

// HTTPAdapter is the operator-configured text analyzer (T29: text-only;
// vision/receipt ports land with T30/T31). The endpoint and key are
// operator-owned env config; the only path ever fetched is
// {base}/v1/analyze — provider output can never steer a fetch.
type HTTPAdapter struct {
	BaseURL  string // https (or http in dev) origin, no path
	APIKey   string // env-only; never logged, never returned
	Model    string // configured model name (exposed in settings)
	HTTP     *http.Client
	MaxResp  int64 // hard response-size cap (decompression/oversize guard)
	External bool  // true when the endpoint is outside this deployment
}

// NewHTTPAdapter builds the adapter with a per-request timeout; a hung
// provider must never stall a worker run past its context.
func NewHTTPAdapter(baseURL, apiKey, model string, timeout time.Duration, maxResp int64) (*HTTPAdapter, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return nil, fmt.Errorf("insights: bad AI base URL %q", baseURL)
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("insights: AI base URL must be an origin, got path %q", u.Path)
	}
	return &HTTPAdapter{
		BaseURL:  strings.TrimSuffix(baseURL, "/"),
		APIKey:   apiKey,
		Model:    model,
		HTTP:     &http.Client{Timeout: timeout},
		MaxResp:  maxResp,
		External: true,
	}, nil
}

func (a *HTTPAdapter) Capabilities(context.Context) map[Capability]CapabilityStatus {
	return map[Capability]CapabilityStatus{
		CapTextAnalyzer: {Available: true, Model: a.Model, ExternalData: a.External},
		CapVisionAnalyzer: {ExternalData: a.External,
			Reason: "No vision provider is configured; ask the operator to enable one in settings."},
		CapPlantIdentifier: {ExternalData: a.External,
			Reason: "No identification provider is configured."},
		CapReceiptExtractor: {ExternalData: a.External,
			Reason: "No receipt-extraction provider is configured."},
	}
}

// analyzeRequest is the wire shape: the bounded snapshot only. The
// adapter composes no free-form prompt; the provider service owns its
// prompt template and must return the result JSON for the pinned schema
// version.
type analyzeRequest struct {
	Capability    string          `json:"capability"`
	PromptVersion string          `json:"promptVersion"`
	SchemaVersion string          `json:"resultSchemaVersion"`
	Snapshot      json.RawMessage `json:"snapshot"`
	RepairOf      string          `json:"repairOf,omitempty"`
}

// analyzeEnvelope is the transport wrapper; "result" carries the
// untrusted analysis output, validated by the caller before anything
// touches the database.
type analyzeEnvelope struct {
	Result            json.RawMessage `json:"result"`
	Provider          string          `json:"provider"`
	Model             string          `json:"model"`
	UsageCostMicroUSD *int64          `json:"usageCostMicroUsd"`
}

func (a *HTTPAdapter) Analyze(ctx context.Context, in AnalyzeInput) (AnalyzeOutput, error) {
	body, err := json.Marshal(analyzeRequest{
		Capability: string(in.Capability), PromptVersion: in.PromptVersion,
		Snapshot: in.Snapshot, RepairOf: in.RepairOf,
	})
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+"/v1/analyze", bytes.NewReader(body))
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	if a.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.APIKey)
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		// client timeout / transport failure: retriable within bounds
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
			Err: fmt.Errorf("provider status %d", resp.StatusCode)}
	case resp.StatusCode != http.StatusOK:
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Status: resp.StatusCode,
			Err: fmt.Errorf("provider status %d", resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, a.MaxResp+1))
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassRetryable, Status: resp.StatusCode, Err: err}
	}
	if int64(len(raw)) > a.MaxResp {
		// oversized answer: treat as invalid output (the validator would
		// reject it anyway; failing here bounds memory for the repair path)
		return AnalyzeOutput{}, &ProviderError{Class: ClassInvalidOutput, Status: resp.StatusCode,
			Err: fmt.Errorf("provider response exceeds %d bytes", a.MaxResp)}
	}
	var env analyzeEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassInvalidOutput, Status: resp.StatusCode,
			Err: errors.New("provider envelope is not valid JSON")}
	}
	if len(env.Result) == 0 {
		return AnalyzeOutput{}, &ProviderError{Class: ClassInvalidOutput, Status: resp.StatusCode,
			Err: errors.New("provider envelope carries no result")}
	}
	out := AnalyzeOutput{Raw: env.Result, Provider: env.Provider, Model: env.Model,
		UsageCostMicroUSD: env.UsageCostMicroUSD}
	if out.Provider == "" || len(out.Provider) > 100 {
		out.Provider = "configured-text-provider"
	}
	if out.Model == "" || len(out.Model) > 100 {
		out.Model = a.Model
	}
	if out.UsageCostMicroUSD != nil && (*out.UsageCostMicroUSD < 0 || *out.UsageCostMicroUSD > 1<<40) {
		// implausible usage: treat as unknown rather than poison the ledger
		out.UsageCostMicroUSD = nil
	}
	return out, nil
}

// ParseUsage is a helper for fake providers in tests.
func ParseUsage(s string) *int64 {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return &v
}
