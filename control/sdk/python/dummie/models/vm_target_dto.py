from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset






T = TypeVar("T", bound="VmTargetDTO")



@_attrs_define
class VmTargetDTO:
    """ 
        Attributes:
            created_at (str | Unset):
            destination (str | Unset):
            expires_at (str | Unset): ExpiresAt is when a temporary allowance is due to be withdrawn, and "" for a
                permanent one. Read from the pending scheduled task rather than from a column
                on the row: the deadline is written in one place, and this is a view of it.
            id (str | Unset):
            kind (str | Unset): domain | ip
            note (str | Unset):
            ports (str | Unset): ip rows only; "" = any
            transport (str | Unset): ip rows only: tcp | udp | any
     """

    created_at: str | Unset = UNSET
    destination: str | Unset = UNSET
    expires_at: str | Unset = UNSET
    id: str | Unset = UNSET
    kind: str | Unset = UNSET
    note: str | Unset = UNSET
    ports: str | Unset = UNSET
    transport: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        created_at = self.created_at

        destination = self.destination

        expires_at = self.expires_at

        id = self.id

        kind = self.kind

        note = self.note

        ports = self.ports

        transport = self.transport


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if destination is not UNSET:
            field_dict["destination"] = destination
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if id is not UNSET:
            field_dict["id"] = id
        if kind is not UNSET:
            field_dict["kind"] = kind
        if note is not UNSET:
            field_dict["note"] = note
        if ports is not UNSET:
            field_dict["ports"] = ports
        if transport is not UNSET:
            field_dict["transport"] = transport

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        created_at = d.pop("created_at", UNSET)

        destination = d.pop("destination", UNSET)

        expires_at = d.pop("expires_at", UNSET)

        id = d.pop("id", UNSET)

        kind = d.pop("kind", UNSET)

        note = d.pop("note", UNSET)

        ports = d.pop("ports", UNSET)

        transport = d.pop("transport", UNSET)

        vm_target_dto = cls(
            created_at=created_at,
            destination=destination,
            expires_at=expires_at,
            id=id,
            kind=kind,
            note=note,
            ports=ports,
            transport=transport,
        )


        vm_target_dto.additional_properties = d
        return vm_target_dto

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
