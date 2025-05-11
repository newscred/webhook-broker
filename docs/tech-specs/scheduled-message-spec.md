# Scheduled Message Specification

## Overview

This specification defines how webhook-broker will implement scheduled message delivery based on Issue #55. The feature allows producers to send messages with a future delivery time.

## Design Goals

- Simple API using HTTP headers
- Reliable scheduled delivery with clear error conditions
- Minimal impact on existing message flow
- Efficient resource utilization during dispatch
- Maintain consistent message identity between scheduled and actual delivery

## API

### Scheduled Message Creation

When a producer broadcasts a message to a channel, they can include a special header to indicate scheduled delivery:

```
X-Broker-Scheduled-For: 2025-12-31T23:59:59Z
```

The value must be a valid ISO-8601 date-time string in UTC timezone.

#### Response Codes

- `201 Created`: Message successfully scheduled
- `400 Bad Request`: Invalid date format
- `412 Precondition Failed`: Scheduled time less than 2 minutes in the future
- `409 Conflict`: Message ID conflict

### Scheduled Message Listing

A new endpoint will be added to list scheduled messages for a specific channel:

```
GET /channel/{channelId}/scheduled-messages
```

Query parameters:
- `page`: Page number (optional)
- `status`: Filter by status (optional, can be "SCHEDULED" or "DISPATCHED")

Response (JSON):
```json
{
  "result": [
    "/channel/{channelId}/scheduled-message/{messageId}",
    "/channel/{channelId}/scheduled-message/{messageId2}",
    ...
  ],
  "pages": {
    "next": "/channel/{channelId}/scheduled-messages?page=next-page-token",
    "prev": "/channel/{channelId}/scheduled-messages?page=prev-page-token"
  }
}
```

### Individual Scheduled Message Endpoint

```
GET /channel/{channelId}/scheduled-message/{messageId}
```

Response (JSON):
```json
{
  "id": "...",
  "messageId": "...",
  "contentType": "...",
  "priority": 0,
  "producedBy": "ProducerName",
  "dispatchSchedule": "2025-12-31T23:59:59Z",
  "dispatchedDate": null,
  "status": "SCHEDULED",
  "payload": "...",
  "headers": {
    "X-Custom-Header": "value"
  }
}
```

### Status Endpoint Enhancement

The existing channel message status endpoint (`GET /channel/{channelId}/messages-status`) will be enhanced to include scheduled message information:

```json
{
  "counts": {
    "ACKNOWLEDGED": {
      "count": 10,
      "links": {
        "messages": "/channel/{channelId}/messages?status=100"
      }
    },
    "DISPATCHED": {
      "count": 20,
      "links": {
        "messages": "/channel/{channelId}/messages?status=101"
      }
    },
    "SCHEDULED": {
      "count": 5,
      "links": {
        "messages": "/channel/{channelId}/scheduled-messages?status=100"
      }
    },
    "SCHEDULED_DISPATCHED": {
      "count": 15,
      "links": {
        "messages": "/channel/{channelId}/scheduled-messages?status=101"
      }
    }
  },
  "next_scheduled_message_at": "2025-12-31T23:59:59Z"
}
```

The `next_scheduled_message_at` field shows the earliest time a scheduled message is due to be dispatched (null if none).

### Implementation Details

#### Storage

Messages scheduled for future delivery will be stored in a new `scheduled_message` table with the following schema:

```sql
CREATE TABLE IF NOT EXISTS `scheduled_message` (
    `id` VARCHAR(255) NOT NULL PRIMARY KEY,
    `messageId` VARCHAR(255) NOT NULL,
    `payload` MEDIUMTEXT NOT NULL,
    `contentType` VARCHAR(255) NOT NULL,
    `priority` INTEGER NOT NULL,
    `channelId` VARCHAR(255) NOT NULL,
    `producerId` VARCHAR(255) NOT NULL,
    `headers` JSON NULL,
    `dispatchSchedule` DATETIME NOT NULL,
    `dispatchedDate` DATETIME NULL,
    `status` INTEGER NOT NULL,
    `createdAt` DATETIME NOT NULL,
    `updatedAt` DATETIME NOT NULL,
    UNIQUE (`messageId`, `channelId`),
    CONSTRAINT `channelScheduledMessageRef` FOREIGN KEY (`channelId`) REFERENCES channel(`channelId`) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT `producerScheduledMessageRef` FOREIGN KEY (`producerId`) REFERENCES producer(`producerId`) ON UPDATE CASCADE ON DELETE RESTRICT
);

-- Index to efficiently find messages due for delivery
CREATE INDEX `idx_scheduled_message_next_dispatch` ON `scheduled_message` (`status`, `dispatchSchedule`);
```

