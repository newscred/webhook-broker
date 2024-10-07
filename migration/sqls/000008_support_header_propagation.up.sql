ALTER TABLE `message`
ADD COLUMN `headers` JSON;

ALTER TABLE `message`
ALTER COLUMN `headers` SET DEFAULT (json_object());
