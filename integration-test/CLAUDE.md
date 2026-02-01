# Integration Test - CLAUDE.md

## Running Integration Tests

### Standard Approach (Docker Compose)

```bash
make itest
```

This uses `docker-compose.integration-test.yaml` to start MySQL, broker, pruner, and tester in containers.

### Local Bash Approach (when Docker networking fails)

If `make itest` fails due to Docker inter-container networking issues (e.g., broker cannot connect to MySQL with `connection timed out` errors despite both being on the same Docker network), use this approach:

#### Prerequisites

- MySQL client installed locally
- Go toolchain
- Python 3 (for file server)

#### Step-by-step

1. **Start MySQL in Docker** (only MySQL runs in Docker):
   ```bash
   cd /path/to/webhook-broker
   docker compose -f docker-compose.integration-test.yaml up -d mysql8
   ```

2. **Wait for MySQL to be ready** (exposed on port 45060):
   ```bash
   for i in $(seq 1 30); do
     mysql -h 127.0.0.1 -P 45060 -u webhook_broker -pzxc909zxc -e "SELECT 1" 2>/dev/null && echo "MySQL ready" && break
     echo "Attempt $i..."; sleep 3
   done
   ```

3. **Reset the database** (clean state):
   ```bash
   mysql -h 127.0.0.1 -P 45060 -u root -proot -e "DROP DATABASE \`webhook-broker\`; CREATE DATABASE \`webhook-broker\`; GRANT ALL ON \`webhook-broker\`.* TO 'webhook_broker'@'%';" 2>/dev/null
   ```

4. **Build broker and tester**:
   ```bash
   cd /path/to/webhook-broker
   go build -o webhook-broker .
   cd integration-test
   go build -mod=readonly -o integration-test .
   ```

5. **Start the broker** (in background terminal/process):
   ```bash
   mkdir -p /tmp/webhook-broker-export
   ./webhook-broker -migrate ./migration/sqls/ -config ./webhook-broker-local-itest.cfg
   ```
   Wait for `curl -sf http://localhost:8080/_status` to return a response.

6. **Start the pruner loop** (in another background terminal/process, uses port 8081 to avoid conflict):
   ```bash
   while true; do
     ./webhook-broker -command prune -config ./webhook-broker-local-itest-pruner.cfg >> /tmp/webhook-broker-export/app.log 2>&1
     sleep 5
   done
   ```

7. **Start the export file server** (in another background terminal/process):
   ```bash
   python3 -m http.server 38080 --directory /tmp/webhook-broker-export
   ```

8. **Run the integration test**:
   ```bash
   cd integration-test
   CONSUMER_HOST=localhost \
   BROKER_BASE_URL=http://localhost:8080 \
   PRUNER_BASE_URL=http://localhost:38080/ \
   MYSQL_CONNECTION_URL="webhook_broker:zxc909zxc@tcp(127.0.0.1:45060)/webhook-broker?charset=utf8mb4&collation=utf8mb4_0900_ai_ci&parseTime=true&multiStatements=true" \
   ./integration-test
   ```

9. **Cleanup**:
   ```bash
   # Stop broker, pruner, and file server processes
   # Then stop MySQL:
   docker compose -f docker-compose.integration-test.yaml down -v
   ```

#### Config Files

- `webhook-broker-local-itest.cfg` — Broker config pointing MySQL to `127.0.0.1:45060`, HTTP on `:8080`
- `webhook-broker-local-itest-pruner.cfg` — Pruner config same DB but HTTP on `:8081` (avoids port conflict with broker)

#### Environment Variables

The integration test binary supports these env vars for local execution:

| Variable | Default (Docker) | Local Value |
|---|---|---|
| `CONSUMER_HOST` | `tester` | `localhost` |
| `BROKER_BASE_URL` | `http://webhook-broker:8080` | `http://localhost:8080` |
| `PRUNER_BASE_URL` | `http://webhook-broker-pruner/` | `http://localhost:38080/` |
| `MYSQL_CONNECTION_URL` | `...@tcp(mysql:3306)/...` | `...@tcp(127.0.0.1:45060)/...` |
