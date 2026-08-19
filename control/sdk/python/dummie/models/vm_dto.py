from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast

if TYPE_CHECKING:
  from ..models.vm_dto_spec import VmDTOSpec





T = TypeVar("T", bound="VmDTO")



@_attrs_define
class VmDTO:
    """ 
        Attributes:
            boot (str | Unset):
            client_id (str | Unset):
            console_url (str | Unset): ConsoleURL is the websocket endpoint the browser terminal talks to, filled
                in on the same views that fill in URL and empty under the same conditions.
                It is not a credential: opening it needs a token from POST
                /vms/:id/console-token, which is minted per user and expires.
            cpus (int | Unset):
            created_at (str | Unset):
            created_by (str | Unset): CreatedBy is "" for a VM nobody here asked for -- one adopted from an
                client's inventory report, or created before ownership was recorded.
            default_port (int | Unset): The routing the proxy config is generated from: where a request goes when
                nothing picks a port, and every port the VM publishes.
            disk_mib (int | Unset):
            expires_at (str | Unset): ExpiresAt is when a TTL'd VM is due to be destroyed, and "" for one with no
                TTL. It comes from the pending scheduled task, which is the only place the
                deadline is written -- there is no expires_at column, so that the answer to
                "when does this die" cannot be two different things.

                A deadline in the past means the runner has not got to it yet. That is worth
                showing as-is rather than hiding: the VM really is still there.
            id (str | Unset):
            ip (str | Unset):
            last_error (str | Unset):
            memory_mib (int | Unset):
            name (str | Unset):
            public_ports (list[int] | Unset):
            reported_at (str | Unset): ReportedAt is when the host last confirmed this VM; "" means it never has.
                'running' is the host's claim as of that moment, not a live observation, so
                a reader has to weigh the status against this timestamp -- a status of
                'running' with a stale ReportedAt means "was running when last seen".
            spec (VmDTOSpec | Unset): swaggertype is for the spec generator only: the stored spec is returned
                verbatim and has no fixed shape, and swag cannot follow a RawMessage into
                the standard library on its own.
            started_at (str | Unset):
            status (str | Unset): pending | running | failed
            url (str | Unset): URL is where the VM answers http, when a caller asked for a view that
                resolves it. "" when the host has no domain, or when the endpoint does not
                work it out -- the list endpoints do not, since resolving it per row would
                be a query per VM.
            vm_id (str | Unset):
     """

    boot: str | Unset = UNSET
    client_id: str | Unset = UNSET
    console_url: str | Unset = UNSET
    cpus: int | Unset = UNSET
    created_at: str | Unset = UNSET
    created_by: str | Unset = UNSET
    default_port: int | Unset = UNSET
    disk_mib: int | Unset = UNSET
    expires_at: str | Unset = UNSET
    id: str | Unset = UNSET
    ip: str | Unset = UNSET
    last_error: str | Unset = UNSET
    memory_mib: int | Unset = UNSET
    name: str | Unset = UNSET
    public_ports: list[int] | Unset = UNSET
    reported_at: str | Unset = UNSET
    spec: VmDTOSpec | Unset = UNSET
    started_at: str | Unset = UNSET
    status: str | Unset = UNSET
    url: str | Unset = UNSET
    vm_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.vm_dto_spec import VmDTOSpec
        boot = self.boot

        client_id = self.client_id

        console_url = self.console_url

        cpus = self.cpus

        created_at = self.created_at

        created_by = self.created_by

        default_port = self.default_port

        disk_mib = self.disk_mib

        expires_at = self.expires_at

        id = self.id

        ip = self.ip

        last_error = self.last_error

        memory_mib = self.memory_mib

        name = self.name

        public_ports: list[int] | Unset = UNSET
        if not isinstance(self.public_ports, Unset):
            public_ports = self.public_ports



        reported_at = self.reported_at

        spec: dict[str, Any] | Unset = UNSET
        if not isinstance(self.spec, Unset):
            spec = self.spec.to_dict()

        started_at = self.started_at

        status = self.status

        url = self.url

        vm_id = self.vm_id


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if boot is not UNSET:
            field_dict["boot"] = boot
        if client_id is not UNSET:
            field_dict["client_id"] = client_id
        if console_url is not UNSET:
            field_dict["console_url"] = console_url
        if cpus is not UNSET:
            field_dict["cpus"] = cpus
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if created_by is not UNSET:
            field_dict["created_by"] = created_by
        if default_port is not UNSET:
            field_dict["default_port"] = default_port
        if disk_mib is not UNSET:
            field_dict["disk_mib"] = disk_mib
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if id is not UNSET:
            field_dict["id"] = id
        if ip is not UNSET:
            field_dict["ip"] = ip
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if memory_mib is not UNSET:
            field_dict["memory_mib"] = memory_mib
        if name is not UNSET:
            field_dict["name"] = name
        if public_ports is not UNSET:
            field_dict["public_ports"] = public_ports
        if reported_at is not UNSET:
            field_dict["reported_at"] = reported_at
        if spec is not UNSET:
            field_dict["spec"] = spec
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if status is not UNSET:
            field_dict["status"] = status
        if url is not UNSET:
            field_dict["url"] = url
        if vm_id is not UNSET:
            field_dict["vm_id"] = vm_id

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.vm_dto_spec import VmDTOSpec
        d = dict(src_dict)
        boot = d.pop("boot", UNSET)

        client_id = d.pop("client_id", UNSET)

        console_url = d.pop("console_url", UNSET)

        cpus = d.pop("cpus", UNSET)

        created_at = d.pop("created_at", UNSET)

        created_by = d.pop("created_by", UNSET)

        default_port = d.pop("default_port", UNSET)

        disk_mib = d.pop("disk_mib", UNSET)

        expires_at = d.pop("expires_at", UNSET)

        id = d.pop("id", UNSET)

        ip = d.pop("ip", UNSET)

        last_error = d.pop("last_error", UNSET)

        memory_mib = d.pop("memory_mib", UNSET)

        name = d.pop("name", UNSET)

        public_ports = cast(list[int], d.pop("public_ports", UNSET))


        reported_at = d.pop("reported_at", UNSET)

        _spec = d.pop("spec", UNSET)
        spec: VmDTOSpec | Unset
        if isinstance(_spec,  Unset):
            spec = UNSET
        else:
            spec = VmDTOSpec.from_dict(_spec)




        started_at = d.pop("started_at", UNSET)

        status = d.pop("status", UNSET)

        url = d.pop("url", UNSET)

        vm_id = d.pop("vm_id", UNSET)

        vm_dto = cls(
            boot=boot,
            client_id=client_id,
            console_url=console_url,
            cpus=cpus,
            created_at=created_at,
            created_by=created_by,
            default_port=default_port,
            disk_mib=disk_mib,
            expires_at=expires_at,
            id=id,
            ip=ip,
            last_error=last_error,
            memory_mib=memory_mib,
            name=name,
            public_ports=public_ports,
            reported_at=reported_at,
            spec=spec,
            started_at=started_at,
            status=status,
            url=url,
            vm_id=vm_id,
        )


        vm_dto.additional_properties = d
        return vm_dto

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
