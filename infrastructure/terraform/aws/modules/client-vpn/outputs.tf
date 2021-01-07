output "vpn_arn" {
  description = "Client VPN ARN"
  value       = aws_ec2_client_vpn_endpoint.vpn.arn
}

output "vpn_id" {
  description = "Client VPN ID"
  value       = aws_ec2_client_vpn_endpoint.vpn.id
}
