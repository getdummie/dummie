from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error import ApiError
from ...models.session_resp import SessionResp
from typing import cast



def _get_kwargs(
    
) -> dict[str, Any]:
    

    

    

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/token_refresh",
    }


    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ApiError | SessionResp | None:
    if response.status_code == 200:
        response_200 = SessionResp.from_dict(response.json())



        return response_200

    if response.status_code == 401:
        response_401 = ApiError.from_dict(response.json())



        return response_401

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

) -> Response[ApiError | SessionResp]:
    """ Refresh the access token

     Reads the httpOnly refresh cookie and mints a new access token. The refresh token is not rotated, so
    a page reload does not churn sessions. Not needed when calling the API with a personal access token,
    which does not expire on this schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | SessionResp]
     """


    kwargs = _get_kwargs(
        
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)

def sync(
    *,
    client: AuthenticatedClient | Client,

) -> ApiError | SessionResp | None:
    """ Refresh the access token

     Reads the httpOnly refresh cookie and mints a new access token. The refresh token is not rotated, so
    a page reload does not churn sessions. Not needed when calling the API with a personal access token,
    which does not expire on this schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | SessionResp
     """


    return sync_detailed(
        client=client,

    ).parsed

async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,

) -> Response[ApiError | SessionResp]:
    """ Refresh the access token

     Reads the httpOnly refresh cookie and mints a new access token. The refresh token is not rotated, so
    a page reload does not churn sessions. Not needed when calling the API with a personal access token,
    which does not expire on this schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | SessionResp]
     """


    kwargs = _get_kwargs(
        
    )

    response = await client.get_async_httpx_client().request(
        **kwargs
    )

    return _build_response(client=client, response=response)

async def asyncio(
    *,
    client: AuthenticatedClient | Client,

) -> ApiError | SessionResp | None:
    """ Refresh the access token

     Reads the httpOnly refresh cookie and mints a new access token. The refresh token is not rotated, so
    a page reload does not churn sessions. Not needed when calling the API with a personal access token,
    which does not expire on this schedule.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | SessionResp
     """


    return (await asyncio_detailed(
        client=client,

    )).parsed
