# Tech Spec: Header Propagation Support

|               |                    |
| ------------- | ------------------ |
| _Version_     | 1                  |
| _By_          | Munif Tanjim       |
| _Date_        | September 24, 2024 |
| _Last Update_ | September 24, 2024 |

## Problem Statement / Goal

For tracing the lifecycle of a message, from producer to the consumers, [trace context](https://www.w3.org/TR/trace-context/) needs
to be passed through webhook-broker. It is also sometimes desirable to pass some context/metadata along with the message. None of them
are currently possible.

The goal is to support metadata for messages by implementing HTTP Headers propagation when calling Consumer callbacks and expose them when reading Message. 

## Implementation Details

**Changes for `message` table**

- Add `headers` column

**Changes for `POST /channel/{channelId}/broadcast` endpoint**

- Accept `X-Broker-Metadata-Headers` header
- If those headers are present in request headers, store it in `headers` column of the message.

**Changes for `GET /channel/{channelId}/message/{messageId}` endpoint**

- If `headers` column has values, add those to the response body.

**Changes for Consumer Callbacks**

- If `headers` column has values for a message, add those to the request headers.

### Specifications

**`X-Broker-Metadata-Headers` header**

_Type_: `string`
_Format_: comma separate list of header names, whitespace not allowed
_Example_: `traceparent,tracestate`

**`headers` column**

_Type_: `JSON`
_Format_: json object where key/value pairs are both string
_Example_: `{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"}`