#### Status Values

- `100`: SCHEDULED - Initial state, waiting for dispatch time
- `101`: DISPATCHED - Message has been dispatched to the regular message flow

#### Data Model

1. **ScheduledMessage Struct**:
   ```go
   // ScheduledMsgStatus represents the state of a ScheduledMsg
   type ScheduledMsgStatus int

   const (
       // ScheduledMsgStatusScheduled represents the state where message is waiting to be dispatched
       ScheduledMsgStatusScheduled ScheduledMsgStatus = iota + 100
       // ScheduledMsgStatusDispatched represents when message has been dispatched
       ScheduledMsgStatusDispatched
       // ScheduledMsgStatusScheduledStr is the string representation of message's scheduled status
       ScheduledMsgStatusScheduledStr = "SCHEDULED"
       // ScheduledMsgStatusDispatchedStr is the string representation of message's dispatched status
       ScheduledMsgStatusDispatchedStr = "DISPATCHED"
   )

   // ScheduledMessage represents a message scheduled for future delivery
   type ScheduledMessage struct {
       BasePaginateable
       MessageID        string
       Payload          string
       ContentType      string
       Priority         uint
       Status           ScheduledMsgStatus
       BroadcastedTo    *Channel
       ProducedBy       *Producer
       DispatchSchedule time.Time
       DispatchedDate   time.Time
       Headers          HeadersMap
   }
   ```

2. **Repository Interface**:
   ```go
   // ScheduledMessageRepository allows storage operations over ScheduledMessage
   type ScheduledMessageRepository interface {
       Create(message *data.ScheduledMessage) error
       Get(channelID string, messageID string) (*data.ScheduledMessage, error)
       GetByID(id string) (*data.ScheduledMessage, error)
       MarkDispatched(message *data.ScheduledMessage) error
       GetMessagesReadyForDispatch(limit int) []*data.ScheduledMessage
       GetScheduledMessagesForChannel(channelID string, page *data.Pagination, statusFilters ...data.ScheduledMsgStatus) ([]*data.ScheduledMessage, *data.Pagination, error)
       GetScheduledMessageStatusCountsByChannel(channelID string) ([]*data.StatusCount[data.ScheduledMsgStatus], error)
       GetNextScheduledMessageTime(channelID string) (*time.Time, error)
   }
   ```

#### Core Components

1. **Controller Extension**:
   The `BroadcastController.Post` method will be extended to:
   - Check for the `X-Broker-Scheduled-For` header
   - Validate the date format and minimum future time requirement
   - Create a ScheduledMessage instead of a regular Message when appropriate

2. **Message Scheduler**:
   A new component that will:
   - Run as a background goroutine initiated at startup
   - Periodically query for scheduled messages ready for delivery
   - Spawn goroutines to dispatch individual messages concurrently
   - Handle race conditions and error scenarios

#### Scheduler Process in Detail

1. **Initialization**:
   ```go
   func NewMessageScheduler(config Config, scheduledMsgRepo ScheduledMessageRepository,
                           msgRepo MessageRepository, dispatcher MessageDispatcher) *MessageScheduler {
       scheduler := &MessageScheduler{
           config:               config,
           scheduledMessageRepo: scheduledMsgRepo,
           messageRepo:          msgRepo,
           dispatcher:           dispatcher,
           stopChan:             make(chan struct{}),
       }
       return scheduler
   }
   ```

2. **Start Process**:
   ```go
   func (scheduler *MessageScheduler) Start() {
       scheduler.wg.Add(1)
       go func() {
           defer scheduler.wg.Done()
           ticker := time.NewTicker(scheduler.config.GetSchedulerInterval())
           defer ticker.Stop()

           for {
               select {
               case <-ticker.C:
                   scheduler.processScheduledMessages()
               case <-scheduler.stopChan:
                   return
               }
           }
       }()
   }
   ```

3. **Processing Loop**:
   ```go
   func (scheduler *MessageScheduler) processScheduledMessages() {
       // Get batches of messages ready for dispatch (where dispatchSchedule <= now and status == SCHEDULED)
       messages := scheduler.scheduledMessageRepo.GetMessagesReadyForDispatch(batchSize)

       // Process each message concurrently
       for _, scheduledMsg := range messages {
           go scheduler.dispatchMessage(scheduledMsg)
       }
   }
   ```

