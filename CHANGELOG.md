# Changelog

This contains all the release of the project from oldest to newest.

## Unreleased

### New Features

- Dispatch pause with job parking ([tech spec](docs/tech-specs/dispatch-pause-spec.md), [runbook](docs/runbooks/dispatch-pause.md))
  - Pause delivery per consumer (`PUT|GET|DELETE /channel/{channelId}/consumer/{consumerId}/dispatch-pause`) or globally (`PUT|DELETE /dispatch-pause`, `GET /dispatch-pauses`) while broadcasts keep being accepted
  - Jobs of paused consumers are parked in a new `job_parking` table; retries are never used up and the retry worker never scans them
  - On resume a single drainer moves parked jobs back oldest first and hands them to the cluster through the load-balanced `retrigger-base-endpoint`
  - Pull consumers see an empty `queued-jobs` list with `X-Broker-Dispatch-Paused: true` and get 409 for hand-out transitions while paused
  - New metrics: `dispatch_paused`, `parked_job_count`, `parked_jobs_total`, `drained_jobs_total`, `handoff_requests_total`, `handoff_failures_total`
- Prune never removes messages that still have parked jobs

### Configuration Changes

- Added `[dispatch-pause]` section: `admin-token`, `registry-refresh-interval-seconds`, `drain-interval-seconds`, `drain-page-size`, `handoff-mode`, `handoff-lease-seconds`, `handoff-timeout-seconds`, `handoff-max-attempts`, `handoff-concurrency`, `handoff-high-water-mark`, `handoff-local-fallback` (see [configuration](docs/configuration.md))
- `[broker] retrigger-base-endpoint` is now used as the hand-off target; point it at the load balancer in clustered deployments

### Upgrade Notes

- Migration `000014` adds two new, empty tables (instant)
- Do not pause anything until every broker node and the prune job run this version

### Bug Fixes

- SQLite duplicate-key errors from `go-sqlite3` are now recognised, so lock conflicts map to `ErrAlreadyLocked`

## Release `0.2.3`
- Add index to optimize queries filtering by (createdAt, id) on the message table.

## Release `0.2.2`
Bug fix: Webhook broker prune job gets stuck for hours due to MySQL not being able to utilize indexes for the `pruneMessageQuery`

## Release `0.2.1`

### New Features

- Add scheduled message support (Issue #55)
  - Schedule messages for future delivery using X-Broker-Scheduled-For header
  - API for listing and retrieving scheduled messages
  - Enhanced status reporting with scheduled message counts
  - Configuration options for scheduler behavior

### Configuration Changes

- Added [scheduler] section with the following options:
  - scheduler-interval-ms: How frequently to check for due messages (default: 5000ms)
  - min-schedule-delay-minutes: Minimum required delay for scheduled messages (default: 2 minutes)
  - scheduler-batch-size: Maximum scheduled messages to process per iteration (default: 100)

## Release `0.2.0`

This includes all issues with milestone [v0.2](https://github.com/newscred/webhook-broker/issues?q=is%3Aissue%20state%3Aclosed%20milestone%3Av0.2).

## Release `0.1.2`

[Bug - 104](https://github.com/newscred/webhook-broker/issues/104) - webhook-broker fails to dispatch with ambiguous error

## Release `0.1.1`

Hot fix: [Issue](https://github.com/newscred/webhook-broker/issues/83) - Fix update Channel, Producer and Consumer Web APIs to work as expected

## Release `0.1.0`

This includes all issues with milestone [v0.1](https://github.com/newscred/webhook-broker/issues?q=is%3Aissue+milestone%3Av0.1). It is the first release so no upgrade note applicable.
