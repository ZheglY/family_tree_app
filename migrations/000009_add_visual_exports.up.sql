ALTER TABLE export_jobs
    DROP CONSTRAINT export_jobs_format_check,
    DROP CONSTRAINT export_jobs_completed_result_check;

ALTER TABLE export_jobs
    ADD CONSTRAINT export_jobs_format_check
        CHECK (format IN ('json_backup', 'zip_backup', 'pdf', 'png', 'svg')),
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
                    (format = 'svg' AND result_mime_type = 'image/svg+xml')
                ) AND
                result_size_bytes > 0 AND
                result_checksum_sha256 <> '' AND
                finished_at IS NOT NULL AND
                expires_at IS NOT NULL
            )
        );
