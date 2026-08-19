from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error import ApiError
from ...models.resolve_host_req import ResolveHostReq
from ...models.resolved_host import ResolvedHost
from typing import cast
from uuid import UUID



def _get_kwargs(
    id: UUID,
    *,
    body: ResolveHostReq,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}


    

    

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/vms/{id}/targets/resolve".format(id=quote(str(id), safe=""),),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ApiError | ResolvedHost | None:
    if response.status_code == 200:
        response_200 = ResolvedHost.from_dict(response.json())



        return response_200

    if response.status_code == 400:
        response_400 = ApiError.from_dict(response.json())



        return response_400

    if response.status_code == 401:
        response_401 = ApiError.from_dict(response.json())



        return response_401

    if response.status_code == 404:
        response_404 = ApiError.from_dict(response.json())



        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[ApiError | ResolvedHost]:
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
    body: ResolveHostReq,

) -> Response[ApiError | ResolvedHost]:
    """ Resolve a hostname

     What a name currently resolves to, so you can record those addresses as allowances. IPv4 only --
    every rule and nftables element downstream is IPv4, so an AAAA record would be an address nothing
    can express. At most 8 addresses; truncated says when there were more.

    A name that does not resolve is a 200 with error set, not a failure: that is an answer about the
    name rather than about this server. Deliberately a one-shot lookup and not a subscription --
    addresses move, and something re-resolving on a timer would silently widen an allowlist nobody re-
    read.

    Scoped to a VM you own even though the answer is not VM-specific, so this is not a public name-
    resolution service.

    Args:
        id (UUID):
        body (ResolveHostReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | ResolvedHost]
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
    body: ResolveHostReq,

) -> ApiError | ResolvedHost | None:
    """ Resolve a hostname

     What a name currently resolves to, so you can record those addresses as allowances. IPv4 only --
    every rule and nftables element downstream is IPv4, so an AAAA record would be an address nothing
    can express. At most 8 addresses; truncated says when there were more.

    A name that does not resolve is a 200 with error set, not a failure: that is an answer about the
    name rather than about this server. Deliberately a one-shot lookup and not a subscription --
    addresses move, and something re-resolving on a timer would silently widen an allowlist nobody re-
    read.

    Scoped to a VM you own even though the answer is not VM-specific, so this is not a public name-
    resolution service.

    Args:
        id (UUID):
        body (ResolveHostReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | ResolvedHost
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
    body: ResolveHostReq,

) -> Response[ApiError | ResolvedHost]:
    """ Resolve a hostname

     What a name currently resolves to, so you can record those addresses as allowances. IPv4 only --
    every rule and nftables element downstream is IPv4, so an AAAA record would be an address nothing
    can express. At most 8 addresses; truncated says when there were more.

    A name that does not resolve is a 200 with error set, not a failure: that is an answer about the
    name rather than about this server. Deliberately a one-shot lookup and not a subscription --
    addresses move, and something re-resolving on a timer would silently widen an allowlist nobody re-
    read.

    Scoped to a VM you own even though the answer is not VM-specific, so this is not a public name-
    resolution service.

    Args:
        id (UUID):
        body (ResolveHostReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | ResolvedHost]
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
    body: ResolveHostReq,

) -> ApiError | ResolvedHost | None:
    """ Resolve a hostname

     What a name currently resolves to, so you can record those addresses as allowances. IPv4 only --
    every rule and nftables element downstream is IPv4, so an AAAA record would be an address nothing
    can express. At most 8 addresses; truncated says when there were more.

    A name that does not resolve is a 200 with error set, not a failure: that is an answer about the
    name rather than about this server. Deliberately a one-shot lookup and not a subscription --
    addresses move, and something re-resolving on a timer would silently widen an allowlist nobody re-
    read.

    Scoped to a VM you own even though the answer is not VM-specific, so this is not a public name-
    resolution service.

    Args:
        id (UUID):
        body (ResolveHostReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | ResolvedHost
     """


    return (await asyncio_detailed(
        id=id,
client=client,
body=body,

    )).parsed
