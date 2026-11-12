-- name: AIConnections :many
SELECT * FROM ai_connections WHERE workspace_id=$1 ORDER BY name,id;
-- name: AIConnection :one
SELECT * FROM ai_connections WHERE workspace_id=$1 AND id=$2;
-- name: SaveAIConnection :one
INSERT INTO ai_connections(id,workspace_id,name,protocol,endpoint,model,deployment_local,supports_vision,external_consent,credential_ciphertext,reasoning_effort,token_limit_field)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT(id) DO UPDATE SET name=excluded.name,protocol=excluded.protocol,endpoint=excluded.endpoint,model=excluded.model,deployment_local=excluded.deployment_local,supports_vision=excluded.supports_vision,external_consent=excluded.external_consent,credential_ciphertext=excluded.credential_ciphertext,reasoning_effort=excluded.reasoning_effort,token_limit_field=excluded.token_limit_field,revision=ai_connections.revision+1,last_tested_at=NULL,last_test_succeeded=NULL,last_test_message=NULL,updated_at=now()
WHERE ai_connections.workspace_id=excluded.workspace_id RETURNING *;
-- name: AIFeatureBindings :many
SELECT * FROM ai_feature_bindings WHERE workspace_id=$1 ORDER BY feature;
-- name: AIFeatureBinding :one
SELECT * FROM ai_feature_bindings WHERE workspace_id=$1 AND feature=$2;
-- name: SaveAIFeatureBinding :exec
INSERT INTO ai_feature_bindings(workspace_id,feature,mode,connection_id) VALUES($1,$2,$3,$4)
ON CONFLICT(workspace_id,feature) DO UPDATE SET mode=excluded.mode,connection_id=excluded.connection_id,revision=ai_feature_bindings.revision+1;
-- name: DisableAIConnectionBindings :exec
UPDATE ai_feature_bindings SET mode='DISABLED',connection_id=NULL,revision=revision+1 WHERE workspace_id=$1 AND connection_id=$2;
-- name: DeleteAIConnection :exec
DELETE FROM ai_connections WHERE workspace_id=$1 AND id=$2;
-- name: RecordAIConnectionTest :exec
UPDATE ai_connections SET last_tested_at=now(),last_test_succeeded=$4,last_test_message=$5 WHERE workspace_id=$1 AND id=$2 AND revision=$3;
-- name: ExportAIConnectionMetadata :many
SELECT (to_jsonb(t)-'credential_ciphertext')::jsonb AS row FROM ai_connections t WHERE workspace_id=$1 ORDER BY id;
-- name: ExportAIFeatureBindings :many
SELECT to_jsonb(t) AS row FROM ai_feature_bindings t WHERE workspace_id=$1 ORDER BY feature;
