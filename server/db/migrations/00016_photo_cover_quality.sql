-- +goose Up
-- Derived scores of normalized thumbnails only; originals/capture metadata stay immutable.
ALTER TABLE assets
 ADD COLUMN cover_quality double precision CHECK (cover_quality BETWEEN 0 AND 100),
 ADD COLUMN cover_quality_version smallint DEFAULT 0 CHECK (cover_quality_version >= 0);
CREATE INDEX assets_cover_quality_pending_idx ON assets(created_at,id)
 WHERE purpose='PHOTO' AND state='READY' AND COALESCE(cover_quality_version,0)=0;
-- +goose Down
DROP INDEX assets_cover_quality_pending_idx;
ALTER TABLE assets DROP COLUMN cover_quality_version,DROP COLUMN cover_quality;
