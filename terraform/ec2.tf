locals {
  # aws --region ap-south-1 ec2 describe-images   --owners 568608671756 --filters 'Name=name,Values=fck-nat-al2023-hvm-1.4.*-arm64-ebs' 'Name=state,Values=available' --query 'sort_by(Images, &CreationDate)[-1].[ImageId,Name,CreationDate]' --output text
  fck_nat_ami_id = "ami-007a361b1732d2cef"

  # aws --region ap-south-1 ec2 describe-images   --owners 099720109477 --filters 'Name=name,Values=ubuntu/images/hvm-ssd*/ubuntu-*-26.04-amd64-server-*' 'Name=state,Values=available' 'Name=architecture,Values=x86_64' --query 'sort_by(Images, &CreationDate)[-1].[ImageId,Name,CreationDate]' --output text
  ubuntu_ami_id = "ami-03aceba5007a505dc"
}

resource "aws_instance" "ec2-aps1-test-nat-001" {
  ami           = local.fck_nat_ami_id
  instance_type = "t4g.nano"
  key_name      = aws_key_pair.kp-aps1-test-001.key_name

  # Subnet and security groups come from the ENI, which is managed separately so
  # the EIP and the private route table keep a stable target across rebuilds.
  primary_network_interface {
    network_interface_id = aws_network_interface.nic-aps1-test-nat-001.id
  }

  tags = {
    Name = "ec2-aps1-test-nat-001"
  }
}

resource "aws_instance" "ec2-aps1-test-app-001" {
  ami                    = local.ubuntu_ami_id
  instance_type          = "c8i.xlarge"
  subnet_id              = aws_subnet.snet-aps1-test-private-001.id
  vpc_security_group_ids = [aws_security_group.sg-aps1-test-app-001.id]
  key_name               = aws_key_pair.kp-aps1-test-001.key_name

  cpu_options {
    nested_virtualization = "enabled"
  }

  tags = {
    Name = "ec2-aps1-test-app-001"
  }
}

