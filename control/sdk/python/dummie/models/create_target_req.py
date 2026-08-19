from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset






T = TypeVar("T", bound="CreateTargetReq")



@_attrs_define
class CreateTargetReq:
    """ 
        Attributes:
            destination (str | Unset):
            kind (str | Unset): Kind is what the caller says this is. Optional: omitted, the destination is
                classified for them. Supplied and disagreeing with the destination, the
                request is rejected -- a form that asked for an address and got a hostname
                has a mistake in it, and quietly storing the other kind hides it.
            note (str | Unset):
            ports (str | Unset): Ports means different things to the two kinds. For an address it is
                Suricata's port syntax and goes straight into a rule header. For a domain it
                is which of the two web ports the name is allowed on -- see domainPorts in
                suricata_rules.go, and domainTargetPorts below for what is accepted.
            transport (str | Unset): Transport is ignored when the destination is a domain: both rules a domain
                compiles to are tcp by construction, so there is nothing to choose.
            ttl_seconds (int | Unset): TTLSeconds makes this a temporary allowance: the control plane removes it
                this many seconds from now and regenerates the host's policy. 0 is a
                permanent one, which is what an omitted field means.
     """

    destination: str | Unset = UNSET
    kind: str | Unset = UNSET
    note: str | Unset = UNSET
    ports: str | Unset = UNSET
    transport: str | Unset = UNSET
    ttl_seconds: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        destination = self.destination

        kind = self.kind

        note = self.note

        ports = self.ports

        transport = self.transport

        ttl_seconds = self.ttl_seconds


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if destination is not UNSET:
            field_dict["destination"] = destination
        if kind is not UNSET:
            field_dict["kind"] = kind
        if note is not UNSET:
            field_dict["note"] = note
        if ports is not UNSET:
            field_dict["ports"] = ports
        if transport is not UNSET:
            field_dict["transport"] = transport
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        destination = d.pop("destination", UNSET)

        kind = d.pop("kind", UNSET)

        note = d.pop("note", UNSET)

        ports = d.pop("ports", UNSET)

        transport = d.pop("transport", UNSET)

        ttl_seconds = d.pop("ttl_seconds", UNSET)

        create_target_req = cls(
            destination=destination,
            kind=kind,
            note=note,
            ports=ports,
            transport=transport,
            ttl_seconds=ttl_seconds,
        )


        create_target_req.additional_properties = d
        return create_target_req

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
