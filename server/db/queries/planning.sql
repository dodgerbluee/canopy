-- name: GetPlantingPlan :one
SELECT * FROM planting_plans WHERE workspace_id=$1 AND id=$2;

-- name: LockPlantingPlan :one
SELECT * FROM planting_plans WHERE workspace_id=$1 AND id=$2 FOR UPDATE;

-- name: InsertPlantingPlan :one
INSERT INTO planting_plans(id,workspace_id,name,scientific_name,cultivar,category,desired_quantity,method,
 intended_location_id,intended_placement,target_precision,target_from,target_to,target_from_month,target_to_month,
 target_year,target_season,budget_minor,budget_currency,notes,state)
VALUES(sqlc.arg(id),sqlc.arg(workspace_id),sqlc.arg(name),sqlc.narg(scientific_name),sqlc.narg(cultivar),sqlc.arg(category),
 sqlc.narg(desired_quantity),sqlc.narg(method),sqlc.narg(intended_location_id),sqlc.narg(intended_placement),
 sqlc.narg(target_precision),sqlc.narg(target_from),sqlc.narg(target_to),sqlc.narg(target_from_month),sqlc.narg(target_to_month),
 sqlc.narg(target_year),sqlc.narg(target_season),sqlc.narg(budget_minor),sqlc.narg(budget_currency),sqlc.narg(notes),sqlc.arg(state))
RETURNING *;

-- name: ReplacePlantingIntent :one
UPDATE planting_plans SET name=sqlc.arg(name),scientific_name=sqlc.narg(scientific_name),cultivar=sqlc.narg(cultivar),
 category=sqlc.arg(category),desired_quantity=sqlc.narg(desired_quantity),method=sqlc.narg(method),
 intended_location_id=sqlc.narg(intended_location_id),intended_placement=sqlc.narg(intended_placement),
 target_precision=sqlc.narg(target_precision),target_from=sqlc.narg(target_from),target_to=sqlc.narg(target_to),
 target_from_month=sqlc.narg(target_from_month),target_to_month=sqlc.narg(target_to_month),
 target_year=sqlc.narg(target_year),target_season=sqlc.narg(target_season),budget_minor=sqlc.narg(budget_minor),
 budget_currency=sqlc.narg(budget_currency),notes=sqlc.narg(notes),revision=revision+1,
 guidance_generation=guidance_generation+CASE WHEN sqlc.arg(context_changed)::boolean THEN 1 ELSE 0 END,
 accepted_guidance_id=CASE WHEN sqlc.arg(period_changed)::boolean THEN NULL ELSE accepted_guidance_id END
WHERE workspace_id=sqlc.arg(workspace_id) AND id=sqlc.arg(id)
RETURNING *;

-- name: SetPlantingPlanState :one
UPDATE planting_plans SET state=sqlc.arg(state),resume_state=sqlc.narg(resume_state),
 cancelled_at=sqlc.narg(cancelled_at),completed_at=sqlc.narg(completed_at),revision=revision+1
WHERE workspace_id=sqlc.arg(workspace_id) AND id=sqlc.arg(id)
RETURNING *;

-- name: BumpPlantingPlan :one
UPDATE planting_plans SET revision=revision+1 WHERE workspace_id=$1 AND id=$2 RETURNING *;

-- name: ListPlantingPlansNameAsc :many
SELECT * FROM planting_plans p
WHERE p.workspace_id=sqlc.arg(workspace_id)
 AND p.state=ANY(sqlc.arg(states)::text[]::planting_plan_state[])
 AND (sqlc.narg(method)::planting_method IS NULL OR p.method=sqlc.narg(method))
 AND (sqlc.narg(location_id)::uuid IS NULL OR p.intended_location_id=sqlc.narg(location_id))
 AND (sqlc.arg(query)::text='' OR strpos(lower(p.name),lower(sqlc.arg(query)))>0
  OR strpos(lower(p.scientific_name),lower(sqlc.arg(query)))>0 OR strpos(lower(p.cultivar),lower(sqlc.arg(query)))>0)
 AND (sqlc.narg(after_name)::text IS NULL OR (lower(p.name) COLLATE "C",p.id)>(lower(sqlc.narg(after_name)) COLLATE "C",sqlc.narg(after_id)::uuid))
