# Tech Spec: Dispatch Pause with Job Parking

|  |  |
| -- | -- |
| *Version* | 2 |
| *By* | Imran M Yousuf |
| *Date* | November 3, 2026 |
| *Last Update* | November 4, 2026 |
| *Status* | Accepted |

## 1. Problem Statement / Goal

Operators need to **keep accepting events while stopping delivery**, either for one consumer or for the whole broker:

* A downstream consumer is down, being migrated, or misbehaving. Pushing to it only uses up retries and fills the DLQ.
* A broker-wide incident (a shared downstream outage, DB maintenance, a bad deploy) needs every delivery stopped while producers keep publishing.

Today the only options are destructive or indirect:

| Option | What goes wrong |
| -- | -- |
| Point the callback at a dead URL | Every attempt uses a retry. Jobs go DEAD and need a manual requeue. |
| Delete the consumer | Messages are lost for that consumer, and deletion is impossible while it has jobs (`consumerJobRef ON DELETE RESTRICT`). |
| Switch the consumer to `type=pull` | Can be stale for up to 4h on other nodes (§4.5), exposes the jobs to any puller, and has no global form. |
| Stop the broker | Stops ingestion too. |

**Goal:** a reversible, cluster-wide **dispatch pause** in two scopes, per consumer and global. While paused, broadcasts are accepted and stored as usual, and the jobs for paused consumers are **parked** in a dedicated `job_parking` table. A background **drainer** notices when parked jobs belong to a consumer that is no longer paused. One node at a time moves them back into `job` in roughly first-in-first-out order and hands them to the cluster through the load-balanced broker endpoint, so the delivery work is spread across nodes.

## 2. Assumptions, Considerations and Non-goals

* **At-least-once delivery is preserved.** A pause defers delivery; it never drops a job.
* A pause never interrupts an attempt that is already `INFLIGHT`.
* **Pause takes effect immediately across the cluster.** The decision to hand out a job is checked in the database (§9.3). The per-node cache (§8) is only an optimisation.
* Parking stores **job rows only**. Job rows are lightweight (ids, timestamps, counters). The heavy row is `message` (payload, headers), and it never moves.
* Both dialects (MySQL 8, SQLite3) are supported. All SQL in this spec is portable; dialect differences live only in migration templates (§4.10).
* Ordering: the broker still makes **no delivery-order guarantee** (see `basic-spec.md`). The drain moves jobs back into `job` roughly oldest first, per consumer (§10.4).

**Non-goals (v1):** channel-scoped pauses, timed pauses ("pause until T"), pausing ingestion, rate-limiting delivery to a consumer that has just recovered (see §22).

## 3. Key Concepts

| Concept | Definition |
| -- | -- |
| Dispatch | Handing a `QUEUED` job to its consumer. For push consumers that is the HTTP push; for pull consumers it is the `QUEUED → INFLIGHT` transition through the API. |
| Pause | A row in `dispatch_pause`, scoped to one consumer or global. Deleting the row resumes. |
| Effectively paused | A consumer is effectively paused if its own pause row **or** the global row exists. |
| Parked job | A job of an effectively paused consumer, stored in `job_parking` instead of `job`. |
| Park | Write a job into `job_parking`, either when the message is dispatched or by moving a `QUEUED` row out of `job` (a **re-park**). |
| Drain | Move parked jobs of a consumer that is no longer paused back into `job` as `QUEUED`, one page at a time. |
| Hand-off | After a drain page is committed, POST the job IDs to the load-balanced broker so that some node puts them in its in-memory queue. |
| Pause registry | Each node's in-memory snapshot of `dispatch_pause`, refreshed periodically. |

## 4. Analysis of the Existing Code

### 4.1 Paths that move a job toward a consumer

```
Broadcast ──► BroadcastController.Post ──(201)── go Dispatcher.Dispatch(msg)     controllers/broadcast.go:146-148
Scheduler ─────────────────────────────────────► Dispatcher.Dispatch(msg)     scheduler/scheduler.go:172
recoverMessagesNotYetDispatched ───────────────► Dispatcher.Dispatch(msg)     dispatcher/msgdispatcher.go:121
                                                       │
                       createJobs: consumerRepo.GetList (NOT cached)          dispatcher/msgdispatcher.go:77
                       djRepo.DispatchMessage: INSERT jobs + msg→DISPATCHED, one tx   storage/deliveryjobrepo.go:29
                       queueJob: push only → jobQueue (chan, max-message-queue-size)   dispatcher/msgdispatcher.go:99
                                                       │
                       dispatchJob → PriorityQueue → asyncDequeueToWorker      dispatcher/msgdispatcher.go:294-306
                       Worker.deliverJob: MarkJobInflight → callConsumer → Delivered|Retry|Dead   dispatcher/worker.go:68

retryQueuedJobs (every rational-delay, default 2s)                            dispatcher/msgdispatcher.go:143
  GetJobsReadyForInflightSince: QUEUED ∧ earliestNextAttemptAt ≤ now-delay    storage/deliveryjobrepo.go:356
  push → inLockRun(queueJob) ; pull with retries exhausted → MarkQueuedJobAsDead
recoverJobsFromLongInflight: stale INFLIGHT → MarkJobRetry (→ QUEUED, retry+1) dispatcher/msgdispatcher.go:174

Pull:  GET  …/queued-jobs  → GetPrioritizedJobsForConsumer(QUEUED)          controllers/job.go:87
       POST …/job/{id}     → MarkJobInflight | MarkDeadJobAsInflight | Delivered | Dead   controllers/job.go:249
DLQ:   POST …/dlq (requeue all), POST …/job/{id}/requeue → DEAD→QUEUED       controllers/message.go:379, controllers/job.go:194
```

| # | Path | What changes in this design |
| -- | -- | -- |
| P1 | `Dispatch` → `createJobs` → `DispatchMessage` | Jobs for effectively paused consumers are inserted into `job_parking` **in the same transaction** as the other jobs and `SetDispatched` (§9.1). |
| P2 | `queueJob` | Parked jobs aren't in `job` and are never enqueued. Jobs in `job` whose consumer the snapshot shows as paused are re-parked instead of enqueued (§9.2). |
| P3 | `Worker.deliverJob` → `MarkJobInflight` | `MarkJobInflight` refuses in the DB if the consumer is effectively paused. On refusal the worker re-parks (§9.3). |
| P4 | `retryQueuedJobs` | Unchanged query. Push jobs of paused consumers are re-parked rather than enqueued, so they leave `job` and stop costing retry scans (§4.8, §9.4). |
| P5 | `recoverJobsFromLongInflight` | Unchanged. The job returns to `QUEUED`, then P4 re-parks it. |
| P6 | Scheduler / message recovery | Unchanged; both go through P1. |
| P7 | Pull `GET queued-jobs` | Returns empty with `X-Broker-Dispatch-Paused: true` while paused. Jobs created during the pause are already invisible because they're parked (§12.5). |
| P8 | Pull `POST job` → `INFLIGHT` | `MarkJobInflight` / `MarkDeadJobAsInflight` refuse in the DB, and the API returns `409` (§12.5). |
| P9 | DLQ requeue | Unchanged. A requeued job becomes `QUEUED` in `job`; for a paused push consumer, P4 re-parks it. |

### 4.2 Job rows are light, message rows are heavy

`job` holds 12 scalar columns (`storage/deliveryjobrepo.go:17`). Payload and headers live on `message`, and `DeliveryJob.Message` is loaded separately in batch (`populateJobsWithMessage`, `storage/deliveryjobrepo.go:127`). Copying a job between `job` and `job_parking` is therefore cheap, and parking never touches message payloads.

### 4.3 Transactions and atomicity

* `transactionalWrites(db, ops...)` runs the ops in one transaction and rolls back on the first error (`storage/rdbms.go:365`).
* `inTransactionExec(tx, …, expectedRows)` fails with `ErrNoRowsUpdated` if `expectedRows > 0` and the affected count differs (`storage/rdbms.go:342`). This is the tool used for "delete exactly N parked rows or abort".
* `DispatchMessage` already combines the job inserts with `SetDispatched` through `txContextKey` (`storage/deliveryjobrepo.go:29-58`, `storage/messagerepo.go:134`). Parking inserts join the same transaction the same way.
* `updateJobStatus` is a conditional `UPDATE … WHERE id like ? AND status = ?` through `transactionalSingleRowWriteExec`, which expects exactly 1 row (`storage/deliveryjobrepo.go:61`). The pause condition is added to this statement (§9.3).

### 4.4 Locks

* `TryLock` is an `INSERT INTO lock`; a duplicate key maps to `ErrAlreadyLocked` (`storage/lockrepo.go`).
* `TimeoutLocks(rationalDelay)` deletes **every** lock older than `rational-delay` (default **2s**). It's called from `recoverMessagesNotYetDispatched` on every node (`dispatcher/msgdispatcher.go:123`).
* So any lock held for longer than ~2s can be removed from under its holder. The drainer must **renew** its lock between pages (§10.2), and correctness must never rely on the lock alone (§10.3).

### 4.5 Consumer cache and consumer PUT

* `CachedConsumerRepository` keeps consumers for **4h** (`storage/rdbms.go:217`) and only invalidates the local node on `Store`/`Delete`. `getJobs` resolves `job.Listener` through it (`storage/deliveryjobrepo.go:158`).
* `ConsumerController.Put` replaces the whole row (`controllers/consumer.go:59-97`).
* Pause state therefore lives in its own table (`dispatch_pause`), never on `consumer`.

### 4.6 Code that reads `job` and must learn about parking

| Reader | Location | Decision |
| -- | -- | -- |
| Prune candidate query | `storage/messagerepo.go:41-49` (`pruneMessageQuery`) | **Must change. Without it, data is lost** (§4.7). |
| `DeleteMessagesAndJobs` | `storage/messagerepo.go:297` | Unchanged. A foreign-key `RESTRICT` from `job_parking.messageId` is the safety net. |
| `GetJobsForMessages` (prune archive) | `storage/deliveryjobrepo.go:251` | Unchanged; messages with parked jobs are never prune candidates. |
| `GetByID` | `storage/deliveryjobrepo.go:363`; used by job GET/POST, requeue and dead-job delete | Fall back to `job_parking`, returning `Status = JobParked` (§7.3). |
| `GetJobsForMessage` (message GET) | `storage/deliveryjobrepo.go:227`, `controllers/message.go:140` | The controller adds the message's parked jobs after paging through `job`. |
| `GetJobStatusCountsGroupedByConsumer` (`/job-status`) | `storage/deliveryjobrepo.go:382` | Add `PARKED` counts from `job_parking`. |
| `GetJobsForConsumer` (DLQ list) | `storage/deliveryjobrepo.go:328` | Unchanged (DEAD only; parked jobs are never dead). |
| `GetPrioritizedJobsForConsumer` (pull) | `storage/deliveryjobrepo.go:345` | Unchanged; the pause gate is in the controller (P7). |
| DLQ summary / dead counts | `storage/dlqsummaryrepo.go`, `GetDeadJobCountsSinceCheckpoint` | Unchanged. |
| Requeue / dead delete | `storage/deliveryjobrepo.go:293-327, 427-455` | Unchanged (they act on DEAD rows only). |

