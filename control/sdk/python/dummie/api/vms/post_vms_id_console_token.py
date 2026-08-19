from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...types import Response, UNSET
from ... import errors

from ...models.api_error import ApiError
from ...models.console_token_dto import ConsoleTokenDTO
from typing import cast
from uuid import UUID



def _get_kwargs(
    id: UUID,

) -> dict[str, Any]:
    

    

    

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/vms/{id}/console-token".format(id=quote(str(id), safe=""),),
    }


    return _kwargs



def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ApiError | ConsoleTokenDTO | None:
    if response.status_code == 200:
        response_200 = ConsoleTokenDTO.from_dict(response.json())



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

    if response.status_code == 409:
        response_409 = ApiError.from_dict(response.json())



        return response_409

    if response.status_code == 503:
        response_503 = ApiError.from_dict(response.json())



        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[ApiError | ConsoleTokenDTO]:
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

) -> Response[ApiError | ConsoleTokenDTO]:
    """ Mint a console token

     Returns the websocket url for this VM's terminal and a short-lived token that opens it. POST rather
    than GET because it is a credential, and a GET would put it in browser history, in referrers and in
    any log that records request lines. The url in vmDTO.console_url is not itself a credential -- it
    needs one of these.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | ConsoleTokenDTO]
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

) -> ApiError | ConsoleTokenDTO | None:
    """ Mint a console token

     Returns the websocket url for this VM's terminal and a short-lived token that opens it. POST rather
    than GET because it is a credential, and a GET would put it in browser history, in referrers and in
    any log that records request lines. The url in vmDTO.console_url is not itself a credential -- it
    needs one of these.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | ConsoleTokenDTO
     """


    return sync_detailed(
        id=id,
client=client,

    ).parsed

async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,

) -> Response[ApiError | ConsoleTokenDTO]:
    """ Mint a console token

     Returns the websocket url for this VM's terminal and a short-lived token that opens it. POST rather
    than GET because it is a credential, and a GET would put it in browser history, in referrers and in
    any log that records request lines. The url in vmDTO.console_url is not itself a credential -- it
    needs one of these.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApiError | ConsoleTokenDTO]
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

) -> ApiError | ConsoleTokenDTO | None:
    """ Mint a console token

     Returns the websocket url for this VM's terminal and a short-lived token that opens it. POST rather
    than GET because it is a credential, and a GET would put it in browser history, in referrers and in
    any log that records request lines. The url in vmDTO.console_url is not itself a credential -- it
    needs one of these.

    Args:
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApiError | ConsoleTokenDTO
     """


    return (await asyncio_detailed(
        id=id,
client=client,

    )).parsed
