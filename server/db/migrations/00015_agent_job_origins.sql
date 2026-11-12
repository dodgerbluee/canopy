-- +goose Up
ALTER TABLE analysis_runs ADD COLUMN agent_grant_id uuid,ADD COLUMN agent_credential_id uuid;
ALTER TABLE plant_knowledge ADD COLUMN agent_grant_id uuid,ADD COLUMN agent_credential_id uuid;
ALTER TABLE export_jobs ADD COLUMN agent_grant_id uuid,ADD COLUMN agent_credential_id uuid;
-- +goose StatementBegin
CREATE FUNCTION agent_job_origin() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE origin jsonb;
BEGIN
 origin:=NULLIF(current_setting('canopy.agent_origin',true),'')::jsonb;
 IF TG_OP='INSERT' OR (to_jsonb(NEW)->>'generation') IS DISTINCT FROM (to_jsonb(OLD)->>'generation') THEN
   NEW.agent_grant_id:=(origin->>'GrantID')::uuid;
   NEW.agent_credential_id:=(origin->>'CredentialID')::uuid;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER analysis_agent_origin BEFORE INSERT ON analysis_runs FOR EACH ROW EXECUTE FUNCTION agent_job_origin();
CREATE TRIGGER knowledge_agent_origin BEFORE INSERT OR UPDATE ON plant_knowledge FOR EACH ROW EXECUTE FUNCTION agent_job_origin();
CREATE TRIGGER export_agent_origin BEFORE INSERT ON export_jobs FOR EACH ROW EXECUTE FUNCTION agent_job_origin();
-- +goose Down
DROP TRIGGER export_agent_origin ON export_jobs;
DROP TRIGGER knowledge_agent_origin ON plant_knowledge;
DROP TRIGGER analysis_agent_origin ON analysis_runs;
DROP FUNCTION agent_job_origin();
ALTER TABLE export_jobs DROP COLUMN agent_grant_id,DROP COLUMN agent_credential_id;
ALTER TABLE plant_knowledge DROP COLUMN agent_grant_id,DROP COLUMN agent_credential_id;
ALTER TABLE analysis_runs DROP COLUMN agent_grant_id,DROP COLUMN agent_credential_id;
