variable "ssh_ingress_cidrs" {
  description = "CIDRs allowed to SSH into the NAT/bastion instance"
  type        = list(string)
}
