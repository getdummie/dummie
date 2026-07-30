resource "aws_eip" "eip-aps1-test-nat-001" {
  domain            = "vpc"
  network_interface = aws_network_interface.nic-aps1-test-nat-001.id

  tags = {
    Name = "eip-aps1-test-nat-001"
  }

  depends_on = [aws_internet_gateway.igw-aps1-test-001]
}
