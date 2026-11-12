// Package aiconnection owns provider-independent AI connection settings and
// transport. Features consume a selected configuration; credentials never
// cross the read-model boundary.
package aiconnection

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	OpenAI    = "OPENAI_COMPATIBLE"
	Anthropic = "ANTHROPIC"
	Gemini    = "GEMINI"
	Garden    = "GARDEN"
	Vision    = "VISION"
	Receipt   = "RECEIPT"
)

var Features = []string{Garden, Vision, Receipt}

type Config struct {
	Protocol, Endpoint, Model, ReasoningEffort, TokenLimitField string
	APIKey                                                      string `json:"-"`
	DeploymentLocal, SupportsVision, ExternalConsent            bool
}

func (c Config) String() string { return c.Protocol + " model " + c.Model }
func (c Config) Origin() string {
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

type Connection struct {
	ID, Name, Protocol, Endpoint, Model, ReasoningEffort, TokenLimitField string
	DeploymentLocal, SupportsVision, ExternalConsent, HasAPIKey           bool
	Revision                                                              int
	LastTestedAt                                                          *time.Time
	LastTestSucceeded                                                     *bool
	LastTestMessage                                                       *string
}
type Binding struct {
	Feature, Mode, ConnectionID string
	Revision                    int
}
type Default struct {
	Feature   string
	Config    Config
	HasAPIKey bool
}
type Settings struct {
	Connections []Connection
	Bindings    []Binding
	Defaults    []Default
}
type SaveInput struct {
	ID, Name, Protocol, Endpoint, Model, ReasoningEffort, TokenLimitField string
	DeploymentLocal, SupportsVision, ExternalConsent                      bool
	APIKey                                                                *string
	ClearAPIKey                                                           bool
	CopyDeploymentKeyFrom                                                 *string
	ExpectedRevision                                                      int
}
type BindingInput struct {
	Feature, Mode, ConnectionID string
	ExpectedRevision            int
}
type TestInput struct {
	ID               string
	ExpectedRevision *int
	Draft            *SaveInput
	TestVision       bool
}
type TestResult struct {
	Succeeded     bool
	Message       string
	TestedAt      time.Time
	VisionRequest bool
}
type Selection struct {
	Config             *Config
	Explicit, Disabled bool
}
type Problem struct {
	Code, Field, Message string
	Revision             *int
}

func (p *Problem) Error() string { return p.Message }
func invalid(field, message string) error {
	return &Problem{Code: "VALIDATION", Field: field, Message: message}
}
func conflict(revision int) error {
	return &Problem{Code: "CONFLICT", Message: "These AI settings changed. Reload them before saving.", Revision: &revision}
}
func notFound() error { return &Problem{Code: "NOT_FOUND", Message: "AI connection not found."} }
func validFeature(feature string) bool {
	for _, f := range Features {
		if f == feature {
			return true
		}
	}
	return false
}
func uid(id pgtype.UUID) string { value, _ := id.Value(); s, _ := value.(string); return s }
func uuid(s string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if id.Scan(s) != nil {
		return id, invalid("id", "Use a valid connection identifier.")
	}
	return id, nil
}
func public(row dbgen.AiConnection) Connection {
	out := Connection{ID: uid(row.ID), Name: row.Name, Protocol: row.Protocol, Endpoint: row.Endpoint, Model: row.Model, DeploymentLocal: row.DeploymentLocal, SupportsVision: row.SupportsVision, ExternalConsent: row.ExternalConsent, HasAPIKey: len(row.CredentialCiphertext) > 0, ReasoningEffort: row.ReasoningEffort, TokenLimitField: row.TokenLimitField, Revision: int(row.Revision)}
	if row.LastTestedAt.Valid {
		t := row.LastTestedAt.Time
		out.LastTestedAt = &t
	}
	if row.LastTestSucceeded.Valid {
		v := row.LastTestSucceeded.Bool
		out.LastTestSucceeded = &v
	}
	if row.LastTestMessage.Valid {
		v := row.LastTestMessage.String
		out.LastTestMessage = &v
	}
	return out
}
func normalize(in SaveInput) (Config, error) {
	in.Endpoint = strings.TrimRight(strings.TrimSpace(in.Endpoint), "/")
	u, err := url.Parse(in.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || len(in.Endpoint) > 2000 || strings.ContainsAny(in.Endpoint, "\r\n\t ") {
		return Config{}, invalid("endpoint", "Use an API base URL without credentials, query parameters or fragments.")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !in.DeploymentLocal) {
		return Config{}, invalid("endpoint", "Use HTTPS, or explicitly choose a deployment-local HTTP service.")
	}
	if in.Protocol != OpenAI && in.Protocol != Anthropic && in.Protocol != Gemini {
		return Config{}, invalid("protocol", "Choose a supported provider protocol.")
	}
	if strings.TrimSpace(in.Model) == "" || len(in.Model) > 200 || strings.ContainsAny(in.Model, "\r\n\t") {
		return Config{}, invalid("model", "Enter the exact model identifier supplied by your provider.")
	}
	if len(strings.TrimSpace(in.Name)) == 0 || len(in.Name) > 120 {
		return Config{}, invalid("name", "Give this connection a name of up to 120 characters.")
	}
	switch in.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
	default:
		return Config{}, invalid("reasoningEffort", "Choose a supported reasoning setting.")
	}
	if in.Protocol != OpenAI && in.ReasoningEffort != "" {
		return Config{}, invalid("reasoningEffort", "Reasoning effort here applies only to OpenAI-compatible services.")
	}
	if in.TokenLimitField != "MAX_TOKENS" && in.TokenLimitField != "MAX_COMPLETION_TOKENS" {
		return Config{}, invalid("tokenLimitField", "Choose a token limit parameter.")
	}
	if in.APIKey != nil && (len(*in.APIKey) > 4096 || strings.ContainsAny(*in.APIKey, "\r\n")) {
		return Config{}, invalid("apiKey", "The API key is invalid.")
	}
	if in.ExpectedRevision < 0 {
		return Config{}, invalid("expectedRevision", "Invalid settings revision.")
	}
	return Config{Protocol: in.Protocol, Endpoint: in.Endpoint, Model: strings.TrimSpace(in.Model), ReasoningEffort: in.ReasoningEffort, TokenLimitField: in.TokenLimitField, DeploymentLocal: in.DeploymentLocal, SupportsVision: in.SupportsVision, ExternalConsent: in.ExternalConsent}, nil
}

type workspaceKey struct{}

func WithWorkspace(ctx context.Context, ws pgtype.UUID) context.Context {
	return context.WithValue(ctx, workspaceKey{}, ws)
}
func Workspace(ctx context.Context) (pgtype.UUID, error) {
	id, ok := ctx.Value(workspaceKey{}).(pgtype.UUID)
	if !ok || !id.Valid {
		return pgtype.UUID{}, errors.New("AI workspace context missing")
	}
	return id, nil
}
