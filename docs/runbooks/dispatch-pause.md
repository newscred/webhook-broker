# Runbook: Pausing and resuming dispatch

## When to use this

- A downstream consumer is down, being migrated or misbehaving, and pushing to it would only use up retries and fill
  the DLQ.
- A broker-wide incident (shared downstream outage, DB maintenance, bad deploy) needs every delivery stopped while
  producers keep publishing.

A pause never drops messages. Broadcasts are accepted as usual; jobs for paused consumers are **parked** in the
`job_parking` table and delivered after resume. Design: [tech spec](../tech-specs/dispatch-pause-spec.md).

## Preconditions

- **Every broker node and the prune job run a version with dispatch pause.** Older nodes ignore pauses and deliver;
  an older prune binary does not know about parking (the `job_parking.messageId` foreign key makes it fail rather than
  delete, but do not rely on that).
- For global pauses and the cluster-wide hand-off: `[dispatch-pause] admin-token` is set on every node (the same
  value) and `[broker] retrigger-base-endpoint` points at the load balancer.

## Pause

Consumer (channel and consumer tokens; works for push and pull consumers):

```bash
curl -X PUT "$BROKER/channel/$CHANNEL/consumer/$CONSUMER/dispatch-pause" \
  -H "X-Broker-Channel-Token: $CHANNEL_TOKEN" -H "X-Broker-Consumer-Token: $CONSUMER_TOKEN" \
  --data-urlencode "reason=downstream migration CHG-1234"
```

Global (admin token):

```bash
curl -X PUT "$BROKER/dispatch-pause" -H "X-Broker-Admin-Token: $ADMIN_TOKEN" --data-urlencode "reason=incident INC-42"
```

Both are idempotent: pausing again returns the original pause (original `pausedAt` and `reason`).

The pause takes effect immediately on every node: hand-outs are checked in the database. Deliveries already in flight
finish normally. Pull consumers get an empty `queued-jobs` list with `X-Broker-Dispatch-Paused: true` and `409` when
moving a job to `INFLIGHT`; they may still complete jobs they already hold.

## Verify

```bash
curl "$BROKER/dispatch-pauses" -H "X-Broker-Admin-Token: $ADMIN_TOKEN"
curl "$BROKER/channel/$CHANNEL/consumer/$CONSUMER/dispatch-pause" \
  -H "X-Broker-Channel-Token: $CHANNEL_TOKEN" -H "X-Broker-Consumer-Token: $CONSUMER_TOKEN"
```

Metrics (`/metrics` on any node):

- `dispatch_paused{channel,consumer}` = 1 per paused consumer; the global pause is `{channel="*",consumer="*"}`.
- `parked_job_count{channel,consumer}` (refreshed at most once a minute by the drain lock holder).
- `parked_jobs_total{reason}` grows while the pause holds jobs back.

Suggested alerts: `dispatch_paused == 1` for more than 4h (forgotten pause); `parked_job_count` not decreasing 10
minutes after a resume (stuck drain); `rate(handoff_failures_total[5m]) > 0`.

## Before resuming: size the backlog

The backlog drains at roughly `drain-page-size` jobs per small transaction (thousands per second) and is spread across
the cluster. Check the size and make sure the downstream can absorb it:

```bash
curl "$BROKER/dispatch-pauses" -H "X-Broker-Admin-Token: $ADMIN_TOKEN"   # parked[].count per consumer
```

```sql
-- QUEUED=1001, INFLIGHT=1002, DELIVERED=1003, DEAD=1004 (parked jobs are in job_parking, not job)
SELECT c.channelId, c.consumerId, COUNT(*) AS parked, MIN(p.parkedAt) AS oldest
FROM job_parking p JOIN consumer c ON c.id = p.consumerId
GROUP BY c.channelId, c.consumerId;
```

For a very large backlog on a fragile downstream, consider lowering `drain-page-size` / `handoff-concurrency` first
(config change restarts the node).

## Resume

```bash
curl -X DELETE "$BROKER/channel/$CHANNEL/consumer/$CONSUMER/dispatch-pause" \
  -H "X-Broker-Channel-Token: $CHANNEL_TOKEN" -H "X-Broker-Consumer-Token: $CONSUMER_TOKEN"
curl -X DELETE "$BROKER/dispatch-pause" -H "X-Broker-Admin-Token: $ADMIN_TOKEN"
```

Resuming globally does not resume consumers that have their own pause, and resuming a consumer does not restart
delivery while a global pause is active.

## Watch the drain

- `drained_jobs_total{consumer}` rises on the node holding the drain lock.
- `handoff_requests_total{outcome="accepted"}` rises on the draining node; `busy` means receivers were above
  `handoff-high-water-mark` and the batch was retried through the load balancer.
- `GET /dispatch-pauses` keeps listing the consumer under `parked` (with `paused: false`) until parking is empty.
- Logs: `draining parked jobs` / `finished draining parked jobs` (Info), `lost the drain lock` /
  `all hand-off attempts failed` (Warn).

If a hand-off is lost (node crash, network), the jobs are already `QUEUED` and the retry worker delivers them after
`handoff-lease-seconds`. Nothing needs to be done manually.

### Manual check: node restart during a drain

Not covered by the automated integration tests. In a test environment:

1. Pause a consumer, broadcast a few thousand messages, resume.
2. While `parked_job_count` is still decreasing, restart one broker node (e.g. `docker restart ibroker2`).
3. Confirm the drain finishes (parking empty), the consumer receives every message (at least once) and no job id is in
   both tables:

   ```sql
   SELECT COUNT(*) FROM job j JOIN job_parking p ON j.id = p.id;  -- expect 0
   ```

## Rollback

1. Resume every pause (`GET /dispatch-pauses` must show no pauses).
2. Wait until parking is empty: `SELECT COUNT(*) FROM job_parking;` returns 0.
3. Deploy the previous version.
4. Only if required, run the down migration for `000014`. It drops `job_parking` and `dispatch_pause`; it must only run
   when `job_parking` is empty.

---
*Generated with assistance from Claude AI*
