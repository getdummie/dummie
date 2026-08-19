from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error import ApiError
from ...models.vm_dto import VmDTO
from typing import cast
from uuid import UUID



def _get_kwargs(
    id: UUID,

) -> dict[str, Any]:
    

    

    

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/vms/{id}".format(id=quote(str(id), safe=""),),
    }


    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | ApiError | VmDTO | None:
    if response.status_code == 202:
        response_202 = VmDTO.from_dict(response.json())



        return response_202

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

    if response.status_code == 409:
        response_409 = ApiError.from_dict(response.json())



        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | ApiError | VmDTO]:
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

) -> Response[Any | ApiError | VmDTO]:
    """ Delete a VM

     Destroys the guest on its host and deletes the record. Unlike /destroy, nothing is left behind to
    look up afterwards. 202 means the destroy was delivered and the record will go when the host
    confirms; 204 means there was nothing on any host and the record is already gone. Frees the quota
    the VM was holding.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ApiError | VmDTO]
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

) -> Any | ApiError | VmDTO | None:
    """ Delete a VM

     Destroys the guest on its host and deletes the record. Unlike /destroy, nothing is left behind to
    look up afterwards. 202 means the destroy was delivered and the record will go when the host
    confirms; 204 means there was nothing on any host and the record is already gone. Frees the quota
    the VM was holding.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ApiError | VmDTO
     """


    return sync_detailed(
        id=id,
client=client,

    ).parsed

async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,

) -> Response[Any | ApiError | VmDTO]:
    """ Delete a VM

     Destroys the guest on its host and deletes the record. Unlike /destroy, nothing is left behind to
    look up afterwards. 202 means the destroy was delivered and the record will go when the host
    confirms; 204 means there was nothing on any host and the record is already gone. Frees the quota
    the VM was holding.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ApiError | VmDTO]
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

) -> Any | ApiError | VmDTO | None:
    """ Delete a VM

     Destroys the guest on its host and deletes the record. Unlike /destroy, nothing is left behind to
    look up afterwards. 202 means the destroy was delivered and the record will go when the host
    confirms; 204 means there was nothing on any host and the record is already gone. Frees the quota
    the VM was holding.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ApiError | VmDTO
     """


    return (await asyncio_detailed(
        id=id,
client=client,

    )).parsed
