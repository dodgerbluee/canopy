-- +goose Up
CREATE TABLE agent_operations (
 grant_id uuid NOT NULL REFERENCES agent_grants(id) ON DELETE CASCADE,
 workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 operation_id text NOT NULL CHECK(length(operation_id) BETWEEN 1 AND 160),
 tool text NOT NULL, input jsonb NOT NULL, input_hash text NOT NULL,
 state text NOT NULL CHECK(state IN ('EXECUTING','APPLIED')),
 resource jsonb NOT NULL DEFAULT '{}', response jsonb,
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(grant_id,operation_id)
);
CREATE TABLE agent_actions (
 id uuid PRIMARY KEY, grant_id uuid NOT NULL REFERENCES agent_grants(id),workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 credential_id uuid NOT NULL, operation_id text NOT NULL, tool text NOT NULL,input jsonb NOT NULL,input_hash text NOT NULL,
 before_snapshot jsonb NOT NULL DEFAULT '{}',
 state text NOT NULL DEFAULT 'PENDING' CHECK(state IN ('PENDING','APPLIED','REJECTED','EXPIRED','REVOKED')),
 created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL DEFAULT(now()+interval '24 hours'),
 applied_at timestamptz, UNIQUE(grant_id,operation_id)
);
ALTER TABLE audit_revisions ADD COLUMN agent_grant_id uuid, ADD COLUMN agent_credential_id uuid, ADD COLUMN agent_tool text, ADD COLUMN agent_operation_id text;
-- +goose StatementBegin
CREATE FUNCTION agent_audit_origin() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE origin jsonb;
BEGIN
 origin:=NULLIF(current_setting('canopy.agent_origin',true),'')::jsonb;
 IF origin IS NOT NULL THEN
  NEW.agent_grant_id:=(origin->>'GrantID')::uuid;
  NEW.agent_credential_id:=(origin->>'CredentialID')::uuid;
  NEW.agent_tool:=origin->>'Tool';NEW.agent_operation_id:=origin->>'OperationID';NEW.request_id:=origin->>'RequestID';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER audit_agent_origin BEFORE INSERT ON audit_revisions FOR EACH ROW EXECUTE FUNCTION agent_audit_origin();
-- +goose Down
DROP TRIGGER audit_agent_origin ON audit_revisions;
DROP FUNCTION agent_audit_origin();
ALTER TABLE audit_revisions DROP COLUMN agent_grant_id,DROP COLUMN agent_credential_id,DROP COLUMN agent_tool,DROP COLUMN agent_operation_id;
DROP TABLE agent_actions;
DROP TABLE agent_operations;
