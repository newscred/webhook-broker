package config

// DefaultConfiguration is the configuration that will be in effect if no configuration is loaded from any of the expected locations
const DefaultConfiguration = `[rdbms]
dialect=sqlite3
connection-url=webhook-broker.sqlite3?_foreign_keys=on
connxn-max-idle-time-seconds=0
connxn-max-lifetime-seconds=0
max-idle-connxns=30
max-open-connxns=100
[http]
listener=:8080
read-timeout=240
write-timeout=240
[log]
filename=
max-file-size-in-mb=200
max-backups=3
max-age-in-days=28
compress-backups=true
[broker]
max-message-queue-size=10000
max-workers=200
priority-dispatcher-enabled=true
retrigger-base-endpoint=http://localhost:8080
max-retry=5
rational-delay-in-seconds=2
retry-backoff-delays-in-seconds=5,30,60
recovery-workers-enabled=true
[consumer-connection]
token-header-name=X-Broker-Consumer-Token
user-agent=Webhook Message Broker
connection-timeout-in-seconds=30
[initial-channels]
sample-channel=Sample Channel
[initial-producers]
sample-producer=Sample Producer
[initial-consumers]
sample-consumer=http://sample-endpoint/webhook-receiver
[initial-channel-tokens]
sample-channel=sample-channel-token
[initial-producer-tokens]
sample-producer=sample-producer-token
[sample-consumer]
token=sample-consumer-token
channel=sample-channel
`
