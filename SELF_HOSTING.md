
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
