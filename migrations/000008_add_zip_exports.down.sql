DELETE FROM background_jobs AS job
USING export_jobs AS export
WHERE export.format = 'zip_backup'
  AND job.kind IN ('export.generate', 'export.delete')
  AND job.payload ->> 'export_id' = export.id::text;

DELETE FROM export_jobs WHERE format = 'zip_backup';

ALTER TABLE export_jobs
    DROP CONSTRAINT export_jobs_format_check,
    DROP CONSTRAINT export_jobs_completed_result_check;

ALTER TABLE export_jobs
    ADD CONSTRAINT export_jobs_format_check
        CHECK (format = 'json_backup'),
    ADD CONSTRAINT export_jobs_completed_result_check
        CHECK (
            status <> 'completed' OR (
                progress = 100 AND
                result_object_key <> '' AND
                result_mime_type = 'application/json' AND
                result_size_bytes > 0 AND
                result_checksum_sha256 <> '' AND
                finished_at IS NOT NULL AND
                expires_at IS NOT NULL
            )
        );
