from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset






T = TypeVar("T", bound="AdminUserDTO")



@_attrs_define
class AdminUserDTO:
    """ 
        Attributes:
            created_at (str | Unset):
            disk_limit_mib (int | Unset):
            email (str | Unset):
            first_name (str | Unset):
            id (str | Unset):
            last_name (str | Unset):
            memory_limit_mib (int | Unset):
            public_key (str | Unset): '' when the account has no key on file, which is what blocks it from
                creating a VM.
            updated_at (str | Unset):
            user_type (str | Unset):
            username (str | Unset):
            vcpu_limit (int | Unset): Recorded allowances. Nothing enforces these yet; see 0008_user_quotas.
     """

    created_at: str | Unset = UNSET
    disk_limit_mib: int | Unset = UNSET
    email: str | Unset = UNSET
    first_name: str | Unset = UNSET
    id: str | Unset = UNSET
    last_name: str | Unset = UNSET
    memory_limit_mib: int | Unset = UNSET
    public_key: str | Unset = UNSET
    updated_at: str | Unset = UNSET
    user_type: str | Unset = UNSET
    username: str | Unset = UNSET
    vcpu_limit: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        created_at = self.created_at

        disk_limit_mib = self.disk_limit_mib

        email = self.email

        first_name = self.first_name

        id = self.id

        last_name = self.last_name

        memory_limit_mib = self.memory_limit_mib

        public_key = self.public_key

        updated_at = self.updated_at

        user_type = self.user_type

        username = self.username

        vcpu_limit = self.vcpu_limit


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if disk_limit_mib is not UNSET:
            field_dict["disk_limit_mib"] = disk_limit_mib
        if email is not UNSET:
            field_dict["email"] = email
        if first_name is not UNSET:
            field_dict["first_name"] = first_name
        if id is not UNSET:
            field_dict["id"] = id
        if last_name is not UNSET:
            field_dict["last_name"] = last_name
        if memory_limit_mib is not UNSET:
            field_dict["memory_limit_mib"] = memory_limit_mib
        if public_key is not UNSET:
            field_dict["public_key"] = public_key
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if user_type is not UNSET:
            field_dict["user_type"] = user_type
        if username is not UNSET:
            field_dict["username"] = username
        if vcpu_limit is not UNSET:
            field_dict["vcpu_limit"] = vcpu_limit

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        created_at = d.pop("created_at", UNSET)

        disk_limit_mib = d.pop("disk_limit_mib", UNSET)

        email = d.pop("email", UNSET)

        first_name = d.pop("first_name", UNSET)

        id = d.pop("id", UNSET)

        last_name = d.pop("last_name", UNSET)

        memory_limit_mib = d.pop("memory_limit_mib", UNSET)

        public_key = d.pop("public_key", UNSET)

        updated_at = d.pop("updated_at", UNSET)

        user_type = d.pop("user_type", UNSET)

        username = d.pop("username", UNSET)

        vcpu_limit = d.pop("vcpu_limit", UNSET)

        admin_user_dto = cls(
            created_at=created_at,
            disk_limit_mib=disk_limit_mib,
            email=email,
            first_name=first_name,
            id=id,
            last_name=last_name,
            memory_limit_mib=memory_limit_mib,
            public_key=public_key,
            updated_at=updated_at,
            user_type=user_type,
            username=username,
            vcpu_limit=vcpu_limit,
        )


        admin_user_dto.additional_properties = d
        return admin_user_dto

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
