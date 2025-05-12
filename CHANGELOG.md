# Changelog

This contains all the release of the project from oldest to newest.

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
