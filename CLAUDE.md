# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Webhook-broker is a Pub/Sub message broker that operates over HTTP. It's designed to simplify inter-service communication in systems with SOA or microservice architecture. The broker guarantees at-least-once delivery of messages between producers and consumers.

Key concepts:
- **Channel**: A broadcast highway for messages
- **Producer**: System generating messages to broadcast to a channel
- **Consumer**: System registered to listen for messages on a channel
- **Message**: Payload to be delivered from producer to consumer
- **DeliveryJob**: Represents the task of delivering a message to a specific consumer

## Development Commands

### Building the Project

```bash
# Install dependencies and build tools
make dep-tools

# Download/vendor dependencies
make deps

# Run tests
make test

# Run time-optimized tests
make time-test

# Run CI tests (with -short flag)
make ci-test

# Build the project
make build

# Clean build artifacts
make clean

# Full build process (clean, install deps, run tests, build)
make all

# Build Docker image
make build-docker-image

# Run integration tests
make itest

# Stop integration test containers
make itest-down
```

### Docker Setup

```bash
# Start MySQL
docker-compose up -d mysql

# Start the broker (after MySQL is ready)
docker-compose up broker

# Sample curl command to test
curl -v localhost:18181/channel/sample-channel/broadcast -X POST \
  -H "X-Broker-Channel-Token: sample-channel-token" \
  -H "X-Broker-Producer-Token: sample-producer-token" \
  -H "X-Broker-Producer-ID: sample-producer" \
  -H "Content-Type: application/json" \
  --data '{"test": "Hello World!"}'
```

### Running with Custom Config

```bash
./webhook-broker -config /path/to/config.cfg
```

## Code Architecture

### Core Components

1. **Storage Layer** (`/storage`)
   - Repository interfaces for persistence
   - SQL implementation of repositories
   - Data models for core entities

2. **Controllers** (`/controllers`)
   - HTTP API endpoints for all operations
   - Authentication and validation logic

3. **Dispatcher** (`/dispatcher`)
   - Message dispatch and delivery logic
   - Job queue and worker management

4. **Configuration** (`/config`)
   - Configuration loading and validation
   - Default configuration values

5. **Prune** (`/prune`)
   - Message pruning and archiving functionality

### Key Workflows

1. **Message Broadcasting**:
   - Producer sends message to `/channel/{channelId}/broadcast`
   - Message is stored in database
   - Dispatcher enqueues delivery jobs for each consumer
   - Workers process jobs and deliver messages to consumers

2. **Consumer Types**:
   - **Push Consumers**: Broker pushes messages to consumer's callback URL
   - **Pull Consumers**: Consumer pulls messages via API endpoints

3. **Message and Job Lifecycle**:
   - Messages can be in ACKNOWLEDGED or DISPATCHED state
   - Jobs can be in QUEUED, INFLIGHT, DELIVERED, or DEAD state

## Implementation Guide for Issue #55: Scheduled Messages

Issue #55 requires implementing scheduled message delivery. New features:

1. Add `dispatchDate` field to messages to specify future delivery time
2. Support for recurring schedules:
   - Optional `cron` expression for recurring delivery
   - `times` parameter for specific number of recurrences
   - `until` parameter for time-bound recurrences (mutually exclusive with `times`)

### Implementation Steps:

1. Add a new model `ScheduledMessage` with fields:
   - Base message fields (same as regular messages)
   - `dispatchDate` (required) - when to deliver the message
   - `cron` (optional) - cron expression for recurring delivery
   - `times` (optional) - number of recurrences (if `cron` is specified)
   - `until` (optional) - end date for recurrences (if `cron` is specified)

2. Schema changes:
   - Add new table for scheduled messages
   - Add necessary indices for efficient querying

3. API changes:
   - Add endpoint for creating scheduled messages
   - Add endpoint for canceling scheduled messages
   - Add endpoint for listing scheduled messages

4. Scheduler implementation:
   - Background worker to scan for messages due for delivery
   - Logic to handle recurring messages based on cron expression
   - Tracking of delivery count for messages with `times` parameter

### Notes for Development:

- Database schema changes will require migration scripts in `/migration/sqls`
- Controller changes should follow existing patterns in `/controllers`
- New repository interfaces and implementations will be needed in `/storage`
- Scheduler should be integrated with the existing dispatcher system