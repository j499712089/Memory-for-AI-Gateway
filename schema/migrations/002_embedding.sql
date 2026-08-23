ALTER TABLE assets ADD COLUMN embedding BLOB;
ALTER TABLE assets ADD COLUMN embedding_model_version TEXT;
CREATE INDEX IF NOT EXISTS idx_assets_embedding ON assets(id) WHERE embedding IS NOT NULL;
