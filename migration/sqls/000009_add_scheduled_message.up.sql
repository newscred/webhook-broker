CREATE TABLE IF NOT EXISTS `scheduled_message` (
    `id` VARCHAR(255) NOT NULL PRIMARY KEY,
    `messageId` VARCHAR(255) NOT NULL,
    `payload` MEDIUMTEXT NOT NULL,
    `contentType` VARCHAR(255) NOT NULL,
    `priority` INTEGER NOT NULL,
    `channelId` VARCHAR(255) NOT NULL,
    `producerId` VARCHAR(255) NOT NULL,
    `headers` JSON NULL,
    `dispatchSchedule` DATETIME NOT NULL,
    `dispatchedAt` DATETIME NULL,
    `status` INTEGER NOT NULL,
    `createdAt` DATETIME NOT NULL,
    `updatedAt` DATETIME NOT NULL,
    UNIQUE (`messageId`, `channelId`),
    CONSTRAINT `channelScheduledMessageRef` FOREIGN KEY (`channelId`) REFERENCES channel(`channelId`) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT `producerScheduledMessageRef` FOREIGN KEY (`producerId`) REFERENCES producer(`producerId`) ON UPDATE CASCADE ON DELETE RESTRICT
);

-- Index to efficiently find messages due for delivery
CREATE INDEX `idx_scheduled_message_dispatch` ON `scheduled_message` (`status`, `dispatchSchedule`);