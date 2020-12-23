DROP INDEX `retry_job` on `job`;

DROP INDEX `force_timeout_job` on `job`;

DROP INDEX `retry_dispatch` on `message`;

DROP INDEX `lock_attained` ON `lock`;

DROP TABLE IF EXISTS `lock`;
