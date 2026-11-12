package graphql

import (
	"canopy/server/internal/media"
	"canopy/server/internal/platform"
	"context"
	"sync"
	"testing"
	"time"
)

func scoredCoverAsset(t *testing.T, m *mediaEnv, score float64) string {
	t.Helper()
	id, _ := readyAsset(t, m, "PHOTO", "image/jpeg", "quality-fixture.jpg", map[string][]byte{"original": jpegBytes(64)})
	if _, err := m.pool.Exec(context.Background(), `UPDATE assets SET cover_quality=$2,cover_quality_version=1 WHERE id=$1`, id, score); err != nil {
		t.Fatal(err)
	}
	return id
}
func TestAutomaticCoverImprovesWithoutRegressingHistory(t *testing.T) {
	m := newMediaEnv(t, 0)
	plant := mustPlantGQL(t, m)
	attach := func(asset, at, view, key string) map[string]any {
		t.Helper()
		payload, problems := runMut(t, m, "attachPhoto", "AttachPhotoInput", map[string]any{"plantId": plant, "assetId": asset, "dateSource": "USER", "takenAt": at, "viewpoint": view}, strpGQL(key), "photo{id isCover takenAt}")
		mustNoProblems(t, problems, "automatic cover")
		return payload["photo"].(map[string]any)
	}
	firstAsset := scoredCoverAsset(t, m, 50)
	first := attach(firstAsset, "2026-11-01T12:00:00Z", "WHOLE", "first-cover")
	if first["isCover"] != true {
		t.Fatal("first photo did not become cover")
	}
	betterAsset := scoredCoverAsset(t, m, 90)
	better := attach(betterAsset, "2026-11-11T12:00:00Z", "WHOLE", "better-cover")
	if better["isCover"] != true {
		t.Fatal("better current whole-plant photo did not become cover")
	}
	replay := attach(betterAsset, "2026-11-11T12:00:00Z", "WHOLE", "better-cover")
	if replay["id"] != better["id"] {
		t.Fatal("retry duplicated an attachment")
	}
	for _, tc := range []struct {
		name, date, view string
		score            float64
	}{
		{"new blurry", "2026-11-12T12:00:00Z", "WHOLE", 20},
		{"old import", "2026-09-01T12:00:00Z", "WHOLE", 100},
		{"closeup", "2026-11-12T12:00:00Z", "FRUIT", 100},
	} {
		if photo := attach(scoredCoverAsset(t, m, tc.score), tc.date, tc.view, tc.name); photo["isCover"] != false {
			t.Fatal("inappropriate cover", tc.name)
		}
	}
	var count int
	var id string
	if err := m.pool.QueryRow(context.Background(), `SELECT count(*),max(id::text) FROM plant_photos WHERE plant_id=$1 AND is_cover AND deleted_at IS NULL`, plant).Scan(&count, &id); err != nil || count != 1 || id != better["id"] {
		t.Fatal("cover invariant", count, id, err)
	}
	payload, problems := runMut(t, m, "updatePhoto", "UpdatePhotoInput", map[string]any{"id": first["id"], "expectedRevision": 2, "isCover": true}, nil, "photo{id isCover}")
	mustNoProblems(t, problems, "manual cover remains available")
	if payload["photo"].(map[string]any)["isCover"] != true {
		t.Fatal("explicit manual selection ignored")
	}
}
func TestConcurrentAutomaticCoversChooseTheBetterCandidate(t *testing.T) {
	m := newMediaEnv(t, 0)
	plant := mustPlantGQL(t, m)
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	assets := []string{scoredCoverAsset(t, m, 60), scoredCoverAsset(t, m, 95)}
	actor := media.Actor{WorkspaceID: m.wsID, OwnerID: m.ownerID, SessionID: platform.UUIDv4{}.New()}
	var wait sync.WaitGroup
	failures := make(chan string, 2)
	for _, asset := range assets {
		wait.Add(1)
		go func(asset string) {
			defer wait.Done()
			_, problems, err := m.svc.AttachPhoto(context.Background(), actor, media.AttachPhotoInput{PlantID: plant, AssetID: asset, DateSource: "USER", TakenAt: &at, Viewpoint: "WHOLE"}, nil)
			if err != nil || len(problems) > 0 {
				failures <- "attachment failed"
			}
		}(asset)
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	var selected string
	if err := m.pool.QueryRow(context.Background(), `SELECT asset_id::text FROM plant_photos WHERE plant_id=$1 AND is_cover AND deleted_at IS NULL`, plant).Scan(&selected); err != nil || selected != assets[1] {
		t.Fatal("concurrent uploads did not converge on best cover", err, selected)
	}
}
