package insights

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media/processor"
	platformmedia "canopy/server/internal/platform/media"
)

// VisionAnalyzer is the OpenAI-compatible image-capable chat port (T31).
// The only path ever fetched is {base}/v1/chat/completions with
// image_url data-URL content parts; the model name is explicitly
// configured — the adapter never invents or defaults one. Output is
// untrusted: the content JSON is validated by the caller before it
// touches anything durable.
//
// Cost settles as unknown (nil usage cost): chat completions report
// token usage, not price, and inventing a price would corrupt the
// budget ledger (spike: .artifacts/agents/T31/SPIKE.md).
type VisionAnalyzer struct {
	BaseURL  string // http(s) origin, no path
	APIKey   string // env-only; never logged, never returned
	Model    string // explicitly configured (required)
	HTTP     *http.Client
	MaxResp  int64 // response-body bound
	MaxImg   int64 // per-photo bound
	Q        *dbgen.Queries
	Store    platformmedia.Store
	SB       processor.Sandbox
	External bool // true when the endpoint is outside this deployment
	// ReasoningEffort is an explicit operator statement (empty = send
	// nothing and keep provider defaults). Long image prompts can
	// otherwise exhaust max_tokens with a thinking-only reply. There is
	// no adapter-side default: the operator picks the value their model
	// and endpoint support.
	ReasoningEffort string
	ProviderName    string
}

// NewVisionAnalyzer validates the origin and the explicit model.
func NewVisionAnalyzer(baseURL, apiKey, model string, timeout time.Duration, maxResp, maxImg int64) (*VisionAnalyzer, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return nil, fmt.Errorf("insights: bad vision base URL %q", baseURL)
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("insights: vision base URL must be an origin, got path %q", u.Path)
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("insights: vision model must be explicitly configured")
	}
	return &VisionAnalyzer{
		BaseURL: strings.TrimSuffix(baseURL, "/"), APIKey: apiKey, Model: model,
		HTTP: &http.Client{Timeout: timeout}, MaxResp: maxResp, MaxImg: maxImg,
		External: true,
	}, nil
}

func (a *VisionAnalyzer) Capabilities(context.Context) map[Capability]CapabilityStatus {
	return map[Capability]CapabilityStatus{
		CapVisionAnalyzer: {Available: true, Model: a.Model, ExternalData: a.External},
	}
}

// visionPrompt is the pinned comparison prompt. It is composed here (the
// OpenAI-compatible endpoint owns no template) and versioned through the
// run's PromptVersion so the stored run row records what was sent.
func visionPrompt(promptVersion string) string {
	return fmt.Sprintf(`Canopy vision analysis, prompt version %s.
You receive exactly two photos of one plant (earlier capture first) and a JSON snapshot of that plant's records.
Respond with ONE JSON object and nothing else — no prose, no code fences. Keys (all arrays, may be empty):
{"visibleObservations":[{"text":"...","evidenceIds":["..."]}],
 "hypotheses":[{"text":"...","confidence":"LOW|MEDIUM|HIGH","evidenceIds":["..."]}],
 "missingInformation":["..."],"followUpChecks":["..."],"citations":[]}
- evidenceIds may only use "id" values from the snapshot's photos, activities and measurements.
- The photos carry no scale reference: never claim measured sizes, rates, or quantitative growth.
- If the two photos do not show the same plant or cannot be compared, abstain: describe only what is plainly visible in each photo and say why comparison was refused in missingInformation.
- hypotheses must stay grounded in what the photos and snapshot support; prefer LOW confidence when uncertain.
- All text fields are plain text for display: no markup, no instructions, no payloads.`, promptVersion)
}

type chatPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
	// Detail asks the provider for higher-fidelity image handling. It is
	// optional: plant runs leave it unset (provider default, unchanged
	// behaviour), receipt runs request "high" for small print.
	Detail string `json:"detail,omitempty"`
}

