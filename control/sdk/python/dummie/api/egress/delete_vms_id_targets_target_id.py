from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error import ApiError
from typing import cast
from uuid import UUID



def _get_kwargs(
    id: UUID,
    target_id: UUID,

) -> dict[str, Any]:
    

    

    

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/vms/{id}/targets/{target_id}".format(id=quote(str(id), safe=""),target_id=quote(str(target_id), safe=""),),
    }


    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | ApiError | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | ApiError]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    target_id: UUID,
    *,
    client: AuthenticatedClient,

) -> Response[Any | ApiError]:
    r""" Remove an allowed destination

     Cancels any pending expiry on it and pushes the new policy to the host. Until that push lands the
    guest still has the access, so treat the 204 as \"recorded\", not \"already enforced\".

    Args:
        id (UUID):
        target_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ApiError]
     """


    kwargs = _get_kwargs(
        id=id,
target_id=target_id,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    id: UUID,
    target_id: UUID,
    *,
    client: AuthenticatedClient,

) -> Any | ApiError | None:
    r""" Remove an allowed destination

     Cancels any pending expiry on it and pushes the new policy to the host. Until that push lands the
    guest still has the access, so treat the 204 as \"recorded\", not \"already enforced\".

    Args:
        id (UUID):
        target_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ApiError
     """


    return sync_detailed(
        id=id,
target_id=target_id,
client=client,

    ).parsed

async def asyncio_detailed(
    id: UUID,
    target_id: UUID,
    *,
    client: AuthenticatedClient,

) -> Response[Any | ApiError]:
    r""" Remove an allowed destination

     Cancels any pending expiry on it and pushes the new policy to the host. Until that push lands the
    guest still has the access, so treat the 204 as \"recorded\", not \"already enforced\".

    Args:
        id (UUID):
        target_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ApiError]
     """


    kwargs = _get_kwargs(
        id=id,
target_id=target_id,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    id: UUID,
    target_id: UUID,
    *,
    client: AuthenticatedClient,

) -> Any | ApiError | None:
    r""" Remove an allowed destination

     Cancels any pending expiry on it and pushes the new policy to the host. Until that push lands the
    guest still has the access, so treat the 204 as \"recorded\", not \"already enforced\".

    Args:
        id (UUID):
        target_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ApiError
     """


    return (await asyncio_detailed(
        id=id,
target_id=target_id,
client=client,

    )).parsed
