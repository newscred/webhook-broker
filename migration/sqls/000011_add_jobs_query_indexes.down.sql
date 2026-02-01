-- Restore original job indexes, drop the replacement

{{if eq .Dialect "mysql"}}
-- MySQL requires ON table_name clause (non-standard SQL)
DROP INDEX `job_status_created_id` ON `job`;
{{else}}
-- Standard SQL syntax
DROP INDEX `job_status_created_id`;
{{end}}

CREATE INDEX `force_timeout_job` ON `job` (`status`, `statusChangedAt`, `id`, `createdAt`);
CREATE INDEX `order_job_createdAt_id` ON `job` (`createdAt`, `id`);

-- Restore original message index, drop the replacement

{{if eq .Dialect "mysql"}}
-- MySQL requires ON table_name clause (non-standard SQL)
DROP INDEX `message_status_created_id` ON `message`;
{{else}}
-- Standard SQL syntax
DROP INDEX `message_status_created_id`;
{{end}}

CREATE INDEX `order_msg_createdAt_id` ON `message` (`createdAt`, `id`);
