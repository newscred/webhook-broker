# Webhook Broker Chart

[![Artifact HUB](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/imytech)](https://artifacthub.io/packages/search?repo=imytech) ![Helm Build](https://github.com/imyousuf/webhook-broker/workflows/Helm%20Build/badge.svg)

This is a Helm Chart for [Webhook Broker](https://github.com/imyousuf/webhook-broker) project. There is an example of use of this [via Terraform](https://github.com/imyousuf/terraform-aws-webhook-broker/blob/9e199c5c47d1f6c0f54d8becad453edc88c72e93/modules/w7b6/main.tf#L88). The most important value to override is the [DB URL](https://github.com/imyousuf/terraform-aws-webhook-broker/blob/9e199c5c47d1f6c0f54d8becad453edc88c72e93/modules/w7b6/conf/webhook-broker-values.yml#L2) for the project to startup. Check [configuration documentation](https://github.com/imyousuf/webhook-broker/tree/main/docs/configuration.md) to understand what the `broker.*` configurations mean. Besides that all the other parameters are pretty standard Helm Chart values.

One of the configuration which is for CLI input instead of the above configuration is:

```yaml
broker:
  configFileWatchMode: "stop" # Possible values stop, restart, ignore
```

Depending on how you want the application to react pass a configuration; by default on any config map change the existing pod processes will stop, if its `restart`, the web endpoint and workers will restart and if its `ignore`, then the application will not watch for config file change and hence won't react either.
