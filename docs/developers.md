# Developers

The project build is straight forward using the `Makefile`; just doing `make` should work. Please ensure you have the following installed prior -

1. GoLang 1.16.x
1. GCC with deps (required for SQLite driver compilation)

That should be it. Tested on _Ubuntu 20.04_ with _GoLang 1.23.0_. One of the development assumption (especially in tests) is no configuration file exists in the box in which test is being executed; which means only default configuration shipped with code is applicable.

Generate Migration script using command as follows from project root -

```bash
migrate create -ext sql -dir migration/sqls -seq create_sample_table
```

For generating mocks we use [Mockery](https://github.com/vektra/mockery). Currently `storage` and `config` interfaces are mocked using the tool. Command used for them are -

```bash
mockery --all --dir "./config/" --output "./config/mocks"
mockery --all --dir "./storage/" --output "./storage/mocks"
```

When using the `docker-compose` please ensure to first start MySQL and then start the broker so that broker finds MySQL on bootup. For example,

```bash
docker-compose up -d mysql
# Sleep for 30s
docker-compose up broker
# once up try the following curl -
curl -v localhost:18181/channel/sample-channel/broadcast -X POST -H "X-Broker-Channel-Token: sample-channel-token" -H "X-Broker-Producer-Token: sample-producer-token" -H "X-Broker-Producer-ID: sample-producer" -H "Content-Type: application/json" --data '{"test": "Hello World!"}'
# It wont deliver message anywhere since default sample-consumer's callback URL is invalid

# To send a scheduled message (to be delivered in the future):
curl -v localhost:18181/channel/sample-channel/broadcast -X POST \
  -H "X-Broker-Channel-Token: sample-channel-token" \
  -H "X-Broker-Producer-Token: sample-producer-token" \
  -H "X-Broker-Producer-ID: sample-producer" \
  -H "X-Broker-Scheduled-For: 2025-12-31T23:59:59Z" \
  -H "Content-Type: application/json" \
  --data '{"test": "Scheduled message"}'
# Note: The scheduled time must be at least 2 minutes in the future (configurable)

# To list scheduled messages for a channel:
curl -v localhost:18181/channel/sample-channel/scheduled-messages
```

Also note that for testing in docker, you will need the code to be built every time you change code.

## Scheduled Messages

The webhook-broker supports scheduling messages for future delivery using a simple header-based API. Key features:

- Send messages with the `X-Broker-Scheduled-For` header containing an ISO-8601 date-time
- Time must be at least 2 minutes in the future (configurable)
- Messages are stored until their scheduled time, then delivered through the normal flow
- No support for recurring deliveries or cancellation of scheduled messages

API endpoints:
- `POST /channel/{channelId}/broadcast` with `X-Broker-Scheduled-For` header to schedule a message
- `GET /channel/{channelId}/scheduled-messages` to list scheduled messages
- `GET /channel/{channelId}/scheduled-message/{messageId}` to get a specific scheduled message
- `GET /channel/{channelId}/messages-status` includes counts of scheduled messages

### Scheduler Implementation Details

The scheduler runs as a background service, checking periodically for messages due for delivery. Key implementation details:

- The scheduler runs in a single goroutine that processes batches of messages
- Each tick interval (default: 5 seconds), the scheduler checks for messages ready for dispatch
- The scheduler processes up to 100 messages per tick (configurable via `schedulerBatchSize`)
- Messages are processed in chronological order by their scheduled time (oldest first)
- Each message is dispatched in its own goroutine for concurrent processing
- If more than 100 messages are ready, subsequent batches will be processed in future ticks
- If processing a batch takes longer than the tick interval, the next tick will start immediately after the current batch completes
- The scheduler will never process overlapping batches, ensuring consistent database state

## Configuration management

If you are intending to work with deployment of Webhook Broker, we recommend you also install the following:

- Terraform [v0.14+](https://www.terraform.io/downloads.html)
- [Helm](https://helm.sh/) - `sudo snap install helm --classic`
- `kubectl` - `sudo snap install kubectl --classic`
- AWS IAM Authenticator

```bash
sudo curl -o /usr/local/bin/aws-iam-authenticator "https://amazon-eks.s3.us-west-2.amazonaws.com/1.18.9/2020-11-02/bin/linux/amd64/aws-iam-authenticator"
sudo chmod +x /usr/local/bin/aws-iam-authenticator
```

## Release Management

Currently the release is managed through tag pushes and that should be done in 2 PRs. First pull request to update the version for [CLI](https://github.com/newscred/webhook-broker/blob/main/config/config.go#L37), [Main test](https://github.com/newscred/webhook-broker/blob/ef0364661e6fb443fccc6307c04ec9aa52071be2/main_test.go#L41), [Helm docker image tag](https://github.com/newscred/webhook-broker/blob/ef0364661e6fb443fccc6307c04ec9aa52071be2/deploy-pkg/webhook-broker-chart/values.yaml#L11), [Helm package version](https://github.com/newscred/webhook-broker/blob/main/deploy-pkg/webhook-broker-chart/Chart.yaml#L18) and [Helm app version](https://github.com/newscred/webhook-broker/blob/main/deploy-pkg/webhook-broker-chart/Chart.yaml#L23) to set to the new release version, along with any CHANGELOG changes and once this is merged; follow up a tag for this change; that should trigger the release process. Then the second pull request promoting the version to the next dev version.

### How to Release (Step By Step)

#### 1. Merge your changes to main
Ensure CI is passing: Make sure main is green. Continuous images from main will build/push automatically (these use the chart’s appVersion for tagging).

#### 2. Prep the release on a short PR

- Bump versions in one commit/PR:

  - Set the release version for [CLI](https://github.com/newscred/webhook-broker/blob/main/config/config.go#L37), [Main test](https://github.com/newscred/webhook-broker/blob/ef0364661e6fb443fccc6307c04ec9aa52071be2/main_test.go#L41) and [Helm app version](https://github.com/newscred/webhook-broker/blob/main/deploy-pkg/webhook-broker-chart/Chart.yaml#L23)

  - Set the release version for [Helm chart version](https://github.com/newscred/webhook-broker/blob/main/deploy-pkg/webhook-broker-chart/Chart.yaml#L18).

    - If you bump the appVersion (the Webhook Broker release version), you must also bump the Helm chart version.

    - If you make changes only to the chart itself (without changes to the Webhook Broker source code), bump only the Helm chart version and leave appVersion unchanged.

- Update CHANGELOG.md (or release notes section) with a concise summary of changes.

Sample: https://github.com/newscred/webhook-broker/pull/236/files

### 3. Push Tag

After the PR above is merged to main, create an annotated tag that includes the v prefix:

```bash
git checkout main && git pull
export VER=v0.2.4
git tag "$VER"
git push origin "$VER"
```

Pushing vX.Y.Z triggers your two release workflows:

- Release Packages: builds artifacts, creates the GitHub Release, packages/pushes the Helm chart to the S3-backed repo.

- Release Containers: builds multi-arch images and pushes to ECR, GHCR and DockerHub.

#### 4. Post-tag housekeeping

Open a tiny follow-up PR that bumps version references to the next -dev (e.g., 0.3.0-dev) to indicate development has resumed.

Sample: https://github.com/newscred/webhook-broker/pull/237
