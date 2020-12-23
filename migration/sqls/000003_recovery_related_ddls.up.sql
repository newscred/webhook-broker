CREATE TABLE IF NOT EXISTS `lock` (
    `lockId` VARCHAR(255) NOT NULL PRIMARY KEY,
    `attainedAt` DATETIME NOT NULL
);

CREATE INDEX `lock_attained` ON `lock` (`attainedAt`);

CREATE INDEX `retry_job` on `job` (`status`, `earliestNextAttemptAt`);

CREATE INDEX `force_timeout_job` on `job` (`status`, `statusChangedAt`);

CREATE INDEX `retry_dispatch` on `message` (`status`, `receivedAt`);
