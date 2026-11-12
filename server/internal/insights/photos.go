package insights

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media/processor"
	platformmedia "canopy/server/internal/platform/media"
)

// resolveReadyPhotoAsset looks up a READY photo asset, workspace-scoped.
func resolveReadyPhotoAsset(ctx context.Context, q *dbgen.Queries, assetID, wsID pgtype.UUID) (dbgen.GetReadyAssetForAdapterRow, *ProviderError) {
	asset, err := q.GetReadyAssetForAdapter(ctx, dbgen.GetReadyAssetForAdapterParams{
		ID: assetID, WorkspaceID: wsID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.GetReadyAssetForAdapterRow{}, &ProviderError{Class: ClassPermanent,
			Err: errors.New("photo asset not found or not ready")}
	}
	if err != nil {
		return dbgen.GetReadyAssetForAdapterRow{}, &ProviderError{Class: ClassRetryable, Err: err}
	}
	return asset, nil
}

// adapterPhotoBytes resolves a READY photo asset to provider-safe bytes
// (shared by PlantNet T30 and VisionAnalyzer T31): prefer a derived
// variant (display > large > medium > thumb, always jpeg), enforce the
// per-photo byte bound, and transcode non-jpeg/png through the pinned
// media sandbox. The original is only ever read as a fallback.
func adapterPhotoBytes(ctx context.Context, q *dbgen.Queries, store platformmedia.Store, sb processor.Sandbox,
	workDir string, idx int, asset dbgen.GetReadyAssetForAdapterRow, maxBytes int64,
) (data []byte, mime string, perr *ProviderError) {
	key, mime := asset.StorageKey, asset.MimeType.String
	if variants, err := q.GetAssetVariantsForAdapter(ctx, asset.ID); err == nil {
		for _, want := range []string{"display", "large", "medium", "thumb"} {
			found := false
			for _, v := range variants {
				if v.Name == want {
					key, mime = v.StorageKey, "image/jpeg"
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	rc, err := store.Open(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", &ProviderError{Class: ClassPermanent, Err: errors.New("photo object missing from storage")}
	}
	if err != nil {
		return nil, "", &ProviderError{Class: ClassRetryable, Err: err}
	}
	defer func() { _ = rc.Close() }()
	limit := maxBytes
	if limit <= 0 {
		limit = 15 << 20
	}
	data, err = io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, "", &ProviderError{Class: ClassRetryable, Err: err}
	}
	if int64(len(data)) > limit {
		return nil, "", &ProviderError{Class: ClassPermanent, Err: errors.New("photo exceeds size bound")}
	}
	if mime == "image/jpeg" || mime == "image/png" {
		return data, mime, nil
	}
	if sb == nil {
		return nil, "", &ProviderError{Class: ClassPermanent,
			Err: fmt.Errorf("photo mime %q needs transcoding but no sandbox is wired", mime)}
	}
	ext, ok := receiptExt(mime)
	if !ok {
		return nil, "", &ProviderError{Class: ClassPermanent, Err: fmt.Errorf("unsupported photo mime %q", mime)}
	}
	name := "photo-" + strconv.Itoa(idx)
	if err := os.WriteFile(filepath.Join(workDir, name+ext), data, 0o600); err != nil {
		return nil, "", &ProviderError{Class: ClassRetryable, Err: err}
	}
	if _, err := sb.Run(ctx, workDir, "vips", "jpegsave", name+ext, name+".jpg"); err != nil {
		return nil, "", sandboxProviderErr(err)
	}
	out, err := os.ReadFile(filepath.Join(workDir, name+".jpg"))
	if err != nil {
		return nil, "", &ProviderError{Class: ClassRetryable, Err: err}
	}
	return out, "image/jpeg", nil
}
