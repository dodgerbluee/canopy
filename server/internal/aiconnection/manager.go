package aiconnection

import (
	dbgen "canopy/server/db/generated"
	"context"
	"encoding/json"
	"errors"
	guuid "github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	Pool     *pgxpool.Pool
	Q        *dbgen.Queries
	Vault    *Vault
	Defaults map[string]Config
	// Test seam; production clients use the restricted dial/redirect policy.
	Transport http.RoundTripper
	testMu    sync.Mutex
	tests     map[string][]time.Time
}

func New(pool *pgxpool.Pool, keyFile string, defaults map[string]Config) (*Manager, error) {
	vault, err := LoadVault(keyFile)
	if err != nil {
		return nil, err
	}
	return &Manager{Pool: pool, Q: dbgen.New(pool), Vault: vault, Defaults: defaults, tests: map[string][]time.Time{}}, nil
}
func (m *Manager) Settings(ctx context.Context, ws pgtype.UUID) (Settings, error) {
	out := Settings{Connections: []Connection{}, Bindings: []Binding{}, Defaults: []Default{}}
	rows, err := m.Q.AIConnections(ctx, ws)
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		out.Connections = append(out.Connections, public(row))
	}
	bindings, err := m.Q.AIFeatureBindings(ctx, ws)
	if err != nil {
		return out, err
	}
	for _, feature := range Features {
		b := Binding{Feature: feature, Mode: "DEFAULT"}
		for _, row := range bindings {
			if row.Feature == feature {
				b.Mode = row.Mode
				b.Revision = int(row.Revision)
				if row.ConnectionID.Valid {
					b.ConnectionID = uid(row.ConnectionID)
				}
			}
		}
		out.Bindings = append(out.Bindings, b)
		if c, ok := m.Defaults[feature]; ok && c.Endpoint != "" && c.Model != "" {
			has := c.APIKey != ""
			c.APIKey = ""
			out.Defaults = append(out.Defaults, Default{Feature: feature, Config: c, HasAPIKey: has})
		}
	}
	return out, nil
}