ORDER BY lower(p.name) COLLATE "C",p.id LIMIT sqlc.arg(limit_count);

-- name: ListPlantingPlansUpdatedDesc :many
SELECT * FROM planting_plans p
WHERE p.workspace_id=sqlc.arg(workspace_id)
 AND p.state=ANY(sqlc.arg(states)::text[]::planting_plan_state[])
 AND (sqlc.narg(method)::planting_method IS NULL OR p.method=sqlc.narg(method))
 AND (sqlc.narg(location_id)::uuid IS NULL OR p.intended_location_id=sqlc.narg(location_id))
 AND (sqlc.arg(query)::text='' OR strpos(lower(p.name),lower(sqlc.arg(query)))>0
  OR strpos(lower(p.scientific_name),lower(sqlc.arg(query)))>0 OR strpos(lower(p.cultivar),lower(sqlc.arg(query)))>0)
 AND (sqlc.narg(after_updated_at)::timestamptz IS NULL OR (p.updated_at,p.id)<(sqlc.narg(after_updated_at),sqlc.narg(after_id)::uuid))
ORDER BY p.updated_at DESC,p.id DESC LIMIT sqlc.arg(limit_count);

-- name: ListPlantingActions :many
SELECT * FROM planting_actions WHERE workspace_id=$1 AND plan_id=ANY(sqlc.arg(plan_ids)::uuid[])
ORDER BY scheduled_on,id;

-- name: GetPlantingAction :one
SELECT * FROM planting_actions WHERE workspace_id=$1 AND plan_id=$2 AND id=$3;

-- name: CountPlantingActions :one
SELECT count(*)::int FROM planting_actions WHERE workspace_id=$1 AND plan_id=$2;

-- name: CountOpenPlantingActions :one
SELECT count(*)::int FROM planting_actions WHERE workspace_id=$1 AND plan_id=$2 AND state='OPEN';

-- name: InsertPlantingAction :one
INSERT INTO planting_actions(id,workspace_id,plan_id,kind,title,scheduled_on,reminder_on,notes)
VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;

-- name: UpdatePlantingAction :one
UPDATE planting_actions SET kind=sqlc.arg(kind),title=sqlc.arg(title),scheduled_on=sqlc.arg(scheduled_on),
 reminder_on=sqlc.narg(reminder_on),notes=sqlc.narg(notes),
 schedule_revision=schedule_revision+CASE WHEN sqlc.arg(schedule_changed)::boolean THEN 1 ELSE 0 END
WHERE workspace_id=sqlc.arg(workspace_id) AND plan_id=sqlc.arg(plan_id) AND id=sqlc.arg(id) RETURNING *;

-- name: DeletePlantingAction :exec
DELETE FROM planting_actions WHERE workspace_id=$1 AND plan_id=$2 AND id=$3;

-- name: AdvancePlantingActionSchedules :exec
UPDATE planting_actions SET schedule_revision=schedule_revision+1
WHERE workspace_id=$1 AND plan_id=$2 AND state='OPEN';

-- name: ListCurrentPlantingGuidance :many
SELECT g.* FROM planting_guidance_snapshots g JOIN planting_plans p ON p.workspace_id=g.workspace_id AND p.id=g.plan_id
WHERE g.workspace_id=$1 AND g.plan_id=ANY(sqlc.arg(plan_ids)::uuid[])
 AND (g.generation=p.guidance_generation OR g.id=p.accepted_guidance_id);

-- name: ListPlantingFulfillments :many
SELECT * FROM planting_fulfillments WHERE workspace_id=$1 AND plan_id=ANY(sqlc.arg(plan_ids)::uuid[]);

-- name: InsertPlanningMutationReceipt :exec
INSERT INTO mutation_receipts(id,workspace_id,operation,idempotency_key,request_hash,result,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT(workspace_id,operation,idempotency_key) DO UPDATE SET
 request_hash=excluded.request_hash,result=excluded.result,gone=false,expires_at=excluded.expires_at
WHERE mutation_receipts.expires_at<=now();

-- name: LockPlantingLocation :one
-- Hold the referenced key until the save commits, avoiding an FK race with purge.
SELECT id FROM locations WHERE workspace_id=$1 AND id=$2 FOR KEY SHARE;
