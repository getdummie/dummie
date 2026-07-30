resource "aws_key_pair" "kp-aps1-test-001" {
  key_name   = "kp-aps1-test-001"
  public_key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJz0MRvFLR+QpHhmkTn7LJoctIfmRQUyrXkr8bmuohwc cc@scarburn"

  tags = {
    Name = "kp-aps1-test-001"
  }
}
