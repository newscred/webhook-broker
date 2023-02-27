ALTER TABLE `job`
ADD COLUMN `priority` INTEGER NOT NULL DEFAULT 0;

UPDATE `job`
SET `priority` = (SELECT `priority` FROM `message` WHERE `id` = `job`.`messageId`)
WHERE `job`.`status` <> 1003;
