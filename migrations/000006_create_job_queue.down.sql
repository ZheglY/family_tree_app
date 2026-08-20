ALTER TABLE media_assets
    DROP COLUMN processed_at,
    DROP COLUMN processing_error;

DROP TABLE media_variants;
DROP TABLE background_jobs;
