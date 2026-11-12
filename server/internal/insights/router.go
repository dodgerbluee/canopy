package insights

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media/processor"
	platformmedia "canopy/server/internal/platform/media"
)

// Router is a Port that dispatches each attempt to the port registered
// for its capability (T30: text/vision via the HTTP adapter, plant
// identification via PlantNet, receipt extraction via local OCR).
// Capabilities() merges every registered port's status map; later
// registrations win, so composition roots append a NoProvider base and
// override with real adapters.
type Router struct {
	Ports []Port // merge order: later entries override earlier ones
}

func (r *Router) Capabilities(ctx context.Context) map[Capability]CapabilityStatus {
	merged := map[Capability]CapabilityStatus{}
	for _, p := range r.Ports {
		for c, st := range p.Capabilities(ctx) {
			merged[c] = st
		}
	}
	return merged
}

func (r *Router) Analyze(ctx context.Context, in AnalyzeInput) (AnalyzeOutput, error) {
	for i := len(r.Ports) - 1; i >= 0; i-- {
		st := r.Ports[i].Capabilities(ctx)[in.Capability]
		if !st.Available {
			continue
		}
		return r.Ports[i].Analyze(ctx, in)
	}
	return AnalyzeOutput{}, &ProviderError{
		Class: ClassUnavailable,
		Err:   fmt.Errorf("no available port for capability %s", in.Capability),
	}
}

// RouterConfig is the config slice BuildRouter consumes (kept primitive
// so composition roots pass fields, not the platform.Config type).
type RouterConfig struct {
	AIBaseURL          string
	AIAPIKey           string
	AIModel            string
	AITimeout          time.Duration
	AIMaxResponseBytes int64
	VisionBaseURL      string
	VisionAPIKey       string
	VisionModel        string
	VisionTimeout      time.Duration
	VisionMaxRespBytes int64
	VisionMaxImageB    int64
	// VisionLocal marks the vision endpoint as part of this deployment
	// (self-hosted). Default false keeps the external-data disclosure.
	VisionLocal bool
	// VisionReasoningEffort passes an explicit reasoning_effort to the
	// vision endpoint ("" = send nothing, keep provider defaults). Valid
	// values are validated at config load; the adapter never invents one.
	VisionReasoningEffort string
	PlantNetBaseURL       string
	PlantNetAPIKey        string
	OCRLangs              string
	OCRMaxPages           int
}

// BuildRouter assembles the deployment's capability ports in merge
// order: honest no-provider base, then the configured text adapter,
// PlantNet (only when a key is pinned), and the local receipt OCR port
// (always present: tesseract/poppler live in the pinned media image).
// When a vision model is configured, image receipts are served by
// ReceiptVision (which keeps OCR as the PDF path) and that composite is
// the single receipt port appended last, so it wins the merge; without
// vision, plain OCR is the receipt port.
// It takes the pool, not *dbgen.Queries: composition roots (cmd/**)
// must not import db/generated directly (boundary rule).
func BuildRouter(pool *pgxpool.Pool, store platformmedia.Store, sb processor.Sandbox, cfg RouterConfig) (*Router, error) {
	q := dbgen.New(pool)
	ports := []Port{NoProvider{}}
	var textPort Port
	ocr := &ReceiptOCR{
		Q: q, Store: store, SB: sb,
		Langs: cfg.OCRLangs, MaxPages: cfg.OCRMaxPages,
	}
	if cfg.AIBaseURL != "" {
		a, err := NewHTTPAdapter(cfg.AIBaseURL, cfg.AIAPIKey, cfg.AIModel, cfg.AITimeout, cfg.AIMaxResponseBytes)
		if err != nil {
			return nil, err
		}
		textPort = a
		ports = append(ports, a)
	}
	// Structuring OCR text through the configured text model is kept
	// whether or not vision is configured; a nil textPort leaves OCR
	// self-structuring.
	ocr.Structurer = textPort
	receipt := Port(ocr)
	if cfg.VisionBaseURL != "" {
		v, err := NewVisionAnalyzer(cfg.VisionBaseURL, cfg.VisionAPIKey, cfg.VisionModel,
			cfg.VisionTimeout, cfg.VisionMaxRespBytes, cfg.VisionMaxImageB)
		if err != nil {
			return nil, err
		}
		v.Q, v.Store, v.SB = q, store, sb
		// Operator-set reasoning effort; empty leaves the wire shape and
		// the provider default untouched.
		v.ReasoningEffort = cfg.VisionReasoningEffort
		// Deployment-local is an explicit operator statement; anything
		// else keeps the honest external-data disclosure.
		v.External = !cfg.VisionLocal
		ports = append(ports, v)
		// Image receipts go straight to the configured vision model;
		// the OCR port stays behind it as the PDF path. This composite
		// replaces ocr as the receipt port appended below.
		receipt = &ReceiptVision{Vision: v, OCR: ocr}
	}
	if cfg.PlantNetAPIKey != "" {
		ports = append(ports, &PlantNet{
			BaseURL: cfg.PlantNetBaseURL, APIKey: cfg.PlantNetAPIKey,
			Q: q, Store: store, SB: sb,
		})
	}
	ports = append(ports, receipt)
	return &Router{Ports: ports}, nil
}
