# Runbook: Running DB migrations out-of-band

## Why migrations no longer run at pod startup

Migrations are **opt-in**. The broker only applies migrations at startup when **both**:

- `-migrate <source>` is set (where the migration SQL lives), **and**
- `-run-migration` is passed (the explicit apply flag).

The production Helm manifest passes only `-migrate /migration/sqls/` (see
`deploy-pkg/webhook-broker-chart/templates/deployment.yaml`), so **normal pods do not
apply migrations**. This is deliberate.

### The failure this prevents

Migrations run synchronously at startup under an application-level `GET_LOCK`
(`storage/rdbms.go` `runMigration`). A migration that issues DDL on a large table (e.g.
`CREATE INDEX` on `job`) can get stuck **`Waiting for table metadata lock`** behind a
long-lived MDL holder. A DDL *waiting* for its exclusive metadata lock head-of-line-blocks
every subsequent query on that table — freezing the table without holding any usable lock —
while still holding the migration `GET_LOCK`. Every other pod then fails startup with
`can't acquire lock` → `could not start http service` and crashloops fleet-wide.

Because the image tag is mutable (`pullPolicy: Always`), shipping an image whose default is
"do not migrate" stops that crashloop on **every** cluster (AWS + all GCP) with a single
image deploy — no per-cluster manifest edits.

## How to apply migrations deliberately

Run the broker binary with **both** flags, as a **single** short-lived pod (the `GET_LOCK`
serializes it, but only run one at a time to keep it obvious), during a low-traffic window,
using the **same image** already deployed:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: webhook-broker-migrate
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      serviceAccountName: webhook-broker
      containers:
        - name: migrate
          image: <same repository:tag as the running broker>
          command: ["/webhook-broker"]
          args: ["-migrate", "/migration/sqls/", "-run-migration", "-config", "/app-config/webhook-broker.cfg"]
          volumeMounts:
            - name: conf-vol
              mountPath: /app-config/
      volumes:
        - name: conf-vol
          # same config source (Secret/ConfigMap) the Deployment mounts
```

Before running, make sure the migration will not itself wedge on an MDL:

- **Clear long-lived holders first.** Check `SHOW PROCESSLIST` for long-running queries or
  idle-in-transaction sessions on the target table and resolve them; drain any runaway
  backlog (see [`drain-queued-backlog.md`](./drain-queued-backlog.md)).
- Prefer building heavy indexes **out-of-band** (gh-ost / pt-online-schema-change /
  `ALGORITHM=INPLACE, LOCK=NONE`) and let the migration be an idempotent no-op — migration
  `000013_add_prioritized_jobs_index` is written to detect an already-built index and skip.

Watch the Job's logs to confirm the migration completed, then delete the Job.

## Incident recovery order (crashloop from a wedged startup migration)

1. **Deploy the migrations-opt-in image** so pods stop attempting migrations at startup and
   come up healthy. (This does **not** unwedge the DB.)
2. **Unwedge the DB — maintainer/DBA, per database (AWS and GCP are independent):** find the
   stuck DDL in `SHOW PROCESSLIST` (`State: Waiting for table metadata lock`, on `job`) and
   `KILL <pid>`. This releases the table MDL, unblocks the query pile-up, and releases the
   migration `GET_LOCK`.
3. **Build the index safely out-of-band** on each DB (see
   [`drain-queued-backlog.md`](./drain-queued-backlog.md) Part A).
4. **Reconcile migration bookkeeping.** golang-migrate tracks state in `schema_migrations`
   (`version`, `dirty`). A killed in-band migration leaves `dirty = 1`. Once the index
   exists, clear the flag and align the version:
   ```sql
   -- Verify current state first.
   SELECT version, dirty FROM schema_migrations;
   -- After confirming the 000013 index is present:
   UPDATE schema_migrations SET dirty = 0 WHERE dirty = 1;
   ```
   Then run the migration Job (above) so any remaining migrations apply cleanly, or confirm
   `version` already reflects 000013.

## Fresh / empty environments (important)

Because startup no longer migrates, a **brand-new or empty database has no schema**. The
broker's `InitAppData` needs the `app` table to exist, so a first-boot broker against an
empty DB will fail. **Run the migration Job first**, then start/scale up the broker.

<!-- Generated with assistance from Claude AI -->
