-- +goose Up
-- A purged actual plant leaves its fulfillment marker, never a retained copy
-- in a planner agent response, review input or before snapshot.
CREATE INDEX agent_operations_planning_workspace_idx ON agent_operations(workspace_id)
 WHERE starts_with(tool,'planting_');
-- +goose StatementBegin
CREATE FUNCTION redact_planning_agent_plant() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 WITH affected AS (
  UPDATE agent_operations o SET input='{}'::jsonb,
   resource=jsonb_build_object('id',OLD.plan_id::text,'gone',true),
   response=jsonb_build_object('status','failed','operation_id',o.operation_id,
    'problems',jsonb_build_array(jsonb_build_object('code','RESOURCE_GONE','message','The linked plant was removed. This operation retains only its history.')),
    'as_of',now())
  WHERE o.workspace_id=OLD.workspace_id AND starts_with(o.tool,'planting_') AND (
   o.resource->>'plantId'=OLD.plant_id::text OR position(OLD.plant_id::text in o.response::text)>0
   OR (o.tool='planting_add_to_garden' AND o.resource->>'id'=OLD.plan_id::text))
  RETURNING o.grant_id,o.operation_id
 )
 UPDATE agent_actions a SET input='{}'::jsonb,before_snapshot='{}'::jsonb,
  state=CASE WHEN a.state='PENDING' THEN 'REVOKED' ELSE a.state END
 WHERE a.workspace_id=OLD.workspace_id AND starts_with(a.tool,'planting_') AND (
  (a.grant_id,a.operation_id) IN (SELECT grant_id,operation_id FROM affected)
  OR position(OLD.plant_id::text in a.input::text)>0 OR position(OLD.plant_id::text in a.before_snapshot::text)>0);
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER planning_agent_plant_purge AFTER UPDATE OF plant_id ON planting_fulfillments
 FOR EACH ROW WHEN(OLD.plant_id IS NOT NULL AND NEW.plant_id IS NULL)
 EXECUTE FUNCTION redact_planning_agent_plant();

-- +goose Down
DROP TRIGGER IF EXISTS planning_agent_plant_purge ON planting_fulfillments;
DROP FUNCTION IF EXISTS redact_planning_agent_plant();
DROP INDEX IF EXISTS agent_operations_planning_workspace_idx;
-- Redacted private content is deliberately never reconstructed by rollback.
