# Developers

The project build is straight forward using the `Makefile`; just doing `make` should work. Please ensure you have the following installed prior -

1. GoLang 1.15.x
1. GCC with deps (required for SQLite driver compilation)

That should be it. Tested on _Ubuntu 20.04_ with _GoLang 1.15.4_. One of the development assumption (especially in tests) is no configuration file exists in the box in which test is being executed; which means only default configuration shipped with code is applicable.

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
```

Also note that for testing in docker, you will need the code to be built every time you change code.
