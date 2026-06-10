# Webhook Broker API Reference

This document provides a complete reference for all HTTP API endpoints available in Webhook Broker.

## Common Conventions

**Timestamp Formats:**
- **Request Headers**: HTTP-date format (e.g., `Thu, 01 Jan 1970 00:00:00 GMT`)
- **Request Body/Query**: ISO-8601/RFC3339 format (e.g., `2025-01-27T15:30:00Z`)
- **Response Body**: ISO-8601/RFC3339 format (e.g., `2025-09-04T13:59:13Z`)
- **Response Headers**: HTTP-date format (e.g., `Mon, 26 Jan 2026 02:53:41 GMT`)

**Field Naming**: Response JSON uses PascalCase for field names (e.g., `ID`, `Token`, `ChangedAt`, `CallbackURL`)

## Table of Contents

- [Status Endpoints](#status-endpoints)
- [Authentication](#authentication)
- [Channel Management](#channel-management)
- [Producer Management](#producer-management)
- [Consumer Management](#consumer-management)
- [Message Broadcasting](#message-broadcasting)
- [Message Management](#message-management)
- [Scheduled Messages](#scheduled-messages)
- [Job Management](#job-management)
- [Dead Letter Queue (DLQ)](#dead-letter-queue-dlq)
- [Pagination](#pagination)
- [Response Codes](#response-codes)

## Status Endpoints

### Get Application Status

**Endpoint:** `GET /_status`

**Description:** Retrieves the current status of the Webhook Broker application, including seed data and application state.

**Authentication:** None required

**Response:**
```json
{
  "SeedData": {
    "DataHash": "base64-encoded-hash-string",
    "Producers": [
      {
        "ID": "sample-producer",
        "Name": "Sample Producer",
        "Token": "sample-producer-token"
      }
    ],
    "Channels": [
      {
        "ID": "sample-channel",
        "Name": "Sample Channel",
        "Token": "sample-channel-token"
      }
    ],
    "Consumers": [
      {
        "ID": "sample-consumer",
        "Name": "sample-consumer",
        "Token": "sample-consumer-token",
        "CallbackURL": {...},
        "Channel": "sample-channel",
        "Type": "push"
      }
    ]
  },
  "AppStatus": 3
}
```

**Note:** `AppStatus` is an integer representing the application state. Field names in `SeedData` are capitalized.

## Authentication

Webhook Broker uses token-based authentication with specific headers for different operations:

- `X-Broker-Channel-Token`: Channel authentication token
- `X-Broker-Producer-Token`: Producer authentication token
- `X-Broker-Producer-ID`: Producer identifier
- `X-Broker-Consumer-Token`: Consumer authentication token

### How to Obtain Tokens

Tokens are returned when creating/updating resources via PUT endpoints, or can be retrieved from existing resources via GET endpoints.

**Creating with a custom token:**

```bash
curl -X PUT http://localhost:8080/channel/my-channel \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "If-Unmodified-Since: Thu, 01 Jan 1970 00:00:00 GMT" \
  -d "token=my-custom-token&name=My Channel"
```

**Retrieving an existing token:**

```bash
curl -s http://localhost:8080/channel/my-channel | jq '.Token'
```

**Notes:**
- If you omit the `token` parameter, a random 12-character token is auto-generated
- For first-time creation, use `If-Unmodified-Since: Thu, 01 Jan 1970 00:00:00 GMT`
- For updates, use the `Last-Modified` header value from the previous response
- Tokens can be pre-configured via seed data (see [configuration docs](./docs/configuration.md))

## Channel Management

### List All Channels

**Endpoint:** `GET /channels`

**Description:** Retrieves a paginated list of all channels.

**Query Parameters:**
- `previous` (optional): Pagination cursor for previous page
- `next` (optional): Pagination cursor for next page

**Response:**
```json
{
  "Result": [
    "/channel/channel-1",
    "/channel/channel-2"
  ],
  "Pages": {
    "next": "/channels?next=...",
    "previous": "/channels?previous=..."
  }
}
```

**Note:** Response field names are capitalized (e.g., `Result`, `Pages`).

### Get Channel Details

**Endpoint:** `GET /channel/:channelId`

**Description:** Retrieves details for a specific channel.

**Path Parameters:**
- `channelId`: Channel identifier

**Response:**
```json
{
  "ID": "sample-channel",
  "Name": "My Channel",
  "Token": "channel-token",
  "ChangedAt": "2025-09-04T13:59:13Z",
  "ConsumersURL": "/channel/sample-channel/consumers",
  "MessagesURL": "/channel/sample-channel/messages",
  "BroadcastURL": "/channel/sample-channel/broadcast",
  "MessagesStatusURL": "/channel/sample-channel/messages-status"
}
```

**Headers:**
- `Last-Modified`: Timestamp of last update in HTTP-date format (e.g., `Mon, 26 Jan 2026 02:53:41 GMT`)

### Update Channel

**Endpoint:** `PUT /channel/:channelId`

**Description:** Updates a channel's name and/or token.

**Path Parameters:**
- `channelId`: Channel identifier

**Headers:**
- `Content-Type`: `application/x-www-form-urlencoded` (required)
- `If-Unmodified-Since`: Last modification timestamp in HTTP-date format (required, e.g., `Thu, 01 Jan 1970 00:00:00 GMT`)

**Form Parameters:**
- `token` (optional): New authentication token (auto-generated if not provided)
- `name` (optional): Channel name (defaults to channelId if not provided)

**Response:** Same as GET channel details

**Status Codes:**
- `200 OK`: Channel updated successfully
- `400 Bad Request`: Missing `If-Unmodified-Since` header
- `412 Precondition Failed`: `If-Unmodified-Since` header doesn't match
- `415 Unsupported Media Type`: Content-Type is not `application/x-www-form-urlencoded`

## Producer Management

### List All Producers

**Endpoint:** `GET /producers`

**Description:** Retrieves a paginated list of all producers.

**Query Parameters:**
- `previous` (optional): Pagination cursor for previous page
- `next` (optional): Pagination cursor for next page

**Response:**
```json
{
  "result": [
    "/producer/producer-1",
    "/producer/producer-2"
  ],
  "pages": {
    "next": "/producers?next=...",
    "previous": "/producers?previous=..."
  }
}
```

### Get Producer Details

**Endpoint:** `GET /producer/:producerId`

**Description:** Retrieves details for a specific producer.

**Path Parameters:**
- `producerId`: Producer identifier

**Response:**
```json
{
  "ID": "sample-producer",
  "Name": "My Producer",
  "Token": "producer-token",
  "ChangedAt": "2025-09-04T13:59:13Z"
}
```

**Headers:**
- `Last-Modified`: Timestamp of last update in HTTP-date format (e.g., `Mon, 26 Jan 2026 02:53:41 GMT`)

### Update Producer

**Endpoint:** `PUT /producer/:producerId`

**Description:** Updates a producer's name and/or token.

**Path Parameters:**
- `producerId`: Producer identifier

**Headers:**
- `Content-Type`: `application/x-www-form-urlencoded` (required)
- `If-Unmodified-Since`: Last modification timestamp in HTTP-date format (required, e.g., `Thu, 01 Jan 1970 00:00:00 GMT`)

**Form Parameters:**
- `token` (optional): New authentication token (auto-generated if not provided)
- `name` (optional): Producer name (defaults to producerId if not provided)

**Response:** Same as GET producer details

**Status Codes:**
- `200 OK`: Producer updated successfully
- `400 Bad Request`: Missing `If-Unmodified-Since` header
- `412 Precondition Failed`: `If-Unmodified-Since` header doesn't match
- `415 Unsupported Media Type`: Content-Type is not `application/x-www-form-urlencoded`

## Consumer Management

### List Consumers for a Channel

**Endpoint:** `GET /channel/:channelId/consumers`

**Description:** Retrieves a paginated list of all consumers for a specific channel.

**Path Parameters:**
- `channelId`: Channel identifier

**Query Parameters:**
- `previous` (optional): Pagination cursor for previous page
- `next` (optional): Pagination cursor for next page

**Response:**
```json
{
  "result": [
    "/channel/sample-channel/consumer/consumer-1",
    "/channel/sample-channel/consumer/consumer-2"
  ],
  "pages": {
    "next": "/channel/sample-channel/consumers?next=...",
    "previous": "/channel/sample-channel/consumers?previous=..."
  }
}
```

### Get Consumer Details

**Endpoint:** `GET /channel/:channelId/consumer/:consumerId`

**Description:** Retrieves details for a specific consumer.

**Path Parameters:**
- `channelId`: Channel identifier
- `consumerId`: Consumer identifier

**Response:**
```json
{
  "ID": "sample-consumer",
  "Name": "My Consumer",
  "Token": "consumer-token",
  "ChangedAt": "2025-09-04T13:59:14Z",
  "CallbackURL": "https://example.com/webhook",
  "DeadLetterQueueURL": "/channel/sample-channel/consumer/sample-consumer/dlq",
  "Type": "push"
}
```

**Headers:**
- `Last-Modified`: Timestamp of last update in HTTP-date format (e.g., `Mon, 26 Jan 2026 02:53:41 GMT`)

### Update Consumer

**Endpoint:** `PUT /channel/:channelId/consumer/:consumerId`

**Description:** Creates or updates a consumer for a channel.

**Path Parameters:**
- `channelId`: Channel identifier
- `consumerId`: Consumer identifier

**Headers:**
- `Content-Type`: `application/x-www-form-urlencoded` (required)
- `If-Unmodified-Since`: Last modification timestamp in HTTP-date format (required for updates, e.g., `Thu, 01 Jan 1970 00:00:00 GMT`)

**Form Parameters:**
- `token` (optional): Authentication token (auto-generated if not provided)
- `name` (optional): Consumer name (defaults to consumerId if not provided)
- `callbackUrl` (required): Absolute URL for push consumers or callback URL
- `type` (optional): Consumer type - `push` or `pull` (default: `push`)

**Response:** Same as GET consumer details

**Status Codes:**
- `200 OK`: Consumer updated successfully
- `400 Bad Request`: Missing required fields or invalid callback URL
- `404 Not Found`: Channel does not exist
- `412 Precondition Failed`: `If-Unmodified-Since` header doesn't match
- `415 Unsupported Media Type`: Content-Type is not `application/x-www-form-urlencoded`

### Delete Consumer

**Endpoint:** `DELETE /channel/:channelId/consumer/:consumerId`

**Description:** Deletes a consumer from a channel.

**Path Parameters:**
- `channelId`: Channel identifier
- `consumerId`: Consumer identifier

**Headers:**
- `If-Unmodified-Since`: Last modification timestamp in HTTP-date format (required, e.g., `Thu, 01 Jan 1970 00:00:00 GMT`)

**Status Codes:**
- `204 No Content`: Consumer deleted successfully
- `404 Not Found`: Consumer does not exist
- `412 Precondition Failed`: `If-Unmodified-Since` header doesn't match

## Message Broadcasting

### Broadcast Message

**Endpoint:** `POST /channel/:channelId/broadcast`

**Description:** Broadcasts a message to all consumers of a channel. Messages can be broadcast immediately or scheduled for future delivery.

**Path Parameters:**
- `channelId`: Channel identifier

**Headers (Required):**
- `X-Broker-Channel-Token`: Channel authentication token
- `X-Broker-Producer-Token`: Producer authentication token
- `X-Broker-Producer-ID`: Producer identifier

**Headers (Optional):**
- `Content-Type`: Message content type (default: `application/octet-stream`)
- `X-Broker-Message-Priority`: Message priority (integer, default: 0, higher = higher priority)
- `X-Broker-Message-ID`: Custom message ID (auto-generated if not provided)
- `X-Broker-Scheduled-For`: Schedule message for future delivery (ISO-8601/RFC3339 format, e.g., `2025-01-27T15:30:00Z`)
- `X-Broker-Metadata-Headers`: Comma-separated list of header names to include as metadata

**Request Body:** Message payload (any format)

**Response Headers:**
- `Location`: URL of the created message (immediate) or scheduled message (scheduled)

**Status Codes:**
- `201 Created`: Message accepted for broadcast
- `400 Bad Request`: Invalid scheduled date format
- `401 Unauthorized`: Producer not found
- `403 Forbidden`: Channel or producer token mismatch
- `404 Not Found`: Channel not found
- `409 Conflict`: Duplicate message ID for channel
- `412 Precondition Failed`: Scheduled time is less than 2 minutes in the future

**Example (Immediate Broadcast):**
```bash
curl -X POST http://localhost:8080/channel/sample-channel/broadcast \
  -H "X-Broker-Channel-Token: channel-token" \
  -H "X-Broker-Producer-Token: producer-token" \
  -H "X-Broker-Producer-ID: sample-producer" \
  -H "Content-Type: application/json" \
  -d '{"event": "user.created", "userId": 123}'
```

**Example (Scheduled Broadcast):**
```bash
curl -X POST http://localhost:8080/channel/sample-channel/broadcast \
  -H "X-Broker-Channel-Token: channel-token" \
  -H "X-Broker-Producer-Token: producer-token" \
  -H "X-Broker-Producer-ID: sample-producer" \
  -H "X-Broker-Scheduled-For: 2025-01-27T15:30:00Z" \
  -H "Content-Type: application/json" \
  -d '{"event": "scheduled.notification"}'
```

## Message Management

### Get Message Details

**Endpoint:** `GET /channel/:channelId/message/:messageId`

**Description:** Retrieves details of a specific message including all delivery jobs.

**Path Parameters:**
- `channelId`: Channel identifier
- `messageId`: Message identifier

**Response:**
```json
{
  "id": "message-123",
  "payload": "{\"event\": \"user.created\"}",
  "contentType": "application/json",
  "producedBy": "My Producer",
  "receivedAt": "2025-01-27T10:00:00Z",
  "dispatchedAt": "2025-01-27T10:00:01Z",
  "status": "DISPATCHED",
  "headers": {
    "X-Custom-Header": "value"
  },
  "jobs": [
    {
      "listenerEndpoint": "https://example.com/webhook",
      "listenerName": "My Consumer",
      "status": "delivered",
      "statusChangedAt": "2025-01-27T10:00:02Z"
    }
  ]
}
```

### List Messages for a Channel

**Endpoint:** `GET /channel/:channelId/messages`

**Description:** Retrieves a paginated list of messages for a channel with optional status filtering.

**Path Parameters:**
- `channelId`: Channel identifier

**Query Parameters:**
- `previous` (optional): Pagination cursor for previous page
- `next` (optional): Pagination cursor for next page
- `status` (optional, repeatable): Filter by message status (integer values)

**Message Status Values:**
- `101`: ACKNOWLEDGED (message received but not yet dispatched)
- `102`: DISPATCHED (message dispatched to consumers)

**Response:**
```json
{
  "result": [
    "/channel/sample-channel/message/msg-1",
    "/channel/sample-channel/message/msg-2"
  ],
  "pages": {
    "next": "/channel/sample-channel/messages?next=...",
    "previous": "/channel/sample-channel/messages?previous=..."
  }
}
```

### Get Message Status Counts

**Endpoint:** `GET /channel/:channelId/messages-status`

**Description:** Retrieves message status counts for a channel, including both regular and scheduled messages.

**Path Parameters:**
- `channelId`: Channel identifier

**Response:**
```json
{
  "counts": {
    "ACKNOWLEDGED": {
      "Count": 5,
      "Links": {
        "messages": "/channel/sample-channel/messages?status=101"
      }
    },
    "DISPATCHED": {
      "Count": 50,
      "Links": {
        "messages": "/channel/sample-channel/messages?status=102"
      }
    },
    "SCHEDULED_PENDING": {
      "Count": 3,
      "Links": {
        "messages": "/channel/sample-channel/scheduled-messages?status=201"
      }
    },
    "SCHEDULED_DISPATCHED": {
      "Count": 1,
      "Links": {
        "messages": "/channel/sample-channel/scheduled-messages?status=202"
      }
    }
  },
  "next_scheduled_message_at": "2025-01-28T10:00:00Z"
}
```

## Scheduled Messages

### Get Scheduled Message Details

**Endpoint:** `GET /channel/:channelId/scheduled-message/:messageId`

**Description:** Retrieves details of a specific scheduled message.

**Path Parameters:**
- `channelId`: Channel identifier
- `messageId`: Scheduled message identifier

**Response:**
```json
{
  "id": "bt123abc456",
  "messageId": "scheduled-msg-123",
  "contentType": "application/json",
  "priority": 5,
  "producedBy": "producer-id",
  "dispatchSchedule": "2025-01-28T10:00:00Z",
  "dispatchedAt": null,
  "status": "PENDING",
  "payload": "{\"event\": \"scheduled.notification\"}",
  "headers": {
    "X-Custom-Header": "value"
  }
}
```

### List Scheduled Messages for a Channel

**Endpoint:** `GET /channel/:channelId/scheduled-messages`

**Description:** Retrieves a paginated list of scheduled messages for a channel with optional status filtering.

**Path Parameters:**
- `channelId`: Channel identifier

**Query Parameters:**
- `previous` (optional): Pagination cursor for previous page
- `next` (optional): Pagination cursor for next page
- `status` (optional): Filter by scheduled message status (integer value)

**Scheduled Message Status Values:**
- `201`: SCHEDULED/PENDING (scheduled but not yet dispatched)
- `202`: DISPATCHED (scheduled time reached and message dispatched)

**Response:**
```json
{
  "result": [
    "/channel/sample-channel/scheduled-message/scheduled-msg-1",
    "/channel/sample-channel/scheduled-message/scheduled-msg-2"
  ],
  "pages": {
    "next": "/channel/sample-channel/scheduled-messages?next=...",
    "previous": "/channel/sample-channel/scheduled-messages?previous=..."
  }
}
```

## Job Management

### Get Job Status Counts

**Endpoint:** `GET /job-status`

**Description:** Retrieves job status counts grouped by consumer.

**Authentication:** None required

**Response:**
```json
{
  "consumer-id": {
    "queued": 10,
    "inflight": 5,
    "delivered": 100,
    "dead": 2
  }
}
```

### Get Queued Jobs for Consumer (Pull-Based)

**Endpoint:** `GET /channel/:channelId/consumer/:consumerId/queued-jobs`

**Description:** Retrieves prioritized queued jobs for a pull-based consumer.

**Path Parameters:**
- `channelId`: Channel identifier
- `consumerId`: Consumer identifier

**Headers (Required):**
- `X-Broker-Channel-Token`: Channel authentication token
- `X-Broker-Consumer-Token`: Consumer authentication token

**Query Parameters:**
- `limit` (optional): Number of jobs to retrieve (default: 25, max: 100)

**Response:**
```json
{
  "Result": [
    {
      "ID": "bt123abc456",
      "Priority": 5,
      "IncrementalTimeout": 300,
      "Message": {
        "MessageID": "msg-123",
        "Payload": "{\"event\": \"user.created\"}",
        "Headers": {
          "X-Custom-Header": "value"
        },
        "ContentType": "application/json"
      }
    }
  ]
}
```

**Status Codes:**
- `200 OK`: Jobs retrieved successfully
- `400 Bad Request`: Invalid limit parameter
- `401 Unauthorized`: Consumer not found
- `403 Forbidden`: Channel or consumer token mismatch
- `404 Not Found`: Channel not found
- `412 Precondition Failed`: Consumer is not pull-based

### Get Job Details

**Endpoint:** `GET /channel/:channelId/consumer/:consumerId/job/:jobId`

**Description:** Retrieves detailed information about a specific delivery job.

**Path Parameters:**
- `channelId`: Channel identifier
- `consumerId`: Consumer identifier
- `jobId`: Job identifier

**Headers (Required):**
- `X-Broker-Channel-Token`: Channel authentication token
- `X-Broker-Consumer-Token`: Consumer authentication token

**Response:**
```json
{
  "ListenerEndpoint": "https://example.com/webhook",
  "ListenerName": "My Consumer",
  "Status": "QUEUED",
  "StatusChangedAt": "2025-01-27T10:00:00Z",
  "MessageURL": "/channel/sample-channel/message/msg-123",
  "ChannelURL": "/channel/sample-channel",
  "ConsumerURL": "/channel/sample-channel/consumer/sample-consumer",
  "ProducerURL": "/producer/sample-producer",
  "JobRequeueURL": "/channel/sample-channel/consumer/sample-consumer/job/bt123abc456/requeue-dead-job"
}
```

**Note:** `JobRequeueURL` is only included if the job status is `DEAD`.

### Update Job Status (Pull-Based)

**Endpoint:** `POST /channel/:channelId/consumer/:consumerId/job/:jobId`

**Description:** Updates the status of a delivery job. Used by pull-based consumers to transition jobs through their lifecycle.

**Path Parameters:**
- `channelId`: Channel identifier
- `consumerId`: Consumer identifier
- `jobId`: Job identifier

**Headers (Required):**
- `X-Broker-Channel-Token`: Channel authentication token
- `X-Broker-Consumer-Token`: Consumer authentication token
- `Content-Type`: `application/json`

**Request Body:**
```json
{
  "nextState": "INFLIGHT",
  "incrementalTimeout": 300
}
```

**Fields:**
- `nextState` (required): Target job state - `INFLIGHT`, `DELIVERED`, or `DEAD` (must be uppercase)
- `incrementalTimeout` (optional): Timeout in seconds (only valid when transitioning to `INFLIGHT`)

**Job State Transitions:**
- `QUEUED` → `INFLIGHT`: Mark job as being processed
- `INFLIGHT` → `DELIVERED`: Mark job as successfully delivered
- `INFLIGHT` → `DEAD`: Mark job as failed
- `DEAD` → `INFLIGHT`: Retry a dead job

**Status Codes:**
- `202 Accepted`: Job status updated successfully
- `400 Bad Request`: Invalid state transition or timeout parameter
- `401 Unauthorized`: Consumer not found or job doesn't belong to consumer
- `403 Forbidden`: Channel or consumer token mismatch
- `404 Not Found`: Job not found

**Example:**
```bash
curl -X POST http://localhost:8080/channel/sample-channel/consumer/sample-consumer/job/bt123abc456 \
  -H "X-Broker-Channel-Token: channel-token" \
  -H "X-Broker-Consumer-Token: consumer-token" \
  -H "Content-Type: application/json" \
  -d '{"nextState": "INFLIGHT", "incrementalTimeout": 300}'
```

## Dead Letter Queue (DLQ)

### Get Dead Jobs for Consumer

**Endpoint:** `GET /channel/:channelId/consumer/:consumerId/dlq`

**Description:** Retrieves all dead (failed) delivery jobs for a consumer.

**Path Parameters:**
- `channelId`: Channel identifier
- `consumerId`: Consumer identifier

**Query Parameters:**
- `previous` (optional): Pagination cursor for previous page
- `next` (optional): Pagination cursor for next page

**Response:**
```json
{
  "DeadJobs": [
    {
      "ListenerEndpoint": "https://example.com/webhook",
      "ListenerName": "My Consumer",
      "Status": "DEAD",
      "StatusChangedAt": "2025-01-27T10:00:00Z",
      "MessageURL": "/channel/sample-channel/message/msg-123",
      "JobRequeueURL": "/channel/sample-channel/consumer/sample-consumer/job/bt123abc456/requeue-dead-job"
    }
  ],
  "Pages": {
    "next": "/channel/sample-channel/consumer/sample-consumer/dlq?next=...",
    "previous": "/channel/sample-channel/consumer/sample-consumer/dlq?previous=..."
  }
}
```

### Requeue All Dead Jobs

**Endpoint:** `POST /channel/:channelId/consumer/:consumerId/dlq`

**Description:** Requeues all dead jobs for a consumer for another delivery attempt.

**Path Parameters:**
- `channelId`: Channel identifier
- `consumerId`: Consumer identifier

**Headers:**
- `Content-Type`: `application/x-www-form-urlencoded` (required)

**Form Parameters:**
- `requeue` (required): Must match the consumer's token

**Status Codes:**
- `202 Accepted`: Jobs requeued successfully
- `400 Bad Request`: Requeue token doesn't match consumer token
- `404 Not Found`: Consumer not found
- `415 Unsupported Media Type`: Content-Type is not `application/x-www-form-urlencoded`

### Requeue Single Dead Job

**Endpoint:** `POST /channel/:channelId/consumer/:consumerId/job/:jobId/requeue-dead-job`

**Description:** Requeues a specific dead job for another delivery attempt.

**Path Parameters:**
- `channelId`: Channel identifier
- `consumerId`: Consumer identifier
- `jobId`: Job identifier

**Headers (Required):**
- `X-Broker-Channel-Token`: Channel authentication token
- `X-Broker-Consumer-Token`: Consumer authentication token

**Status Codes:**
- `202 Accepted`: Job requeued successfully
- `400 Bad Request`: Job is not in dead status
- `401 Unauthorized`: Consumer not found
- `403 Forbidden`: Channel or consumer token mismatch
- `404 Not Found`: Job not found

## Pagination

Many list endpoints support pagination using cursor-based navigation. Pagination is controlled through query parameters:

**Query Parameters:**
- `previous`: Cursor to fetch the previous page of results
- `next`: Cursor to fetch the next page of results

**Response Structure:**
All paginated responses include a `Pages` object with navigation links (note: field names are capitalized):

```json
{
  "Result": [...],
  "Pages": {
    "next": "/endpoint?next=cursor-value",
    "previous": "/endpoint?previous=cursor-value"
  }
}
```

**Notes:**
- Only one of `previous` or `next` should be used per request
- Cursors are opaque values and should not be parsed or constructed manually
- If a pagination link is not present, there are no more results in that direction

## Response Codes

### Success Codes
- `200 OK`: Request successful, resource returned
- `201 Created`: Resource created successfully
- `202 Accepted`: Request accepted for processing
- `204 No Content`: Request successful, no content to return

### Client Error Codes
- `400 Bad Request`: Invalid request parameters or missing required fields
- `401 Unauthorized`: Authentication failed (e.g., producer not found)
- `403 Forbidden`: Authentication token mismatch
- `404 Not Found`: Resource not found
- `409 Conflict`: Resource conflict (e.g., duplicate message ID)
- `412 Precondition Failed`: Conditional update failed
- `415 Unsupported Media Type`: Incorrect Content-Type header

### Server Error Codes
- `500 Internal Server Error`: Unexpected server error

## Error Response Format

Error responses typically return plain text error messages in the response body:

```
Bad Request: Update is missing `If-Unmodified-Since` header
```

For debugging purposes, check the `X-Request-ID` header included in all responses to correlate with server logs.
