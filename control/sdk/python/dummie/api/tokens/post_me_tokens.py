from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error import ApiError
from ...models.create_pat_req import CreatePATReq
from ...models.created_token_resp import CreatedTokenResp
from typing import cast



def _get_kwargs(
    *,
    body: CreatePATReq,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}


    

    

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/me/tokens",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ApiError | CreatedTokenResp | None:
    if response.status_code == 201:
        response_201 = CreatedTokenResp.from_dict(response.json())



        return response_201

    if response.status_code == 400:
        response_400 = ApiError.from_dict(response.json())



        return response_400

    if response.status_code == 401:
        response_401 = ApiError.from_dict(response.json())



        return response_401

    if response.status_code == 403:
        response_403 = ApiError.from_dict(response.json())



        return response_403

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[ApiError | CreatedTokenResp]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: CreatePATReq,

) -> Response[ApiError | CreatedTokenResp]:
    """ Create a personal access token

     The response is the only place the raw token appears; it is stored hashed and cannot be shown again.
    Omit expires_in_days for a token that never expires. Requires a signed-in session, so a leaked token
    cannot mint its successor and outlive the revocation meant to end it.

    Args:
        body (CreatePATReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | CreatedTokenResp]
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
    body: CreatePATReq,

) -> ApiError | CreatedTokenResp | None:
    """ Create a personal access token

     The response is the only place the raw token appears; it is stored hashed and cannot be shown again.
    Omit expires_in_days for a token that never expires. Requires a signed-in session, so a leaked token
    cannot mint its successor and outlive the revocation meant to end it.

    Args:
        body (CreatePATReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | CreatedTokenResp
     """


    return sync_detailed(
        client=client,
body=body,

    ).parsed

async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: CreatePATReq,

) -> Response[ApiError | CreatedTokenResp]:
    """ Create a personal access token

     The response is the only place the raw token appears; it is stored hashed and cannot be shown again.
    Omit expires_in_days for a token that never expires. Requires a signed-in session, so a leaked token
    cannot mint its successor and outlive the revocation meant to end it.

    Args:
        body (CreatePATReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | CreatedTokenResp]
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
    body: CreatePATReq,

) -> ApiError | CreatedTokenResp | None:
    """ Create a personal access token

     The response is the only place the raw token appears; it is stored hashed and cannot be shown again.
    Omit expires_in_days for a token that never expires. Requires a signed-in session, so a leaked token
    cannot mint its successor and outlive the revocation meant to end it.

    Args:
        body (CreatePATReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | CreatedTokenResp
     """


    return (await asyncio_detailed(
        client=client,
body=body,

    )).parsed
