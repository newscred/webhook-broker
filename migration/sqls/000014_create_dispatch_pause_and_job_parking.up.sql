-- Dispatch pause: one row per paused scope. scope is '__global__' for a broker-wide
-- pause or the internal consumer.id for a consumer pause. Deleting the row resumes.
CREATE TABLE IF NOT EXISTS `dispatch_pause` (
    `scope`      VARCHAR(255) NOT NULL PRIMARY KEY,
    `consumerId` VARCHAR(255) NULL,
    `reason`     VARCHAR(1024) NOT NULL DEFAULT '',
    `pausedAt`   DATETIME NOT NULL,
    CONSTRAINT `dispatchPauseConsumerRef` FOREIGN KEY (`consumerId`)
        REFERENCES consumer(`id`) ON UPDATE CASCADE ON DELETE CASCADE
);
CREATE INDEX `dispatch_pause_consumer` ON `dispatch_pause` (`consumerId`);

-- Job parking: jobs of paused consumers live here instead of `job`, so the retry
-- worker never scans them. A job exists in exactly one of `job` or `job_parking`;
-- `id` is the job id and is preserved when a job is parked and drained back.
-- The messageId FK is ON DELETE RESTRICT so that a prune run (including an older
-- binary that does not know about parking) can never delete a message that is
-- still owed to a paused consumer.
CREATE TABLE IF NOT EXISTS `job_parking` (
{{if eq .Dialect "mysql"}}
    `seq` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
{{else}}
    `seq` INTEGER PRIMARY KEY AUTOINCREMENT,
{{end}}
    `id`                    VARCHAR(255) NOT NULL,
    `messageId`             VARCHAR(255) NOT NULL,
    `consumerId`            VARCHAR(255) NOT NULL,
    `dispatchReceivedAt`    DATETIME NOT NULL,
    `retryAttemptCount`     INTEGER NOT NULL DEFAULT 0,
    `earliestNextAttemptAt` DATETIME NOT NULL,
    `priority`              INTEGER NOT NULL DEFAULT 0,
    `incrementalTimeout`    INTEGER NOT NULL DEFAULT 0,
    `createdAt`             DATETIME NOT NULL,
    `parkedAt`              DATETIME NOT NULL,
    UNIQUE (`id`),
    UNIQUE (`messageId`, `consumerId`),
    CONSTRAINT `jobParkingMessageRef`  FOREIGN KEY (`messageId`)  REFERENCES message(`id`)  ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT `jobParkingConsumerRef` FOREIGN KEY (`consumerId`) REFERENCES consumer(`id`) ON UPDATE CASCADE ON DELETE RESTRICT
);
-- Drain pages (consumerId = ? ORDER BY createdAt, seq) and SELECT DISTINCT consumerId.
CREATE INDEX `job_parking_drain` ON `job_parking` (`consumerId`, `createdAt`, `seq`);
-- Prune NOT EXISTS check and per-message job listings.
CREATE INDEX `job_parking_message` ON `job_parking` (`messageId`);

-- Generated with assistance from Claude AI
