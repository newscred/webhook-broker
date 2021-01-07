# client-vpn

This is a Terraform module used to create a AWS Client VPN connection with a VPC with full internet and VPC private network access.

## Usage

```terraform
module "client_vpn" {
  source = "./modules/client-vpn/"

  vpc_id              = var.vpc_id
  vpn_cidr            = local.vpn_cidr_block
  private_subnets     = [private_subnets[0], private_subnets[1]]
  vpn_server_cert_arn = var.vpn_server_cert_arn
  vpn_client_cert_arn = var.vpn_client_cert_arn
  region              = var.region
}
```

Follow [the blog post](https://cwong47.gitlab.io/technology-terraform-aws-client-vpn/) to create and upload certifications to use for VPN connections. Once the certificates are available just pass their ARN as variable here.

## Arguments

| Variable Name | Purpose | Required | Default Value |
| -- | -- | -- | -- |
| region | AWS Regions for VPN | yes | `us-east-1` |
| private_subnets | 2 Private subnets exactly | yes | `[]` empty array |
| vpc_id | VPC ID to pair the VPN with | yes | `""` (blank) |
| vpn_cidr | VPN CIDR to assign IP from | no | `17.10.0.0/16` |
| vpn_server_cert_arn | AWS ACM ARN for VPN Server certificate | yes | `arn:aws:acm:eu-west-2:xxxxxxxx:certificate/xxxxx` |
| vpn_client_cert_arn | AWS ACM ARN for VPN Server certificate | yes | `arn:aws:acm:eu-west-2:xxxxxxxxx:certificate/xxxxx` |
| opvn_filepath | Client OVPN file for connecting via OpenVPN | no | `config.ovpn` |

## Attributes

| Variable Name | Purpose |
| -- | -- |
| vpn_arn | The ARN of the VPN |
| vpn_id | The Client VPN Endpoint ID |

## Post Apply Notes

Once you apply the module, you will need to add the certs manually to the ovpn file as follows:

```cfg
cert /path/to/issued/client.vpn.example.com.crt
key /path/to/private/client.vpn.example.com.key
```

Also note when you connect with VPN, it will direct all traffic through the VPN, so make sure to have subnets have a IGW or NATGW with permissible network ACL, see [here](https://docs.aws.amazon.com/vpn/latest/clientvpn-admin/troubleshooting.html#no-internet-access) for troubleshooting.
