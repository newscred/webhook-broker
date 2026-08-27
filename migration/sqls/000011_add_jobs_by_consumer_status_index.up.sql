CREATE INDEX `jobs_by_consumer_status` ON `job` (`consumerId`, `status`, `statusChangedAt`);
