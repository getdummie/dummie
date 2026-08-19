from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error import ApiError
from ...models.create_vm_req import CreateVMReq
from ...models.vm_dto import VmDTO
from typing import cast



def _get_kwargs(
    *,
    body: CreateVMReq,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}


    

    

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/vms",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ApiError | VmDTO | None:
    if response.status_code == 202:
        response_202 = VmDTO.from_dict(response.json())



        return response_202

    if response.status_code == 400:
        response_400 = ApiError.from_dict(response.json())



        return response_400

    if response.status_code == 401:
        response_401 = ApiError.from_dict(response.json())



        return response_401

    if response.status_code == 403:
        response_403 = ApiError.from_dict(response.json())



        return response_403

    if response.status_code == 404:
        response_404 = ApiError.from_dict(response.json())



        return response_404

    if response.status_code == 409:
        response_409 = ApiError.from_dict(response.json())



        return response_409

    if response.status_code == 502:
        response_502 = ApiError.from_dict(response.json())



        return response_502

    if response.status_code == 503:
        response_503 = ApiError.from_dict(response.json())



        return response_503

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
    *,
    client: AuthenticatedClient,
    body: CreateVMReq,

) -> Response[ApiError | VmDTO]:
    r""" Create a VM

     Pick a host from /vms/hosts and an artifact from /vms/kernels and /vms/osimages. Your SSH public key
    has to be on your account first -- it is built into the image at boot, so it cannot be added
    afterwards. The size is charged against your quota; disk_size takes the client's syntax (\"2G\",
    \"512M\", or plain bytes).

    Set ttl_seconds to make this a temporary sandbox: the control plane destroys it that many seconds
    after the row is written. The clock is wall-clock from the create and keeps running while the VM is
    stopped.

    targets is the egress allowlist to give the VM at birth — each entry takes exactly the shape `POST
    /vms/{id}/targets` accepts, including its own ttl_seconds. At most 32; add the rest afterwards. The
    whole list is validated before anything is written and inserted in the same transaction as the VM,
    so a bad entry is a 400 with no VM created rather than a VM with a partial allowlist. Omit it for a
    VM that may reach nothing until you allow something.

    The response is the row as written, with status 'pending'. The host reports the result over its own
    socket, so poll GET /vms/{id} to see it reach 'running' or 'failed'. The allowlist reaches the host
    with the guest's address, so it is in force by the time the VM is up.

    Args:
        body (CreateVMReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | VmDTO]
     """


    kwargs = _get_kwargs(
        body=body,

    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    *,
    client: AuthenticatedClient,
    body: CreateVMReq,

) -> ApiError | VmDTO | None:
    r""" Create a VM

     Pick a host from /vms/hosts and an artifact from /vms/kernels and /vms/osimages. Your SSH public key
    has to be on your account first -- it is built into the image at boot, so it cannot be added
    afterwards. The size is charged against your quota; disk_size takes the client's syntax (\"2G\",
    \"512M\", or plain bytes).

    Set ttl_seconds to make this a temporary sandbox: the control plane destroys it that many seconds
    after the row is written. The clock is wall-clock from the create and keeps running while the VM is
    stopped.

    targets is the egress allowlist to give the VM at birth — each entry takes exactly the shape `POST
    /vms/{id}/targets` accepts, including its own ttl_seconds. At most 32; add the rest afterwards. The
    whole list is validated before anything is written and inserted in the same transaction as the VM,
    so a bad entry is a 400 with no VM created rather than a VM with a partial allowlist. Omit it for a
    VM that may reach nothing until you allow something.

    The response is the row as written, with status 'pending'. The host reports the result over its own
    socket, so poll GET /vms/{id} to see it reach 'running' or 'failed'. The allowlist reaches the host
    with the guest's address, so it is in force by the time the VM is up.

    Args:
        body (CreateVMReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | VmDTO
     """


    return sync_detailed(
        client=client,
body=body,

    ).parsed

async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: CreateVMReq,

) -> Response[ApiError | VmDTO]:
    r""" Create a VM

     Pick a host from /vms/hosts and an artifact from /vms/kernels and /vms/osimages. Your SSH public key
    has to be on your account first -- it is built into the image at boot, so it cannot be added
    afterwards. The size is charged against your quota; disk_size takes the client's syntax (\"2G\",
    \"512M\", or plain bytes).

    Set ttl_seconds to make this a temporary sandbox: the control plane destroys it that many seconds
    after the row is written. The clock is wall-clock from the create and keeps running while the VM is
    stopped.

    targets is the egress allowlist to give the VM at birth — each entry takes exactly the shape `POST
    /vms/{id}/targets` accepts, including its own ttl_seconds. At most 32; add the rest afterwards. The
    whole list is validated before anything is written and inserted in the same transaction as the VM,
    so a bad entry is a 400 with no VM created rather than a VM with a partial allowlist. Omit it for a
    VM that may reach nothing until you allow something.

    The response is the row as written, with status 'pending'. The host reports the result over its own
    socket, so poll GET /vms/{id} to see it reach 'running' or 'failed'. The allowlist reaches the host
    with the guest's address, so it is in force by the time the VM is up.

    Args:
        body (CreateVMReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | VmDTO]
     """


    kwargs = _get_kwargs(
        body=body,

    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: AuthenticatedClient,
    body: CreateVMReq,

) -> ApiError | VmDTO | None:
    r""" Create a VM

     Pick a host from /vms/hosts and an artifact from /vms/kernels and /vms/osimages. Your SSH public key
    has to be on your account first -- it is built into the image at boot, so it cannot be added
    afterwards. The size is charged against your quota; disk_size takes the client's syntax (\"2G\",
    \"512M\", or plain bytes).

    Set ttl_seconds to make this a temporary sandbox: the control plane destroys it that many seconds
    after the row is written. The clock is wall-clock from the create and keeps running while the VM is
    stopped.

    targets is the egress allowlist to give the VM at birth — each entry takes exactly the shape `POST
    /vms/{id}/targets` accepts, including its own ttl_seconds. At most 32; add the rest afterwards. The
    whole list is validated before anything is written and inserted in the same transaction as the VM,
    so a bad entry is a 400 with no VM created rather than a VM with a partial allowlist. Omit it for a
    VM that may reach nothing until you allow something.

    The response is the row as written, with status 'pending'. The host reports the result over its own
    socket, so poll GET /vms/{id} to see it reach 'running' or 'failed'. The allowlist reaches the host
    with the guest's address, so it is in force by the time the VM is up.

    Args:
        body (CreateVMReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | VmDTO
     """


    return (await asyncio_detailed(
        client=client,
body=body,

    )).parsed
