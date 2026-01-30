-- Restore original job indexes, drop the replacement
DROP INDEX IF EXISTS `job_status_created_id` ON `job`;
CREATE INDEX IF NOT EXISTS `force_timeout_job` ON `job` (`status`, `statusChangedAt`, `id`, `createdAt`);
CREATE INDEX IF NOT EXISTS `order_job_createdAt_id` ON `job` (`createdAt`, `id`);

-- Restore original message index, drop the replacement
DROP INDEX IF EXISTS `message_status_created_id` ON `message`;
CREATE INDEX IF NOT EXISTS `order_msg_createdAt_id` ON `message` (`createdAt`, `id`);
