from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset






T = TypeVar("T", bound="QuotaDTO")



@_attrs_define
class QuotaDTO:
    """ 
        Attributes:
            disk_limit_mib (int | Unset):
            disk_used_mib (int | Unset):
            memory_limit_mib (int | Unset):
            memory_used_mib (int | Unset):
            vcpu_limit (int | Unset):
            vcpu_used (int | Unset):
     """

    disk_limit_mib: int | Unset = UNSET
    disk_used_mib: int | Unset = UNSET
    memory_limit_mib: int | Unset = UNSET
    memory_used_mib: int | Unset = UNSET
    vcpu_limit: int | Unset = UNSET
    vcpu_used: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        disk_limit_mib = self.disk_limit_mib

        disk_used_mib = self.disk_used_mib

        memory_limit_mib = self.memory_limit_mib

        memory_used_mib = self.memory_used_mib

        vcpu_limit = self.vcpu_limit

        vcpu_used = self.vcpu_used


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if disk_limit_mib is not UNSET:
            field_dict["disk_limit_mib"] = disk_limit_mib
        if disk_used_mib is not UNSET:
            field_dict["disk_used_mib"] = disk_used_mib
        if memory_limit_mib is not UNSET:
            field_dict["memory_limit_mib"] = memory_limit_mib
        if memory_used_mib is not UNSET:
            field_dict["memory_used_mib"] = memory_used_mib
        if vcpu_limit is not UNSET:
            field_dict["vcpu_limit"] = vcpu_limit
        if vcpu_used is not UNSET:
            field_dict["vcpu_used"] = vcpu_used

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        disk_limit_mib = d.pop("disk_limit_mib", UNSET)

        disk_used_mib = d.pop("disk_used_mib", UNSET)

        memory_limit_mib = d.pop("memory_limit_mib", UNSET)

        memory_used_mib = d.pop("memory_used_mib", UNSET)

        vcpu_limit = d.pop("vcpu_limit", UNSET)

        vcpu_used = d.pop("vcpu_used", UNSET)

        quota_dto = cls(
            disk_limit_mib=disk_limit_mib,
            disk_used_mib=disk_used_mib,
            memory_limit_mib=memory_limit_mib,
            memory_used_mib=memory_used_mib,
            vcpu_limit=vcpu_limit,
            vcpu_used=vcpu_used,
        )


        quota_dto.additional_properties = d
        return quota_dto

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
