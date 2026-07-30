resource "aws_vpc" "vpc-aps1-test-001" {
  cidr_block = "10.61.0.0/16"

  tags = {
    Name = "vpc-aps1-test-001"
  }

  lifecycle {
    prevent_destroy = true
  }
}
