# Developers

The project build is straight forward using the `Makefile`; just doing `make` should work. Please ensure you have the following installed prior -

1. GoLang 1.16.x
1. GCC with deps (required for SQLite driver compilation)

That should be it. Tested on _Ubuntu 20.04_ with _GoLang 1.16.2_. One of the development assumption (especially in tests) is no configuration file exists in the box in which test is being executed; which means only default configuration shipped with code is applicable.

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

Currently the release is managed through tag pushes and that should be done in 2 PRs. First pull request to update the version for [CLI](https://github.com/imyousuf/webhook-broker/blob/main/config/config.go#L37), [Main test](https://github.com/imyousuf/webhook-broker/blob/ef0364661e6fb443fccc6307c04ec9aa52071be2/main_test.go#L41), [Helm docker image tag](https://github.com/imyousuf/webhook-broker/blob/ef0364661e6fb443fccc6307c04ec9aa52071be2/deploy-pkg/webhook-broker-chart/values.yaml#L11), [Helm package version](https://github.com/imyousuf/webhook-broker/blob/main/deploy-pkg/webhook-broker-chart/Chart.yaml#L18) and [Helm app version](https://github.com/imyousuf/webhook-broker/blob/main/deploy-pkg/webhook-broker-chart/Chart.yaml#L23) to set to the new release version, along with any CHANGELOG changes and once this is merged; follow up a tag for this change; that should trigger the release process. Then the second pull request promoting the version to the next dev version.
