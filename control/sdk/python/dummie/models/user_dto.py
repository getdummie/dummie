from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset






T = TypeVar("T", bound="UserDTO")



@_attrs_define
class UserDTO:
    """ 
        Attributes:
            email (str | Unset):
            first_name (str | Unset):
            id (str | Unset):
            last_name (str | Unset):
            user_type (str | Unset):
            username (str | Unset):
     """

    email: str | Unset = UNSET
    first_name: str | Unset = UNSET
    id: str | Unset = UNSET
    last_name: str | Unset = UNSET
    user_type: str | Unset = UNSET
    username: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        email = self.email

        first_name = self.first_name

        id = self.id

        last_name = self.last_name

        user_type = self.user_type

        username = self.username


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if email is not UNSET:
            field_dict["email"] = email
        if first_name is not UNSET:
            field_dict["first_name"] = first_name
        if id is not UNSET:
            field_dict["id"] = id
        if last_name is not UNSET:
            field_dict["last_name"] = last_name
        if user_type is not UNSET:
            field_dict["user_type"] = user_type
        if username is not UNSET:
            field_dict["username"] = username

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        email = d.pop("email", UNSET)

        first_name = d.pop("first_name", UNSET)

        id = d.pop("id", UNSET)

        last_name = d.pop("last_name", UNSET)

        user_type = d.pop("user_type", UNSET)

        username = d.pop("username", UNSET)

        user_dto = cls(
            email=email,
            first_name=first_name,
            id=id,
            last_name=last_name,
            user_type=user_type,
            username=username,
        )


        user_dto.additional_properties = d
        return user_dto

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
