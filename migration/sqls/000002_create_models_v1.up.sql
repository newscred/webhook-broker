CREATE TABLE IF NOT EXISTS producer (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    producerId VARCHAR(255) NOT NULL,
    `name` VARCHAR(255) NOT NULL,
    token VARCHAR(255) NOT NULL,
    createdAt DATETIME NOT NULL,
    updatedAt DATETIME NOT NULL
);

CREATE UNIQUE INDEX produced_producerid_idx ON producer (producerId);
