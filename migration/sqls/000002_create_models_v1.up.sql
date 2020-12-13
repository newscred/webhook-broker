CREATE TABLE IF NOT EXISTS producer (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    producerId VARCHAR(255) NOT NULL,
    `name` VARCHAR(255) NOT NULL,
    token VARCHAR(255) NOT NULL,
    createdAt DATETIME NOT NULL,
    updatedAt DATETIME NOT NULL,
    UNIQUE (producerId)
);

CREATE TABLE IF NOT EXISTS channel (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    channelId VARCHAR(255) NOT NULL,
    `name` VARCHAR(255) NOT NULL,
    token VARCHAR(255) NOT NULL,
    createdAt DATETIME NOT NULL,
    updatedAt DATETIME NOT NULL,
    UNIQUE (channelId)
);

CREATE TABLE IF NOT EXISTS consumer (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    consumerId VARCHAR(255) NOT NULL,
    `name` VARCHAR(255) NOT NULL,
    token VARCHAR(255) NOT NULL,
    callbackUrl TEXT(1000) NOT NULL,
    channelId VARCHAR(255) NOT NULL,
    createdAt DATETIME NOT NULL,
    updatedAt DATETIME NOT NULL,
    UNIQUE (consumerId, channelId),
    CONSTRAINT channelConsumerRef FOREIGN KEY (channelId) REFERENCES channel(channelId) ON UPDATE CASCADE ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS message (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    messageId VARCHAR(255) NOT NULL,
    payload MEDIUMTEXT NOT NULL,
    contentType VARCHAR(255) NOT NULL,
    priority INTEGER NOT NULL,
    status INTEGER NOT NULL,
    channelId VARCHAR(255) NOT NULL,
    producerId VARCHAR(255) NOT NULL,
    receivedAt DATETIME NOT NULL,
    outboxedAt DATETIME NOT NULL,
    createdAt DATETIME NOT NULL,
    updatedAt DATETIME NOT NULL,
    UNIQUE (messageId, channelId),
    CONSTRAINT channelMessageRef FOREIGN KEY (channelId) REFERENCES channel(channelId) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT producerMessageRef FOREIGN KEY (producerId) REFERENCES producer(producerId) ON UPDATE CASCADE ON DELETE RESTRICT
);
