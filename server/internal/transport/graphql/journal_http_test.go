// T16 verification: photo journal projection + chronological photo series
// over the frozen journal query. Covers date-source semantics (unknown
// naive EXIF never invents an instant), atomic capture-date correction,
// tombstones, ordering, pagination and cursor/filter binding.
package graphql

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

const journalPhotoSel = `
query Journal($plantId: ID, $first: Int, $after: String, $filter: JournalFilter) {
  journal(plantId: $plantId, first: $first, after: $after, filter: $filter) {
    items {
      __typename
      ... on PhotoEntry {
        id plantId occurredAt createdAt summary tombstonedAt
        photo { id takenAt dateSource viewpoint caption isCover revision deletedAt
                asset { id state createdAt } }
      }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

type journalResp struct {
	Journal struct {
		Items    []map[string]any `json:"items"`
		PageInfo struct {
			HasNextPage bool    `json:"hasNextPage"`
			EndCursor   *string `json:"endCursor"`
		} `json:"pageInfo"`
	} `json:"journal"`
}

func journalQuery(t *testing.T, m *mediaEnv, vars map[string]any) journalResp {
	t.Helper()
	r := m.gql(journalPhotoSel, vars)
	if len(r.Errors) > 0 {
		t.Fatalf("journal query errors: %v", r.Raw)
	}
	var out journalResp
	if err := remarshal(r.Data, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func remarshal(v any, dst any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// photoEntry pulls one PhotoEntry out of an items list by index.
func photoEntry(t *testing.T, items []map[string]any, i int) map[string]any {
	t.Helper()
	if i >= len(items) {
		t.Fatalf("item %d missing (have %d)", i, len(items))
	}
	it := items[i]
	if it["__typename"] != "PhotoEntry" {
		t.Fatalf("item %d is %v, want PhotoEntry", i, it["__typename"])
	}
	return it
}

func entryPhoto(t *testing.T, entry map[string]any) map[string]any {
	t.Helper()
	p, ok := entry["photo"].(map[string]any)
	if !ok {
		t.Fatalf("entry has no photo: %v", entry)
	}
	return p
}

func attachPhotoMut(t *testing.T, m *mediaEnv, plantID, assetID string, in map[string]any) map[string]any {
	t.Helper()
	full := map[string]any{"plantId": plantID, "assetId": assetID}
	for k, v := range in {
		full[k] = v
	}
	payload, probs := runMut(t, m, "attachPhoto", "AttachPhotoInput", full, nil,
		`photo { id takenAt dateSource viewpoint caption revision
		        asset { id state createdAt } }`)
	if len(probs) > 0 {
		t.Fatalf("attachPhoto problems: %v", probs)
	}
	return payload["photo"].(map[string]any)
}

func readyPhoto(t *testing.T, m *mediaEnv) string {
	t.Helper()
	assetID, _ := readyAsset(t, m, "PHOTO", "image/jpeg", "garden.jpg", map[string][]byte{
		"thumb": jpegBytes(64), "display": jpegBytes(256),
	})
	return assetID
}

// ---- date semantics ---------------------------------------------------------

// Unknown naive EXIF: the photo carries no instant (UI must prompt), and
// the timeline orders by the upload instant — visibly labeled, never an
// invented capture time.
func TestJournalExifUnknownDate(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)
	photo := attachPhotoMut(t, m, plantID, readyPhoto(t, m), map[string]any{
		"dateSource": "EXIF", "viewpoint": "WHOLE",
	})
	if photo["takenAt"] != nil {
		t.Fatalf("naive EXIF must leave takenAt null, got %v", photo["takenAt"])
	}
	if photo["dateSource"] != "EXIF" {
		t.Fatalf("dateSource = %v, want EXIF", photo["dateSource"])
	}
	uploadInstant := photo["asset"].(map[string]any)["createdAt"].(string)

	page := journalQuery(t, m, map[string]any{"plantId": plantID})
	if len(page.Journal.Items) != 1 {
		t.Fatalf("want 1 entry, got %d", len(page.Journal.Items))
	}
	e := photoEntry(t, page.Journal.Items, 0)
	if e["occurredAt"] != uploadInstant {
		t.Fatalf("occurredAt = %v, want upload instant %v", e["occurredAt"], uploadInstant)
	}
	if entryPhoto(t, e)["dateSource"] != "EXIF" {
		t.Fatal("entry photo must carry the EXIF label for the UI prompt")
	}
	if e["summary"] != "Photo of whole plant" {
		t.Fatalf("summary = %v", e["summary"])
	}
}

// USER dates are exact; UPLOAD_FALLBACK orders by upload instant and says so.
func TestJournalUserAndFallbackDates(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)
	userAt := "2026-05-04T09:30:00Z"
	userPhoto := attachPhotoMut(t, m, plantID, readyPhoto(t, m), map[string]any{
		"dateSource": "USER", "takenAt": userAt, "viewpoint": "LEAF", "caption": "new growth",
	})
	if userPhoto["takenAt"] != userAt {
		t.Fatalf("USER takenAt = %v, want %v", userPhoto["takenAt"], userAt)
	}
	fb := attachPhotoMut(t, m, plantID, readyPhoto(t, m), map[string]any{
		"dateSource": "UPLOAD_FALLBACK", "viewpoint": "FRUIT",
	})
	uploadInstant := fb["asset"].(map[string]any)["createdAt"].(string)

	page := journalQuery(t, m, map[string]any{"plantId": plantID})
	if len(page.Journal.Items) != 2 {
		t.Fatalf("want 2 entries, got %d", len(page.Journal.Items))
	}
	// DESC by occurredAt: upload instant (now) is after May 2026
	first := photoEntry(t, page.Journal.Items, 0)
	if first["occurredAt"] != uploadInstant {
		t.Fatalf("fallback entry occurredAt = %v, want %v", first["occurredAt"], uploadInstant)
	}
	if first["summary"] != "Photo of fruit" {
		t.Fatalf("summary = %v", first["summary"])
	}
	second := photoEntry(t, page.Journal.Items, 1)
	if second["occurredAt"] != userAt {
		t.Fatalf("USER entry occurredAt = %v, want %v", second["occurredAt"], userAt)
	}
	if second["summary"] != "new growth" {
		t.Fatalf("caption summary = %v", second["summary"])
	}
}

// A capture-date correction moves the projection entry in the same tx as
// the photo row: after the mutation both agree, and there is still exactly
// one entry for the photo.
func TestJournalCaptureCorrectionAtomic(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)
	photo := attachPhotoMut(t, m, plantID, readyPhoto(t, m), map[string]any{
		"dateSource": "EXIF", "viewpoint": "WHOLE",
	})
	photoID := photo["id"].(string)
	uploadInstant := photo["asset"].(map[string]any)["createdAt"].(string)

	corrected := "2026-07-15T16:45:00Z"
	payload, probs := runMut(t, m, "updatePhoto", "UpdatePhotoInput", map[string]any{
		"id": photoID, "expectedRevision": 1,
		"takenAt": corrected, "dateSource": "EXIF", "timezone": "America/Los_Angeles",
	}, nil, `photo { id takenAt revision }`)
	if len(probs) > 0 {
		t.Fatalf("updatePhoto problems: %v", probs)
	}
	up := payload["photo"].(map[string]any)
	if up["takenAt"] != corrected || up["revision"].(float64) != 2 {
		t.Fatalf("photo after correction: %v", up)
	}

	page := journalQuery(t, m, map[string]any{"plantId": plantID})
	if len(page.Journal.Items) != 1 {
		t.Fatalf("correction must not duplicate the entry, got %d", len(page.Journal.Items))
	}
	e := photoEntry(t, page.Journal.Items, 0)
	if e["occurredAt"] != corrected {
		t.Fatalf("entry occurredAt = %v, want corrected %v (was upload %v)",
			e["occurredAt"], corrected, uploadInstant)
	}
	if e["id"] == photoID {
		t.Fatal("entry id must be the journal entry, not the photo id")
	}
	if entryPhoto(t, e)["id"] != photoID {
		t.Fatal("entry.photo.id must be the photo")
	}
}

// ---- tombstones -------------------------------------------------------------

func TestJournalDeleteTombstones(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)
	photo := attachPhotoMut(t, m, plantID, readyPhoto(t, m), map[string]any{
		"dateSource": "USER", "takenAt": "2026-06-01T12:00:00Z", "viewpoint": "LEAF",
	})
	photoID := photo["id"].(string)

	r := m.gql(`mutation ($input: DeletePhotoInput!, $cmid: String!) {
		deletePhoto(input: $input, clientMutationId: $cmid) { deletedPhotoId problems { code message } } }`,
		map[string]any{"input": map[string]any{"id": photoID, "expectedRevision": 1}, "cmid": "del-1"})
	if len(r.Errors) > 0 {
		t.Fatalf("delete errors: %v", r.Raw)
	}
	var dl struct {
		DeletePhoto struct {
			DeletedPhotoID string           `json:"deletedPhotoId"`
			Problems       []map[string]any `json:"problems"`
		} `json:"deletePhoto"`
	}
	if err := remarshal(r.Data, &dl); err != nil {
		t.Fatal(err)
	}
	if len(dl.DeletePhoto.Problems) > 0 || dl.DeletePhoto.DeletedPhotoID != photoID {
		t.Fatalf("delete failed: %+v", dl)
	}

	// hidden from the default timeline
	page := journalQuery(t, m, map[string]any{"plantId": plantID})
	if len(page.Journal.Items) != 0 {
		t.Fatalf("deleted photo entry must be hidden, got %d", len(page.Journal.Items))
	}

	// visible as a tombstone, still attributable
	page = journalQuery(t, m, map[string]any{
		"plantId": plantID, "filter": map[string]any{"includeTombstoned": true},
	})
	if len(page.Journal.Items) != 1 {
		t.Fatalf("want 1 tombstoned entry, got %d", len(page.Journal.Items))
	}
	e := photoEntry(t, page.Journal.Items, 0)
	if e["tombstonedAt"] == nil {
		t.Fatal("tombstonedAt must be set")
	}
	if entryPhoto(t, e)["deletedAt"] == nil {
		t.Fatal("tombstoned entry must still reference the soft-deleted photo")
	}

	// a concurrent update against the pre-delete revision finds nothing
	_, probs := runMut(t, m, "updatePhoto", "UpdatePhotoInput", map[string]any{
		"id": photoID, "expectedRevision": 1, "caption": "ghost",
	}, nil, `photo { id } problems { code message }`)
	if len(probs) != 1 || probs[0]["code"] != "NOT_FOUND" {
		t.Fatalf("update after delete: %v", probs)
	}
}

// ---- ordering, pagination, cursor binding ------------------------------------

func TestJournalPaginatedSeries(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)
	dates := []string{"2026-01-10T08:00:00Z", "2026-03-02T08:00:00Z", "2026-02-14T08:00:00Z"}
	for _, d := range dates {
		attachPhotoMut(t, m, plantID, readyPhoto(t, m), map[string]any{
			"dateSource": "USER", "takenAt": d, "viewpoint": "OTHER",
		})
	}

	// full page: DESC by occurredAt
	page := journalQuery(t, m, map[string]any{"plantId": plantID})
	wantOrder := []string{"2026-03-02T08:00:00Z", "2026-02-14T08:00:00Z", "2026-01-10T08:00:00Z"}
	if len(page.Journal.Items) != 3 {
		t.Fatalf("want 3 entries, got %d", len(page.Journal.Items))
	}
	for i, w := range wantOrder {
		if got := photoEntry(t, page.Journal.Items, i)["occurredAt"]; got != w {
			t.Fatalf("entry %d occurredAt = %v, want %v", i, got, w)
		}
	}

	// keyset paging: 3 pages of 1, no repeats, no gaps
	var seen []string
	var after *string
	for pageN := 0; pageN < 4; pageN++ {
		p := journalQuery(t, m, map[string]any{"plantId": plantID, "first": 1, "after": after})
		if len(p.Journal.Items) != 1 {
			t.Fatalf("page %d: want 1 item, got %d", pageN, len(p.Journal.Items))
		}
		seen = append(seen, photoEntry(t, p.Journal.Items, 0)["occurredAt"].(string))
		if !p.Journal.PageInfo.HasNextPage {
			break
		}
		after = p.Journal.PageInfo.EndCursor
		if after == nil {
			t.Fatal("hasNextPage without endCursor")
		}
	}
	if fmt.Sprint(seen) != fmt.Sprint(wantOrder) {
		t.Fatalf("paged series = %v, want %v", seen, wantOrder)
	}

	// cursor bound to the filter signature: plant B rejects plant A's cursor
	plantB := mustPlantGQL(t, m)
	r := m.gql(journalPhotoSel, map[string]any{
		"plantId": plantB, "first": 1, "after": *page.Journal.PageInfo.EndCursor,
	})
	if len(r.Errors) == 0 || r.Errors[0].Extensions["code"] != "VALIDATION" {
		t.Fatalf("cross-filter cursor: %v", r.Raw)
	}
	// malformed cursors are rejected, not ignored
	r = m.gql(journalPhotoSel, map[string]any{"plantId": plantID, "after": "nonsense"})
	if len(r.Errors) == 0 || r.Errors[0].Extensions["code"] != "VALIDATION" {
		t.Fatalf("malformed cursor: %v", r.Raw)
	}
}

// entryTypes/from/to filters and the workspace-wide series.
func TestJournalFiltersAndWorkspaceWide(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantA := mustPlantGQL(t, m)
	plantB := mustPlantGQL(t, m)
	attachPhotoMut(t, m, plantA, readyPhoto(t, m), map[string]any{
		"dateSource": "USER", "takenAt": "2026-04-01T00:00:00Z", "viewpoint": "WHOLE",
	})
	attachPhotoMut(t, m, plantB, readyPhoto(t, m), map[string]any{
		"dateSource": "USER", "takenAt": "2026-04-15T00:00:00Z", "viewpoint": "WHOLE",
	})

	// types not yet projected (T17/T18) honestly yield nothing
	page := journalQuery(t, m, map[string]any{
		"filter": map[string]any{"entryTypes": []string{"ACTIVITY", "PURCHASE"}},
	})
	if len(page.Journal.Items) != 0 {
		t.Fatalf("unprojected types must be empty, got %d", len(page.Journal.Items))
	}

	// workspace-wide: both plants interleaved DESC
	page = journalQuery(t, m, map[string]any{
		"filter": map[string]any{"entryTypes": []string{"PHOTO"}},
	})
	if len(page.Journal.Items) != 2 {
		t.Fatalf("workspace-wide want 2, got %d", len(page.Journal.Items))
	}
	if page.Journal.Items[0]["plantId"] == page.Journal.Items[1]["plantId"] {
		t.Fatal("expected entries from both plants")
	}
	if page.Journal.Items[0]["occurredAt"].(string) < page.Journal.Items[1]["occurredAt"].(string) {
		t.Fatal("workspace-wide series must be occurredAt DESC")
	}

	// from/to window
	page = journalQuery(t, m, map[string]any{
		"filter": map[string]any{
			"entryTypes": []string{"PHOTO"},
			"from":       "2026-04-10T00:00:00Z",
			"to":         "2026-04-20T00:00:00Z",
		},
	})
	if len(page.Journal.Items) != 1 ||
		page.Journal.Items[0]["occurredAt"] != "2026-04-15T00:00:00Z" {
		t.Fatalf("window filter: %v", page.Journal.Items)
	}

	// cross-workspace plantId behaves as empty, never another workspace's data
	page = journalQuery(t, m, map[string]any{"plantId": "00000000-0000-0000-0000-000000000000"})
	if len(page.Journal.Items) != 0 {
		t.Fatal("unknown plantId must yield an empty page")
	}
}

// The e2e-shaped check: a photo attached with a USER date shows up in the
// plant's chronological series with variants ready for delivery.
func TestJournalSeriesCarriesDeliverableVariants(t *testing.T) {
	m := newMediaEnv(t, 10<<20)
	plantID := mustPlantGQL(t, m)
	assetID := readyPhoto(t, m)
	attachPhotoMut(t, m, plantID, assetID, map[string]any{
		"dateSource": "USER", "takenAt": time.Now().UTC().Format(time.RFC3339), "viewpoint": "WHOLE", "isCover": true,
	})
	page := journalQuery(t, m, map[string]any{"plantId": plantID})
	e := photoEntry(t, page.Journal.Items, 0)
	p := entryPhoto(t, e)
	if p["isCover"] != true {
		t.Fatal("cover flag must survive into the series")
	}
	asset := p["asset"].(map[string]any)
	if asset["id"] != assetID || asset["state"] != "READY" {
		t.Fatalf("asset: %v", asset)
	}
}
