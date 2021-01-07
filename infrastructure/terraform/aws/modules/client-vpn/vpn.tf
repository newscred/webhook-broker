provider aws {
   region = var.region
}

resource aws_security_group vpn_access {
   name = "shared-vpn-access"
   vpc_id = var.vpc_id
   ingress {
     from_port = 0
     protocol = "-1"
     to_port = 0
     cidr_blocks = ["0.0.0.0/0"]
   }
   egress {
     from_port = 0
     protocol = "-1"
     to_port = 0
     cidr_blocks = ["0.0.0.0/0"]
   }
}

resource aws_security_group vpn_dns {
   name = "vpn_dns"
   vpc_id = var.vpc_id
   ingress {
     from_port = 0
     protocol = "-1"
     to_port = 0
     security_groups = [aws_security_group.vpn_access.id]
   }
   egress {
     from_port = 0
     protocol = "-1"
     to_port = 0
     cidr_blocks = ["0.0.0.0/0"]
   }
 }

resource aws_route53_resolver_endpoint vpn_dns_resolver {
   name = "vpn-dns-access"
   direction = "INBOUND"

   security_group_ids = [aws_security_group.vpn_dns.id]

   ip_address {
     subnet_id = var.private_subnets[0]
   }
   ip_address {
     subnet_id = var.private_subnets[1]
   }
}

resource aws_ec2_client_vpn_endpoint vpn {
   client_cidr_block      = var.vpn_cidr
   split_tunnel           = false
   server_certificate_arn = var.vpn_server_cert_arn
   dns_servers = [
     aws_route53_resolver_endpoint.vpn_dns_resolver.ip_address.*.ip[0],
     aws_route53_resolver_endpoint.vpn_dns_resolver.ip_address.*.ip[1],
     "8.8.8.8"
   ]

   authentication_options {
     type                       = "certificate-authentication"
     root_certificate_chain_arn = var.vpn_client_cert_arn
   }

   connection_log_options {
     enabled = false
   }
}

resource aws_ec2_client_vpn_network_association private_sb_1 {
   client_vpn_endpoint_id = aws_ec2_client_vpn_endpoint.vpn.id
   subnet_id              = var.private_subnets[0]
   security_groups        = [aws_security_group.vpn_access.id]
}

resource aws_ec2_client_vpn_network_association private_sb_2 {
   client_vpn_endpoint_id = aws_ec2_client_vpn_endpoint.vpn.id
   subnet_id              = var.private_subnets[1]
   security_groups        = [aws_security_group.vpn_access.id]
}

resource aws_ec2_client_vpn_authorization_rule "client_vpn_ingress" {
  client_vpn_endpoint_id = aws_ec2_client_vpn_endpoint.vpn.id
  target_network_cidr    = "0.0.0.0/0"
  authorize_all_groups   = true
}

resource aws_ec2_client_vpn_route "internet_traffic_route" {
  client_vpn_endpoint_id = aws_ec2_client_vpn_endpoint.vpn.id
  destination_cidr_block = "0.0.0.0/0"
  target_vpc_subnet_id   = var.private_subnets[0]
}


data external "client_config" {
   depends_on = [aws_ec2_client_vpn_endpoint.vpn]
   program = [ "python", "${path.module}/vpn_config_file.py", aws_ec2_client_vpn_endpoint.vpn.id]
}

resource local_file "ovpn_file" {
  content              = data.external.client_config.result.output
  filename             = var.opvn_filepath
  file_permission      = "0644"
  directory_permission = "0755"
}