### 4.7 Pruning: a data-loss risk that must be fixed

`pruneMessageQuery` selects `DISPATCHED` messages for which **no `job` row has a status other than `DELIVERED`**. A message whose only undelivered job is parked has no such row, so it would be archived and deleted while still owed to the paused consumer. Fix (§7.4):

1. Add `AND NOT EXISTS (SELECT 1 FROM job_parking p WHERE p.messageId = m.id)` to `pruneMessageQuery`.
2. Add `job_parking.messageId → message(id) ON DELETE RESTRICT`. If an old prune binary runs during a rolling deploy (§16), its `DELETE FROM message` fails and the prune aborts with an error instead of deleting.

> SQLite only enforces foreign keys when the `foreign_keys` pragma is on. The default connection URL already sets `_foreign_keys=on` (`config/defaultconfig.go:6`), so the storage unit tests exercise the `RESTRICT` guard. The MySQL path is covered by the integration tests (§18.10).

### 4.8 Retry worker scan cost, and why parking beats filtering

`GetJobsReadyForInflightSince` pages, in pages of 100 ordered by `createdAt DESC, id DESC`, through every `QUEUED` job with `earliestNextAttemptAt ≤ now - delay` (`storage/deliveryjobrepo.go:184-220`), every 2 seconds. Neither of the candidate indexes, `retry_job (status, earliestNextAttemptAt, id, createdAt)` and `job_status_created_id (status, createdAt DESC, id DESC, statusChangedAt)`, contains `consumerId`. So a SQL filter that excludes paused consumers would still read and throw away every paused row on every cycle.

Parking takes those rows **out of `job`**, so the scan cost doesn't depend on how long a pause lasts. The only paused rows the retry worker ever sees are the few re-entering through P4/P5/P9, and P4 re-parks each of them on first sight.

### 4.9 The broker calling itself: `retrigger-base-endpoint`

`[broker] retrigger-base-endpoint` (`config/config.go:254`) is documented as *"the URL to the load balanced endpoint for the broker for retriggering jobs"*, and **nothing uses it today**. Its default `http://localhost:8080` matches the default `[http] listener=:8080`, so a single node calls itself out of the box, and a cluster points it at the load balancer. The hand-off (§11) uses it.

When `[gateway-auth] mode=hmac`, the HMAC middleware applies to every path not listed as exempt (`controllers/hmac_middleware.go`). Hand-off requests must therefore be **signed** with the shared secret, in addition to the admin token (§11.3).

### 4.10 Dialect constraints

* Repositories have no runtime dialect knowledge; only migrations are dialect-templated (`storage/dialect_source.go`).
* So the runtime SQL must avoid `UPDATE … LIMIT`, `SELECT … FOR UPDATE SKIP LOCKED`, `FOR SHARE`, `RETURNING` and upsert syntax. Everything in this spec uses plain `SELECT`, `INSERT … VALUES`, `INSERT … SELECT`, `DELETE … WHERE … IN (…)` and correlated `EXISTS` subqueries, all portable.
* The auto-increment column needs dialect templating in the migration (§7.1).

### 4.11 Lifecycle and wiring

* `HTTPServiceContainer` (`main.go:50-58`) holds the Dispatcher, Scheduler and DLQUpdater. The Scheduler and DLQUpdater are started after `initApp` and stopped before the Dispatcher (`main.go:281-289`).
* `NewMessageDispatcher` starts its workers in the constructor (`dispatcher/msgdispatcher.go:321`).
* The new **PauseRegistry** must be constructed, with its first refresh done, **before** the dispatcher. The **Drainer** starts and stops alongside the DLQUpdater.

## 5. Design Overview

```
                    ┌──────────── dispatch_pause (scope PK) ────────────┐
                    │  '__global__'  |  <consumer.id> …                  │
                    └───────────────▲───────────────────────▲───────────┘
            Pause/Resume API ───────┘                       │ refresh (5s) / direct DB checks
                                                            │
 Broadcast/Scheduler/Recovery ──► Dispatch(msg) ── tx ──┬─► job          (consumers not paused) ──► queueJob ──► worker
                                                        ├─► job_parking  (paused consumers)
                                                        └─► message.status = DISPATCHED
 worker / retry worker finds a paused consumer's QUEUED job ── tx ──► move job → job_parking   (re-park)

 Drainer (every node, one holds the renewable lock "job-parking-drainer"):
   consumers with parked rows AND not effectively paused
     └─ per page (≤ drain-page-size), one tx:  DELETE parked rows (expect N)  +  INSERT into job as QUEUED
          └─ hand-off: POST {retrigger-base-endpoint}/_internal/jobs/enqueue {jobIds}
                 └─ load balancer → some node → loads jobs → local in-memory queue → worker
             (if the hand-off fails: jobs are durable as QUEUED; the retry worker picks them up after the hand-off lease)
```

**State is detected by polling, not by events.** Nothing listens for a "resume" event. The drainer repeatedly asks: *which consumers have parked rows and are not effectively paused?* A node that restarts, misses a refresh, or sees pause → resume → pause in under a second still does the right thing. A job parked during the instant of a resume can't be stranded; the next drainer pass simply finds it.

## 6. Data Model

### 6.1 `dispatch_pause`

| Column | Type | Notes |
| -- | -- | -- |
| `scope` | VARCHAR(255) PK | `'__global__'` or the internal `consumer.id` |
| `consumerId` | VARCHAR(255) NULL | FK → `consumer(id)` `ON UPDATE CASCADE ON DELETE CASCADE`; NULL for global |
| `reason` | VARCHAR(1024) NOT NULL DEFAULT '' | free text, for audit |
| `pausedAt` | DATETIME NOT NULL | |

Index `dispatch_pause_consumer (consumerId)`. SQLite doesn't create FK indexes automatically; with this index both dialects behave the same.

### 6.2 `job_parking`

| Column | Type | Notes |
| -- | -- | -- |
| `seq` | BIGINT auto-increment PK | arrival order; tie-breaker for draining |
| `id` | VARCHAR(255) NOT NULL UNIQUE | **the job id**, kept across park/drain so API links stay stable |
| `messageId` | VARCHAR(255) NOT NULL | FK → `message(id)` `ON DELETE RESTRICT` (prune safety net, §4.7) |
| `consumerId` | VARCHAR(255) NOT NULL | FK → `consumer(id)` `ON DELETE RESTRICT` (same as `job`) |
| `dispatchReceivedAt` | DATETIME NOT NULL | copied |
| `retryAttemptCount` | INTEGER NOT NULL | copied; never changed by parking |
| `earliestNextAttemptAt` | DATETIME NOT NULL | copied (for information only) |
| `priority` | INTEGER NOT NULL | copied |
| `incrementalTimeout` | INTEGER NOT NULL | copied |
| `createdAt` | DATETIME NOT NULL | the job's original `createdAt`; drain order key |
| `parkedAt` | DATETIME NOT NULL | when parked or last re-parked |

Constraints and indexes:

* `UNIQUE (messageId, consumerId)` mirrors `job`. The move logic guarantees a job exists in exactly one of the two tables.
* `job_parking_drain (consumerId, createdAt, seq)` serves drain pages and `SELECT DISTINCT consumerId`.
* `job_parking_message (messageId)` serves the prune `NOT EXISTS` and the per-message job lists.

### 6.3 Migration `000014_create_dispatch_pause_and_job_parking`

```sql
-- up
CREATE TABLE IF NOT EXISTS `dispatch_pause` (
    `scope`      VARCHAR(255) NOT NULL PRIMARY KEY,
    `consumerId` VARCHAR(255) NULL,
    `reason`     VARCHAR(1024) NOT NULL DEFAULT '',
    `pausedAt`   DATETIME NOT NULL,
    CONSTRAINT `dispatchPauseConsumerRef` FOREIGN KEY (`consumerId`)
        REFERENCES consumer(`id`) ON UPDATE CASCADE ON DELETE CASCADE
);
CREATE INDEX `dispatch_pause_consumer` ON `dispatch_pause` (`consumerId`);

CREATE TABLE IF NOT EXISTS `job_parking` (
{{if eq .Dialect "mysql"}}
    `seq` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
{{else}}
    `seq` INTEGER PRIMARY KEY AUTOINCREMENT,
{{end}}
    `id`                    VARCHAR(255) NOT NULL,
    `messageId`             VARCHAR(255) NOT NULL,
    `consumerId`            VARCHAR(255) NOT NULL,
    `dispatchReceivedAt`    DATETIME NOT NULL,
    `retryAttemptCount`     INTEGER NOT NULL DEFAULT 0,
    `earliestNextAttemptAt` DATETIME NOT NULL,
    `priority`              INTEGER NOT NULL DEFAULT 0,
    `incrementalTimeout`    INTEGER NOT NULL DEFAULT 0,
    `createdAt`             DATETIME NOT NULL,
    `parkedAt`              DATETIME NOT NULL,
    UNIQUE (`id`),
    UNIQUE (`messageId`, `consumerId`),
    CONSTRAINT `jobParkingMessageRef`  FOREIGN KEY (`messageId`)  REFERENCES message(`id`)  ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT `jobParkingConsumerRef` FOREIGN KEY (`consumerId`) REFERENCES consumer(`id`) ON UPDATE CASCADE ON DELETE RESTRICT
);
CREATE INDEX `job_parking_drain`   ON `job_parking` (`consumerId`, `createdAt`, `seq`);
CREATE INDEX `job_parking_message` ON `job_parking` (`messageId`);

-- down
DROP TABLE IF EXISTS `job_parking`;
DROP TABLE IF EXISTS `dispatch_pause`;
```

Column types and FK actions mirror `job` (`000002_create_models_v1`, `000006_add_priority_to_job`, `000007_add_timeout_to_job`): `INTEGER` counters, `DATETIME` timestamps, `ON UPDATE CASCADE ON DELETE RESTRICT`. Both tables are **new and empty**, so the migration is instant and needs no online index build on `job`. The down migration drops parked jobs, so the rollback runbook (§16) requires `job_parking` to be empty first.

## 7. Data Layer

### 7.1 Models (`storage/data`)

```go
// dispatchpause.go
const GlobalDispatchPauseScope = "__global__"

type DispatchPause struct {
	Scope      string
	ConsumerID string // internal consumer.id; empty for global
	Reason     string
	PausedAt   time.Time
}
func (p *DispatchPause) IsGlobal() bool
func (p *DispatchPause) QuickFix() bool       // PausedAt = now if zero; trims Reason to 1024
func (p *DispatchPause) IsInValidState() bool // global ⇔ ConsumerID empty; consumer ⇒ Scope == ConsumerID
func NewGlobalDispatchPause(reason string) *DispatchPause
func NewConsumerDispatchPause(consumer *Consumer, reason string) (*DispatchPause, error)

// DrainLock implements Lockable; GetLockID() == "job-parking-drainer"
type DrainLock struct{}
```

