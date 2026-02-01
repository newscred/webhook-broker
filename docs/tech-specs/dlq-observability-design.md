# DLQ Observability: Design Document

## Problem

The `/job-status` endpoint runs a full `GROUP BY` across the entire `job` table joined with `consumer`. In production with 23M+ dead jobs, this query takes 60-108 seconds and hogs DB CPU. It is not suitable for frequent polling or alerting.

We need a lightweight way to monitor dead job counts per consumer for alerting via Datadog (Prometheus metrics) or Nagios (REST endpoint).

## Solution: Incremental Checkpoint with Summary Table

### New Table: `dlq_summary`

```sql
CREATE TABLE IF NOT EXISTS `dlq_summary` (
    `consumerId` VARCHAR(255) NOT NULL PRIMARY KEY,
    `consumerName` VARCHAR(255) NOT NULL,
    `channelId` VARCHAR(255) NOT NULL,
    `channelName` VARCHAR(255) NOT NULL,
    `dead_count` BIGINT NOT NULL DEFAULT 0,
    `last_checked_at` DATETIME(6) NOT NULL,
    CONSTRAINT `dlqSummaryConsumerRef` FOREIGN KEY (`consumerId`)
        REFERENCES consumer(`id`) ON UPDATE CASCADE ON DELETE CASCADE
);
```

Stores pre-computed dead job counts per consumer with channel/consumer names for easy consumption.

### Locking

Uses the existing `lock` table (same pattern as dispatcher/scheduler):

1. Create a `Lockable` entity with a fixed lock ID (`"dlq-summary-updater"`)
2. `TryLock` attempts `INSERT INTO lock` — if another instance holds it, returns `ErrAlreadyLocked` and the instance skips
3. Run the incremental count
4. `ReleaseLock` deletes the lock row

This ensures only one instance in the cluster runs the count at a time, without any new infrastructure.

### Background Goroutine (runs in every instance)

1. Wake up every **configurable interval** (default 10 minutes)
2. `TryLock("dlq-summary-updater")` — if `ErrAlreadyLocked`, go back to sleep
3. Read `MAX(last_checked_at)` from `dlq_summary`
4. If `now - last_checked_at < interval`, release lock and skip (prevents duplicate work across cluster)
5. Run incremental count query:
   ```sql
   SELECT consumerId, COUNT(id)
   FROM job
   WHERE status = 1004 AND statusChangedAt > :last_checked_at
   GROUP BY consumerId
   ```
   This only scans new dead rows since last checkpoint — fast regardless of total table size.
6. Upsert into `dlq_summary` — increment `dead_count`, set `last_checked_at = now`, populate `consumerName`/`channelId`/`channelName` from the `consumer`/`channel` tables (small lookup)
7. Update Prometheus `dead_job_count` gauge from summary data
8. Release lock

### Bootstrap (first run / empty table)

If `dlq_summary` is empty, do a one-time full count:

```sql
SELECT consumerId, COUNT(id)
FROM job
WHERE status = 1004
GROUP BY consumerId
```

Then populate the table with names from consumer/channel lookups. Subsequent runs are incremental.

### Requeue Decrement (immediate, in application code)

Requeue operations already have well-defined code paths in `storage/deliveryjobrepo.go`. After requeue, decrement the summary table immediately:

- **`RequeueDeadJobsForConsumer`**: `UPDATE dlq_summary SET dead_count = dead_count - :affected_rows WHERE consumerId = ?`
- **`RequeueDeadJob`**: `UPDATE dlq_summary SET dead_count = dead_count - 1 WHERE consumerId = ?`

This keeps the summary accurate for requeues without waiting for the next checkpoint. Using rows affected ensures correctness even if the job was already requeued by another process.

### Dead Job Deletion

Operators need the ability to discard dead jobs that are known to be unrecoverable, not just requeue them. Without delete, the DLQ grows unbounded. Deletion is only permitted for jobs that are in DEAD status **and** have exhausted retries (`retryAttemptCount >= maxRetryCount` from broker config). Two new endpoints:

**`DELETE /channel/{channelId}/consumer/{consumerId}/dlq`** — Purge all dead jobs for a consumer

- Requires `X-Broker-Channel-Token` and `X-Broker-Consumer-Token`
- In a single transaction: `DELETE FROM job WHERE consumerId = ? AND status = 1004 AND retryAttemptCount >= ?`, get rows affected, then `UPDATE dlq_summary SET dead_count = dead_count - :rows_affected WHERE consumerId = ?`

**`DELETE /channel/{channelId}/consumer/{consumerId}/job/{jobId}`** — Delete a single dead job

- Requires `X-Broker-Channel-Token` and `X-Broker-Consumer-Token`
- In a single transaction: `DELETE FROM job WHERE id = ? AND status = 1004 AND retryAttemptCount >= ?`, get rows affected (0 or 1), then `UPDATE dlq_summary SET dead_count = dead_count - :rows_affected WHERE consumerId = ?`

Both endpoints also decrement the Prometheus `dead_job_count` gauge by rows affected.

### API Endpoint: `GET /dlq-status`

Reads the `dlq_summary` table directly — instant response:

```json
{
  "consumers": [
    {
      "channelId": "order-events",
      "channelName": "Order Events",
      "consumerId": "payment-service",
      "consumerName": "Payment Service",
      "deadCount": 23469224
    }
  ]
}
```

### Prometheus Metric

```go
DeadJobCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
    Name: "dead_job_count",
    Help: "Number of dead jobs per consumer",
}, []string{"channel", "consumer"})
```

Updated from the summary table after each incremental count run, and decremented immediately on requeue.

### Alerting

- **Datadog**: Scrape `/metrics`, alert on `dead_job_count > threshold` per consumer
- **Nagios**: Poll `/dlq-status` REST endpoint, parse JSON, alert on count thresholds

## Job Status Reference

| Constant | DB Value |
|---|---|
| QUEUED | 1001 |
| INFLIGHT | 1002 |
| DELIVERED | 1003 |
| DEAD | 1004 |

**Note**: These values come from `iota + 1000` in `storage/data/job.go`, offset by 1 because `deliverJobLockPrefix` is the first entry in the const block consuming iota=0.

## Implementation Checklist

- [ ] Update OpenAPI spec (`docs/open-api-spec/api-spec.yaml`) with:
  - `GET /dlq-status` endpoint
  - `DELETE /channel/{channelId}/consumer/{consumerId}/dlq` endpoint
  - `DELETE /channel/{channelId}/consumer/{consumerId}/job/{jobId}` endpoint
  - `DLQStatus` response schema

## Related PRs

- **PR #246**: Adds index `(consumerId, status, statusChangedAt)` on `job` table — directly benefits the incremental count query
- **PR #251**: Adds index `(status, createdAt, id)` on `job` table — benefits status-filtered queries

Both should be merged (migration sequence conflict on 000011 needs resolution).
