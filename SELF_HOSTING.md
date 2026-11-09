
## Self Hosting Guide

**Quick Note**: dummie is still in the early stages of development, and APIs
and architecture may change. Having said that, its in a working state and can
be used at this point in time.

To setup dummie, you'll need
- 2 separate servers
- 2 different domains (eg: dummie.dev and dummie.app)

One acts as the control server, and the other acts as a qemu host machine where
the VMs will be spawned. The qemu host machine will need to have nested
virtualization enabled, if its a VM in itself.

### Domains

- console.dummie.dev
- s3.dummie.dev
- console-s3.dummie.dev
- *.dummie.app
- *.shell.dummie.dev

### Control Server

- RustFS Prep

```sh
mkdir rustfs-logs rustfs-data
sudo chown -R 10001:10001 rustfs-logs rustfs-data
```

- Create a bucket

- Create a key with this policy

```sh
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:*"
      ],
      "Resource": [
        "arn:aws:s3:::dummie/*"
      ]
    },
    {
      "Effect": "Allow",
      "Action": [
        "sts:AssumeRole"
      ]
    }
  ]
}
```

```sh
sudo tee /etc/dclient/config.yaml > /dev/null <<'EOF'
control_url: https://console.dummie.dev
enrollment_key: paste-the-generated-enrollment-key
insecure: true

data_dir: /var/lib/dclient
socket: /run/dclient/dclient.sock
group: dclient

features:
  ip_forward: true
  kvm_access: true
  nftables: true
  docker_compat: true
  suricata: true
  dhcp: true
  metadata: true

network:
  pool: 10.64.0.0/16
  gateway: 10.64.0.1
  uplink: enp0s2
  dns: 1.1.1.1
  queues: 4
EOF

sudo dclient install
```

### FSx for OpenZFS VM disks (optional)

VM disks can live on FSx for OpenZFS instead of the host's local disk, so
compute and storage scale separately. Each VM gets its own volume, cloned from
a golden snapshot of its OS image, and hosts mount it over NFS.

1. Create an FSx for OpenZFS file system in the same VPC and region as the
   qemu hosts, ideally in the hosts' AZ:
   - deployment type `SINGLE_AZ_HA_2`, so a file server failure doesn't take
     the disks down with it
   - SSD storage sized for every VM disk plus the golden images
   - leave the root volume at its defaults; dummie creates the rest

2. Give the file system a security group that allows inbound TCP 2049 (NFS)
   from the qemu hosts' security group only. The control server doesn't
   need NFS access.

3. Attach an IAM role to the control server's EC2 instance with the policy
   below. The qemu hosts need no AWS permissions because they only mount NFS.
   Replace `REGION`, `ACCOUNT` and `FS_ID`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ReadFSx",
      "Effect": "Allow",
      "Action": [
        "fsx:DescribeFileSystems",
        "fsx:DescribeVolumes",
        "fsx:DescribeSnapshots"
      ],
      "Resource": "*"
    },
    {
      "Sid": "ManageVMVolumes",
      "Effect": "Allow",
      "Action": [
        "fsx:CreateVolume",
        "fsx:UpdateVolume",
        "fsx:DeleteVolume",
        "fsx:CreateSnapshot"
      ],
      "Resource": [
        "arn:aws:fsx:REGION:ACCOUNT:file-system/FS_ID",
        "arn:aws:fsx:REGION:ACCOUNT:volume/FS_ID/*",
        "arn:aws:fsx:REGION:ACCOUNT:snapshot/*"
      ]
    }
  ]
}
```

   Control picks up credentials through the default AWS chain, so no keys go
   in its config. If control runs outside AWS, use an IAM user with this
   policy, pass `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` in
   `control/.env`, and rotate them.

4. Prepare each qemu host: install `nfs-common`, then make sure
   `dclient doctor` passes "fsx volumes can be mounted". Check that a host can
   mount the file system the way dclient does:

```sh
sudo mkdir -p /mnt/fsx-test
sudo mount -t nfs -o nfsvers=4.1 <fsx-dns-name>:/fsx /mnt/fsx-test
sudo umount /mnt/fsx-test
```

5. In the console, under Admin → Settings:
   - **FSx file system ID**: `fs-…`
   - **FSx region**: the file system's region. Leave it empty to use the
     control server's AWS region.
   - **FSx NFS clients**: the qemu hosts' subnet CIDR, e.g. `10.0.1.0/24`.
     Hosts mount as root, so keep this as narrow as possible.
   - **FSx parent volume ID**: leave empty to use the root volume.
   - **FSx for OpenZFS VM disks**: on.

Users then pick the disk storage per VM. Admin → FSx images / FSx disks lists
the volumes dummie manages; delete them there rather than in the AWS console.
The first VM from a new OS image takes longer because it builds the golden
image; later VMs only need a clone.

## FAQ

- Can I run systemd-resolved on the qemu-host?
Yes. CoreDNS does not bind 0.0.0.0, it binds only the gateway address of the VM network (10.64.0.1:53), which is what guests are pointed at for DNS.
