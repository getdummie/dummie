resource "aws_subnet" "snet-aps1-test-public-001" {
  vpc_id                  = aws_vpc.vpc-aps1-test-001.id
  cidr_block              = "10.61.0.0/24"
  availability_zone       = "ap-south-1a"
  map_public_ip_on_launch = true

  tags = {
    Name = "snet-aps1-test-public-001"
  }
}

resource "aws_subnet" "snet-aps1-test-private-001" {
  vpc_id            = aws_vpc.vpc-aps1-test-001.id
  cidr_block        = "10.61.1.0/24"
  availability_zone = "ap-south-1a"

  tags = {
    Name = "snet-aps1-test-private-001"
  }
}