func (m *Manager) mutate(ctx context.Context, ws pgtype.UUID, operation string, key *string, input any, out any, work func(*dbgen.Queries) error) error {
	if key != nil && (len(*key) == 0 || len(*key) > 200) {
		return invalid("clientMutationId", "Use a request identifier of up to 200 characters.")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return errors.New("AI settings request is invalid")
	}
	fingerprint := m.Vault.fingerprint(body)
	tx, err := m.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	if err = q.LockMutationKey(ctx, uid(ws)+"|ai-connections"); err != nil {
		return err
	}
	if key != nil {
		receipt, readErr := q.GetMutationReceipt(ctx, dbgen.GetMutationReceiptParams{WorkspaceID: ws, Operation: operation, IdempotencyKey: *key})
		if readErr == nil {
			if receipt.RequestHash != fingerprint {
				return &Problem{Code: "CONFLICT", Message: "This request identifier was already used for different AI settings."}
			}
			return json.Unmarshal(receipt.Result, out)
		}
		if !errors.Is(readErr, pgx.ErrNoRows) {
			return readErr
		}
	}
	if err = work(q); err != nil {
		return err
	}
	if key != nil {
		result, err := json.Marshal(out)
		if err != nil {
			return err
		}
		id, _ := uuid(guuid.NewString())
		_, err = q.InsertMutationReceipt(ctx, dbgen.InsertMutationReceiptParams{ID: id, WorkspaceID: ws, Operation: operation, IdempotencyKey: *key, RequestHash: fingerprint, Result: result, ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true}})
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (m *Manager) prepare(ctx context.Context, q *dbgen.Queries, ws pgtype.UUID, in SaveInput) (Config, dbgen.AiConnection, bool, error) {
	c, err := normalize(in)
	if err != nil {
		return c, dbgen.AiConnection{}, false, err
	}
	id, err := uuid(in.ID)
	if err != nil {
		return c, dbgen.AiConnection{}, false, err
	}
	old, err := q.AIConnection(ctx, dbgen.AIConnectionParams{WorkspaceID: ws, ID: id})
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return c, old, false, err
	}
	if exists && int(old.Revision) != in.ExpectedRevision {
		return c, old, true, conflict(int(old.Revision))
	}
	if !exists && in.ExpectedRevision != 0 {
		return c, old, false, notFound()
	}
	if in.APIKey != nil && *in.APIKey != "" && in.ClearAPIKey {
		return c, old, exists, invalid("apiKey", "Choose either replacing or clearing the API key.")
	}
	if in.CopyDeploymentKeyFrom != nil {
		if in.APIKey != nil && *in.APIKey != "" || in.ClearAPIKey {
			return c, old, exists, invalid("apiKey", "Choose one credential source.")
		}
		deployment, ok := m.Defaults[*in.CopyDeploymentKeyFrom]
		if !ok || deployment.Endpoint != c.Endpoint || deployment.Protocol != c.Protocol {
			return c, old, exists, invalid("apiKey", "A deployment key can only be copied to the same provider and endpoint. Enter a new key for a different endpoint.")
		}
		c.APIKey = deployment.APIKey
	} else if in.ClearAPIKey {
		c.APIKey = ""
	} else if in.APIKey != nil && *in.APIKey != "" {
		c.APIKey = *in.APIKey
	} else if exists {
		if old.Endpoint != c.Endpoint || old.Protocol != c.Protocol {
			if len(old.CredentialCiphertext) > 0 {
				return c, old, true, invalid("apiKey", "Enter a new key or explicitly clear the saved key when changing provider or endpoint.")
			}
		}
		c.APIKey, err = m.Vault.open(old.CredentialCiphertext, uid(ws)+"|"+in.ID)
		if err != nil {
			return c, old, true, err
		}
	}
	return c, old, exists, nil
}
func (m *Manager) Save(ctx context.Context, ws pgtype.UUID, in SaveInput, key *string) (Connection, error) {
	var out Connection
	err := m.mutate(ctx, ws, "saveAIConnection", key, in, &out, func(q *dbgen.Queries) error {
		c, _, _, err := m.prepare(ctx, q, ws, in)
		if err != nil {
			return err
		}
		id, _ := uuid(in.ID)
		encrypted, err := m.Vault.seal(c.APIKey, uid(ws)+"|"+in.ID)
		if err != nil {
			return err
		}
		row, err := q.SaveAIConnection(ctx, dbgen.SaveAIConnectionParams{ID: id, WorkspaceID: ws, Name: strings.TrimSpace(in.Name), Protocol: c.Protocol, Endpoint: c.Endpoint, Model: c.Model, DeploymentLocal: c.DeploymentLocal, SupportsVision: c.SupportsVision, ExternalConsent: c.ExternalConsent, CredentialCiphertext: encrypted, ReasoningEffort: c.ReasoningEffort, TokenLimitField: c.TokenLimitField})
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		out = public(row)
		return nil
	})
	return out, err
}
func (m *Manager) SaveBindings(ctx context.Context, ws pgtype.UUID, in []BindingInput, key *string) ([]Binding, error) {
	out := []Binding{}
	if len(in) == 0 || len(in) > 3 {
		return out, invalid("input", "Choose one to three feature assignments.")
	}
	err := m.mutate(ctx, ws, "saveAIFeatureBindings", key, in, &out, func(q *dbgen.Queries) error {
		seen := map[string]bool{}
		for _, item := range in {
			if !validFeature(item.Feature) || seen[item.Feature] {
				return invalid("feature", "Choose each supported feature only once.")
			}
			seen[item.Feature] = true
			row, err := q.AIFeatureBinding(ctx, dbgen.AIFeatureBindingParams{WorkspaceID: ws, Feature: item.Feature})
			revision := 0
			if err == nil {
				revision = int(row.Revision)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if revision != item.ExpectedRevision {
				return conflict(revision)
			}
			var connectionID pgtype.UUID
			switch item.Mode {
			case "DEFAULT", "DISABLED":
				if item.ConnectionID != "" {
					return invalid("connectionId", "A default or disabled feature cannot name a connection.")
				}
			case "CONNECTION":
				connectionID, err = uuid(item.ConnectionID)
				if err != nil {
					return err
				}
				connection, err := q.AIConnection(ctx, dbgen.AIConnectionParams{WorkspaceID: ws, ID: connectionID})
				if errors.Is(err, pgx.ErrNoRows) {
					return notFound()
				}
				if err != nil {
					return err
				}
				if (item.Feature == Vision || item.Feature == Receipt) && !connection.SupportsVision {
					return invalid("connectionId", "Choose a vision-capable connection for photo or image-receipt analysis.")
				}
			default:
				return invalid("mode", "Choose deployment default, disabled, or a saved connection.")
			}
			if err = q.SaveAIFeatureBinding(ctx, dbgen.SaveAIFeatureBindingParams{WorkspaceID: ws, Feature: item.Feature, Mode: item.Mode, ConnectionID: connectionID}); err != nil {
				return err
			}
			out = append(out, Binding{Feature: item.Feature, Mode: item.Mode, ConnectionID: item.ConnectionID, Revision: revision + 1})
		}
		return nil
	})
	return out, err
}
func (m *Manager) Delete(ctx context.Context, ws pgtype.UUID, idText string, revision int, key *string) error {
	var out struct{ Deleted bool }
	return m.mutate(ctx, ws, "deleteAIConnection", key, []any{idText, revision}, &out, func(q *dbgen.Queries) error {
		id, err := uuid(idText)
		if err != nil {
			return err
		}
		row, err := q.AIConnection(ctx, dbgen.AIConnectionParams{WorkspaceID: ws, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if int(row.Revision) != revision {
			return conflict(int(row.Revision))
		}
		if err = q.DisableAIConnectionBindings(ctx, dbgen.DisableAIConnectionBindingsParams{WorkspaceID: ws, ConnectionID: id}); err != nil {
			return err
		}
		if err = q.DeleteAIConnection(ctx, dbgen.DeleteAIConnectionParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		out.Deleted = true
		return nil
	})
}
func (m *Manager) Resolve(ctx context.Context, ws pgtype.UUID, feature string) (Selection, error) {
	if !validFeature(feature) {
		return Selection{}, errors.New("AI feature is unknown")
	}
	b, err := m.Q.AIFeatureBinding(ctx, dbgen.AIFeatureBindingParams{WorkspaceID: ws, Feature: feature})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && b.Mode == "DEFAULT" {
		c, ok := m.Defaults[feature]
		if !ok || c.Endpoint == "" || c.Model == "" {
			return Selection{}, nil
		}
		return Selection{Config: &c}, nil
	}
	if err != nil {
		return Selection{}, err
	}
	if b.Mode == "DISABLED" {
		return Selection{Explicit: true, Disabled: true}, nil
	}
	row, err := m.Q.AIConnection(ctx, dbgen.AIConnectionParams{WorkspaceID: ws, ID: b.ConnectionID})
	if err != nil {
		return Selection{}, errors.New("Assigned AI connection is unavailable")
	}
	if !row.DeploymentLocal && !row.ExternalConsent {
		return Selection{}, errors.New("Enable external-data consent for this AI connection in Settings")
	}
	key, err := m.Vault.open(row.CredentialCiphertext, uid(ws)+"|"+uid(row.ID))
	if err != nil {
		return Selection{}, err
	}
	c := Config{Protocol: row.Protocol, Endpoint: row.Endpoint, Model: row.Model, APIKey: key, DeploymentLocal: row.DeploymentLocal, SupportsVision: row.SupportsVision, ExternalConsent: row.ExternalConsent, ReasoningEffort: row.ReasoningEffort, TokenLimitField: row.TokenLimitField}
	return Selection{Config: &c, Explicit: true}, nil
}
