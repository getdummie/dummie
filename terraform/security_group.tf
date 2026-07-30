resource "aws_security_group" "sg-aps1-test-nat-001" {
  description = "NAT instance"
  vpc_id      = aws_vpc.vpc-aps1-test-001.id

  ingress {
    description = "All traffic from within the VPC"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = [aws_vpc.vpc-aps1-test-001.cidr_block]
  }

  ingress {
    description = "SSH for bastion access"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = var.ssh_ingress_cidrs
  }

  egress {
    description = "All outbound traffic"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "sg-aps1-test-nat-001"
  }
}

resource "aws_security_group" "sg-aps1-test-app-001" {
  description = "Private app instance"
  vpc_id      = aws_vpc.vpc-aps1-test-001.id

  ingress {
    description = "SSH from within the VPC"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [aws_vpc.vpc-aps1-test-001.cidr_block]
  }

  egress {
    description = "All outbound traffic"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "sg-aps1-test-app-001"
  }
}

resource "aws_network_interface" "nic-aps1-test-nat-001" {
  subnet_id         = aws_subnet.snet-aps1-test-public-001.id
  security_groups   = [aws_security_group.sg-aps1-test-nat-001.id]
  source_dest_check = false

  tags = {
    Name = "nic-aps1-test-nat-001"
  }
}
