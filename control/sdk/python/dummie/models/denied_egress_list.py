from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast

if TYPE_CHECKING:
  from ..models.denied_attempt_dto import DeniedAttemptDTO
  from ..models.denied_sources import DeniedSources





T = TypeVar("T", bound="DeniedEgressList")



@_attrs_define
class DeniedEgressList:
    """ 
        Attributes:
            available (bool | Unset): Available is false when nothing is collecting, which is a different fact
                from an empty list and has to be readable as one.
            items (list[DeniedAttemptDTO] | Unset):
            recording (DeniedSources | Unset):
     """

    available: bool | Unset = UNSET
    items: list[DeniedAttemptDTO] | Unset = UNSET
    recording: DeniedSources | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        from ..models.denied_attempt_dto import DeniedAttemptDTO
        from ..models.denied_sources import DeniedSources
        available = self.available

        items: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.items, Unset):
            items = []
            for items_item_data in self.items:
                items_item = items_item_data.to_dict()
                items.append(items_item)



        recording: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recording, Unset):
            recording = self.recording.to_dict()


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if available is not UNSET:
            field_dict["available"] = available
        if items is not UNSET:
            field_dict["items"] = items
        if recording is not UNSET:
            field_dict["recording"] = recording

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.denied_attempt_dto import DeniedAttemptDTO
        from ..models.denied_sources import DeniedSources
        d = dict(src_dict)
        available = d.pop("available", UNSET)

        _items = d.pop("items", UNSET)
        items: list[DeniedAttemptDTO] | Unset = UNSET
        if _items is not UNSET:
            items = []
            for items_item_data in _items:
                items_item = DeniedAttemptDTO.from_dict(items_item_data)



                items.append(items_item)


        _recording = d.pop("recording", UNSET)
        recording: DeniedSources | Unset
        if isinstance(_recording,  Unset):
            recording = UNSET
        else:
            recording = DeniedSources.from_dict(_recording)




        denied_egress_list = cls(
            available=available,
            items=items,
            recording=recording,
        )


        denied_egress_list.additional_properties = d
        return denied_egress_list

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
