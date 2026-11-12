package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type toolCaller interface {
	CallTool(context.Context, *sdk.CallToolParams) (*sdk.CallToolResult, error)
}

func callData(ctx context.Context, caller toolCaller, name string, args map[string]any) (map[string]any, error) {
	result, err := caller.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil || result == nil {
		return nil, errors.New("Canopy tool request failed; retry with the same operation identity")
	}
	if result.IsError {
		return nil, fmt.Errorf("Canopy rejected %s; inspect its permissions and tool result in your agent", name)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return nil, errors.New("invalid Canopy tool response")
	}
	var body struct {
		Status string         `json:"status"`
		Data   map[string]any `json:"data"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Data == nil {
		return nil, errors.New("Canopy returned no usable tool data")
	}
	return body.Data, nil
}
func uploadPhoto(ctx context.Context, caller toolCaller, client *http.Client, endpoint, path, plant, operation, timezone string) (map[string]any, error) {
	if len(operation) > 140 {
		return nil, errors.New("operation identity must contain at most 140 bytes")
	}
	if _, err := uuid.Parse(plant); err != nil {
		return nil, errors.New("plant must be a UUID")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("could not open the selected local photo")
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() < 1 || stat.Size() > 25<<20 {
		return nil, errors.New("select a regular photo file between 1 byte and 25 MiB")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, io.LimitReader(file, (25<<20)+1)); err != nil {
		return nil, errors.New("could not checksum selected photo")
	}
	expected := hex.EncodeToString(hash.Sum(nil))
	if _, err = file.Seek(0, 0); err != nil {
		return nil, errors.New("could not read selected photo")
	}
	reserved, err := callData(ctx, caller, "upload_prepare", map[string]any{"operation_id": operation + ":prepare", "input": map[string]any{"purpose": "PHOTO", "filename": filepath.Base(path), "claimed_bytes": stat.Size()}})
	if err != nil {
		return nil, err
	}
	asset, _ := reserved["asset"].(map[string]any)
	id, _ := asset["id"].(string)
	if _, err = uuid.Parse(id); err != nil {
		return nil, errors.New("Canopy returned an invalid asset identity")
	}
	base, _ := url.Parse(endpoint)
	base.Path = "/agent/uploads/" + id
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, base.String(), file)
	if err != nil {
		return nil, errors.New("could not prepare upload")
	}
	request.ContentLength = stat.Size()
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("photo transfer failed; retry with the same operation identity")
	}
	var receipt struct {
		SHA256 string `json:"sha256"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&receipt)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || decodeErr != nil || receipt.SHA256 != expected {
		return nil, errors.New("photo transfer was rejected or its checksum differed; original bytes were not attached")
	}
	if _, err = callData(ctx, caller, "upload_finalize", map[string]any{"operation_id": operation + ":finalize", "input": map[string]any{"asset_id": id, "sha256": expected}}); err != nil {
		return nil, err
	}
	wait, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for {
		status, err := callData(wait, caller, "upload_status", map[string]any{"asset_id": id})
		if err != nil {
			return nil, err
		}
		if status["state"] == "READY" {
			break
		}
		if status["state"] == "FAILED" {
			return nil, errors.New("photo processing failed; inspect the upload in Canopy")
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-wait.Done():
			timer.Stop()
			return nil, errors.New("photo is still processing; retry with the same operation identity")
		case <-timer.C:
		}
	}
	return callData(ctx, caller, "photo_attach", map[string]any{"operation_id": operation + ":attach", "input": map[string]any{"plant_id": plant, "asset_id": id, "timezone": timezone}})
}
