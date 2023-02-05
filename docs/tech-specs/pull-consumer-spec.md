# Tech Spec: Adding Pull Consumer Support

<!-- |  |  |
| -- | -- |
| *Version* | 1 |
| *By* | Bishwajit Bhattacharjee |
| *Date* | February 5, 2023 |
| *Last Update* | February 5, 2023 | -->

## Problem Statement / Goal

In some use-cases pull based consumers are more desirable than push based ones. We want to give each consumer an option to register itself as push based or pull based. Furthermore, allowing to pull enables consumers to rate-limit/throttle consumption of events at their own convenience.

## Use cases

In most cases push based consumers are preferable. But it might not be ideal for events that require long processing. We might handle long running events in the push consumers in two ways:

- Process the event and then reply to the broker. For long running events, the broker will timeout before the processing completes.
- Reply success to the webhook-broker immediately and process the event in background. If we do so, the webhook-broker might overwhelm the consumer with events.

We can solve this problem with pull based consumers. With the support of Pull mechanism, a consumer can also rate-limit or throttle the consumption of events.

### High-level Flow

1. Each consumer [here](https://github.com/newscred/webhook-broker/blob/main/storage/data/consumer.go#L6) will be classified as either **Push** or **Pull** based on a boolean flag _IsPullConsumer_. Default value of the flag should be _False_ i.e: the consumers by default will be **Push** based.
1. We need to stop the enqueueing of the jobs [here](https://github.com/newscred/webhook-broker/blob/15a107c320b6f3c843863deca22b56e87f825fec/dispatcher/msgdispatcher.go#L100) for the pull consumers. Instead, we will just mark the jobs _JobQueued_ and open an endpoint [here](https://github.com/newscred/webhook-broker/blob/main/controllers/consumer.go) to the consumer to get the pending jobs that are in [_JobQueued_](https://github.com/newscred/webhook-broker/blob/15a107c320b6f3c843863deca22b56e87f825fec/storage/data/job.go#L13) status. This endpoint has to be paginated.
1. Consumers will be given the responsibility to change the status of the jobs through an http endpoint. Allowed State transitions are:

   - _JobQueued_ -> _JobInFlight_
   - _JobInFlight_ -> _JobDelivered_ (if successful)
   - _JobInFlight_ -> _JobDead_ (if failure)
   - _JobDead_ -> _JobInFlight_ (with retry count increased)

   Here, a job's status can be _JobDead_ if the consumer while processing the job decides it cannot process any furthur. The broker can also change the status of a job to _JobDead_ too if the consumer does not change the status for a certain period (Should be a long period).

### Newly Added Endpoints

We need to add two new endpoints for supporting the pull consumers -

1. GET /channel/{channel-id}/consumer/{consumer-id}/queued-jobs : Lists the jobs available in the channel for the consumer with jobstatus _JobQueued_. This endpoint will be paginated.
1. POST /channel/{channel-id}/consumer/{consumer-id}/job/{job-id} : Changes the status of the job according to the state machine. The desired state should be passed in data. Allowed _NextState_ values should be: `JobInFlight`, `JobQueued`, `JobDead`. The consumer should pass both the `X-Broker-Channel-Token` and `X-Broker-Consumer-Token` in the request header to verify its identity.

   ```javascript
   POST /channel/cfcvtt116477r2nkgvr0/consumer/cfcvtt116477r2nkgvqg/job/cfcvv0h16477r2nkh0rg
   Accept: application/json
   Content-Type: application/json
   X-Broker-Channel-Token: sample-channel-token
   X-Broker-Consumer-Token: sample-consumer-token
   Content-Length: 81

   {
       "NextState": "JobInFlight"
   }
   ```
