-- name: KnowledgeSettings :one
SELECT w.id AS workspace_id,COALESCE(s.country,'')::text AS country,COALESCE(s.region,'')::text AS region,COALESCE(s.place,w.location_label,'')::text AS place,
 w.latitude,w.longitude,w.timezone,s.hardiness_zone,s.zone_system,COALESCE(s.enabled,false)::boolean AS enabled,COALESCE(s.revision,0)::integer AS revision
FROM workspace w LEFT JOIN garden_knowledge_settings s ON s.workspace_id=w.id WHERE w.id=$1;

-- name: SaveKnowledgeSettings :one
INSERT INTO garden_knowledge_settings(workspace_id,country,region,place,hardiness_zone,zone_system,enabled)
VALUES($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT(workspace_id) DO UPDATE SET country=excluded.country,region=excluded.region,place=excluded.place,hardiness_zone=excluded.hardiness_zone,zone_system=excluded.zone_system,enabled=excluded.enabled,revision=garden_knowledge_settings.revision+1 RETURNING *;

-- name: SetKnowledgeGeography :exec
UPDATE workspace SET latitude=$2,longitude=$3,location_label=$4,timezone=$5,settings_revision=settings_revision+1 WHERE id=$1;

-- name: RefreshKnowledge :execrows
UPDATE plant_knowledge SET generation=generation+1,state='PENDING',profile_key=NULL,message='Waiting for regional guidance.',updated_at=now(),lease_until=NULL
WHERE workspace_id=sqlc.arg(workspace_id) AND (sqlc.narg(plant_id)::uuid IS NULL OR plant_id=sqlc.narg(plant_id));

-- name: KnowledgeGarden :many
SELECT p.id,p.name,p.cultivar,p.category,k.state,k.message,k.updated_at,
 COALESCE(profile.facts,'[]'::jsonb) AS facts,
 (SELECT ph.asset_id FROM plant_photos ph WHERE ph.workspace_id=p.workspace_id AND ph.plant_id=p.id AND ph.is_cover AND ph.deleted_at IS NULL LIMIT 1) AS photo_asset_id
FROM plants p JOIN plant_knowledge k ON k.plant_id=p.id AND k.workspace_id=p.workspace_id
LEFT JOIN knowledge_profiles profile ON profile.workspace_id=k.workspace_id AND profile.profile_key=k.profile_key
WHERE p.workspace_id=$1 AND p.status='ACTIVE' ORDER BY p.name,p.id;

-- name: PendingKnowledge :many
SELECT * FROM plant_knowledge WHERE state='PENDING' OR (state='PROCESSING' AND lease_until<now()) ORDER BY updated_at LIMIT 8;

-- name: ClaimKnowledge :one
UPDATE plant_knowledge SET state='PROCESSING',lease_until=now()+interval '20 minutes',updated_at=now(),message='Reading regional sources…'
WHERE plant_id=$1 AND workspace_id=$2 AND generation=$3 AND (state='PENDING' OR (state='PROCESSING' AND lease_until<now())) RETURNING *;

-- name: FinishKnowledge :execrows
UPDATE plant_knowledge SET state=$4,message=$5,profile_key=$6,lease_until=NULL,updated_at=now()
WHERE plant_id=$1 AND workspace_id=$2 AND generation=$3;

-- name: GetKnowledgeProfile :one
SELECT * FROM knowledge_profiles WHERE workspace_id=$1 AND profile_key=$2 AND updated_at>now()-interval '30 days';

-- name: PutKnowledgeProfile :exec
INSERT INTO knowledge_profiles(workspace_id,profile_key,facts) VALUES($1,$2,$3)
ON CONFLICT(workspace_id,profile_key) DO UPDATE SET facts=excluded.facts,updated_at=now();

-- name: GetKnowledgeDocument :one
SELECT * FROM knowledge_documents WHERE url=$1 AND expires_at>now();

-- name: PutKnowledgeDocument :exec
INSERT INTO knowledge_documents(url,provider,content_sha256,facts,expires_at) VALUES($1,$2,$3,$4,now()+interval '30 days')
ON CONFLICT(url) DO UPDATE SET provider=excluded.provider,content_sha256=excluded.content_sha256,facts=excluded.facts,fetched_at=now(),expires_at=excluded.expires_at;

-- name: ExportKnowledgeSettings :many
SELECT to_jsonb(t) AS row FROM garden_knowledge_settings t WHERE workspace_id=$1;
-- name: ExportKnowledgeProfiles :many
SELECT to_jsonb(t) AS row FROM knowledge_profiles t WHERE workspace_id=$1 ORDER BY profile_key;
-- name: ExportPlantKnowledge :many
SELECT to_jsonb(t) AS row FROM plant_knowledge t WHERE workspace_id=$1 ORDER BY plant_id;

-- name: KnowledgeYearRecords :many
WITH events AS (
 SELECT a.plant_id,extract(month from a.occurred_at AT TIME ZONE sqlc.arg(timezone)::text)::integer AS month,a.kind::text AS kind
 FROM activities a WHERE a.workspace_id=sqlc.arg(workspace_id) AND a.voided_at IS NULL AND extract(year from a.occurred_at AT TIME ZONE sqlc.arg(timezone)::text)=sqlc.arg(year)::integer
 UNION ALL
 SELECT p.plant_id,extract(month from p.taken_at AT TIME ZONE sqlc.arg(timezone)::text)::integer,'PHOTO'
 FROM plant_photos p WHERE p.workspace_id=sqlc.arg(workspace_id) AND p.deleted_at IS NULL AND p.taken_at IS NOT NULL AND p.date_source<>'UPLOAD_FALLBACK' AND extract(year from p.taken_at AT TIME ZONE sqlc.arg(timezone)::text)=sqlc.arg(year)::integer
)
SELECT plant_id,month,count(*) FILTER(WHERE kind='WATER') AS waterings,count(*) FILTER(WHERE kind='PHOTO') AS photos,count(*) FILTER(WHERE kind='HARVEST') AS harvests,count(*) FILTER(WHERE kind='FERTILIZE') AS fertilizations FROM events GROUP BY plant_id,month ORDER BY plant_id,month;

-- name: RequeueStaleKnowledge :execrows
UPDATE plant_knowledge k SET generation=k.generation+1,state='PENDING',profile_key=NULL,
 message='Validating complete source timing with the updated extraction process.',updated_at=now(),lease_until=NULL
FROM plants p JOIN garden_knowledge_settings s ON s.workspace_id=p.workspace_id
WHERE k.plant_id=p.id AND k.workspace_id=p.workspace_id AND p.status='ACTIVE' AND s.enabled AND k.state='READY'
 AND NOT EXISTS (
  SELECT 1 FROM knowledge_profiles profile WHERE profile.workspace_id=k.workspace_id AND profile.profile_key=k.profile_key
   AND jsonb_array_length(profile.facts)>0
   AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(profile.facts) fact WHERE COALESCE(fact->>'extractionVersion','')<>sqlc.arg(extraction_version)::text)
 );
