resource "aws_route_table" "rt-aps1-test-private-001" {
  vpc_id = aws_vpc.vpc-aps1-test-001.id

  route {
    cidr_block           = "0.0.0.0/0"
    network_interface_id = aws_network_interface.nic-aps1-test-nat-001.id
  }

  tags = {
    Name = "rt-aps1-test-private-001"
  }
}

resource "aws_route_table" "rt-aps1-test-public-001" {
  vpc_id = aws_vpc.vpc-aps1-test-001.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.igw-aps1-test-001.id
  }

  tags = {
    Name = "rt-aps1-test-public-001"
  }
}
