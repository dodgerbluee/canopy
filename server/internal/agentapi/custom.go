package agentapi

import (
	"bytes"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/agentaccess"
	"canopy/server/internal/agentapi/catalog"
	httpx "canopy/server/internal/transport/http"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"time"
)

func (a *API) custom(ctx context.Context, p agentaccess.Principal, t catalog.Tool, args map[string]any) (any, []byte, string, error) {
	principal := httpx.Principal{WorkspaceID: agentaccess.ID(p.Grant.WorkspaceID), OwnerID: agentaccess.ID(p.OwnerID)}
	switch t.Root {
	case "@overview":
		value := map[string]any{"asOf": time.Now().UTC().Format(time.RFC3339), "guidanceIsNotScheduledCare": true, "unloggedWorkIsUnknown": true}
		queries := map[string]string{"workspace": "query {workspace{id displayName timezone defaultCurrency}}", "plants": "query {plants(first:25){items{__typename id name category status} pageInfo{hasNextPage endCursor}}}"}
		if p.Grant.Has("care:read") {
			queries["careAgenda"] = "query {careAgenda(first:25){items{id plantId currentDueDate status} pageInfo{hasNextPage endCursor}}}"
		}
		for key, doc := range queries {
			out, err := a.Engine.Execute(ctx, principal, doc, nil)
			if err != nil {
				return nil, nil, "", err
			}
			value[key] = out[key]
		}
		return value, nil, "", nil
	case "@photos":
		first := args["first"]
		if first == nil {
			first = 25
		}
		doc := "query($plantId:ID!,$first:Int,$after:String){journal(plantId:$plantId,first:$first,after:$after,filter:{entryTypes:[PHOTO]}){items{... on PhotoEntry{id plantId photo{id takenAt dateSource timezone caption viewpoint isCover revision asset{id state mimeType variants{name widthPx heightPx}}}}}pageInfo{endCursor hasNextPage}}}"
		out, err := a.Engine.Execute(ctx, principal, doc, map[string]any{"plantId": args["plant_id"], "first": first, "after": args["after"]})
		return out["journal"], nil, "", err
	case "@photo":
		row, err := a.Access.Q.GetPhoto(ctx, dbgen.GetPhotoParams{ID: agentaccess.ID(str(args["photo_id"])), WorkspaceID: agentaccess.ID(p.Grant.WorkspaceID)})
		if err != nil || !p.Grant.Plant(agentaccess.String(row.PlantID)) {
			return nil, nil, "", agentaccess.ErrDenied
		}
		value := map[string]any{"id": agentaccess.String(row.ID), "plantId": agentaccess.String(row.PlantID), "assetId": agentaccess.String(row.AssetID), "dateSource": row.DateSource, "takenAt": nil, "revision": row.Revision}
		if row.TakenAt.Valid {
			value["takenAt"] = row.TakenAt.Time.UTC().Format(time.RFC3339Nano)
		}
		if row.Caption.Valid {
			value["caption"] = row.Caption.String
		}
		if row.Timezone.Valid {
			value["timezone"] = row.Timezone.String
		}
		if args["include_image"] == false {
			return value, nil, "", nil
		}
		var storage, mime string
		err = a.Access.Pool.QueryRow(ctx, `SELECT v.storage_key,CASE WHEN v.name LIKE '%webp%' THEN 'image/webp' ELSE 'image/jpeg' END FROM asset_variants v JOIN assets a ON a.id=v.asset_id WHERE a.id=$1 AND a.workspace_id=$2 AND a.state='READY' AND v.name='thumb'`, row.AssetID, p.Grant.WorkspaceID).Scan(&storage, &mime)
		if err != nil {
			return value, nil, "", errors.New("Photo preview is not ready.")
		}
		bytes, err := a.readBytes(storage)
		return value, bytes, mime, err
	case "@upload":
		row, err := a.Access.Q.GetAsset(ctx, dbgen.GetAssetParams{WorkspaceID: agentaccess.ID(p.Grant.WorkspaceID), ID: agentaccess.ID(str(args["asset_id"]))})
		if err != nil {
			return nil, nil, "", agentaccess.ErrDenied
		}
		value := map[string]any{"id": agentaccess.String(row.ID), "state": row.State, "purpose": row.Purpose, "byteCount": row.ByteCount, "sha256": row.Sha256}
		if row.ErrorCode.Valid {
			value["errorCode"] = row.ErrorCode.String
		}
		return value, nil, "", nil
	case "@receipt":
		if !p.Grant.Has("purchases:read") || len(p.Grant.PlantIDs) > 0 {
			return nil, nil, "", agentaccess.ErrDenied
		}
		var asset, key, mime string
		err := a.Access.Pool.QueryRow(ctx, `SELECT a.id,a.storage_key,a.mime_type FROM purchase_receipts r JOIN assets a ON a.id=r.asset_id WHERE r.id=$1 AND r.workspace_id=$2 AND a.state='READY'`, str(args["receipt_id"]), p.Grant.WorkspaceID).Scan(&asset, &key, &mime)
		if err != nil {
			return nil, nil, "", agentaccess.ErrDenied
		}
		if mime == "application/pdf" {
			return map[string]any{"assetId": asset, "mimeType": mime, "message": "PDF receipt: use receipt extraction or the owner download. Inline previews are image-only."}, nil, "", nil
		}
		original, err := a.readBytes(key)
		if err != nil {
			return nil, nil, "", err
		}
		preview, err := receiptPreview(original)
		return map[string]any{"assetId": asset, "mimeType": "image/jpeg", "previewOnly": true}, preview, "image/jpeg", err
	case "@operation":
		var tool, state string
		var resource, response []byte
		err := a.Access.Pool.QueryRow(ctx, `SELECT tool,state,resource,response FROM agent_operations WHERE grant_id=$1 AND operation_id=$2`, p.Grant.ID, str(args["operation_id"])).Scan(&tool, &state, &resource, &response)
		if err != nil {
			return nil, nil, "", agentaccess.ErrDenied
		}
		var refs any
		_ = json.Unmarshal(resource, &refs)
		value := map[string]any{"tool": tool, "state": state, "resource": refs}
		return value, nil, "", nil
	case "@proposals", "@proposal":
		rows, err := a.Access.Actions(ctx, agentaccess.ID(p.Grant.WorkspaceID), p.Grant.ID)
		if err != nil {
			return nil, nil, "", err
		}
		permitted := rows[:0]
		for _, row := range rows {
			d, known := a.definition(row.Tool)
			var requested map[string]any
			if known && Allowed(p, d.Tool) && json.Unmarshal(row.Input, &requested) == nil && a.authorize(ctx, p, d.Tool, requested) == nil {
				permitted = append(permitted, row)
			}
		}
		rows = permitted
		if t.Root == "@proposal" {
			for _, row := range rows {
				if row.ID == str(args["id"]) {
					return row, nil, "", nil
				}
			}
			return nil, nil, "", agentaccess.ErrDenied
		}
		return map[string]any{"items": rows, "limit": 100}, nil, "", nil
	}
	return nil, nil, "", errors.New("Tool is not available.")
}
func (a *API) readBytes(key string) ([]byte, error) {
	if a.Store == nil {
		return nil, errors.New("Media delivery is unavailable.")
	}
	r, err := a.Store.Open(key)
	if err != nil {
		return nil, errors.New("Preview bytes are unavailable.")
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return nil, errors.New("Preview exceeds the 1 MiB image limit. Use the owner app for the original.")
	}
	return b, nil
}

// Decode and re-encode receipt previews to remove EXIF/GPS and embedded metadata.
func receiptPreview(original []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(original))
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 24_000_000 {
		return nil, errors.New("Receipt preview dimensions are unsupported; use receipt extraction.")
	}
	decoded, _, err := image.Decode(bytes.NewReader(original))
	if err != nil {
		return nil, errors.New("Receipt preview could not be decoded; use receipt extraction.")
	}
	var output bytes.Buffer
	if err = jpeg.Encode(&output, decoded, &jpeg.Options{Quality: 85}); err != nil || output.Len() > 1<<20 {
		return nil, errors.New("Receipt preview exceeds the 1 MiB limit; use receipt extraction.")
	}
	return output.Bytes(), nil
}
