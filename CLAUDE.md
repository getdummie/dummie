Each directory is a mini project in its own

`control` - the main control server controlling everything. within this is `cmd/dclient/`, this is the `dclient` binary which is supposed to run on the qemu host servers. `dclient` is responsible for spinning up a bunch of services and managing their configurations on the qemu host servers, including but not limited to `suricata` (as a docker image),  a `proxy` service and a `dpipe` service
`backstage` - this directory houses the code for `proxy` and `dpipe` referenced above. the `proxy` is used for establishing the connection and it hands of the file descriptor to `dpipe` and `dpipe` is responsible for making sure the connection stays connected even if `proxy` restarts or dies
`nix-vms` - this for local testing. VM created using this act as the qemu host server with nested virtualization so local testing can happen, and `dclient` runs on it. while the `control` server runs in docker on the host (not the VM acting as the qemu server)
`kernel` - this is where linux kernel is compiled, this is used by `dclient` to spin up microvms
`images` - this is where custom docker images are present, again used by `dclient` to spin up microvms

This is all that you need to know to navigate this project monorepo.
