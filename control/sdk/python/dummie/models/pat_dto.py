from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset






T = TypeVar("T", bound="PatDTO")



@_attrs_define
class PatDTO:
    """ 
        Attributes:
            created_at (str | Unset):
            expires_at (str | Unset): "" for the two that have not happened: a token that never expires, and one
                that has never been used.
            id (str | Unset):
            label (str | Unset):
            last_used_at (str | Unset):
            status (str | Unset): active | revoked | expired
            token_prefix (str | Unset):
     """

    created_at: str | Unset = UNSET
    expires_at: str | Unset = UNSET
    id: str | Unset = UNSET
    label: str | Unset = UNSET
    last_used_at: str | Unset = UNSET
    status: str | Unset = UNSET
    token_prefix: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        created_at = self.created_at

        expires_at = self.expires_at

        id = self.id

        label = self.label

        last_used_at = self.last_used_at

        status = self.status

        token_prefix = self.token_prefix


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if id is not UNSET:
            field_dict["id"] = id
        if label is not UNSET:
            field_dict["label"] = label
        if last_used_at is not UNSET:
            field_dict["last_used_at"] = last_used_at
        if status is not UNSET:
            field_dict["status"] = status
        if token_prefix is not UNSET:
            field_dict["token_prefix"] = token_prefix

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        created_at = d.pop("created_at", UNSET)

        expires_at = d.pop("expires_at", UNSET)

        id = d.pop("id", UNSET)

        label = d.pop("label", UNSET)

        last_used_at = d.pop("last_used_at", UNSET)

        status = d.pop("status", UNSET)

        token_prefix = d.pop("token_prefix", UNSET)

        pat_dto = cls(
            created_at=created_at,
            expires_at=expires_at,
            id=id,
            label=label,
            last_used_at=last_used_at,
            status=status,
            token_prefix=token_prefix,
        )


        pat_dto.additional_properties = d
        return pat_dto

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