4. **Message Dispatch**:
   ```go
   func (scheduler *MessageScheduler) dispatchMessage(scheduledMsg *data.ScheduledMessage) {
       // Create regular message from scheduled message
       message, err := data.NewMessage(
           scheduledMsg.BroadcastedTo,
           scheduledMsg.ProducedBy,
           scheduledMsg.Payload,
           scheduledMsg.ContentType,
           scheduledMsg.Headers,
       )

       // Use the same Message ID for continuity
       message.MessageID = scheduledMsg.MessageID
       message.Priority = scheduledMsg.Priority

       // Create the message in the regular message table
       err = scheduler.messageRepo.Create(message)
       if err != nil {
           if err == storage.ErrDuplicateMessageIDForChannel {
               // Handle potential race condition
               time.Sleep(100 * time.Millisecond)
               refreshedMsg, _ := scheduler.scheduledMessageRepo.GetByID(scheduledMsg.ID.String())
               if refreshedMsg.Status == data.ScheduledMsgStatusScheduled {
                   log.Error().Str("messageId", message.MessageID).Msg("Race condition detected: Message already created but status not updated")
               }
           } else {
               log.Error().Err(err).Str("messageId", message.MessageID).Msg("Failed to create message from scheduled message")
           }
           return
       }

       // Update scheduled message status to dispatched and set dispatchedDate
       scheduledMsg.Status = data.ScheduledMsgStatusDispatched
       scheduledMsg.DispatchedDate = time.Now()
       err = scheduler.scheduledMessageRepo.MarkDispatched(scheduledMsg)
       if err != nil {
           log.Error().Err(err).Str("messageId", message.MessageID).Msg("Failed to mark scheduled message as dispatched")
       }

       // Dispatch message through normal flow
       go scheduler.dispatcher.Dispatch(message)
   }
   ```

#### Concurrency and Performance

- Independent goroutines dispatch each scheduled message concurrently
- The scheduler uses a rate-limited approach to avoid excessive database load
- Index on (status, dispatchDate) optimizes query performance
- The scheduled message ID is preserved as the actual message ID for tracking continuity
- The scheduler is designed to work correctly in a multi-instance deployment scenario

## Error Handling

1. **Schedule Validation Errors**:
   - Invalid date format: Return HTTP 400
   - Time too close to present: Return HTTP 412
   - Malformed header: Return HTTP 400

2. **Dispatch Errors**:
   - Message creation failure: Log error, retry in next scheduler iteration
   - Concurrent dispatch attempts: Handle with 100ms delay and status check
   - Database connectivity issues: Retry logic with exponential backoff

3. **Operational Handling**:
   - Scheduler failure: Log errors, metrics exposed for monitoring
   - Database deadlocks: Retry with randomized backoff

## Configuration

The scheduled message feature will utilize the following configuration parameters:

- `schedulerIntervalMs`: How frequently to check for due messages (default: 5000ms)
- `minScheduleDelayMinutes`: Minimum required delay for scheduled messages (default: 2 minutes)
- `schedulerBatchSize`: Maximum number of scheduled messages to process per iteration (default: 100)

## Integration with Existing Components

1. **Controller Layer**:
   - Extend `BroadcastController` to handle scheduled message header and creation
   - Add new controllers following existing patterns:
     - `ScheduledMessageController`: For retrieving individual scheduled messages
     - `ScheduledMessagesController`: For listing scheduled messages
   - Update `MessagesStatusController` to include scheduled message counts
   - Add these new controllers to `Controllers` struct in `router.go`
   - Include controllers in `ControllerInjector` wire set in `router.go`
   - Register controllers in `setupAPIRoutes` function

2. **Repository Layer**:
   - Add `ScheduledMessageRepository` interface in `dataaccess.go`
   - Create `scheduledmessagerepo.go` with repository implementation
   - Add getter method to `DataAccessor` interface:
     ```go
     GetScheduledMessageRepository() ScheduledMessageRepository
     ```
   - Implement this method in `RelationalDBDataAccessor`
   - Add repository field to `RelationalDBDataAccessor` struct
   - Update `RDBMSStorageInternalInjector` in `storage/rdbms.go` to include the scheduled message repository:
     ```go
     var RDBMSStorageInternalInjector = wire.NewSet(
         NewAppRepository,
         NewProducerRepository,
         NewChannelRepository,
         NewConsumerRepository,
         NewMessageRepository,
         NewDeliveryJobRepository,
         NewLockRepository,
         NewScheduledMessageRepository, // Add new repository
         wire.Struct(new(RelationalDBDataAccessor),
             "appRepository",
             "producerRepository",
             "channelRepository",
             "consumerRepository",
             "messageRepository",
             "deliveryJobRepository",
             "lockRepository",
             "scheduledMessageRepository", // Add new field
             "db")
     )
     ```

