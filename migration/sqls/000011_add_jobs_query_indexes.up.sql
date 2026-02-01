-- Create optimized indexes for job and message queries
-- These indexes support ORDER BY createdAt DESC, id DESC with early termination via LIMIT

-- Replace job indexes: force_timeout_job (status, statusChangedAt, id, createdAt)
--                      order_job_createdAt_id (createdAt, id)
-- With: job_status_created_id (status, createdAt DESC, id DESC, statusChangedAt)
CREATE INDEX `job_status_created_id` ON `job` (`status`, `createdAt` DESC, `id` DESC, `statusChangedAt`);

{{if eq .Dialect "mysql"}}
-- MySQL requires ON table_name clause (non-standard SQL)
DROP INDEX `force_timeout_job` ON `job`;
DROP INDEX `order_job_createdAt_id` ON `job`;
{{else}}
-- Standard SQL syntax
DROP INDEX `force_timeout_job`;
DROP INDEX `order_job_createdAt_id`;
{{end}}

-- Replace message index: order_msg_createdAt_id (createdAt, id)
-- With: message_status_created_id (status, createdAt DESC, id DESC)
CREATE INDEX `message_status_created_id` ON `message` (`status`, `createdAt` DESC, `id` DESC);

{{if eq .Dialect "mysql"}}
-- MySQL requires ON table_name clause (non-standard SQL)
DROP INDEX `order_msg_createdAt_id` ON `message`;
{{else}}
-- Standard SQL syntax
DROP INDEX `order_msg_createdAt_id`;
{{end}}