### 7.2 The virtual `PARKED` status

```go
// job.go: a SEPARATE const block. The JobStatus iota block above MUST NOT be touched (see CLAUDE.md).
const (
	// JobParked is never persisted to job.status. It marks a DeliveryJob loaded from job_parking.
	JobParked    JobStatus = 1100
	JobParkedStr           = "PARKED"
)
```

* `JobStatus.String()` gains `case JobParked`.
* `DeliveryJob.IsInValidState()` keeps rejecting `JobParked`, so a parked job can never be written to `job` by accident.
* `QuickFix()` leaves `JobParked` unchanged, but parked jobs are never passed through job-table writes anyway.

### 7.3 `DispatchPauseRepository` (new, `storage/dispatchpauserepo.go`)

```go
type DispatchPauseRepository interface {
	Pause(p *data.DispatchPause) (*data.DispatchPause, error) // idempotent: on a duplicate key, return the existing row
	Resume(scope string) (bool, error)                        // true if a row was deleted
	Get(scope string) (*data.DispatchPause, error)
	GetAll() ([]*data.DispatchPause, error)
	IsEffectivelyPaused(consumerInternalID string) (bool, error) // SELECT 1 … WHERE scope IN (?, '__global__') LIMIT 1
}
```

`Pause` uses `INSERT` and maps duplicate-key error 1062 (and SQLite's unique-constraint error) through `normalizeDBError` (`storage/dberror_normalizer.go`), then returns the existing row with `Get`.

### 7.4 `JobParkingRepository` (new, `storage/jobparkingrepo.go`)

```go
type JobParkingRepository interface {
	// Re-park: atomically move one QUEUED job row from job to job_parking, only if its consumer is effectively paused.
	// Returns true if moved; false if the job wasn't QUEUED or the consumer isn't paused.
	ReparkQueuedJob(job *data.DeliveryJob) (bool, error)
	// Consumers (internal ids) that have parked rows and are not effectively paused.
	GetDrainableConsumerIDs() ([]string, error)
	// Atomically move up to pageSize parked jobs of the consumer into job as QUEUED (§10.3).
	DrainPage(consumerInternalID string, pageSize int, earliestNextAttemptAt time.Time) ([]*data.DeliveryJob, error)
	GetByID(id string) (*data.DeliveryJob, error)                 // Status = JobParked
	GetForMessage(message *data.Message) ([]*data.DeliveryJob, error)
	GetCountsByConsumer() (map[string]*data.StatusCount[data.JobStatus], error)
}
```

**Re-park SQL.** One transaction, portable:

```sql
INSERT INTO job_parking (id, messageId, consumerId, dispatchReceivedAt, retryAttemptCount,
                         earliestNextAttemptAt, priority, incrementalTimeout, createdAt, parkedAt)
SELECT id, messageId, consumerId, dispatchReceivedAt, retryAttemptCount,
       earliestNextAttemptAt, priority, incrementalTimeout, createdAt, ?
FROM job
WHERE id = ? AND status = <QUEUED>
  AND EXISTS (SELECT 1 FROM dispatch_pause WHERE scope IN (?, '__global__'));
-- rows == 0 → commit nothing, return false
DELETE FROM job WHERE id = ? AND status = <QUEUED>;   -- expect exactly 1, else ErrNoRowsUpdated → rollback
```

Under MySQL's REPEATABLE READ, `INSERT … SELECT` takes shared locks on the source `job` row. A concurrent `MarkJobInflight` on another node either commits first (then the `SELECT` matches 0 rows) or waits and then finds the row gone (it updates 0 rows). SQLite serialises writers. Either way, the job ends up in exactly one table.

**Drainable consumers:**

```sql
SELECT DISTINCT p.consumerId FROM job_parking p
WHERE NOT EXISTS (SELECT 1 FROM dispatch_pause d WHERE d.scope IN (p.consumerId, '__global__'));
```

This is served by `job_parking_drain`. MySQL can use a loose index scan, and `job_parking` is empty when nothing is paused.

### 7.5 `DeliveryJobRepository` changes

| Method | Change |
| -- | -- |
| **new** `DispatchMessageWithParking(message *data.Message, jobs []*data.DeliveryJob, parked []*data.DeliveryJob) error` | One transaction: multi-row `INSERT INTO job` (if any), multi-row `INSERT INTO job_parking` (if any), `SetDispatched`. The existing `DispatchMessage(message, jobs...)` keeps its signature and delegates with `parked = nil`, so its 32 existing test call sites stay unchanged (§18.1). |
| `MarkJobInflight` | Adds `AND NOT EXISTS (SELECT 1 FROM dispatch_pause WHERE scope IN (?, '__global__'))` with `job.Listener.ID`. It still expects 1 row, else `ErrNoRowsUpdated` (callers tell a pause apart from a race, §9.3 and §12.5). |
| `MarkDeadJobAsInflight` | Same pause condition (pull `DEAD→INFLIGHT`). |
| `GetByID` | On `sql.ErrNoRows`, fall back to `JobParkingRepository.GetByID`. To keep wiring simple, the parking lookup is implemented inside `DeliveryJobDBRepository`. |
| `GetJobStatusCountsGroupedByConsumer` | Merge in `PARKED` counts from `SELECT consumerId, COUNT(*), MIN(parkedAt), MAX(parkedAt) FROM job_parking GROUP BY consumerId` (channel from the consumer lookup). |
| **new** `GetQueuedJobsByIDs(ids []string) ([]*data.DeliveryJob, error)` | `… WHERE id IN (…) AND status = <QUEUED>`, chunked at 200 like `GetJobsForMessages`; messages batch-loaded through `populateJobsWithMessage`. Used by the hand-off receiver. |

The other `UPDATE`s stay unchanged: `MarkJobDelivered`, `MarkJobDead`, `MarkJobRetry` and `MarkQueuedJobAsDead` act on jobs already handed out, or on exhausted pull jobs.

### 7.6 `MessageRepository` change

`pruneMessageQuery` gains `AND NOT EXISTS (SELECT 1 FROM job_parking p WHERE p.messageId = m.id)`.

### 7.7 `LockRepository` change

New method `RenewLock(lock *data.Lock) error`: `UPDATE lock SET attainedAt = ? WHERE lockId = ?`, expecting 1 row. Zero rows means the lock was timed out and possibly taken by another node, so the holder must stop.

### 7.8 Wiring

* Add `GetDispatchPauseRepository()` and `GetJobParkingRepository()` to `DataAccessor` / `RelationalDBDataAccessor` (`storage/dataaccess.go`, `storage/rdbms.go:36-70`).
* Add `NewDispatchPauseRepository`, `NewJobParkingRepository` and their struct fields to `RDBMSStorageInternalInjector` (`storage/rdbms.go:214`), then regenerate `storage/wire_gen.go` (never edit it by hand).
* Mocks: `storage/mocks/DispatchPauseRepository.go`, `storage/mocks/JobParkingRepository.go`, plus regenerated `DeliveryJobRepository`, `LockRepository`, `DataAccessor`.

## 8. Pause Registry (per node)

`dispatcher/pause.go`:

```go
type PauseRegistry interface {
	IsGloballyPaused() bool
	IsEffectivelyPaused(consumerInternalID string) bool
	Snapshot() []*data.DispatchPause
	Apply(p *data.DispatchPause) // write-through after a local Pause API call
	Remove(scope string)         // write-through after a local Resume API call
	Refresh() error
	Start()
	Stop()
}
```

* The state is an immutable `{global bool; consumers map[string]*DispatchPause}` behind an `atomic.Pointer`, so reads take no lock. `Apply`/`Remove` copy and swap under a mutex.
* The constructor runs the first `Refresh()` synchronously, before the dispatcher is built. `Start()` launches the refresh loop (`[dispatch-pause] registry-refresh-interval-seconds`, default 5).
* If `Refresh()` fails, the last snapshot is kept and an error is logged.
* **The registry is never the final authority.** It routes work (park at creation, skip enqueue, re-park in the retry worker) and answers read-only API fields. Every hand-out is checked in the DB (§9.3). A stale snapshot costs at most a wasted enqueue or an extra park/drain round trip, never a delivery during a pause.

## 9. Dispatcher Changes

### 9.1 P1: park when the message is dispatched

`createJobs` splits consumers using `pauseRegistry.IsEffectivelyPaused(consumer.ID)`:

```go
jobs, parked := splitJobs(consumers, message) // parked: DeliveryJob values destined for job_parking
err = djRepo.DispatchMessageWithParking(message, jobs, parked)
for _, job := range jobs { queueJob(msgDispatcher, job) }
```

* If the snapshot is stale and says "not paused" while a pause exists, the job goes into `job`, is enqueued, and §9.3 refuses and re-parks it.
* If it's stale and says "paused" after a resume, the job is parked and the next drainer pass drains it.

### 9.2 P2: `queueJob`

```go
queueJob = func(d *MessageDispatcherImpl, job *data.DeliveryJob) {
	if job.Listener.Type != data.PushConsumer { return }
	if d.pauseRegistry.IsEffectivelyPaused(job.Listener.ID.String()) {
		reparkAsync(d, job) // JobParkingRepository.ReparkQueuedJob; metrics; never blocks the caller
		return
	}
	d.jobQueue <- NewJob(job)
}
```

### 9.3 P3: hand-out is checked in the database

`MarkJobInflight` refuses if the consumer is effectively paused (§7.5). In `deliverJob` (`dispatcher/worker.go:74`), a failed `MarkJobInflight` calls `ReparkQueuedJob(job)`:

* `true`: the job was paused and is now parked. Increment `parked_jobs_total{reason="worker"}` and return.
* `false`: the job wasn't `QUEUED`, because another worker or node took it or its status changed. Log as today and return.

This makes a pause **take effect immediately on every node**. The cost is two primary-key lookups on a table of a few rows inside an `UPDATE` that already exists. `Worker` receives the `JobParkingRepository` through `NewWorker`.

### 9.4 P4: retry worker

No query change. In the push branch of `retryQueuedJobs`, `queueJob` (§9.2) re-parks jobs of paused consumers. On a global pause, `retryQueuedJobs` still runs, so that exhausted pull jobs are still marked dead. Any push jobs it finds are re-parked, which removes them from future scans.

### 9.5 New dispatcher API (used by the hand-off receiver)

```go
type MessageDispatcher interface {
	Dispatch(message *data.Message)
	EnqueueJobs(jobs []*data.DeliveryJob) int // push jobs only, re-checks the registry; returns the number enqueued
	QueueDepth() int                          // len(jobQueue) + jobPriorityQueue.Len()
	Stop()
}
```

Regenerate `dispatcher/mocks/MessageDispatcher.go`.

## 10. Drainer

New package `parking/` (`parking/drainer.go`), with the same lifecycle shape as `dlq/updater.go`: `Start()`, `Stop()`, injected configuration, a panic if a dependency is nil.

### 10.1 Loop

Every `drain-interval-seconds` (default 1):

1. `consumers := JobParkingRepository.GetDrainableConsumerIDs()`. If empty, sleep.
2. `TryLock(DrainLock)`. On `ErrAlreadyLocked`, another node is draining, so sleep.
3. While holding the lock, go round-robin over `consumers`, one page per consumer per round:
   1. `jobs := DrainPage(consumer, drain-page-size, now + handoff-lease)` (§10.3).
   2. If no jobs, drop the consumer from this run.
   3. Queue `jobs` for hand-off (§11). This is asynchronous with bounded concurrency `handoff-concurrency`, so the loop doesn't wait on HTTP.
   4. `RenewLock(DrainLock)` after every page. If it fails, the lock was lost: stop the run and go back to sleep.
   5. Stop the run when all consumers are drained, `Stop()` is called, or the hand-off queue is full (backpressure, §11.2).
4. `ReleaseLock`, then refresh the drainer metrics (§14).

The default page size is 50 (configurable, e.g. 10–500). Round-robin keeps one huge backlog from starving the other consumers after a global resume.

### 10.2 Why a renewable lock

`TimeoutLocks` deletes locks older than `rational-delay` (§4.4). One page (one small transaction) takes milliseconds, so renewing after each page keeps the lock alive. If the holder dies, the lock lapses after ~2s and another node takes over. The lock gives the single drainer you asked for; correctness doesn't depend on it (§10.3).

### 10.3 Moving a page (one transaction)

```sql
-- 1. pick the page (oldest first; skip if the consumer was paused in the meantime)
SELECT seq, id, messageId, consumerId, dispatchReceivedAt, retryAttemptCount, priority, incrementalTimeout, createdAt
FROM job_parking p
WHERE p.consumerId = ?
  AND NOT EXISTS (SELECT 1 FROM dispatch_pause d WHERE d.scope IN (?, '__global__'))
ORDER BY p.createdAt, p.seq
LIMIT ?;
-- 2. claim it by deleting first: expect exactly N rows, else ErrNoRowsUpdated → rollback
DELETE FROM job_parking WHERE seq IN (?, ?, …);
-- 3. write the jobs back: expect exactly N rows
INSERT INTO job (id, messageId, consumerId, dispatchReceivedAt, statusChangedAt, earliestNextAttemptAt,
                 status, createdAt, updatedAt, priority, incrementalTimeout, retryAttemptCount)
VALUES (…, now, <handoff-at>, <QUEUED>, <original createdAt>, now, …, <original retryAttemptCount>), …;
COMMIT;
```

* **Deleting before inserting makes concurrent drainers harmless.** If the lock ever lapses and two nodes pick the same page, the second `DELETE` blocks on row locks until the first commits, then affects fewer than N rows and rolls back. A parked job can't be moved twice. The unique keys on `job (id)` and `job (messageId, consumerId)` are a second guard.
* **The write into `job` and the delete from `job_parking` are in one transaction.** A crash leaves the page either fully parked or fully queued.
* `earliestNextAttemptAt = now + handoff-lease` (default 60s) for push consumers, so the retry worker doesn't race the hand-off. If the hand-off is lost, the retry worker picks the jobs up after the lease. Pull consumers get `earliestNextAttemptAt = now` and **no hand-off**, because pull consumers fetch their own jobs.
* `retryAttemptCount`, `priority`, `createdAt` and the job `id` are kept. Parking never uses up retries.
* The consumer's type is read through the consumer cache at drain time. If it's stale, the outcome is still safe: a push job treated as pull is delivered by the retry worker after `now`; a pull job treated as push is ignored by `EnqueueJobs` (push only).

### 10.4 Ordering

* Within a consumer, pages are moved in `(createdAt, seq)` order: the original job creation time, with arrival order as the tie-breaker. A job re-parked from an in-flight retry keeps its original place instead of going to the back.
* Moves are serialised by the lock, so jobs **re-enter `job` roughly oldest first**. Delivery order is still not guaranteed: several nodes and workers deliver at once, and the in-memory priority queue reorders by `priority` within what is queued. This matches the broker's existing no-ordering guarantee.
* Auto-increment values are assigned at insert time, not commit time, so a concurrent dispatch transaction can occasionally commit a slightly earlier `seq` after a page was taken. The cost is minor reordering, never a loss.

## 11. Hand-off (Load-Balanced Distribution)

### 11.1 Internal endpoint

`POST /_internal/jobs/enqueue`, body `{"jobIds": ["…", …]}` (≤ `drain-page-size` ids).

1. Authenticate (§11.3).
2. If `QueueDepth() + len(jobIds) > handoff-high-water-mark` (default `max-message-queue-size / 2`), return `503 Service Unavailable` with `Retry-After: 1`.
3. `jobs := GetQueuedJobsByIDs(jobIds)`. Jobs no longer `QUEUED` (already taken, or re-parked) are silently skipped.
4. `n := Dispatcher.EnqueueJobs(jobs)`. Jobs of consumers that are paused again are re-parked, as in §9.2.
5. Return `202 {"accepted": n}`.

### 11.2 Sender (in the drainer)

* Target: `{retrigger-base-endpoint}/_internal/jobs/enqueue`. Use a dedicated `http.Client` with timeout `handoff-timeout-seconds` (default 5); don't reuse the consumer-delivery client.
* On `503`, a timeout or a connection error: retry up to `handoff-max-attempts` (default 3) with jittered backoff. Each retry goes back through the load balancer and usually lands on a different node.
* If every attempt fails: if `handoff-local-fallback=true` (default), call `Dispatcher.EnqueueJobs` locally. Otherwise give up; the jobs are durable as `QUEUED` and the retry worker picks them up after `handoff-lease`. Either way, increment `handoff_failures_total`.
* **Backpressure on the drainer.** Pending hand-off batches are bounded by `handoff-concurrency × 2`. When that's full, the drain run ends and the lock is released, so a slow cluster isn't flooded and other nodes can take over the drain.
* If `handoff-mode=local`, or no `admin-token` is configured (§11.3), pages go straight to `Dispatcher.EnqueueJobs` on the draining node, with a startup warning that drain traffic won't be spread across nodes.

### 11.3 Security

* The endpoint requires `X-Broker-Admin-Token` matching `[dispatch-pause] admin-token`, compared in constant time. If no token is configured, the endpoint returns `404` and the drainer uses local hand-off.
* When `[gateway-auth] mode=hmac`, the sender also signs the request (`X-Broker-Signature: t=…,v1=…` over `{timestamp}.{body}`, base64-decoded secret, the same as `controllers/hmac_middleware.go`). `/_internal/` is **not** added to the auth-exempt paths.
* The body is job IDs only, never payloads. The receiver only enqueues jobs that are already `QUEUED`, and the database check in §9.3 still applies. So even a forged call can't deliver to a paused consumer or create work.

## 12. Public API

### 12.1 Consumer pause

Auth: `X-Broker-Channel-Token` + `X-Broker-Consumer-Token` via `getConsumerWithValidation` (`controllers/job.go:139`).

| Method & Path | Behavior | Responses |
| -- | -- | -- |
| `PUT /channel/{channelId}/consumer/{consumerId}/dispatch-pause` | Pause. Optional form field `reason` (≤1024). Idempotent. | `200` pause model; `400`; `401/403/404` as today |
| `GET /channel/{channelId}/consumer/{consumerId}/dispatch-pause` | Current consumer pause. | `200`; `404` not paused |
| `DELETE /channel/{channelId}/consumer/{consumerId}/dispatch-pause` | Resume. The drainer picks the consumer up on its next pass. | `204`; `404` not paused |

### 12.2 Global pause

Auth: `X-Broker-Admin-Token`. If no `admin-token` is configured, these endpoints return `404`.

| Method & Path | Behavior | Responses |
| -- | -- | -- |
| `PUT /dispatch-pause` | Global pause. Optional `reason`. Idempotent. | `200` pause model |
| `DELETE /dispatch-pause` | Global resume. | `204`; `404` |
| `GET /dispatch-pauses` | All pauses from the DB, plus parked counts per consumer. Needs the admin token when one is configured, otherwise open (like `/dlq-status`). | `200` |

Pause model:

```json
{
  "scope": "consumer",
  "channelId": "order-events",
  "consumerId": "payment-service",
  "reason": "downstream migration CHG-1234",
  "pausedAt": "2026-11-03T15:04:05Z",
  "globallyPaused": false,
  "parkedJobCount": 18234
}
```

`GET /dispatch-pauses` response:

```json
{
  "globallyPaused": false,
  "pauses": [ { "...": "pause model" } ],
  "parked": [ { "channelId": "...", "consumerId": "...", "count": 120, "oldestParkedAt": "...", "paused": false } ]
}
```

`parked` includes consumers that are no longer paused but still draining, so operators can watch a drain progress.

### 12.3 Changes to existing endpoints

| Endpoint | Change |
| -- | -- |
| `GET …/consumer/{id}` | `ConsumerModel` gains `DispatchPaused bool` (effective pause, from the registry). `PUT` never changes pause state. |
| `GET …/message/{id}` | Job list includes parked jobs with `Status: "PARKED"` (`StatusChangedAt` = `parkedAt`). |
| `GET/POST …/job/{jobId}` | `GET` of a parked job shows `PARKED`. `POST` on a parked job returns `409` (`dispatch paused`). |
| `POST …/job/{jobId}/requeue`, `DELETE …/job/{jobId}` | Parked jobs aren't DEAD, so `400` / `404` as today. |
| `GET /job-status` | Adds `PARKED` status counts. |
| `GET /dlq-status` | Unchanged. |

### 12.4 Controller wiring

* New `controllers/dispatchpause.go`: `ConsumerDispatchPauseController`, `GlobalDispatchPauseController`, `DispatchPausesController`.
* New `controllers/internaljobs.go`: `InternalJobEnqueueController`.
* Add them to the `Controllers` struct, `ControllerInjector` (`controllers/router.go:35`) and `setupAPIRoutes` (`controllers/router.go:231`). Regenerate the root `wire_gen.go`.
* Pause and resume handlers call the repository, then the registry's `Apply`/`Remove`, then log an audit line (§14).

### 12.5 Pull consumers

* `GET queued-jobs` while effectively paused: `200 {"Result": []}` plus `X-Broker-Dispatch-Paused: true`. We return 200 so existing pull clients keep polling instead of erroring.
* `POST job` `QUEUED→INFLIGHT` or `DEAD→INFLIGHT`: `MarkJobInflight` / `MarkDeadJobAsInflight` refuse in the DB. The controller then calls `IsEffectivelyPaused` and returns `409 dispatch paused`, or `400 errInvalidTransitionRequest` as today if it isn't paused.
* `INFLIGHT→DELIVERED/DEAD` is still allowed, so work already in progress can finish.
* A pull consumer's `QUEUED` jobs that existed before the pause stay in `job` (nothing re-parks pull jobs) and are blocked by the two gates above. That avoids moving a potentially huge pull backlog.

## 13. Configuration

New section `[dispatch-pause]`, with interface `config.DispatchPauseConfig` (pattern: `[dlq]`, `config/config.go:675`):

```ini
[dispatch-pause]
admin-token=
registry-refresh-interval-seconds=5
drain-interval-seconds=1
drain-page-size=50
handoff-mode=http
handoff-lease-seconds=60
handoff-timeout-seconds=5
handoff-max-attempts=3
handoff-concurrency=4
handoff-high-water-mark=0
handoff-local-fallback=true
```

| Key | Default | Validation / meaning |
| -- | -- | -- |
| `admin-token` | empty | Empty disables the global endpoints and HTTP hand-off. |
| `registry-refresh-interval-seconds` | 5 | 1–300 |
| `drain-interval-seconds` | 1 | 1–60 |
| `drain-page-size` | 50 | 1–500 (bounded by placeholder limits; see `getJobsForMessagesChunkSize`) |
| `handoff-mode` | `http` | `http` or `local` |
| `handoff-lease-seconds` | 60 | ≥ `rational-delay` × 5 |
| `handoff-timeout-seconds` | 5 | 1–60 |
| `handoff-max-attempts` | 3 | 1–10 |
| `handoff-concurrency` | 4 | 1–64 |
| `handoff-high-water-mark` | 0 | 0 means `max-message-queue-size / 2` |
| `handoff-local-fallback` | true | |

Invalid values fall back to the default with a warning, as the existing sections do. The hand-off target is `[broker] retrigger-base-endpoint`; no new key is needed. Update `config/defaultconfig.go`, `docs/configuration.md` and `webhook-broker.cfg.template`.

## 14. Observability

| Metric | Type | Labels | Source |
| -- | -- | -- | -- |
| `dispatch_paused` | gauge | `channel`, `consumer` (`*`/`*` for global) | Rebuilt on each registry refresh (Reset + Set, as `dead_job_count` does) |
| `parked_job_count` | gauge | `channel`, `consumer` | Lock holder, at most once per 60s, from `GetCountsByConsumer` |
| `parked_jobs_total` | counter | `reason` = `dispatch` \| `worker` \| `retry` \| `handoff` | Each park |
| `drained_jobs_total` | counter | `consumer` | Drainer |
| `handoff_requests_total` | counter | `outcome` = `accepted` \| `busy` \| `error` \| `local` | Sender |
| `handoff_failures_total` | counter | | Sender, when all attempts failed |

Logs: `Info` on pause and resume (scope, channel, consumer, reason, request ID) as an audit trail; `Info` when a drain starts or finishes per consumer, with counts; `Warn` on hand-off fallback or a lost lock.

Suggested alerts: `dispatch_paused == 1 for > 4h` (a forgotten pause); `parked_job_count` not falling 10 min after a resume (a stuck drain); `rate(handoff_failures_total) > 0`.

## 15. Consistency and Race Analysis

The invariant: **every job exists in exactly one of `job` or `job_parking`; a paused consumer is never handed a job; no job is lost.**

| Scenario | Outcome |
| -- | -- |
| Broadcast while paused | Message `201` as usual; the paused consumer's job is parked in the same transaction as `SetDispatched`. |
| Pause while jobs sit in a node's in-memory queue | The worker's `MarkJobInflight` refuses in the DB, and the job is re-parked. |
| Pause while a job is `INFLIGHT` | The attempt finishes. On failure, `MarkJobRetry` makes it `QUEUED`, then the retry worker re-parks it. |
| Snapshot stale after a pause (another node) | Job created in `job` and enqueued, then refused in the DB and re-parked. No delivery. |
| Snapshot stale after a resume | Job parked; the next drainer pass moves it. Delay of at most `registry-refresh-interval` + `drain-interval`. |
| Resume during a dispatch transaction that read "paused" | The transaction commits parked rows after the resume; the next drainer pass finds them (polling, §5). Nothing is stranded, with no locking read needed. |
| Re-pause during a drain | The drain page `SELECT` skips paused consumers. Pages already moved: the hand-off receiver's `EnqueueJobs` or the worker's DB check re-parks them. |
| Two drainers (lock lapsed) | Delete-first page move (§10.3); the second rolls back. |
| Drainer crashes after the page transaction, before hand-off | Jobs are `QUEUED` with `earliestNextAttemptAt = now + lease`; the retry worker delivers after the lease. |
| Hand-off lost or receiver crashes | Same as above. |
| Hand-off delivered and the retry worker also enqueues after the lease | The conditional `QUEUED→INFLIGHT` lets only one win; no double delivery. |
| Re-park races `MarkJobInflight` on another node | Row locks serialise them; exactly one succeeds (§7.4). |
| Global and consumer pauses overlap | Effective-pause logic: drained only when neither applies. |
| Consumer deleted while paused | A consumer with jobs can't be deleted (`RESTRICT` on `job` and `job_parking`); its pause row cascades on deletion. |
| Prune during a pause | Messages with parked jobs are excluded (§4.7); the FK `RESTRICT` is a second guard. |
| Node restart | Registry reloads from the DB; parked rows are durable; the drainer resumes by polling. |
| Clock skew between nodes | Same exposure as today (`earliestNextAttemptAt` already uses node clocks). Leases are generous. |

## 16. Rollout, Compatibility and Rollback

* The migration is additive and instant (new empty tables).
* **Mixed versions during a rolling deploy:** old nodes don't know about pauses. They insert jobs into `job` and deliver them, and an old `prune` binary doesn't check parking. Therefore:
  * **Don't pause anything until every broker node and the prune job run the new version.** The runbook says so explicitly.
  * If an old prune does run, `job_parking.messageId ON DELETE RESTRICT` makes it fail instead of deleting (§4.7).
* **Rollback:**
  1. Resume all pauses.
  2. Wait until `GET /dispatch-pauses` shows no parked jobs (or `SELECT COUNT(*) FROM job_parking` = 0).
  3. Deploy the old version.
  4. Run the down migration if wanted. It drops `job_parking`, so it must be empty.
* New runbook `docs/runbooks/dispatch-pause.md`: when to pause; how to check (`/dispatch-pauses`, metrics); sizing a backlog before resuming; watching a drain; rollback steps.

## 17. Performance

| Concern | Assessment |
| -- | -- |
| Retry scans | Paused backlog is outside `job`, so there's no extra cost (§4.8). |
| Hand-out | Two PK lookups on a table of a few rows, inside the existing `MarkJobInflight` `UPDATE`. |
| Dispatch | Same transaction; one extra multi-row `INSERT` only when some consumer is paused. |
| Drain throughput | One small transaction per page plus async hand-off. With 50 jobs/page and ~10–20 ms per page, roughly 2.5k–5k jobs/s moved, then spread across nodes. Tunable via `drain-page-size`. |
| Drain memory | Bounded by page size × pending hand-off batches. Unlike a retry-worker drain, the backlog is never loaded at once. |
| `job_parking` size | Grows with the paused backlog; rows are small (§4.2). |
| Index changes on `job` | **None.** |
| `/job-status` | Adds one `GROUP BY` over `job_parking`; that endpoint is already a full scan. |

Verification: query-plan regression tests on both dialects (§18.4, §18.10).

## 18. Test Design

The goal is that every row of the race analysis (§15) and every failure mode has a named test that fails if the guarantee breaks, using the harnesses the repo already has, inside the existing CI budgets.

### 18.1 Existing harnesses we build on

| Layer | Existing harness | Reference |
| -- | -- | -- |
| Storage | Package `TestMain` creates a fresh SQLite DB through the **real migrations**, with FKs on (`_foreign_keys=on`). Package-level fixture setup functions. Subtests run with `t.Parallel()` and unique ID prefixes. `sqlmock` covers DB error paths. `EXPLAIN QUERY PLAN` regression tests. | `storage/rdbms_test.go:29`, `storage/channelrepo_test.go:75`, `storage/deliveryjobrepo_test.go:545` |
| Models | Table-style unit tests | `storage/data/job_test.go` |
| Dispatcher | `TestMain` with a real SQLite `DataAccessor`, plus a local consumer HTTP server on a free port with a per-consumer handler map. Package function variables (`callConsumer`, `deliverJob`, `queueJob`, …) are swapped and restored with `defer`. Mocked repos inject errors. Log output is captured into a buffer and asserted. | `dispatcher/msgdispatcher_test.go:50-140`, `dispatcher/worker_test.go:51` |
| Background services | Mocks only, millisecond intervals, the cycle method called directly, a table of nil-dependency panics | `dlq/updater_test.go` |
| Controllers | `TestMain` with SQLite, `createTestRouter(endpoints...)` + `httptest.NewRecorder`. Real repos for happy paths, mocks for errors; auth subtests. | `controllers/status_test.go:39-71`, `controllers/dlqdelete_test.go` |
| Config | INI fixtures; tests for defaults and wrong values; `TestConfigInterfaces` | `config/config_test.go` |
| Wiring | Startup and shutdown smoke test | `main_test.go` |
| Race | Tests named `*_Race`, run again with `-race` by `make test` / `make ci-test` | `Makefile:109,113`, `storage/memcache_test.go:71` |
| Integration | Sequential `testX()` functions in `integration-test/main.go`; consumer handler map; `waitTimeout`; polling loops; a unique `os.Exit` code per failure (last used: 109); direct MySQL access (`connectToMySQL`). Runs on MySQL 8 with **two broker nodes sharing one DB** (`ibroker`, `ibroker-hmac`). | `docker-compose.integration-test.yaml`, `integration-test/main.go:463` |

Constraints these impose:

* `-timeout 45s` per package, so every interval comes from an injected config fake in milliseconds, and no unit test sleeps longer than about 50ms at a time.
* Parallel subtests share one DB, so every test builds its own channel and consumers with a unique prefix and asserts only on its own rows.
* `make ci-test` passes `-short`, so randomized or long tests scale down under `testing.Short()`.

**Keep existing tests green without edits:**

* `DispatchMessage(message, jobs...)` keeps its signature (§7.5), so its 32 test call sites are untouched.
* `MarkJobInflight`'s signature is unchanged, so existing mock expectations still hold. Its new pause condition is a no-op when no pause rows exist, which is the case for all existing fixtures.
* Mocks are regenerated with `make generate` (mockery `--all`), never edited by hand.
* The one deliberate helper change is `getCompleteDispatcherConfiguration` in `dispatcher/msgdispatcher_test.go`, which gains the registry parameter.

### 18.2 New shared test helpers

These go in `storage/testutils.go`, which is exported so the dispatcher, controller and parking tests can reuse it, as they already do with `SetupForDeliveryJobTestsWithOptions`:

| Helper | Purpose |
| -- | -- |
| `SetupPauseFixture(prefix string, push, pull int) (*data.Channel, *data.Producer, []*data.Consumer)` | An isolated channel/producer/consumer set per test |
| `JobPlacement(db, messageID, consumerID) (inJob, inParking int)` | Where a job lives |
| `AssertParkingInvariants(t, db, consumerIDs...)` | (a) each `(messageId, consumerId)` appears in exactly one of `job` / `job_parking`; (b) `job ⋈ job_parking ON id` is empty; (c) no `job_parking` row for a consumer that isn't, and never was, paused during the test (the caller passes the set) |
| `InsertPauseRow(db, scope, consumerID)` / `DeletePauseRow` | Simulates another node pausing or resuming **behind the registry's back** (stale-snapshot tests) |

Package-local fakes (the `mockDLQConfig` pattern): `fakeDispatchPauseConfig` (millisecond durations), `fakePauseRegistry` (settable map), `handoffRecorder` (an `httptest.Server` that records batches and plays scripted responses such as 202, 503 or a hang past the timeout). Mockery mocks are generated for `PauseRegistry`, `DispatchPauseRepository`, `JobParkingRepository` and the new `MessageDispatcher` methods.

### 18.3 Model tests (`storage/data`)

| Test | Asserts |
| -- | -- |
| `TestDispatchPauseQuickFix` | `PausedAt` defaulted; `Reason` trimmed to 1024 |
| `TestDispatchPauseIsInValidState` (table) | global with a consumer id → invalid; consumer scope ≠ consumer id → invalid; empty scope → invalid; both valid forms → valid |
| `TestNewConsumerDispatchPause` | nil consumer → error; scope = `consumer.ID` |
| `TestDrainLockGetLockID` | `"job-parking-drainer"`; `NewLock` works (as in `TestDLQSummaryLockNewLock`) |
| `TestJobParkedString` | `"PARKED"` |
| `TestJobParkedNeverValidForPersistence` | `IsInValidState()` is false for a job with `Status = JobParked` |
| `TestJobStatusPersistedValues` | **Guard:** `JobQueued == 1001`, `JobInflight == 1002`, `JobDelivered == 1003`, `JobDead == 1004`, `JobParked == 1100`. This catches any accidental edit to the iota block. |

### 18.4 Repository tests (`storage`, real SQLite)

**`DispatchPauseRepository`** (`storage/dispatchpauserepo_test.go`)

| Test | Subtests and assertions |
| -- | -- |
| `TestDispatchPausePause` | `Consumer`; `Global`; `Idempotent` (a second call returns the original `pausedAt`/`reason`); `CascadeOnConsumerDelete` (a consumer without jobs is deleted, so its pause row disappears); `InvalidState` → `ErrInvalidStateToSave` |
| `TestDispatchPauseResume` | existing → `true`, row gone; missing → `false` |
| `TestDispatchPauseIsEffectivelyPaused` | table: none / consumer / global / both / another consumer's pause |
| `TestDispatchPauseGetAll` | returns global and consumer rows |
| `TestDispatchPauseDBErrors` | `sqlmock`: insert error, duplicate key mapped through `normalizeDBError`, select error |

**`JobParkingRepository` and the parking-aware `DeliveryJobRepository` methods** (`storage/jobparkingrepo_test.go`)

| Test | Subtests and assertions |
| -- | -- |
| `TestDispatchMessageWithParking` | `SplitAcrossTables` (counts in each table; message `DISPATCHED`); `AllParked`; `NoneParked` (same result as `DispatchMessage`); `AtomicRollback`: a pre-existing parking row for the same `(messageId, consumerId)` makes the parking insert fail, so **no** `job` rows exist and the message is still `ACKNOWLEDGED`; `sqlmock` failure at each of the three statements → rollback |
| `TestReparkQueuedJob` | `PausedConsumer` → `true`; every copied field equal (`id`, `retryAttemptCount`, `priority`, `createdAt`, `incrementalTimeout`); job row gone. `GlobalPause` → `true`. `NotPaused` → `false`, nothing moved. `NotQueued` (INFLIGHT, DEAD, DELIVERED) → `false`. `AlreadyParked` → `false`. `DeleteMismatchRollsBack` (`sqlmock`: INSERT 1 row, DELETE 0 rows → rollback, no parking row) |
| `TestGetDrainableConsumerIDs` | empty table; excludes paused; global → empty; resumed consumer included |
| `TestDrainPage` | `OrderByCreatedAtThenSeq` (crafted `createdAt`, including ties); `PageSizeLimit`; `RestoredFields` (status QUEUED, `earliestNextAttemptAt` = argument, `retryAttemptCount`/`id`/`createdAt`/`priority` kept); `PausedSinceSelection` → empty; `EmptyParking` → empty; `InsertConflictRollsBack` (a conflicting `job` row → error; parked rows still present); `DeleteShortRollsBack` (`sqlmock`: DELETE affects N-1 → rollback, no INSERT issued) |
| `TestDrainPage_ConcurrentDrainers_Race` | Two goroutines call `DrainPage` on the same consumer until empty. The union of returned ids equals the parked set with **no duplicates**; `AssertParkingInvariants` holds. |
| `TestReparkVsMarkJobInflight_Race` | For 50 jobs, `ReparkQueuedJob` and `MarkJobInflight` race per job while a pause row exists. Each job ends either parked or INFLIGHT (never both, never neither), and no INFLIGHT transition happened for a job of a paused consumer. |
| `TestMarkJobInflight_PauseGate` | no pause → OK; consumer pause → `ErrNoRowsUpdated`, row still QUEUED; global → same; after resume → OK |
| `TestMarkDeadJobAsInflight_PauseGate` | same matrix for DEAD → INFLIGHT |
| `TestGetByID_ParkedFallback` | parked job → `Status == JobParked`, `Message` and `Listener` loaded; missing from both → error |
| `TestGetQueuedJobsByIDs` | non-QUEUED skipped; unknown ids ignored; 450 ids (crosses the 200 chunk size) |
| `TestJobParkingGetForMessage` / `TestJobParkingGetCountsByConsumer` | contents and counts; min/max `parkedAt` |
| `TestGetJobStatusCountsGroupedByConsumer` (extended) | includes a `PARKED` entry |
| `TestJobParkingQueryPlans` | `EXPLAIN QUERY PLAN` (the existing pattern): the drain page query uses `job_parking_drain` with no `USE TEMP B-TREE FOR ORDER BY`; the drainable query uses `job_parking_drain`; the prune `NOT EXISTS` uses `job_parking_message`. The queries are exposed as package constants, like `prioritizedJobsForConsumerQuery`. |

**`MessageRepository` and `LockRepository`**

| Test | Asserts |
| -- | -- |
| `TestGetMessagesFromBeforeDurationThatAreCompletelyDelivered_ExcludesParked` | All `job` rows DELIVERED plus one parked → **not** returned. After drain and delivery → returned. |
| `TestDeleteMessagesAndJobs_ParkedRowRestricts` | Deleting a message that has a parked row fails (FK `RESTRICT`) and the whole batch rolls back. This is the safety net for an old prune binary. |
| `TestRenewLock` | held → OK and `attainedAt` advances; after `TimeoutLocks` removed it → `ErrNoRowsUpdated`; nil → `ErrNoLock` |

**Model-based invariant test:** `TestParkingInvariants_Randomized` (`storage`).

* A seeded random walk over a small fixture (3 push and 1 pull consumers). Operations: pause/resume a consumer, pause/resume global, dispatch a message (routed by the current pause state), re-park a random QUEUED job, drain a page, `MarkJobInflight` a random QUEUED job, `MarkJobDelivered` / `MarkJobRetry` a random INFLIGHT job.
* After **every** step it asserts:
  1. `AssertParkingInvariants`;
  2. `MarkJobInflight` never succeeded while the job's consumer was effectively paused;
  3. `retryAttemptCount` never changes across park and drain;
  4. the total job count per message equals the channel's consumer count.
* 500 steps normally, 50 under `-short`. The seed is logged with `t.Logf`, and `PARKING_INVARIANT_SEED` replays it.

### 18.5 Registry and dispatcher tests (`dispatcher`)

| Test | Asserts |
| -- | -- |
| `TestNewPauseRegistry` | the constructor refreshes once (mock repo); constructor error handling |
| `TestPauseRegistryEffectivePause` | matrix of none / consumer / global / both |
| `TestPauseRegistryWriteThrough` | `Apply` and `Remove` are visible immediately without a refresh |
| `TestPauseRegistryRefreshFailureKeepsSnapshot` | repo error → previous state kept; error logged (buffer assert) |
| `TestPauseRegistryStartStop` | millisecond interval; refresh called repeatedly; `Stop` doesn't hang |
| `TestPauseRegistryGauge` | `dispatch_paused` Reset + Set per refresh |
| `TestPauseRegistry_Race` | concurrent `IsEffectivelyPaused` readers with `Apply`, `Remove` and `Refresh` writers |
| `TestDispatchParksPausedConsumers` | Real DB plus the local consumer server: pause 2 push and 1 pull consumers in the registry, then `Dispatch`. Paused consumers' handlers are **never** invoked within 3× `rational-delay`; the others receive the message; the parking and job rows match. |
| `TestQueueJobReparksWhenSnapshotPaused` | `fakePauseRegistry` says paused: `ReparkQueuedJob` is called, `jobQueue` length unchanged, `parked_jobs_total{reason="retry"}` incremented |
| `TestDeliverJobPauseGateReparks` | Stale registry (not paused) plus a pause row inserted with `InsertPauseRow`. `callConsumer` is swapped for a stub that **fails the test if called**. After `deliverJob`, the job is in parking and the counter is incremented. |
| `TestDeliverJobInflightRaceNotParked` | Job already INFLIGHT, no pause → re-park returns false; same log line as today; no parking row |
| `TestRetryQueuedJobsReparksPausedPush` | A due QUEUED push job with a pause row: the first `retryQueuedJobs` moves it to parking; a second call doesn't see it (the scan no longer returns it) |
| `TestRetryQueuedJobsGlobalPauseStillMarksExhaustedPullDead` | Global pause: an exhausted pull job still goes DEAD; push jobs are re-parked |
| `TestEnqueueJobsAndQueueDepth` | push enqueued, pull skipped, paused re-parked; `QueueDepth` reflects the channel and the priority queue |
| `TestRecoverMessagesNotYetDispatchedWithPause` | An ACKNOWLEDGED, never-dispatched message plus a pause: recovery parks the paused consumer's job |

### 18.6 Drainer and hand-off tests (`parking`)

**Unit tests with mocks** (`parking/drainer_test.go`, the `dlq/updater_test.go` pattern; `runOnce()` is called directly):

| Test | Asserts |
| -- | -- |
| `TestNewDrainer` | a nil-dependency panic table |
| `TestDrainerStartStop` | no hang |
| `TestDrainRunNoDrainableSkipsLock` | `TryLock` not called |
| `TestDrainRunLockContention` | `ErrAlreadyLocked` → no `DrainPage` |
| `TestDrainRunRoundRobin` | Consumers A (3 pages), B (1), C (2) → `DrainPage` order A, B, C, A, C, A; `RenewLock` after every page; `ReleaseLock` once |
| `TestDrainRunRenewFailureStops` | `RenewLock` fails after page 2 → no page 3; `Warn` logged |
| `TestDrainRunBackpressureEndsRun` | full hand-off queue → run ends and the lock is released |
| `TestDrainRunPushUsesLease` / `TestDrainRunPullNoHandoff` | `earliestNextAttemptAt` argument ≈ now + lease for push, ≈ now for pull with no hand-off call (an injected clock function variable makes this exact) |
| `TestDrainRunDrainPageErrorContinues` | error on one consumer → logged; the others are still drained |

**Hand-off sender** (`parking/handoff_test.go`, using `handoffRecorder`):

| Test | Asserts |
| -- | -- |
| `TestHandoffSuccess` | `202` → `accepted`; request carries `X-Broker-Admin-Token` and a JSON body of ids |
| `TestHandoffSignsWhenHMACEnabled` | the receiving side verifies `X-Broker-Signature` with the same algorithm as `controllers/hmac_middleware.go` |
| `TestHandoffRetriesOn503` | 503, 503, 202 → 3 attempts (the backoff sleep is an injectable function variable, so no real sleeping) |
| `TestHandoffTimeout` | the server hangs past `handoff-timeout` (ms) → counted as a failed attempt |
| `TestHandoffAllFailLocalFallback` | fallback on → `Dispatcher.EnqueueJobs` called with the jobs; fallback off → not called; `handoff_failures_total` incremented |
| `TestHandoffLocalModeAndNoToken` | `handoff-mode=local`, or an empty `admin-token` → no HTTP request; local enqueue; startup warning logged |

**Component tests (real SQLite, real repositories):**

| Test | Asserts |
| -- | -- |
| `TestDrainerEndToEnd` | 120 parked jobs across 3 consumers, resumed; the drainer runs until parking is empty; every job handed off **exactly once** to `handoffRecorder`; all `job` rows QUEUED; within each consumer, batches arrive in non-decreasing `createdAt` order |
| `TestDrainerRepauseMidDrain` | Pause again after the first page: no further pages for that consumer; already-moved jobs are re-parked when `EnqueueJobs` runs on the receiving side |
| `TestDrainerTwoInstances_Race` | Two drainers on the same DB. A hook calls `TimeoutLocks` between pages to force the lock to lapse, so both actively drain. No job handed off twice; `AssertParkingInvariants` holds. |
| `TestDrainerCrashAfterPageBeforeHandoff` | The hand-off sender is stubbed to drop the batch. The jobs are QUEUED with `earliestNextAttemptAt` ≈ now + lease (ms in the test); once the lease passes, `GetJobsReadyForInflightSince` returns them. |

### 18.7 Controller tests (`controllers`)

| Controller | Tests |
| -- | -- |
| `ConsumerDispatchPauseController` | `GetPath` / `FormatAsRelativeLink`; PUT success (real repos) and idempotent repeat; `reason` > 1024 → 400; GET 200/404; DELETE 204/404; auth matrix (missing or wrong channel token, wrong consumer token, unknown consumer) using the same expected codes as the DLQ tests; registry `Apply`/`Remove` called (mock); repo error → 500 (mock) |
| `GlobalDispatchPauseController` | no `admin-token` configured → 404 for PUT/DELETE; wrong token → 401; correct token → 200/204; repeat PUT is idempotent |
| `DispatchPausesController` | open without a configured token; 401 without the header when a token is configured; includes `parked` entries for consumers that were resumed but are still draining |
| `InternalJobEnqueueController` | 404 without a configured token; 401 wrong token; 400 malformed JSON or more ids than `drain-page-size`; 503 with `Retry-After` when `QueueDepth` is above the high-water mark (mock dispatcher); 202 with `accepted` excluding non-QUEUED ids. HMAC variant: a router built with the real `NewHMACMiddleware` and `mode=hmac` → an unsigned request gets 401, proving `/_internal/` isn't exempt. |
| Existing controllers | `ConsumerController` GET `DispatchPaused` true/false. `MessageController` GET lists a `PARKED` job. `JobController` GET on a parked job → `PARKED`; POST on a parked job → 409; QUEUED → INFLIGHT while paused → 409 vs. a non-pause race → 400; DEAD → INFLIGHT paused → 409; INFLIGHT → DELIVERED while paused → 202. `JobsController` queued-jobs while paused → empty plus `X-Broker-Dispatch-Paused`. `JobStatusController` → `PARKED` counts. `JobRequeueController` on a parked job → 400. `DeadJobDeleteController` on a parked job → 404. |
| Router | `NewRouter` builds without httprouter path conflicts with the new routes (extend `router_test.go`) |

### 18.8 Config and wiring tests

* `config/config_test.go`:
  * Extend `TestGetAutoConfiguration_Default` (every new default).
  * Extend `TestGetAutoConfiguration_WrongValues` (each key out of range or unparsable → default, plus a warning).
  * Invalid `handoff-mode` → `http`.
  * `handoff-high-water-mark=0` → `max-message-queue-size / 2`.
  * `TestConfigInterfaces` includes `DispatchPauseConfig`.
* `main_test.go`: the startup smoke test still passes; the container exposes a non-nil `PauseRegistry` and `Drainer`; shutdown stops the Drainer before the Dispatcher (asserted through mock components injected into `HTTPServiceContainer`).

### 18.9 Integration topology changes

`docker-compose.integration-test.yaml`:

* **`ibroker2`** (new): the same image and MySQL as `ibroker`, listening on 8083, `gateway-auth mode=none`.
* **`ibroker-lb`** (new): nginx (already used by `ipruner`) round-robining to `ibroker:8080` and `ibroker2:8083`.
* `ibroker` and `ibroker2`: `retrigger-base-endpoint=http://ibroker-lb`, `admin-token` set, `handoff-mode=http`.
* `ibroker-hmac` keeps `retrigger-base-endpoint=http://localhost:8082` and signs its hand-offs to itself. That exercises HMAC signing for real whenever it wins the drain lock.
* Short timings in all three configs: `registry-refresh-interval-seconds=1`, `drain-interval-seconds=1`, `handoff-lease-seconds=5`.

The cluster now has three nodes on one DB, which is the real shape of production.

### 18.10 Integration scenarios (`integration-test/main.go`)

New functions run after `testDLQDeletePurge()` and before `testHMACAuth()`, each followed by `resetHandlers()`. Exit codes **110–149** are reserved for them. Assertions use both the HTTP API and direct MySQL queries (`connectToMySQL`).

| Function | Scenario and assertions |
| -- | -- |
| `testDispatchPauseConsumer` | Pause push consumer A and broadcast 200 messages. A's handler counts 0 for 3s while B counts 200. MySQL: 200 parked rows for A, all `retryAttemptCount = 0`. Message GET shows `PARKED`; `/dispatch-pauses` shows 200 parked. Resume: within 30s A has received every message id at least once (duplicates are counted and logged, not failed); parking is empty; no `job.id` also appears in `job_parking`. |
| `testDispatchPauseInflight` | A's handler sleeps 2s. Broadcast 20 messages, then pause while attempts are in flight. Those attempts complete; nothing else reaches A after pause + 1s (handler timestamps vs. the pause response time); the rest are parked. |
| `testDispatchPauseGlobal` | Global pause; broadcast on the general and pull channels; no push delivery from any node for 3s; resume; everything delivered; parking empty. |
| `testDispatchPausePull` | Pause a pull consumer: `queued-jobs` is empty with `X-Broker-Dispatch-Paused: true`, and `INFLIGHT` on a pre-existing job → 409. Resume: jobs created during the pause can be fetched and completed. |
| `testDispatchPausePruneSafety` | Pause A and broadcast; the others take delivery; trigger pruning with the mechanism `testPruning` uses; A's messages still exist in `message`. Resume, deliver, prune again → they're archived and removed. |
| `testDispatchPauseHandoffDistribution` | Pause A, broadcast 1000, resume. Scrape `/metrics` from all three nodes before and after. The drained total equals `handoff_requests_total{outcome="accepted"}` summed across the load-balanced nodes plus local enqueues. If the drain lock was won by `ibroker` or `ibroker2` (identified by the `drained_jobs_total` delta), **both** `ibroker` and `ibroker2` accepted at least one hand-off. The winner isn't deterministic, so the distribution check is conditional and the conservation check always runs. |
| `testDispatchPauseChaos` | For 10s, broadcast 2000 messages while toggling A's pause every 300ms and the global pause every 1.1s, then resume everything. Within 60s, A received **every** message id at least once; parking is empty; no job is in both tables. No delivery to A started inside a paused window, except attempts already INFLIGHT when it began (tolerance 1s, using pause/resume response times vs. handler timestamps). |
| `testDispatchPauseQueryPlans` | MySQL `EXPLAIN` on the drain page query and the drainable-consumers query (`key = job_parking_drain`), the prune query (`job_parking_message` on the subquery), and the gated `MarkJobInflight` (`PRIMARY` on `job`, `PRIMARY` on `dispatch_pause`). |

Manual runbook check (not automated, because the tester can't restart containers): run `docker restart ibroker2` during a large drain and confirm the drain finishes and the invariants hold.

### 18.11 Traceability from the race analysis (§15) to tests

| Scenario | Unit / component | Integration |
| -- | -- | -- |
| Broadcast while paused | `TestDispatchMessageWithParking`, `TestDispatchParksPausedConsumers` | `testDispatchPauseConsumer` |
| Paused job already in a node's memory | `TestDeliverJobPauseGateReparks` | `testDispatchPauseChaos` |
| Pause during INFLIGHT | `TestRetryQueuedJobsReparksPausedPush` | `testDispatchPauseInflight` |
| Snapshot stale after pause | `TestDeliverJobPauseGateReparks`, `TestQueueJobReparksWhenSnapshotPaused` | `testDispatchPauseChaos` |
| Snapshot stale after resume / resume mid-dispatch | `TestGetDrainableConsumerIDs`, `TestParkingInvariants_Randomized` | `testDispatchPauseChaos` |
| Re-pause during drain | `TestDrainPage` (`PausedSinceSelection`), `TestDrainerRepauseMidDrain` | `testDispatchPauseChaos` |
| Two drainers | `TestDrainPage_ConcurrentDrainers_Race`, `TestDrainerTwoInstances_Race` | (three-node cluster) |
| Crash after page, before hand-off / lost hand-off | `TestDrainerCrashAfterPageBeforeHandoff`, `TestHandoffAllFailLocalFallback` | n/a |
| Hand-off plus retry worker double enqueue | `TestReparkVsMarkJobInflight_Race`, `TestMarkJobInflight_PauseGate` | `testDispatchPauseHandoffDistribution` |
| Global and consumer pause overlap | `TestDispatchPauseIsEffectivelyPaused`, `TestPauseRegistryEffectivePause` | `testDispatchPauseGlobal`, `testDispatchPauseChaos` |
| Consumer deleted while paused | `TestDispatchPausePause` (`CascadeOnConsumerDelete`) | n/a |
| Prune during pause / old prune binary | `…ExcludesParked`, `TestDeleteMessagesAndJobs_ParkedRowRestricts` | `testDispatchPausePruneSafety` |
| Node restart | `TestNewPauseRegistry` (initial refresh) | manual runbook check |
| Status integers untouched | `TestJobStatusPersistedValues` | n/a |

### 18.12 CI budget, race runs and coverage

* **Time:** every unit and component test fits the 45s package timeout. Intervals are milliseconds through fakes; sleeps and backoff are injectable function variables; the randomized test drops to 50 steps under `-short` (used by `make ci-test`). The new integration scenarios add roughly 3 minutes to `make itest`.
* **Race:** `TestPauseRegistry_Race`, `TestDrainPage_ConcurrentDrainers_Race`, `TestReparkVsMarkJobInflight_Race` and `TestDrainerTwoInstances_Race` follow the `_Race` naming, so `make test` and `make ci-test` run them again under `-race`.
* **Determinism:** no assertion depends on which node wins a lock. Randomized tests log and accept a seed. Where wall-clock comparisons are unavoidable (integration only), they use explicit tolerances.
* **Coverage:** new files (`dispatchpauserepo.go`, `jobparkingrepo.go`, `dispatcher/pause.go`, `parking/*`, `controllers/dispatchpause.go`, `controllers/internaljobs.go`) target ≥ 90% statement coverage. Existing packages must not drop. The PR description includes `go test -cover ./storage/... ./dispatcher/... ./parking/... ./controllers/... ./config/...`.
* **Generated code:** mocks via `make generate`; `wire_gen.go` via `wire`. Never hand-edited (CLAUDE.md).

## 19. Implementation Plan

Each milestone is independently reviewable. It lands with its tests from §18, and `make test` passes with all existing tests unchanged (§18.1).

**M1: Schema and models**
* Migration `000014` (both dialects); `data.DispatchPause`, `data.DrainLock`, the virtual `JobParked` (separate const block).
* Tests: §18.3, including the `TestJobStatusPersistedValues` guard.

**M2: Repositories**
* `DispatchPauseRepository`, `JobParkingRepository`.
* `DeliveryJobRepository`: `DispatchMessageWithParking` (one transaction covering both tables + `SetDispatched`), pause-gated `MarkJobInflight`/`MarkDeadJobAsInflight`, `GetByID` fallback, `GetQueuedJobsByIDs`, `PARKED` counts.
* `MessageRepository` prune query; `LockRepository.RenewLock`.
* Wiring, `storage/wire_gen.go` regeneration, mocks (`make generate`); the shared helpers from §18.2.
* Tests: §18.4, including the randomized invariant test and the SQLite query-plan tests.

**M3: Config**
* `DispatchPauseConfig` with parsing, defaults and validation. Tests: §18.8.

**M4: Registry and dispatcher**
* `PauseRegistry`; `createJobs` split; `queueJob` re-park; worker DB check + re-park; `EnqueueJobs`/`QueueDepth`; metrics; mocks.
* `HTTPServiceContainer` wiring and lifecycle (`main.go`); root `wire_gen.go` regeneration.
* Tests: §18.5 and the wiring part of §18.8.

**M5: Drainer and hand-off**
* `parking.Drainer`; hand-off sender (HMAC signing, admin token, retries, local fallback); `InternalJobEnqueueController`.
* Tests: §18.6 and the `InternalJobEnqueueController` row of §18.7.

**M6: Public API**
* Pause controllers, `/dispatch-pauses`, `ConsumerModel.DispatchPaused`, `PARKED` in the message/job/job-status responses, pull gates.
* OpenAPI (`docs/open-api-spec/api-spec.yaml`): new paths and schemas, `PARKED` enum value, `DispatchPaused`, `X-Broker-Admin-Token` security scheme, `X-Broker-Dispatch-Paused` header. The internal endpoint is marked internal.
* Tests: §18.7.

**M7: Integration tests, docs, runbook**
* Topology changes (§18.9) and integration scenarios (§18.10); confirm every row of §18.11 has a passing test.
* Docs: a "Dispatch Pause" section in `CLAUDE.md`, `docs/configuration.md`, a `CHANGELOG.md` entry, and `docs/runbooks/dispatch-pause.md`, including the manual restart check from §18.10.

## 20. Alternatives Considered

| Alternative | Why rejected |
| -- | -- |
| `paused` column on `consumer` | Reset by the full-replace `PUT` (§4.5); stale for up to 4h through the consumer cache. |
| Keep paused jobs in `job`, filter them in the retry SQL | Every paused row is read and discarded every 2s; no existing index helps (§4.8). |
| Keep them in `job` with a far-future `earliestNextAttemptAt` | Avoids the scan, but needs a new composite index on the largest table (online build), mixes paused and live rows in every other `job` query, and still leaves "find parked rows for a consumer" competing with live traffic. |
| New `PAUSED` value in `JobStatus` | Touches the iota block that must not change (CLAUDE.md), and needs bulk status rewrites across millions of rows. |
| Skip creating jobs while paused | The message becomes `DISPATCHED` without the consumer's job, which breaks at-least-once delivery. |
| Edge-triggered resume events / a DRAINING state machine | Events are missed on restart or across fast toggles, and it needs grace periods and locking reads to avoid stranded jobs. Polling the parking table is simpler and strands nothing (§5). |
| Drain by setting `earliestNextAttemptAt = now` and letting every node's retry worker pick up | Every node loads the whole ready set and fights over per-job locks; nothing balances the load; the backlog is loaded at once. |
| Each node claims its own pages | Needs leases and per-node backpressure, and loses the single-sequence oldest-first move. A single drainer plus the load balancer is simpler. |
| Global pause as a config flag | Needs a restart on every node, mid-incident, and leaves no audit trail. |

## 21. Decisions

Resolved November 4, 2026:

1. **Drain order key:** `(createdAt, seq)`. A re-parked job keeps its original place in line; arrival `seq` breaks ties.
2. **Tokens:** one `[dispatch-pause] admin-token` protects both the internal hand-off endpoint and the global pause endpoints.
3. **Recovery rate:** no per-consumer drain rate limit in v1. Page size, hand-off concurrency and broker backpressure bound the drain; a per-consumer limit stays in Future Work (§22).

## 22. Future Work

* **Channel scope:** a `channelId` FK column with `scope = 'channel:<id>'`; effective pause gains a third term.
* **Timed pauses:** add `resumeAt`; the registry refresh deletes expired rows.
* **Drain rate limit** per consumer (token bucket in the drainer).
* **Eager re-park** of a pull consumer's pre-existing `QUEUED` backlog, if operators want pull pauses fully reflected in job listings.

## 23. Implementation Notes (deviations from this spec)

Recorded during implementation; the code is authoritative where it differs from the sections above.

| Section | Spec said | Implemented | Why |
| -- | -- | -- | -- |
| §7.4 | `GetCountsByConsumer() map[string]*StatusCount` | Returns `[]*data.ParkedJobCount` (channel id, internal consumer id, count, oldest/newest as strings); added `CountForConsumer` | Callers need the channel id; aggregate DATETIMEs are typed inconsistently across dialects; `CountForConsumer` keeps the pause API off a full `GROUP BY` |
| §7.5 | `GetByID` fallback implemented "inside `DeliveryJobDBRepository`" | Shared helper `loadParkedJobByID` used by both repositories | Same behavior, no duplicated SQL |
| §7.5 | Pause condition with a consumer id parameter | Correlated on the job row (`d.scope IN (job.consumerId, '__global__')`) | Does not depend on `job.Listener` being loaded |
| §7.8 | Root providers for the new repositories in M2 | Added in M4/M5 with their first consumer | Wire rejects unused providers |
| §12.1 | Auth via `getConsumerWithValidation` | `getChannelConsumerWithAuth` | The former only accepts pull consumers; the latter is what the DLQ purge/delete endpoints use |
| §12.3 | `ConsumerController` gains the registry | Wire uses `NewConsumerControllerWithPauseRegistry`; `NewConsumerController` is unchanged (nil registry = not paused) | Avoids changing 32 existing test call sites |
| §14 | `dispatch_paused` labels | `channel` = channel id, `consumer` = public consumer id (internal id if the consumer can not be loaded) | Readable dashboards |
| §18.2 | `AssertParkingInvariants(t, ...)` | `CheckParkingInvariants(db, ...) error` (+ `CheckMessageJobCount`) | `storage/testutils.go` is non-test code; it must not import `testing` |
| §18.5 | Dispatcher test creating a global pause | Global behavior tested with a fake registry and in the storage/controllers packages; dispatcher DB tests use consumer pauses only | Dispatchers started by earlier tests may still run against the shared DB and would park their jobs |
| §18.8 | Shutdown order asserted via mocks in the container | `startBackgroundServices` / `stopBackgroundServices` extracted in `main.go` and tested with recording fakes | Testable seam |
| §18.10 | Distribution asserted with `handoff_requests_total` on receivers | Distribution asserted by the number of distinct broker hosts delivering to the consumer; conservation by `drained_jobs_total` summed across nodes | `handoff_requests_total` is counted on the sending (draining) node |
| §18.9 | HMAC node hands off to itself | Also configured with the admin token (shared across nodes) | Needed for HTTP hand-off and to exercise signed requests |
| — | — | `normalizeDBError` now recognises `go-sqlite3`'s `sqlite3.Error` (unique/primary key only) | Duplicate keys were never mapped on SQLite (also affected `TryLock`) |
| — | — | `github.com/prometheus/client_model` moved to a direct requirement | Tests read counter values |
| — | — | Root `Dockerfile` adds the `parking` package | Images list package directories explicitly |
| §6.2, §6.3 | Index `job_parking_message (messageId)` | Dropped; `UNIQUE (messageId, consumerId)` serves the prune `NOT EXISTS` and per-message listings | Redundant index (MySQL plan used the unique index) |
| §7.4, §10.3 | Drain page pause check correlated on `p.consumerId` | Pause check takes the consumer id as a parameter: `d.scope IN (?, '__global__')` | MySQL turned the correlated form into an anti-join with "Using temporary; Using filesort", sorting the consumer's whole backlog per page. Uncorrelated, it is evaluated once and `job_parking_drain` serves `ORDER BY ... LIMIT` |
| §7.4 | `GetDrainableConsumerIDs` as one correlated `NOT EXISTS` query | `SELECT DISTINCT consumerId FROM job_parking` (loose index scan on `job_parking_drain`) + `SELECT scope FROM dispatch_pause`, filtered in Go | Avoids a per-row anti-join in a query every node runs every drain interval |
| §18.9 | Three nodes running everything | `ibroker2` runs with `recovery-workers-enabled=false`; MySQL `--max-connections=500` | Existing retry-count assertions keep their two-node recovery setup; three brokers (100 connections each) plus the pruner exceed MySQL's default 151 (which also caused the pre-existing `testDLQFlow` failure) |
| §18.10 | Integration scenarios only in the full run | Also runnable alone with `ITEST_ONLY=dispatch-pause`; `testDLQObservability` accepts `dead_job_count` from any reachable node | Faster iteration; only the DLQ-updater lock holder exports that gauge |

---
*Generated with assistance from Claude AI*
