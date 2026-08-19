from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error import ApiError
from ...models.denied_egress_list import DeniedEgressList
from typing import cast
from uuid import UUID



def _get_kwargs(
    id: UUID,

) -> dict[str, Any]:
    

    

    

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/vms/{id}/denied".format(id=quote(str(id), safe=""),),
    }


    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ApiError | DeniedEgressList | None:
    if response.status_code == 200:
        response_200 = DeniedEgressList.from_dict(response.json())



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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[ApiError | DeniedEgressList]:
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

) -> Response[ApiError | DeniedEgressList]:
    r""" List denied egress attempts

     What this guest tried to reach and was refused. kind is \"lookup\" (a name the resolver would not
    answer) or \"packet\" (a flow the ruleset dropped); a refused lookup has no address to offer.

    Read available before reading items: an empty list means either nothing was denied or nothing is
    collecting, and those are very different facts. recording names which of the two sources answered.

    Only events since this VM was created are shown -- a destroyed VM's address goes back to the pool,
    so without that bound a new VM would inherit the history of whatever held its address before it.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | DeniedEgressList]
     """


    kwargs = _get_kwargs(
        id=id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    id: UUID,
    *,
    client: AuthenticatedClient,

) -> ApiError | DeniedEgressList | None:
    r""" List denied egress attempts

     What this guest tried to reach and was refused. kind is \"lookup\" (a name the resolver would not
    answer) or \"packet\" (a flow the ruleset dropped); a refused lookup has no address to offer.

    Read available before reading items: an empty list means either nothing was denied or nothing is
    collecting, and those are very different facts. recording names which of the two sources answered.

    Only events since this VM was created are shown -- a destroyed VM's address goes back to the pool,
    so without that bound a new VM would inherit the history of whatever held its address before it.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | DeniedEgressList
     """


    return sync_detailed(
        id=id,
client=client,

    ).parsed

async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,

) -> Response[ApiError | DeniedEgressList]:
    r""" List denied egress attempts

     What this guest tried to reach and was refused. kind is \"lookup\" (a name the resolver would not
    answer) or \"packet\" (a flow the ruleset dropped); a refused lookup has no address to offer.

    Read available before reading items: an empty list means either nothing was denied or nothing is
    collecting, and those are very different facts. recording names which of the two sources answered.

    Only events since this VM was created are shown -- a destroyed VM's address goes back to the pool,
    so without that bound a new VM would inherit the history of whatever held its address before it.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | DeniedEgressList]
     """


    kwargs = _get_kwargs(
        id=id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient,

) -> ApiError | DeniedEgressList | None:
    r""" List denied egress attempts

     What this guest tried to reach and was refused. kind is \"lookup\" (a name the resolver would not
    answer) or \"packet\" (a flow the ruleset dropped); a refused lookup has no address to offer.

    Read available before reading items: an empty list means either nothing was denied or nothing is
    collecting, and those are very different facts. recording names which of the two sources answered.

    Only events since this VM was created are shown -- a destroyed VM's address goes back to the pool,
    so without that bound a new VM would inherit the history of whatever held its address before it.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | DeniedEgressList
     """


    return (await asyncio_detailed(
        id=id,
client=client,

    )).parsed
