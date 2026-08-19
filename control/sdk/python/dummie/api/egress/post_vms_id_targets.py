from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error import ApiError
from ...models.create_target_req import CreateTargetReq
from ...models.vm_target_dto import VmTargetDTO
from typing import cast
from uuid import UUID



def _get_kwargs(
    id: UUID,
    *,
    body: CreateTargetReq,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}


    

    

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/vms/{id}/targets".format(id=quote(str(id), safe=""),),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ApiError | VmTargetDTO | None:
    if response.status_code == 201:
        response_201 = VmTargetDTO.from_dict(response.json())



        return response_201

    if response.status_code == 400:
        response_400 = ApiError.from_dict(response.json())



        return response_400

    if response.status_code == 401:
        response_401 = ApiError.from_dict(response.json())



        return response_401

    if response.status_code == 404:
        response_404 = ApiError.from_dict(response.json())



        return response_404

    if response.status_code == 409:
        response_409 = ApiError.from_dict(response.json())



        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[ApiError | VmTargetDTO]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateTargetReq,

) -> Response[ApiError | VmTargetDTO]:
    """ Allow a destination

     destination is a domain, an IP address, or a CIDR. Omit kind to have it classified for you; supply
    it and it must agree, since a form that asked for an address and got a hostname has a mistake in it.

    For an address, ports is Suricata's syntax (`22`, `80,443`, `8000:8100`) and transport is tcp, udp
    or any. For a domain, ports may only be `443`, `80`, both, or `none` -- the ports Suricata looks for
    http and tls on are fixed per host, so a domain allowed on 8443 would compile to a rule that never
    matches. Allow the address instead.

    Only tls and http carry the destination name in the traffic, so ssh or postgres to a hostname is not
    expressible: use /vms/{id}/targets/resolve and record the addresses.

    Set ttl_seconds for a temporary allowance the control plane withdraws on its own.

    Args:
        id (UUID):
        body (CreateTargetReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | VmTargetDTO]
     """


    kwargs = _get_kwargs(
        id=id,
body=body,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateTargetReq,

) -> ApiError | VmTargetDTO | None:
    """ Allow a destination

     destination is a domain, an IP address, or a CIDR. Omit kind to have it classified for you; supply
    it and it must agree, since a form that asked for an address and got a hostname has a mistake in it.

    For an address, ports is Suricata's syntax (`22`, `80,443`, `8000:8100`) and transport is tcp, udp
    or any. For a domain, ports may only be `443`, `80`, both, or `none` -- the ports Suricata looks for
    http and tls on are fixed per host, so a domain allowed on 8443 would compile to a rule that never
    matches. Allow the address instead.

    Only tls and http carry the destination name in the traffic, so ssh or postgres to a hostname is not
    expressible: use /vms/{id}/targets/resolve and record the addresses.

    Set ttl_seconds for a temporary allowance the control plane withdraws on its own.

    Args:
        id (UUID):
        body (CreateTargetReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | VmTargetDTO
     """


    return sync_detailed(
        id=id,
client=client,
body=body,

    ).parsed

async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateTargetReq,

) -> Response[ApiError | VmTargetDTO]:
    """ Allow a destination

     destination is a domain, an IP address, or a CIDR. Omit kind to have it classified for you; supply
    it and it must agree, since a form that asked for an address and got a hostname has a mistake in it.

    For an address, ports is Suricata's syntax (`22`, `80,443`, `8000:8100`) and transport is tcp, udp
    or any. For a domain, ports may only be `443`, `80`, both, or `none` -- the ports Suricata looks for
    http and tls on are fixed per host, so a domain allowed on 8443 would compile to a rule that never
    matches. Allow the address instead.

    Only tls and http carry the destination name in the traffic, so ssh or postgres to a hostname is not
    expressible: use /vms/{id}/targets/resolve and record the addresses.

    Set ttl_seconds for a temporary allowance the control plane withdraws on its own.

    Args:
        id (UUID):
        body (CreateTargetReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | VmTargetDTO]
     """


    kwargs = _get_kwargs(
        id=id,
body=body,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateTargetReq,

) -> ApiError | VmTargetDTO | None:
    """ Allow a destination

     destination is a domain, an IP address, or a CIDR. Omit kind to have it classified for you; supply
    it and it must agree, since a form that asked for an address and got a hostname has a mistake in it.

    For an address, ports is Suricata's syntax (`22`, `80,443`, `8000:8100`) and transport is tcp, udp
    or any. For a domain, ports may only be `443`, `80`, both, or `none` -- the ports Suricata looks for
    http and tls on are fixed per host, so a domain allowed on 8443 would compile to a rule that never
    matches. Allow the address instead.

    Only tls and http carry the destination name in the traffic, so ssh or postgres to a hostname is not
    expressible: use /vms/{id}/targets/resolve and record the addresses.

    Set ttl_seconds for a temporary allowance the control plane withdraws on its own.

    Args:
        id (UUID):
        body (CreateTargetReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | VmTargetDTO
     """


    return (await asyncio_detailed(
        id=id,
client=client,
body=body,

    )).parsed
