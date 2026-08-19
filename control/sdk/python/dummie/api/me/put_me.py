from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.admin_user_dto import AdminUserDTO
from ...models.api_error import ApiError
from ...models.update_profile_req import UpdateProfileReq
from typing import cast



def _get_kwargs(
    *,
    body: UpdateProfileReq,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}


    

    

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/me",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> AdminUserDTO | ApiError | None:
    if response.status_code == 200:
        response_200 = AdminUserDTO.from_dict(response.json())



        return response_200

    if response.status_code == 400:
        response_400 = ApiError.from_dict(response.json())



        return response_400

    if response.status_code == 401:
        response_401 = ApiError.from_dict(response.json())



        return response_401

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[AdminUserDTO | ApiError]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: UpdateProfileReq,

) -> Response[AdminUserDTO | ApiError]:
    """ Update your account

     Only the display name and the SSH public key are editable. Username, email, role and every allowance
    are an admin's to set. An empty public key is accepted and clears it, which blocks creating new VMs.

    Args:
        body (UpdateProfileReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AdminUserDTO | ApiError]
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
    body: UpdateProfileReq,

) -> AdminUserDTO | ApiError | None:
    """ Update your account

     Only the display name and the SSH public key are editable. Username, email, role and every allowance
    are an admin's to set. An empty public key is accepted and clears it, which blocks creating new VMs.

    Args:
        body (UpdateProfileReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AdminUserDTO | ApiError
     """


    return sync_detailed(
        client=client,
body=body,

    ).parsed

async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: UpdateProfileReq,

) -> Response[AdminUserDTO | ApiError]:
    """ Update your account

     Only the display name and the SSH public key are editable. Username, email, role and every allowance
    are an admin's to set. An empty public key is accepted and clears it, which blocks creating new VMs.

    Args:
        body (UpdateProfileReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AdminUserDTO | ApiError]
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
    body: UpdateProfileReq,

) -> AdminUserDTO | ApiError | None:
    """ Update your account

     Only the display name and the SSH public key are editable. Username, email, role and every allowance
    are an admin's to set. An empty public key is accepted and clears it, which blocks creating new VMs.

    Args:
        body (UpdateProfileReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AdminUserDTO | ApiError
     """


    return (await asyncio_detailed(
        client=client,
body=body,

    )).parsed
