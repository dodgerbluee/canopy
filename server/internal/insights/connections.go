package insights

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/aiconnection"
	"canopy/server/internal/media/processor"
	platformmedia "canopy/server/internal/platform/media"
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

// ConnectionRouter resolves a workspace's explicit selection for every attempt.
// Resolution errors and disabled selections fail closed; defaults retain the
// established deployment adapter, including local OCR and PlantNet.
type ConnectionRouter struct {
	Manager     *aiconnection.Manager
	Fallback    Port
	Q           *dbgen.Queries
	Store       platformmedia.Store
	SB          processor.Sandbox
	OCRLangs    string
	OCRMaxPages int
}

func WithConnections(m *aiconnection.Manager, fallback Port, pool *pgxpool.Pool, store platformmedia.Store, sb processor.Sandbox, langs string, pages int) Port {
	return &ConnectionRouter{m, fallback, dbgen.New(pool), store, sb, langs, pages}
}
func featureFor(c Capability) string {
	switch c {
	case CapTextAnalyzer:
		return aiconnection.Garden
	case CapVisionAnalyzer:
		return aiconnection.Vision
	case CapReceiptExtractor:
		return aiconnection.Receipt
	}
	return ""
}
func (r *ConnectionRouter) Capabilities(ctx context.Context) map[Capability]CapabilityStatus {
	caps := r.Fallback.Capabilities(ctx)
	ws, err := aiconnection.Workspace(ctx)
	if err != nil {
		return caps
	}
	for _, cap := range []Capability{CapTextAnalyzer, CapVisionAnalyzer, CapReceiptExtractor} {
		selected, err := r.Manager.Resolve(ctx, ws, featureFor(cap))
		if err != nil {
			caps[cap] = CapabilityStatus{Reason: "Review the selected AI connection in Settings."}
			continue
		}
		if !selected.Explicit {
			continue
		}
		if selected.Disabled || selected.Config == nil {
			caps[cap] = CapabilityStatus{Reason: "This AI feature is turned off in Settings."}
			continue
		}
		c := selected.Config
		if cap != CapTextAnalyzer && !c.SupportsVision {
			caps[cap] = CapabilityStatus{Reason: "The selected connection needs image support."}
			continue
		}
		caps[cap] = CapabilityStatus{Available: true, Model: c.Model, ExternalData: !c.DeploymentLocal}
	}
	return caps
}
func (r *ConnectionRouter) Analyze(ctx context.Context, in AnalyzeInput) (AnalyzeOutput, error) {
	feature := featureFor(in.Capability)
	if feature == "" {
		return r.Fallback.Analyze(ctx, in)
	}
	var ws pgtype.UUID
	if ws.Scan(in.WorkspaceID) != nil {
		return AnalyzeOutput{}, errors.New("invalid analysis workspace")
	}
	selected, err := r.Manager.Resolve(ctx, ws, feature)
	if err != nil || selected.Disabled {
		return AnalyzeOutput{}, &ProviderError{Class: ClassUnavailable, Err: errors.New("The selected AI connection is unavailable.")}
	}
	if !selected.Explicit {
		return r.Fallback.Analyze(ctx, in)
	}
	if selected.Config == nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassUnavailable}
	}
	c := *selected.Config
	if in.Capability != CapTextAnalyzer && !c.SupportsVision {
		return AnalyzeOutput{}, &ProviderError{Class: ClassUnavailable}
	}
	v, err := NewVisionAnalyzer(c.Origin(), c.APIKey, c.Model, 150*time.Second, 1<<20, 15<<20)
	if err != nil {
		return AnalyzeOutput{}, err
	}
	v.HTTP = r.Manager.Client(c)
	v.Q = r.Q
	v.Store = r.Store
	v.SB = r.SB
	v.External = !c.DeploymentLocal
	v.ReasoningEffort = c.ReasoningEffort
	v.ProviderName = c.Protocol
	if in.Capability == CapReceiptExtractor {
		return (&ReceiptVision{Vision: v, OCR: &ReceiptOCR{Q: r.Q, Store: r.Store, SB: r.SB, Langs: r.OCRLangs, MaxPages: r.OCRMaxPages}}).Analyze(ctx, in)
	}
	if in.Capability == CapVisionAnalyzer {
		return v.Analyze(ctx, in)
	}
	messages, err := historyMessages(in)
	if err != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassPermanent, Err: err}
	}
	out, pe := v.complete(ctx, chatRequest{Model: c.Model, MaxTokens: 4000, Messages: messages})
	if pe != nil {
		return out, pe
	}
	return out, nil
}
