ALTER TABLE `message`
ADD COLUMN `headers` JSON NOT NULL DEFAULT (json_object());
