CREATE TABLE IF NOT EXISTS `dlq_summary` (
    `consumerId` VARCHAR(255) NOT NULL PRIMARY KEY,
    `consumerName` VARCHAR(255) NOT NULL,
    `channelId` VARCHAR(255) NOT NULL,
    `channelName` VARCHAR(255) NOT NULL,
    `dead_count` BIGINT NOT NULL DEFAULT 0,
    `last_checked_at` DATETIME(6) NOT NULL,
    CONSTRAINT `dlqSummaryConsumerRef` FOREIGN KEY (`consumerId`)
        REFERENCES consumer(`id`) ON UPDATE CASCADE ON DELETE CASCADE
);
