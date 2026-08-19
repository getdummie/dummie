from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, BinaryIO, TextIO, TYPE_CHECKING, Generator

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset






T = TypeVar("T", bound="DeniedAttemptDTO")



@_attrs_define
class DeniedAttemptDTO:
    """ 
        Attributes:
            address (str | Unset):
            app_proto (str | Unset): AppProto is what suricata identified the traffic as, when it got far enough to
                say -- "ssh", "ftp", "failed" when detection ran and found nothing. Empty for a
                flow dropped at the syn, which is most of them.
            attempts (int | Unset):
            domain (str | Unset):
            kind (str | Unset): Kind is "lookup" or "packet" -- which of the two things above happened. The UI
                switches on it, because a refused lookup has no address to offer.
            last_seen (str | Unset):
            port (int | Unset):
            proto (str | Unset): Proto is lowercased for display: suricata writes TCP, and every other place a
                transport appears in this system is lowercase.
            signature (str | Unset):
     """

    address: str | Unset = UNSET
    app_proto: str | Unset = UNSET
    attempts: int | Unset = UNSET
    domain: str | Unset = UNSET
    kind: str | Unset = UNSET
    last_seen: str | Unset = UNSET
    port: int | Unset = UNSET
    proto: str | Unset = UNSET
    signature: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)





    def to_dict(self) -> dict[str, Any]:
        address = self.address

        app_proto = self.app_proto

        attempts = self.attempts

        domain = self.domain

        kind = self.kind

        last_seen = self.last_seen

        port = self.port

        proto = self.proto

        signature = self.signature


        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if address is not UNSET:
            field_dict["address"] = address
        if app_proto is not UNSET:
            field_dict["app_proto"] = app_proto
        if attempts is not UNSET:
            field_dict["attempts"] = attempts
        if domain is not UNSET:
            field_dict["domain"] = domain
        if kind is not UNSET:
            field_dict["kind"] = kind
        if last_seen is not UNSET:
            field_dict["last_seen"] = last_seen
        if port is not UNSET:
            field_dict["port"] = port
        if proto is not UNSET:
            field_dict["proto"] = proto
        if signature is not UNSET:
            field_dict["signature"] = signature

        return field_dict



    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        address = d.pop("address", UNSET)

        app_proto = d.pop("app_proto", UNSET)

        attempts = d.pop("attempts", UNSET)

        domain = d.pop("domain", UNSET)

        kind = d.pop("kind", UNSET)

        last_seen = d.pop("last_seen", UNSET)

        port = d.pop("port", UNSET)

        proto = d.pop("proto", UNSET)

        signature = d.pop("signature", UNSET)

        denied_attempt_dto = cls(
            address=address,
            app_proto=app_proto,
            attempts=attempts,
            domain=domain,
            kind=kind,
            last_seen=last_seen,
            port=port,
            proto=proto,
            signature=signature,
        )


        denied_attempt_dto.additional_properties = d
        return denied_attempt_dto

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
