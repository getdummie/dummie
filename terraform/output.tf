output "bastion_ip" {
  description = "Stable public IP of the NAT/bastion instance (EIP on its secondary ENI)"
  value       = aws_eip.eip-aps1-test-nat-001.public_ip
}

output "app_private_ip" {
  description = "Private IP of the app instance"
  value       = aws_instance.ec2-aps1-test-app-001.private_ip
}