3. **Scheduler Service**:
   - Create new scheduler package similar to dispatcher package
   - Implement a scheduler that periodically checks for due messages
   - Wire scheduler into application startup/shutdown flow
   - Add scheduler to `HTTPServiceContainer` struct

4. **Metrics and Monitoring**:
   - Add metrics similar to existing ones in dispatcher:
     - Number of scheduled messages created
     - Number of scheduled messages dispatched
     - Number of scheduling errors
     - Scheduler lag time (difference between actual dispatch time and scheduled time)

## Wire Integration

Following the existing dependency injection pattern:

1. **Controller Registration**:
   - Add new controllers to wire set in `controllers/router.go`
   - Update `Controllers` struct with new controller fields
   - Update `setupAPIRoutes` to include the new controllers

2. **Repository Integration**:
   - Update `RDBMSStorageInternalInjector` in `storage/rdbms.go`
   - Implement repository provider function similar to existing ones

3. **Application Lifecycle**:
   - Update `main.go` to initialize and start/stop the scheduler
   - Add scheduler to `HTTPServiceContainer`

These changes maintain consistency with the existing application architecture and dependency injection approach.

## Limitations

- No recurring messages support (no cron expressions)
- No ability to cancel scheduled messages
- Messages must be scheduled at least 2 minutes in the future
- No retroactive scheduling (cannot schedule messages in the past)

## Migration and Deployment Considerations

- New table requires database migration
- Rolling deployments supported (old instances will ignore scheduled messages)
- No special rollback procedures required beyond standard database rollback

## Key Files and References

### Files to Implement

1. **Database Schema**:
   - `/migration/sqls/000009_add_scheduled_message.up.sql` - Create scheduled messages table
   - `/migration/sqls/000009_add_scheduled_message.down.sql` - Drop schema changes

2. **Data Model**:
   - `/storage/data/scheduledmessage.go` - ScheduledMessage model definition

3. **Repository**:
   - Updates to `/storage/dataaccess.go` - Add ScheduledMessageRepository interface
   - `/storage/scheduledmessagerepo.go` - Implementation of repository
   - Updates to `/storage/rdbms.go` - Add to RDBMSStorageInternalInjector

4. **Controllers**:
   - `/controllers/scheduledmessage.go` - Single scheduled message retrieval
   - `/controllers/scheduledmessages.go` - Scheduled message listing
   - Updates to `/controllers/broadcast.go` - Add header parsing
   - Updates to `/controllers/router.go` - Add to Controllers struct and ControllerInjector

5. **Scheduler**:
   - `/scheduler/scheduler.go` - Main scheduler implementation
   - `/scheduler/metrics.go` - Metrics collection

6. **Wire Updates**:
   - Updates to `/wire.go` and other DI configuration

### Reference/Inspiration Files

1. **Data Model** inspiration:
   - `/storage/data/message.go` - For message model pattern
   - `/storage/data/job.go` - For status handling

2. **Repository** inspiration:
   - `/storage/messagerepo.go` - For message repository pattern
   - `/storage/dataaccess.go` - Interface definitions

3. **Controller** inspiration:
   - `/controllers/message.go` - For message controller
   - `/controllers/broadcast.go` - For handling message creation

4. **Scheduler** inspiration:
   - `/dispatcher/worker.go` - For worker pattern
   - `/dispatcher/msgdispatcher.go` - For dispatcher pattern

5. **Wire Integration** inspiration:
   - `/wire.go` and `/wire_gen.go` - For DI patterns
   - `/controllers/router.go` - For controller registration

## Implementation Milestones

The implementation will proceed through the following milestones, each resulting in its own commit:

1. **Database Schema Migration**
   - Create migration scripts for scheduled_message table
   - Add necessary indices and foreign key constraints
   - Commit: "Add database schema for scheduled messages"

2. **Data Model**
   - Implement ScheduledMessage model with validation
   - Add status enums and helper methods
   - Add unit tests for model
   - Commit: "Add scheduled message data model and tests"

3. **Repository Layer**
   - Add ScheduledMessageRepository interface to dataaccess.go
   - Implement repository in scheduledmessagerepo.go
   - Update DataAccessor interface
   - Implement in RelationalDBDataAccessor
   - Update RDBMSStorageInternalInjector
   - Add unit tests for repository
   - Commit: "Add scheduled message repository and tests"

4. **Controller Layer - Part 1: Extension**
   - Extend BroadcastController for handling scheduled message header
   - Add date parsing and validation logic
   - Add unit tests for extensions
   - Commit: "Extend broadcast controller for scheduled messages"

