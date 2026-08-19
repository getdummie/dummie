from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast

if TYPE_CHECKING:
  from ..models.pat_dto import PatDTO





T = TypeVar("T", bound="CreatedTokenResp")



@_attrs_define
class CreatedTokenResp:
    """ 
        Attributes:
            personal_access_token (PatDTO | Unset):
            token (str | Unset):
     """

    personal_access_token: PatDTO | Unset = UNSET
    token: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.pat_dto import PatDTO
        personal_access_token: dict[str, Any] | Unset = UNSET
        if not isinstance(self.personal_access_token, Unset):
            personal_access_token = self.personal_access_token.to_dict()

        token = self.token


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if personal_access_token is not UNSET:
            field_dict["personal_access_token"] = personal_access_token
        if token is not UNSET:
            field_dict["token"] = token

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.pat_dto import PatDTO
        d = dict(src_dict)
        _personal_access_token = d.pop("personal_access_token", UNSET)
        personal_access_token: PatDTO | Unset
        if isinstance(_personal_access_token,  Unset):
            personal_access_token = UNSET
        else:
            personal_access_token = PatDTO.from_dict(_personal_access_token)




        token = d.pop("token", UNSET)

        created_token_resp = cls(
            personal_access_token=personal_access_token,
            token=token,
        )


        created_token_resp.additional_properties = d
        return created_token_resp

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
