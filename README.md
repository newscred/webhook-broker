# webhook-broker

![webhook-broker CI](https://github.com/imyousuf/webhook-broker/workflows/webhook-broker%20CI/badge.svg?branch=main)
![webhook-broker Container CI](https://github.com/imyousuf/webhook-broker/workflows/webhook-broker%20Container%20CI/badge.svg)
[![Test Coverage](https://api.codeclimate.com/v1/badges/bf5ba73ffe2743c7c7ad/test_coverage)](https://codeclimate.com/github/imyousuf/webhook-broker/test_coverage)
[![Maintainability](https://api.codeclimate.com/v1/badges/bf5ba73ffe2743c7c7ad/maintainability)](https://codeclimate.com/github/imyousuf/webhook-broker/maintainability)
[![Go Report Card](https://goreportcard.com/badge/github.com/imyousuf/webhook-broker)](https://goreportcard.com/report/github.com/imyousuf/webhook-broker)

This is a fully HTTP based Pub/Sub Broker with a goal to simplify systems architected in SOA or Microservice architecture. It aims to solve the inter service communication problem.

## Use Case

In a system where there are multiple services it is natural that there is inter service dependency which are asynchronous in nature. Lets see an example to understand it. There is a SaaS product; when a user registration happens there will be a congratulatory welcome email sent to the user. Here, for the sake of this conversation, lets assume there is a service that is responsible for sending out email to the end user; so the registration action will trigger the email; but email sending does not necessarily need to block the registration process; so it is asynchronous in nature. In this example, the registration service will call the email service to send out the welcome email to a certain email-address. Now when this call is made, there is no guarantee that email service is available so how does registration service ensure sending the message. Here is where the webhook broker (**w7b6**) comes in. The broker is a simple service that does very straightforward work and is deployed with high-availability in mind and it guarantees delivery of a message that it received with transparency at least once.

So in the example above -

**registration service** ---------- Email address and welcome email ------> **webhook-broker** ------ relay message -------> **email service**
What broker introduces here is the asynchronous, non-blocking, guarantee of delivery.

Some of the use cases we are considering using it for is -

1. Message passing between systems where the use cases are -
   1. Propagate changes to objects for search-index indexing
   1. Propagate changes to objects for Authentication and Authorization computation
   1. For triggering asynchronous processing/denormalizing objects
1. For delivery of Public API Webhook events.

If the use case seems overlapping with RabbitMQ, AWS SQS, Celery or similar services then yes it is similar. The advantage of using **w7b6** over these services would be -

1. Tech stack agnostic for producer and subscriber/consumer
1. Development and deployment is easier since everything is just Web APIs
1. With HTTP2 there is the advantage of multiplexing request/response hence better network utilization between server and client.
1. Easy use of "prioritized" message queues
1. Guaranteed delivery of message at least once (push and not polling)
1. Ease of new queue/topic setup

## Install & Usage

Consider one of the following 4 strategies to use Webhook Broker.

### Terraform

As part of this project we are committed to maintain a [terraform module](https://github.com/imyousuf/terraform-aws-webhook-broker) to make it easy to deploy and manage Webhook Broker in a Kubernetes setting. The module is available in [Terraform Registry](https://registry.terraform.io/modules/imyousuf/webhook-broker/aws/latest?tab=inputs) as well; please make sure to checkout the [w7b6 submodule](https://registry.terraform.io/modules/imyousuf/webhook-broker/aws/latest/submodules/w7b6) if you are just interested in Webhook Broker; we also recommend to additionally consider [k8s modules](https://registry.terraform.io/modules/imyousuf/webhook-broker/aws/latest/submodules/kubernetes-goodies).

### Helm Chart

As part of the Terraform configuration, we deploy Webhook Broker using a [Helm Chart](https://artifacthub.io/packages/helm/imytech/webhook-broker-chart) maintained within [this repo](./deploy-pkg/webhook-broker-chart/README.md).

### DIY - Use Docker Image

Our docker images are host in 2 repositories -

1. [Docker Hub](https://hub.docker.com/repository/docker/imyousuf/webhook-broker)
2. [Github Docker Registry](https://github.com/users/imyousuf/packages/container/package/webhook-broker)

The difference being Docker Hub will have images for builds from `main` branch whenever a commit is pushed to the branch. Whereas Github registry will only contain releases. The distinction is made so that docker hub can be used for continuous deployment whereas Github Docker Registry for stable releases. The docker [compose file](https://github.com/imyousuf/webhook-broker/blob/main/docker-compose.integration-test.yaml) for integration test gives a good idea how about to setup an environment using docker images and how to configure it through volume mount.

### DIY - Build and Use

All the above should give plenty of hints around how to deploy the application on its own. The CLI Arguments look like -

```bash
$ make dep-tools deps build
$ ./webhook-broker -h
{"level":"debug","time":"2021-01-16T20:58:51-05:00","message":"Webhook Broker - 0.1-dev"}
Usage of ./webhook-broker:
  -config string
      Config file location
  -do-not-watch-conf-change
      Do not watch config change
  -migrate string
      Migration source folder
  -stop-on-conf-change
      Restart internally on -config change if this flag is absent
```

In addition consult our [configuration documentation](./docs/configuration.md) to setup the application.

## Implementation Details

The Tech Specs are good place to understand the implementation details -

* [Base Release Specs](./docs/tech-specs/basic-spec.md)

## Contributors

The [Project Board](https://github.com/imyousuf/webhook-broker/projects/1) represents the works slated for current release. Once you find something you want to work on please fork the project, create a topic/issue/ticket branch, once complete open a PR against master.

If you find any bug, please report it [here](https://github.com/imyousuf/webhook-broker/issues).

For all support and discussion please use the Slack Channel **`#webhook-broker`** in the [Gophers](https://gophers.slack.com/) workspace. For direct invite please email to `webhook-broker at imytech.net`.

Please check [Developers](./docs/developers.md) for more developer note.

## License

The project is released under [ASL 2.0](./LICENSE)
