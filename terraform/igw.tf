resource "aws_internet_gateway" "igw-aps1-test-001" {
  vpc_id = aws_vpc.vpc-aps1-test-001.id

  tags = {
    Name = "igw-aps1-test-001"
  }
}
