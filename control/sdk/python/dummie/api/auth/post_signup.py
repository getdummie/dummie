from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error import ApiError
from ...models.session_resp import SessionResp
from ...models.signup_req import SignupReq
from typing import cast



def _get_kwargs(
    *,
    body: SignupReq,

) -> dict[str, Any]:
    headers: dict[str, Any] = {}


    

    

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/signup",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ApiError | SessionResp | None:
    if response.status_code == 200:
        response_200 = SessionResp.from_dict(response.json())



        return response_200

    if response.status_code == 400:
        response_400 = ApiError.from_dict(response.json())



        return response_400

    if response.status_code == 409:
        response_409 = ApiError.from_dict(response.json())



        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[ApiError | SessionResp]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: SignupReq,

) -> Response[ApiError | SessionResp]:
    """ Create an account

     The first account to sign up becomes the admin; every one after is a regular user. The client does
    not get to choose. Sets the httpOnly access and refresh cookies as well as returning the access
    token.

    Args:
        body (SignupReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | SessionResp]
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
    client: AuthenticatedClient | Client,
    body: SignupReq,

) -> ApiError | SessionResp | None:
    """ Create an account

     The first account to sign up becomes the admin; every one after is a regular user. The client does
    not get to choose. Sets the httpOnly access and refresh cookies as well as returning the access
    token.

    Args:
        body (SignupReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | SessionResp
     """


    return sync_detailed(
        client=client,
body=body,

    ).parsed

async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: SignupReq,

) -> Response[ApiError | SessionResp]:
    """ Create an account

     The first account to sign up becomes the admin; every one after is a regular user. The client does
    not get to choose. Sets the httpOnly access and refresh cookies as well as returning the access
    token.

    Args:
        body (SignupReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | SessionResp]
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
    client: AuthenticatedClient | Client,
    body: SignupReq,

) -> ApiError | SessionResp | None:
    """ Create an account

     The first account to sign up becomes the admin; every one after is a regular user. The client does
    not get to choose. Sets the httpOnly access and refresh cookies as well as returning the access
    token.

    Args:
        body (SignupReq):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | SessionResp
     """


    return (await asyncio_detailed(
        client=client,
body=body,

    )).parsed
