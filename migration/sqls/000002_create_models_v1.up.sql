CREATE TABLE IF NOT EXISTS producer (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    producerId VARCHAR(255) NOT NULL,
    `name` VARCHAR(255) NOT NULL,
    token VARCHAR(255) NOT NULL,
    createdAt DATETIME NOT NULL,
    updatedAt DATETIME NOT NULL
);

CREATE UNIQUE INDEX producer_producerid_idx ON producer (producerId);

CREATE TABLE IF NOT EXISTS channel (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    channelId VARCHAR(255) NOT NULL,
    `name` VARCHAR(255) NOT NULL,
    token VARCHAR(255) NOT NULL,
    createdAt DATETIME NOT NULL,
    updatedAt DATETIME NOT NULL
);

CREATE UNIQUE INDEX channel_channelid_idx ON channel (channelId);

CREATE TABLE IF NOT EXISTS consumer (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    consumerId VARCHAR(255) NOT NULL,
    `name` VARCHAR(255) NOT NULL,
    token VARCHAR(255) NOT NULL,
    callbackUrl TEXT(1000) NOT NULL,
    channelId VARCHAR(255) NOT NULL,
    createdAt DATETIME NOT NULL,
    updatedAt DATETIME NOT NULL,
    CONSTRAINT channelRef FOREIGN KEY (channelId) REFERENCES channel(id) ON UPDATE CASCADE ON DELETE RESTRICT
);

CREATE UNIQUE INDEX consumer_consumerid_idx ON consumer (consumerId);
