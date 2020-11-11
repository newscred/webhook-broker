# Tech Spec: A Reliable Webhook Broker

|  |  |
| -- | -- |
| *Version* | 1 |
| *By* | Imran M Yousuf |
| *Date* | November 10, 2020 |

## Problem Statement / Goals

In a service oriented or microservice architecture the necessity to reliable pass messages between systems is essential. We want a reliable message broker without increasing architectural complexity with high throughput and scalability.

## Assumptions & Considerations

The assumptions into this tech spec are -

* All the consumers have an HTTP based interface available
* The interface or data model of webhook payload is agreed upon between producer and consumer
* The consumer will take care of out of order message being pushed

The considerations are -

* The broker will ensure at least once webhook event is delivered
* Consumer should have access to all the messages it failed to receive due to error on its part
* Consumer could access past messages for replaying purpose

## Key Concepts

| Concept | Definition |
| -- | -- |
| Channel | A channel is the broadcast highway for messages. |
| Producer | Producer is the system generating a message and broadcasting it to a channel |
| Consumer | Consumer is the system registering itself to listen to messages broadcasted to a specific channel |
| DLQ / Dead Letter Queue | A queue (collection) of messages that failed deliver to the consumer for non 2XX response from client |
| Message | The payload Producer wants distributed across the Consumer within a Channel |
| Message Delivery | Reaching the consumer with the message should be considered as delivery, in case of success it would be marked as consumed, else end up in dead letter queue |
| Message Status | Message has 2 status - Acknowledged and Out-for-delivery |
| Message Delivery Status | Message Delivery status is associated with a Message, Channel and Consumer combination and is a enumeration of - In-flight, Retry-Delivery, Retry-In-flight, Delivered, Dead |
| Rational-delay | Time delta for fail-safe mechanism to kick in |
| Delivery-Timeout | How long will we wait for message delivery to finish before we step in |

## Life-cycle of a Message

### High-level Flow

* Broker receives a **Message** addressed to a **Channel** from a **Producer**
* Broker retrieves the **Consumers** interested in broadcast of a **Channel**
* Broker _delivers_ **Message** to each **Consumer** retrieved in previous step

### Key milestones in that flow

* Acknowledge to **Producer** of receipt of **Message**
* Track _delivery status_ of the **Message** against each **Consumer**

### Non-trivial scenarios

* Broker crashed before sending the acknowledgement
* Broker crashed after sending the acknowledgement but before attempting to deliver the messages
* Broker crashed attempting to deliver to some/all consumers but succeeded with others (if any)

### Design considerations

Based on the flow, non-trivial scenarios we want to derive at the following design considerations -

* Acknowledgement should be tied to being able to store Message **and** sending the ACK signal, but not dependent on Producer received the ACK signal.
* There should be a fail-safe way to ensure Acknowledged messages are attempted to deliver in case synchronous triggering of delivery process fails
* Consumer may not be available, so we should have a retry policy

### One level deeper

So based on the above conversation lets elaborate the high-level flow to more concrete blocks and dive deep in them. The building blocks would be -

1. Receive Message
1. Start Delivery Process
1. Attempt Individual Delivery

Lets look into each of them.

#### Receive Message

* Start Transaction
* Store Message
* Commit Transaction
* Send ACK
* Trigger Delivery Process

The acknowledgement transmission is intentionally not part of the transaction, as there is little bearing on the Producer actually receiving it. The worst case scenario will be Producer will resend the message.

#### Start Delivery Process

There is 2 entry points to the process.

##### Triggered By Receipt Message

* Retrieve the message
* Retrieve the consumers
* Start transaction
* Create delivery status for each consumer
* Mark message out-for-delivery
* Commit transaction
* Trigger independent asynchronous delivery process

##### Triggered Fail-safe message

* Retrieve all messages older than rational-delay
* For each message use the Receipt Message trigger flow

#### Attempt Individual Delivery

For each message delivery we would have to follow the delivery lifecycle as designated by the _Message Delivery Status_. Here too, there will be 2 triggering functions -

* Triggered by fail-safe mechanism
  * Retrieve message deliveries in _Retry-Delivery_ with _next attempt timestamp_ past rational-delay
  * For each message delivery run the _Triggered by Start Delivery Process_ process
* Triggered by Start Delivery Process
  * Update message delivery status to _Retry-In-Flight_ if the status is _Retry-Delivery_
  * For delivery message retrieve consumer
  * Attempt to deliver message
  * Wait for the period of Delivery-Timeout
    * If timed out then update status as _Retry-Delivery_ with exponentially backed off _next attempt timestamp_
  * If work finishes before Timeout, mark status as _Delivered_
  * If consumer failed to connect, update status as _Retry-Delivery_ with exponentially backed off _next attempt timestamp_
  * If Max-Retries is reached and status is not _Delivered_ then mark delivery as _Dead_

#### Key Open Question

* Should be make it mandatory for **Producer** to associate a Message ID? It could be used to ensure that we do not resend a Message in case the Producer crashed before receiving the ACK. Or should we make it optional to keep the flexibility?
* Should there be a Signature of the Message passed in header for data integrity verification? Or should we make it optional to keep the flexibility?
* What is the rational-delay for fail-safe process to pick up Message and Message Delivery? Going with a minute could be too late in certain circumstance.
* What is a reasonable delivery-timeout?
* What is a reasonable Max-Retry limit?

## Implementation Details

TBD
