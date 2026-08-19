from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast






T = TypeVar("T", bound="ResolvedHost")



@_attrs_define
class ResolvedHost:
    """ 
        Attributes:
            addresses (list[str] | Unset):
            error (str | Unset): Error is set, with a 200, when the name did not resolve. That is an answer
                about the name rather than a failure of this server.
            host (str | Unset):
            truncated (bool | Unset): Truncated is true when the name answered with more addresses than are
                worth recording; see resolveMaxAddresses.
     """

    addresses: list[str] | Unset = UNSET
    error: str | Unset = UNSET
    host: str | Unset = UNSET
    truncated: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        addresses: list[str] | Unset = UNSET
        if not isinstance(self.addresses, Unset):
            addresses = self.addresses



        error = self.error

        host = self.host

        truncated = self.truncated


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if addresses is not UNSET:
            field_dict["addresses"] = addresses
        if error is not UNSET:
            field_dict["error"] = error
        if host is not UNSET:
            field_dict["host"] = host
        if truncated is not UNSET:
            field_dict["truncated"] = truncated

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        addresses = cast(list[str], d.pop("addresses", UNSET))


        error = d.pop("error", UNSET)

        host = d.pop("host", UNSET)

        truncated = d.pop("truncated", UNSET)

        resolved_host = cls(
            addresses=addresses,
            error=error,
            host=host,
            truncated=truncated,
        )


        resolved_host.additional_properties = d
        return resolved_host

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
