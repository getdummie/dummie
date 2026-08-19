from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset






T = TypeVar("T", bound="DeniedSources")



@_attrs_define
class DeniedSources:
    """ 
        Attributes:
            lookups (bool | Unset):
            packets (bool | Unset):
     """

    lookups: bool | Unset = UNSET
    packets: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        lookups = self.lookups

        packets = self.packets


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if lookups is not UNSET:
            field_dict["lookups"] = lookups
        if packets is not UNSET:
            field_dict["packets"] = packets

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        lookups = d.pop("lookups", UNSET)

        packets = d.pop("packets", UNSET)

        denied_sources = cls(
            lookups=lookups,
            packets=packets,
        )


        denied_sources.additional_properties = d
        return denied_sources

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
