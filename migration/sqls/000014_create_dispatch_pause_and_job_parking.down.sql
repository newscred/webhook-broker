-- WARNING: dropping job_parking discards parked jobs. Resume all pauses and wait for
-- job_parking to be empty before rolling back (see docs/runbooks/dispatch-pause.md).
DROP TABLE IF EXISTS `job_parking`;
DROP TABLE IF EXISTS `dispatch_pause`;

-- Generated with assistance from Claude AI
