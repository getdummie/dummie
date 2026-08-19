from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error import ApiError
from ...models.update_vm_ports_req import UpdateVMPortsReq
from ...models.vm_dto import VmDTO
from typing import cast
from uuid import UUID



def _get_kwargs(
    id: UUID,
    *,
    body: UpdateVMPortsReq,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}


    

    

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/vms/{id}/ports".format(id=quote(str(id), safe=""),),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ApiError | VmDTO | None:
    if response.status_code == 200:
        response_200 = VmDTO.from_dict(response.json())



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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[ApiError | VmDTO]:
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
    body: UpdateVMPortsReq,

) -> Response[ApiError | VmDTO]:
    """ Set a VM's published ports

     default_port is where a request goes when nothing picks a port; omit it or send 0 for 8000.
    public_ports is every port the VM publishes -- send an empty list to publish nothing. The list is
    de-duplicated but not reordered, since its order is the order the generated config lists them in. At
    most 32 ports.

    Args:
        id (UUID):
        body (UpdateVMPortsReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | VmDTO]
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
    body: UpdateVMPortsReq,

) -> ApiError | VmDTO | None:
    """ Set a VM's published ports

     default_port is where a request goes when nothing picks a port; omit it or send 0 for 8000.
    public_ports is every port the VM publishes -- send an empty list to publish nothing. The list is
    de-duplicated but not reordered, since its order is the order the generated config lists them in. At
    most 32 ports.

    Args:
        id (UUID):
        body (UpdateVMPortsReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | VmDTO
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
    body: UpdateVMPortsReq,

) -> Response[ApiError | VmDTO]:
    """ Set a VM's published ports

     default_port is where a request goes when nothing picks a port; omit it or send 0 for 8000.
    public_ports is every port the VM publishes -- send an empty list to publish nothing. The list is
    de-duplicated but not reordered, since its order is the order the generated config lists them in. At
    most 32 ports.

    Args:
        id (UUID):
        body (UpdateVMPortsReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | VmDTO]
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
    body: UpdateVMPortsReq,

) -> ApiError | VmDTO | None:
    """ Set a VM's published ports

     default_port is where a request goes when nothing picks a port; omit it or send 0 for 8000.
    public_ports is every port the VM publishes -- send an empty list to publish nothing. The list is
    de-duplicated but not reordered, since its order is the order the generated config lists them in. At
    most 32 ports.

    Args:
        id (UUID):
        body (UpdateVMPortsReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | VmDTO
     """


    return (await asyncio_detailed(
        id=id,
client=client,
body=body,

    )).parsed
