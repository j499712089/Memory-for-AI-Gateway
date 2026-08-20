ALTER TABLE assets ADD COLUMN embedding BLOB;
CREATE INDEX IF NOT EXISTS idx_assets_embedding ON assets(id) WHERE embedding IS NOT NULL;
