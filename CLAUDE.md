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

5. **Scheduler** (`/scheduler`)
   - Scheduled message delivery management
   - Periodic checking of messages due for delivery

6. **Prune** (`/prune`)
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

4. **Scheduled Messages**:
   - Messages can be scheduled for future delivery using the `X-Broker-Scheduled-For` header
   - Scheduler service periodically checks for messages due for delivery
   - When scheduled time is reached, messages are dispatched through normal flow

## Architecture Notes for Future Feature Development

When implementing new features in webhook-broker, follow these architectural patterns:

### Important Note on Generated Files

NEVER edit wire_gen.go files directly as they are auto-generated. Instead:
- Make changes to the injector sets, provider functions, or wire.go files
- Run the wire tool to regenerate wire_gen.go
- Generated files should only be modified by their respective generation tools

### Adding a New Entity

1. **Database Schema**:
   - Create migration files in `/migration/sqls/` with proper sequencing
   - Include necessary indices and foreign key constraints
   - Migration file should have both `.up.sql` and `.down.sql` versions

2. **Data Model**:
   - Define model struct in `/storage/data/` directory
   - Include `BasePaginateable` for common fields (ID, created/updated timestamps)
   - Implement `QuickFix()` and `IsInValidState()` methods
   - Define appropriate enum types for status fields with string conversions

3. **Repository Layer**:
   - Define repository interface in `/storage/dataaccess.go`
   - Implement in dedicated file (e.g., `/storage/entitynamerepo.go`)
   - Add getter to `DataAccessor` interface
   - Implement getter in `RelationalDBDataAccessor`
   - Update `RDBMSStorageInternalInjector` wire set in `storage/rdbms.go`
   - Add field to `RelationalDBDataAccessor` struct

4. **Controller Layer**:
   - Create controller(s) in `/controllers/` directory
   - Implement appropriate HTTP methods (Get, Post, Put, Delete) as needed
   - Add to `Controllers` struct in `router.go`
   - Add to `ControllerInjector` wire set
   - Include in `setupAPIRoutes` function

5. **Background Services**:
   - Create new package similar to `/dispatcher` if needed
   - Implement lifecycle methods (Start/Stop)
   - Add service to `HTTPServiceContainer` in `main.go`
   - Handle graceful startup/shutdown

### Dependency Injection

1. **Wire Integration**:
   - Follow existing patterns in `wire.go` and `wire_gen.go`
   - Add components to appropriate wire sets
   - Keep field names consistent between structs and wire sets
   - Create provider functions as needed

2. **Controller Registration**:
   - Update `Controllers` struct in `router.go`
   - Add to `ControllerInjector` wire set
   - Add controller initialization in wire generation chain

3. **Repository Integration**:
   - Update `RDBMSStorageInternalInjector` in `storage/rdbms.go`
   - Add necessary provider functions
   - Ensure field names match exactly in wire struct annotations

### Testing Requirements

1. **Unit Tests**:
   - Create `_test.go` files for all new components
   - Follow existing test patterns (setup, execution, assertions)
   - Use mocks where appropriate
   - Test edge cases and error conditions

2. **Integration Tests**:
   - Extend the integration test suite in `/integration-test/main.go`
   - Add test function to `main()` to be executed with other tests
   - Verify end-to-end functionality
   - Include appropriate error case testing

3. **Mock Generation**:
   - Create mocks for interfaces in `/storage/mocks/` or similar directories
   - Use mocks for unit testing controllers and services

These patterns should be followed for all new features to maintain consistency with the existing architecture.

## AI-Generated Code Guidelines

When AI agents (like Claude) generate code for this project, the code should be properly formatted according to these guidelines:

1. **Code Attribution**: Always include an attribution comment at the end of the file or significant code block:
   ```go
   // Generated with assistance from Claude AI
   ```

2. **Code Style**:
   - Follow Go idioms and conventions
   - Use consistent naming with the rest of the codebase
   - Avoid unnecessary comments that don't add value
   - Maintain proper indentation (tabs for Go code)
   - Organize imports according to standard Go practices

3. **Documentation**:
   - Include proper godoc-style comments for exported functions and types
   - Document complex logic with concise, clear comments
   - Ensure error handling is properly documented

4. **Error Handling**:
   - Always handle errors appropriately
   - Use existing error patterns from the codebase
   - Do not suppress errors or use empty catch blocks

5. **Testing**:
   - Include unit tests for all new functionality
   - Follow the existing test patterns in the codebase
   - Ensure test coverage for edge cases and error conditions

AI agents should focus on code quality and consistency with existing patterns rather than adding explanatory comments about what the code does unless specifically requested.