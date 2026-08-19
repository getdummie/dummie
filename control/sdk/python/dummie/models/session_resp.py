from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast

if TYPE_CHECKING:
  from ..models.user_dto import UserDTO





T = TypeVar("T", bound="SessionResp")



@_attrs_define
class SessionResp:
    """ 
        Attributes:
            access_token (str | Unset):
            user (UserDTO | Unset):
     """

    access_token: str | Unset = UNSET
    user: UserDTO | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.user_dto import UserDTO
        access_token = self.access_token

        user: dict[str, Any] | Unset = UNSET
        if not isinstance(self.user, Unset):
            user = self.user.to_dict()


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if access_token is not UNSET:
            field_dict["access_token"] = access_token
        if user is not UNSET:
            field_dict["user"] = user

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.user_dto import UserDTO
        d = dict(src_dict)
        access_token = d.pop("access_token", UNSET)

        _user = d.pop("user", UNSET)
        user: UserDTO | Unset
        if isinstance(_user,  Unset):
            user = UNSET
        else:
            user = UserDTO.from_dict(_user)




        session_resp = cls(
            access_token=access_token,
            user=user,
        )


        session_resp.additional_properties = d
        return session_resp

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
