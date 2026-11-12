package aiconnection

import (
	dbgen "canopy/server/db/generated"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

func (m *Manager) Test(ctx context.Context, ws pgtype.UUID, in TestInput) (TestResult, error) {
	out := TestResult{TestedAt: time.Now().UTC(), VisionRequest: in.TestVision}
	if (in.ID == "") == (in.Draft == nil) {
		return out, invalid("input", "Choose a saved connection or a draft, but not both.")
	}
	m.testMu.Lock()
	now := time.Now()
	history := []time.Time{}
	for _, at := range m.tests[uid(ws)] {
		if now.Sub(at) < time.Hour {
			history = append(history, at)
		}
	}
	if len(history) >= 20 {
		m.testMu.Unlock()
		return out, invalid("input", "Connection tests are limited to 20 attempts per hour. Try again later.")
	}
	m.tests[uid(ws)] = append(history, now)
	m.testMu.Unlock()
	var c Config
	var err error
	var saved dbgen.AiConnection
	if in.Draft != nil {
		c, _, _, err = m.prepare(ctx, m.Q, ws, *in.Draft)
	} else {
		id, parseErr := uuid(in.ID)
		if parseErr != nil {
			return out, parseErr
		}
		saved, err = m.Q.AIConnection(ctx, dbgen.AIConnectionParams{WorkspaceID: ws, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return out, notFound()
		}
		if err == nil {
			if in.ExpectedRevision == nil || *in.ExpectedRevision != int(saved.Revision) {
				return out, conflict(int(saved.Revision))
			}
			c = Config{Protocol: saved.Protocol, Endpoint: saved.Endpoint, Model: saved.Model, DeploymentLocal: saved.DeploymentLocal, SupportsVision: saved.SupportsVision, ExternalConsent: saved.ExternalConsent, ReasoningEffort: saved.ReasoningEffort, TokenLimitField: saved.TokenLimitField}
			c.APIKey, err = m.Vault.open(saved.CredentialCiphertext, uid(ws)+"|"+in.ID)
		}
	}
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out.Succeeded, out.Message = m.Probe(ctx, c, in.TestVision)
	if in.ID != "" {
		err = m.Q.RecordAIConnectionTest(ctx, dbgen.RecordAIConnectionTestParams{WorkspaceID: ws, ID: saved.ID, Revision: saved.Revision, LastTestSucceeded: pgtype.Bool{Bool: out.Succeeded, Valid: true}, LastTestMessage: pgtype.Text{String: out.Message, Valid: true}})
		if err != nil && ctx.Err() == nil {
			return out, err
		}
	}
	return out, nil
}
