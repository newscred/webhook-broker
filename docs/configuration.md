# Configuration

The configuration for Webhook Broker is in [INI format](https://en.wikipedia.org/wiki/INI_file#Format); there are specific sections for specific purpose. They are discussed below in details. A sample configuration file template is [available](../webhook-broker.cfg.template).

## Section - Relational DB Config: `[rdbms]`

This section has configuration for relational database management systems. Currently that is the only supported storage engine for webhook broker.

| Name | Default Value | Description|
| -- | -- | -- |
| dialect | `sqlite3` | Specified the DB driver to use; supported values `sqlite3` and `mysql` |
| connection-url | webhook-broker.sqlite3 | The DB Connection URL; for MySQL please ensure to pass `charset=utf8&parseTime=true&multiStatements=true` via connection URL |
| connxn-max-idle-time-seconds | 0 | By default idle connections are not recycled ever; if you want them recycled, then specify a idle time to refresh them |
| connxn-max-lifetime-seconds | 0 | By default connections are never cycled regardless of their age; this configuration specifies a TTL for connection before its recycled and refreshed. |
| max-idle-connxns | 30 | Number of connections to keep open at all time |
| max-open-connxns | 100 | Maximum number of connections that could be opened concurrently with DB. Please make sure DB is able to handle the **max connection * number of broker nodes** in action. |

## Section - HTTP Config `[http]`

This section has configuration for the webhook broker's web server service's HTTP specific configuration.

| Name | Default Value | Description|
| -- | -- | -- |
| listener | :8080 | The port the service tries to bind itself to |
| read-timeout | 240 | Read timeout for clients from server |
| write-timeout | 240 | Write timeout for clients to server |

## Section - Log Config `[log]`

This section has configuration for configuring how and where the log output should go; by default its configured to output in console since that is how log is aggregated in the k8s environment.

| Name | Default Value | Description|
| -- | -- | -- |
| filename | "" | Specify a file name, e.g., _"/var/log/webhook-broker.log"_,  to direct log output to it. File will be rolled automatically. Blank means rather have stderr as the output for log. |
| max-file-size-in-mb | 200 | Size of log file to trigger rolling it to a backup and creating new one |
| max-backups | 3 | Maximum number of backups to retain |
| max-age-in-days | 28 | Oldest log back retention period |
| compress-backups | true | Whether backup files are compressed or not |
| log-level | debug | Logging level, valid values - debug, info, error and fatal |

## Section - Broker Config `[broker]`

This section contains configuration pertaining to the broker application itself.

| Name | Default Value | Description|
| -- | -- | -- |
| max-message-queue-size | 10,000 | Maximum number of jobs to be queued before blocking dispatcher worker, provided there is memory available choose a high number. |
| max-workers | 200 | Maximum number of workers within this app instance; the lower the number the higher the queue size that would be required |
| max-retry | 5 | Upon delivery attempt failure, how many times will the app retry delivery. Check backoff time to understand the delays between retries |
| rational-delay-in-seconds | 2 | A delay setting to wait, in addition to expected wait period; for example when a consumer connection isn't closed past `timeout + rational delay`, it will be requeued for delivery assuming the connection has gone rogue. |
| retry-backoff-delays-in-seconds | 5,30,60 | Configuration delays between retry attempt; since default retry is 5, the delays in effect would be - `5s`, `30s`, `60s`, `120s`, `180s` respectively |
| recovery-workers-enabled | true | Whether this process will run the 3 recovery workers. Check [basic techspec](./tech-specs/basic-spec.md) for more details about what the recovery workers are responsible for. |

## Section - Consumer Connection Config `[consumer-connection]`

This section contains configuration pertaining to the broker app attempting to deliver to _Consumers_.

| Name | Default Value | Description|
| -- | -- | -- |
| token-header-name | X-Broker-Consumer-Token | The request header name to contain _Consumer Token_ for consumer to validate the soruce of the request. |
| user-agent | Webhook Message Broker | The `User-Agent` header value when connecting to consumer |
| connection-timeout-in-seconds | 30 | Maximum time to provided consumers to finish the processing of the job. Anything more than 30 please consider using something like SQS, RabbitMQ etc. since maintaining long HTTP connection is risky. |

## Sections  for Seed Dataset

For seed data of the application there are 5 fixed sections and a dynamic section per consumer configured. When Webhook Broker is used for System to System communication or ESB, channels would be relatively be within fixed channels. The sections are:

| Section Name | Description | Key | Value |
| -- | -- | -- | -- |
| initial-channels | Configure channels | Channel ID | Channel Name |
| initial-producers | Configure producers | Producer ID | Producer Name |
| initial-consumers | Configure a consumer | Consumer ID | Callback URL |
| initial-channel-tokens | Configure a channel token | Channel ID, must be present in `initial-channels`; if there is a Channel ID in `initial-channels` but not here, then it will get a random token | Token for the channel |
| initial-producer-tokens | Configure a producer token | Producer ID, same behavior as Channel ID above | Token for the producer |
| `Consumer ID` | Additional details about the consumer | `token` | Token for the Consumer specified by Consumer ID |
| `Consumer ID` | Additional details about the consumer | `channel` | Channel ID that this consumer belongs to; currently a consumer can be linked to a single channel only |

These changes are tracked and will get synchronized only during sync; that is if any change is made after initialization; it wont be reset to configuration on restart; but will be reset if this configuration changes anyway; for example, change in name will reset token to whatever it is in the config for a producer or channel.
