
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

### Edge in front of several qemu hosts (optional)

When more than one qemu host shares a domain, an edge routes each name to the
host serving it: http by Host, https by SNI (passed through, so the edge holds
no certificates) and ssh by login name (`ssh <vm>@dummie.app`). It runs no VMs,
only dproxy and dpipe. Without an edge, point the domain at a host as before;
hosts stay directly reachable either way. RDP is not routed through the edge.

1. Use a small server in the same network as the qemu hosts. Move its own sshd
   off port 22, since dproxy listens on 22, 80 and 443.

2. Open 22, 80 and 443 to the internet on the edge. On the qemu hosts, allow
   22, 80 and 443 from the edge's private address.

3. Install dclient with only the edge role set. Every VM feature stays off, and
   dclient refuses to start if one is on:

```sh
sudo tee /etc/dclient/config.yaml > /dev/null <<'EOF'
control_url: https://console.dummie.dev
enrollment_key: paste-the-generated-enrollment-key
role: edge
EOF

sudo dclient install
```

4. In the console:
   - **Admin → Clients → the edge**: check "Address hosts see" is the edge's
     private address. It defaults to the address of the uplink interface; set
     it if hosts see the edge from another address (NAT, second interface).
   - **Admin → Clients → each host**: likewise check "Address the edge dials".
   - **Admin → Domains**: pick the edge in the domain's Edge column.

5. Point every record that pointed at a qemu host at the edge's public address
   instead: `*.dummie.app` and `*.shell.dummie.dev`. Custom domains follow, since
   they CNAME to `<vm>.dummie.app`.

To check, `journalctl -u dproxy -f` on the edge shows `edge route` per request
and `ssh authorize` per login. On a host, an ssh login through the edge is
logged as `ssh authorize … upstream=true`; `ssh deny: upstream key from an
untrusted source` means the host sees the edge from an address other than the
one set in step 4. The real user of each ssh login is only in the edge's
journal.

## FAQ

- Can I run systemd-resolved on the qemu-host?
Yes. CoreDNS does not bind 0.0.0.0, it binds only the gateway address of the VM network (10.64.0.1:53), which is what guests are pointed at for DNS.
