DELETE FROM background_jobs AS job
USING export_jobs AS export
WHERE export.format = 'gedzip'
  AND job.kind IN ('export.generate', 'export.delete')
  AND job.payload ->> 'export_id' = export.id::text;

DELETE FROM export_jobs WHERE format = 'gedzip';

ALTER TABLE export_jobs
    DROP CONSTRAINT export_jobs_format_check,
    DROP CONSTRAINT export_jobs_completed_result_check;

ALTER TABLE export_jobs
    ADD CONSTRAINT export_jobs_format_check
        CHECK (format IN ('json_backup', 'zip_backup', 'pdf', 'png', 'svg', 'gedcom')),
    ADD CONSTRAINT export_jobs_completed_result_check
        CHECK (
            status <> 'completed' OR (
                progress = 100 AND
                result_object_key <> '' AND
                (
                    (format = 'json_backup' AND result_mime_type = 'application/json') OR
                    (format = 'zip_backup' AND result_mime_type = 'application/zip') OR
                    (format = 'pdf' AND result_mime_type = 'application/pdf') OR
                    (format = 'png' AND result_mime_type = 'image/png') OR
                    (format = 'svg' AND result_mime_type = 'image/svg+xml') OR
                    (format = 'gedcom' AND result_mime_type = 'text/vnd.familysearch.gedcom')
                ) AND
                result_size_bytes > 0 AND
                result_checksum_sha256 <> '' AND
                finished_at IS NOT NULL AND
                expires_at IS NOT NULL
            )
        );