5. **Controller Layer - Part 2: New Controllers**
   - Implement ScheduledMessageController for individual message retrieval
   - Implement ScheduledMessagesController for listing messages
   - Update Controllers struct and wire integration
   - Add controller unit tests
   - Commit: "Add scheduled message controllers and tests"

6. **Status Reporting**
   - Extend MessagesStatusController for scheduled message counts
   - Add next scheduled message time
   - Add unit tests for changes
   - Commit: "Add scheduled message status reporting"

7. **Scheduler Implementation**
   - Create scheduler package
   - Implement message scanning and dispatch logic
   - Add concurrency and race condition handling
   - Add metrics collection
   - Add unit tests for scheduler
   - Commit: "Add scheduled message scheduler and tests"

8. **Application Integration**
   - Update HTTPServiceContainer
   - Add startup/shutdown lifecycle handling
   - Wire everything together
   - Commit: "Integrate scheduled message components"

9. **Integration Tests**
   - Add testScheduledMessages() to integration-test/main.go
   - Test all key scenarios
   - Commit: "Add integration tests for scheduled messages"

10. **Documentation and Cleanup**
    - Update documentation
    - Add configuration examples
    - Final code review cleanup
    - Commit: "Add documentation and cleanup for scheduled messages"

Each milestone should be implemented and tested before moving to the next one. This approach allows for incremental progress and easier debugging if issues arise.

## Testing Strategy

## Unit Testing

Unit tests must be written for all new components. Following the existing testing patterns in the codebase, each new file should have a corresponding `_test.go` file with comprehensive test coverage.

### Data Model Tests (`storage/data/scheduledmessage_test.go`)

- Test `ScheduledMessage` model validation logic
- Verify proper timestamp and UTC handling
- Test status transition validation logic
- Verify `QuickFix` implementation properly sets defaults
- Test `IsInValidState` correctly identifies invalid states
- Verify proper serialization/deserialization of headers

### Repository Tests (`storage/scheduledmessagerepo_test.go`)

- Create and retrieve scheduled messages
- Test database error handling (duplicate messageID, etc.)
- Verify marking messages as dispatched updates all fields correctly
- Test finding messages that are ready for dispatch
- Verify that status count aggregation works properly
- Test finding the next scheduled message time
- Verify pagination works correctly for message listing

### Controller Tests

- `controllers/scheduledmessage_test.go`:
  - Test scheduled message endpoint routing
  - Verify HTTP responses match expected formats
  - Test pagination and filtering behavior
  - Verify proper handling of not-found scenarios

- `controllers/broadcast_test.go` (extensions):
  - Test parsing and validation of scheduled message headers
  - Verify date format validation logic
  - Test minimum future time validation
  - Verify proper error responses for invalid inputs
  - Test creation of scheduled messages through broadcast endpoint

- `controllers/messages_status_test.go` (extensions):
  - Test status endpoint correctly includes scheduled message counts
  - Verify next scheduled message time is included

### Scheduler Component Tests (`scheduler/scheduler_test.go`)

- Test initialization and cleanup
- Verify message dispatching logic
- Test concurrent dispatch handling with multiple goroutines
- Verify error handling and recovery mechanisms
- Test race condition detection and handling
- Verify scheduler properly stops on shutdown
- Mock repository interactions to test edge cases

## Integration Testing

Extend the existing integration test suite in `/integration-test/main.go` with a new test function for scheduled messages.

### New Test Function: `testScheduledMessages()`

This function should test the following scenarios:

1. **Basic Scheduled Message Flow**
   - Schedule a message for delivery a few seconds in the future
   - Verify creation response is correct (status 201)
   - Verify the message appears in the scheduled messages list
   - Verify status endpoint shows correct counts
   - Wait for the scheduled time to pass
   - Verify the message is delivered to consumers
   - Verify status changes from SCHEDULED to DISPATCHED

2. **Error Case Testing**
   - Attempt to schedule with invalid date format (verify 400 response)
   - Attempt to schedule with time too close to present (verify 412 response)
   - Attempt to schedule with duplicate message ID (verify 409 response)

3. **Concurrent Scheduling Test**
   - Schedule multiple messages with different future times
   - Verify all are delivered in the correct order
   - Verify status counts update correctly after each delivery

### Integration Test Implementation Considerations

- Use short time intervals (2-5 seconds) to keep tests running quickly
- Add appropriate timeouts and wait groups to synchronize test execution
- Add the new test to the main() function alongside existing tests
- Ensure tests are idempotent and can run in CI environments
- Add appropriate logging to diagnose test failures

This comprehensive testing strategy will ensure that the scheduled message feature works correctly in isolation (unit tests) and as part of the complete system (integration tests), with good coverage of both normal operation and error handling.