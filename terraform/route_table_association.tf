resource "aws_route_table_association" "rta-aps1-test-public-001" {
  subnet_id      = aws_subnet.snet-aps1-test-public-001.id
  route_table_id = aws_route_table.rt-aps1-test-public-001.id
}

resource "aws_route_table_association" "rta-aps1-test-private-001" {
  subnet_id      = aws_subnet.snet-aps1-test-private-001.id
  route_table_id = aws_route_table.rt-aps1-test-private-001.id
}
