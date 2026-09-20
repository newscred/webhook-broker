-- Drop the prune anti-join covering index (idempotent).

{{if eq .Dialect "mysql"}}
SET @idx_exists := (
    SELECT COUNT(1) FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'job'
      AND index_name = 'job_message_status'
);
SET @ddl := IF(@idx_exists > 0,
    'DROP INDEX `job_message_status` ON `job`',
    'DO 0');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
{{else}}
DROP INDEX IF EXISTS `job_message_status`;
{{end}}

-- Generated with assistance from Claude AI
