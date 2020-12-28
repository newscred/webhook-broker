CREATE TABLE IF NOT EXISTS `lock` (
    `lockId` VARCHAR(255) NOT NULL PRIMARY KEY,
    `attainedAt` DATETIME NOT NULL
);

CREATE INDEX `lock_attained` ON `lock` (`attainedAt`);

CREATE INDEX `retry_job` on `job` (`status`, `earliestNextAttemptAt`, `id`, `createdAt`);

CREATE INDEX `force_timeout_job` on `job` (`status`, `statusChangedAt`, `id`, `createdAt`);

CREATE INDEX `jobs_by_message` on `job` (`messageId`, `id`, `createdAt`);

CREATE INDEX `jobs_by_consumer` on `job` (`consumerId`, `status`, `id`, `createdAt`);

CREATE INDEX `consumers_by_channel` on `consumer` (`channelId`, `id`, `createdAt`);

CREATE INDEX `messages_by_channel` on `message` (`channelId`, `id`, `createdAt`);

CREATE INDEX `retry_dispatch` on `message` (`status`, `receivedAt`, `id`, `createdAt`);

CREATE INDEX `producers` on `producer` (`id`, `createdAt`); 

CREATE INDEX `channels` on `channel` (`id`, `createdAt`);
