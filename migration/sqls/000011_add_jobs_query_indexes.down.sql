-- Restore original job indexes, drop the replacement
DROP INDEX `job_status_created_id` ON `job`;
CREATE INDEX `force_timeout_job` ON `job` (`status`, `statusChangedAt`, `id`, `createdAt`);
CREATE INDEX `order_job_createdAt_id` ON `job` (`createdAt`, `id`);

-- Restore original message index, drop the replacement
DROP INDEX `message_status_created_id` ON `message`;
CREATE INDEX `order_msg_createdAt_id` ON `message` (`createdAt`, `id`);
