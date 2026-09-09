-- Add covering index backing the anti-join used to find fully-delivered messages during prune:
--   ... WHERE NOT EXISTS (SELECT 1 FROM job j WHERE j.messageId = m.id AND j.status <> ? LIMIT 1)
-- The existing `jobs_by_message` index is (messageId, id, createdAt) and does not carry `status`,
-- so evaluating the predicate requires a clustered-index row lookup for every candidate message.
-- Prune must scan past every message blocked by a non-delivered job to fill each page, so that
-- lookup is paid once per blocked message per page. Including `status` keeps the probe
-- index-only.
--
-- Idempotent so it is a no-op at app startup when the index has already been built out-of-band
-- (gh-ost / pt-online-schema-change / ALGORITHM=INPLACE, LOCK=NONE). Pre-building is strongly
-- preferred on a large `job` table; see docs/runbooks/drain-queued-backlog.md for the pattern.

{{if eq .Dialect "mysql"}}
-- Create only if missing; build online (non-blocking) when it must run in-band.
SET @idx_exists := (
    SELECT COUNT(1) FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'job'
      AND index_name = 'job_message_status'
);
SET @ddl := IF(@idx_exists = 0,
    'CREATE INDEX `job_message_status` ON `job` (`messageId`, `status`) ALGORITHM=INPLACE LOCK=NONE',
    'DO 0');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
{{else}}
CREATE INDEX IF NOT EXISTS `job_message_status` ON `job` (`messageId`, `status`);
{{end}}

-- Generated with assistance from Claude AI
