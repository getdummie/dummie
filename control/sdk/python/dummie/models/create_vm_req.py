from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast

if TYPE_CHECKING:
  from ..models.create_target_req import CreateTargetReq





T = TypeVar("T", bound="CreateVMReq")



@_attrs_define
class CreateVMReq:
    """ 
        Attributes:
            client_id (str | Unset):
            cpus (int | Unset):
            default_port (int | Unset): DefaultPort is where a request goes when nothing picks a port. 0 means
                unset, and becomes defaultVMPort.
            disk_size (str | Unset): DiskSize sizes the per-VM overlay. Deliberately not rootfs_size: that sizes
                the base image, which is cached by the tar's digest alone and shared by
                every VM built from that tar -- so a per-user value there would be silently
                ignored for everyone after the first.
            kernel_id (str | Unset): KernelID names a row in the kernels catalogue. A caller picks from what an
                admin uploaded rather than supplying a url: the artifact a guest boots is
                the installation's decision, and an arbitrary url would make every VM's
                kernel a fetch from wherever its creator pointed.
            memory_mib (int | Unset):
            name (str | Unset):
            osimage_id (str | Unset): OSImageID names a row in the OS images catalogue, and is the root
                filesystem half of the same arrangement as KernelID.
            public_ports (list[int] | Unset): PublicPorts is every port the VM publishes. Empty publishes nothing.
            targets (list[CreateTargetReq] | Unset): Targets is the egress allowlist to give the VM at birth, in exactly the
                shape POST /vms/{id}/targets takes one at a time. Empty is a VM that may
                reach nothing until somebody allows something.

                Here rather than left to follow-up calls because the guest boots and starts
                trying to reach things immediately: an allowlist applied a moment later is a
                window in which the sandbox is already running and already denied, which
                reads as a broken VM rather than as policy arriving.
            ttl_seconds (int | Unset): TTLSeconds makes this a temporary sandbox: the control plane destroys the VM
                this many seconds after the row is written. 0 is the ordinary case -- a VM
                that lives until somebody destroys it.

                Measured from the create, and it keeps running while the VM is stopped. A
                clock that paused on stop would make the TTL evadable by exactly the trick
                the quota already refuses to reward, and what a temporary sandbox is
                bounding is wall-clock exposure rather than uptime.
     """

    client_id: str | Unset = UNSET
    cpus: int | Unset = UNSET
    default_port: int | Unset = UNSET
    disk_size: str | Unset = UNSET
    kernel_id: str | Unset = UNSET
    memory_mib: int | Unset = UNSET
    name: str | Unset = UNSET
    osimage_id: str | Unset = UNSET
    public_ports: list[int] | Unset = UNSET
    targets: list[CreateTargetReq] | Unset = UNSET
    ttl_seconds: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.create_target_req import CreateTargetReq
        client_id = self.client_id

        cpus = self.cpus

        default_port = self.default_port

        disk_size = self.disk_size

        kernel_id = self.kernel_id

        memory_mib = self.memory_mib

        name = self.name

        osimage_id = self.osimage_id

        public_ports: list[int] | Unset = UNSET
        if not isinstance(self.public_ports, Unset):
            public_ports = self.public_ports



        targets: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.targets, Unset):
            targets = []
            for targets_item_data in self.targets:
                targets_item = targets_item_data.to_dict()
                targets.append(targets_item)



        ttl_seconds = self.ttl_seconds


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if client_id is not UNSET:
            field_dict["client_id"] = client_id
        if cpus is not UNSET:
            field_dict["cpus"] = cpus
        if default_port is not UNSET:
            field_dict["default_port"] = default_port
        if disk_size is not UNSET:
            field_dict["disk_size"] = disk_size
        if kernel_id is not UNSET:
            field_dict["kernel_id"] = kernel_id
        if memory_mib is not UNSET:
            field_dict["memory_mib"] = memory_mib
        if name is not UNSET:
            field_dict["name"] = name
        if osimage_id is not UNSET:
            field_dict["osimage_id"] = osimage_id
        if public_ports is not UNSET:
            field_dict["public_ports"] = public_ports
        if targets is not UNSET:
            field_dict["targets"] = targets
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_target_req import CreateTargetReq
        d = dict(src_dict)
        client_id = d.pop("client_id", UNSET)

        cpus = d.pop("cpus", UNSET)

        default_port = d.pop("default_port", UNSET)

        disk_size = d.pop("disk_size", UNSET)

        kernel_id = d.pop("kernel_id", UNSET)

        memory_mib = d.pop("memory_mib", UNSET)

        name = d.pop("name", UNSET)

        osimage_id = d.pop("osimage_id", UNSET)

        public_ports = cast(list[int], d.pop("public_ports", UNSET))


        _targets = d.pop("targets", UNSET)
        targets: list[CreateTargetReq] | Unset = UNSET
        if _targets is not UNSET:
            targets = []
            for targets_item_data in _targets:
                targets_item = CreateTargetReq.from_dict(targets_item_data)



                targets.append(targets_item)


        ttl_seconds = d.pop("ttl_seconds", UNSET)

        create_vm_req = cls(
            client_id=client_id,
            cpus=cpus,
            default_port=default_port,
            disk_size=disk_size,
            kernel_id=kernel_id,
            memory_mib=memory_mib,
            name=name,
            osimage_id=osimage_id,
            public_ports=public_ports,
            targets=targets,
            ttl_seconds=ttl_seconds,
        )


        create_vm_req.additional_properties = d
        return create_vm_req

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