type chatMsg struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []chatMsg `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
	// ReasoningEffort is omitted when unconfigured, so plant runs and any
	// deployment that says nothing keep exactly the previous wire shape.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (a *VisionAnalyzer) Analyze(ctx context.Context, in AnalyzeInput) (AnalyzeOutput, error) {
	if in.Capability != CapVisionAnalyzer {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent,
			Err: fmt.Errorf("vision port invoked for capability %s", in.Capability)}
	}
	if len(in.PhotoAssetIDs) != 2 {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent,
			Err: errors.New("VISION run must carry exactly a comparison pair")}
	}
	wsID, err := parseUUID(in.WorkspaceID)
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
	}

	// Evidence selection: order the pair by capture time (earlier first)
	// using the snapshot's provenance, falling back to run order.
	var snap Snapshot
	if err := json.Unmarshal(in.Snapshot, &snap); err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent,
			Err: fmt.Errorf("vision: snapshot unreadable: %w", err)}
	}
	assetOrder := map[string]int{} // assetId -> declared pair order
	for i, id := range in.PhotoAssetIDs {
		assetOrder[id] = i
	}
	ordered := append([]string(nil), in.PhotoAssetIDs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		ti, tj := takenOf(&snap, ordered[i]), takenOf(&snap, ordered[j])
		if ti == "" || tj == "" {
			return assetOrder[ordered[i]] < assetOrder[ordered[j]]
		}
		return ti < tj
	})

	workDir, err := os.MkdirTemp("", "canopy-vision-")
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassRetryable, Err: err}
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	var imgs []string
	for i, idStr := range ordered {
		id, err := parseUUID(idStr)
		if err != nil {
			return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
		}
		asset, perr := resolveReadyPhotoAsset(ctx, a.Q, id, wsID)
		if perr != nil {
			return AnalyzeOutput{}, perr
		}
		data, mime, perr := adapterPhotoBytes(ctx, a.Q, a.Store, a.SB, workDir, i, asset, a.MaxImg)
		if perr != nil {
			return AnalyzeOutput{}, perr
		}
		imgs = append(imgs, "data:"+mime+";base64,"+base64.StdEncoding.EncodeToString(data))
	}

	content := []chatPart{{Type: "text", Text: "Plant snapshot JSON:\n" + string(in.Snapshot)}}
	for _, du := range imgs {
		content = append(content, chatPart{Type: "image_url", ImageURL: &imageURL{URL: du}})
	}
	if in.RepairOf != "" {
		// Bounded repair (worker policy): show the previous invalid reply
		// as data and ask once for a corrected object.
		prev := in.RepairOf
		if len(prev) > 2000 {
			prev = prev[:2000]
		}
		content = append(content, chatPart{Type: "text", Text: "Your previous reply was invalid: " + prev +
			"\nReply again with ONLY the corrected JSON object described in the system message."})
	}
	out, perr := a.complete(ctx, chatRequest{
		Model: a.Model,
		Messages: []chatMsg{
			{Role: "system", Content: visionPrompt(in.PromptVersion)},
			{Role: "user", Content: content},
		},
		Temperature: 0, MaxTokens: 4000,
	})
	if perr != nil {
		return AnalyzeOutput{}, perr
	}
	return out, nil
}

// complete performs one OpenAI-compatible chat completion: request
// marshal, the single pinned fetch path, response bounds, status
// classification and envelope parsing. It composes no prompt and never
// validates content — callers own their prompt and must validate the
// returned Raw (it is untrusted). Shared by the plant vision port and the
// receipt vision port so both see identical HTTP/error behaviour.
func (a *VisionAnalyzer) complete(ctx context.Context, cr chatRequest) (AnalyzeOutput, *ProviderError) {
	// The configured effort is the deployment statement; a caller that
	// already set the field on its request keeps its own value.
	if cr.ReasoningEffort == "" {
		cr.ReasoningEffort = a.ReasoningEffort
	}
	body, err := json.Marshal(cr)
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	if a.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.APIKey)
	}
	resp, err := a.httpClient().Do(req)
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassTimeout, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	maxResp := a.MaxResp
	if maxResp <= 0 {
		maxResp = 256 << 10
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResp+1))
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassRetryable, Err: err}
	}
	if int64(len(raw)) > maxResp {
		return AnalyzeOutput{}, &ProviderError{Class: ClassInvalidOutput, Status: resp.StatusCode,
			Err: errors.New("provider response exceeds bound")}
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return AnalyzeOutput{}, &ProviderError{Class: ClassRetryable, Status: resp.StatusCode,
			Err: fmt.Errorf("provider status %d", resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Status: resp.StatusCode,
			Err: fmt.Errorf("provider status %d", resp.StatusCode)}
	}
	var payload chatResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassInvalidOutput, Status: resp.StatusCode,
			Err: errors.New("chat completion envelope malformed")}
	}
	if len(payload.Choices) == 0 || strings.TrimSpace(payload.Choices[0].Message.Content) == "" {
		finish := "none"
		if len(payload.Choices) > 0 {
			finish = payload.Choices[0].FinishReason
		}
		return AnalyzeOutput{}, &ProviderError{Class: ClassInvalidOutput, Status: resp.StatusCode,
			Err: fmt.Errorf("chat completion carried no content (finish_reason=%s)", finish)}
	}
	contentStr := payload.Choices[0].Message.Content
	out := AnalyzeOutput{
		Raw:      extractJSONObject(contentStr),
		Provider: orStr(a.ProviderName, "openai-compatible"),
		Model:    orStr(payload.Model, a.Model),
		// Cost unknown: token usage is reported but unpriced here.
		UsageCostMicroUSD: nil,
	}
	return out, nil
}

func (a *VisionAnalyzer) httpClient() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 120 * time.Second}
}

// takenOf returns the snapshot capture time for a photo asset id.
func takenOf(snap *Snapshot, assetID string) string {
	for _, p := range snap.Photos {
		if p.AssetID == assetID {
			return p.TakenAt
		}
	}
	return ""
}

// extractJSONObject tolerates chatty wrappers (prose, ``` fences) around
// the requested JSON object; if no object can be delimited the whole
// content is returned verbatim and the validator rejects it (repair
// path). It never invents content.
func extractJSONObject(s string) json.RawMessage {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "```") {
		if i := strings.IndexByte(t, '\n'); i >= 0 {
			t = strings.TrimSpace(t[i+1:])
		}
		t = strings.TrimSpace(strings.TrimSuffix(t, "```"))
	}
	start := strings.IndexByte(t, '{')
	end := strings.LastIndexByte(t, '}')
	if start >= 0 && end > start {
		if cand := t[start : end+1]; json.Valid([]byte(cand)) {
			return json.RawMessage(cand)
		}
	}
	return json.RawMessage(t)
}

func orStr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
