# AWS Infrastructure

This Terraform configuration provides a infrastructure as a code for Webhook Broker in AWS.

## Usage

Please make sure to create a `custom_vars.auto.tfvars` file with the following variables to use Client VPN [module](./modules/client-vpn/README.md).

```terraform
vpn_server_cert_arn = "arn:aws:acm:<REGION>:<ACCOUNT_ID>:certificate/<CERT_ARN_FOR_SERVER>"
vpn_client_cert_arn = "arn:aws:acm:<REGION>:<ACCOUNT_ID>:certificate/<CERT_ARN_FOR_CLIENT>"
```

Once you apply the config, it will generate a `config.ovpn` file for connecting to VPN; make sure to edit it as per Client VPN [README](./modules/client-vpn/README.md).

Also set the following variables -

```terraform
webhook_broker_https_cert_arn    = "arn:aws:acm:<REGION>:<ACCOUNT_ID>:certificate/<HTTPS_CERT_FOR_HOSTNAME>"
webhook_broker_access_log_bucket = "logs-bucket"
webhook_broker_access_log_path   = "path-prefix"
webhook_broker_hostname          = "match-hostname-to-certificate"
```

One thing to note is, when `terraform destroy` is called, it will not delete the ALB or the Route53 records; so please delete them manually for the time being.

The `kubernetes-dashboard` ingress controller is disabled by default as we are deploying the cluster in public subnet; please consider enabling it when deploying in a private subnet by passing [Helm Chart values](https://artifacthub.io/packages/helm/k8s-dashboard/kubernetes-dashboard).

Get login token to access the dashboard using -

```bash
kubectl -n kubernetes-dashboard describe secret $(kubectl -n kubernetes-dashboard get secret | grep k8s-dashboard-svc-controller-token | awk '{print $1}')
```
