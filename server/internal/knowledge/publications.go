package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5"
)

// publication is shared by owned-plant and future-crop research. The cache stores
// only fetched public text and validated public extraction, never private context.
func (s *Service) publication(ctx context.Context, source Source) (dbgen.KnowledgePublication, error) {
	var zero dbgen.KnowledgePublication
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	if err = q.LockMutationKey(ctx, "knowledge-publication|"+source.URL); err != nil {
		return zero, err
	}
	doc, err := q.GetPublication(ctx, source.URL)
	if err == nil {
		return doc, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	if s.Fetch == nil {
		return zero, errors.New("source reader unavailable")
	}
	text, err := s.Fetch.Read(ctx, source)
	if err != nil {
		return zero, err
	}
	if len(text) >= 70000 {
		return zero, errors.New("source exceeds complete-publication read limit")
	}
	sum := sha256.Sum256([]byte(text))
	doc, err = q.PutPublication(ctx, dbgen.PutPublicationParams{Url: source.URL, Provider: source.ID, ContentSha256: hex.EncodeToString(sum[:]), SourceText: text})
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return doc, nil
}
