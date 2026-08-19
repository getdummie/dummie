# sdk

A generated Python client for the control API, built with
[openapi-python-client](https://github.com/openapi-generators/openapi-python-client).

```
docker compose exec control-sdk just sdk-python
docker compose exec control-sdk just sdk-install
```

`just sdk-python` from the repo root does the same generation on the host. It runs
two steps:

1. `normalize_spec.py` reads `../docs/swagger.json` and writes `openapi.json`.
   swag's output is not directly generatable — see the docstring in that file for
   what it fixes and why the fixes are not in the Go annotations.
2. `openapi-python-client` generates `python/` from the normalized spec.

`../docs/swagger.json` is rewritten by swag on every control-server build, so
regenerate after changing a handler's annotations. Both `openapi.json` and `python/`
are generated: edit the annotations or the normalizer, never those.

## Using it

The generated client appends the spec's paths to `base_url` as given; it ignores the
spec's `servers` entry. So `/api/v1` has to be part of `base_url`, or every request
lands on the wrong route:

```python
import os

from dummie import AuthenticatedClient
from dummie.api.vms import get_vms

# from the sdk container, the control service is reachable by its compose name
client = AuthenticatedClient(base_url="http://control:1323/api/v1", token=os.environ["DUMMIE_TOKEN"])
vms = get_vms.sync(client=client)
```

`AuthenticatedClient` sends `Authorization: Bearer <token>`, which takes a personal
access token or the `access_token` from `POST /signin`. The token routes under
`/me/tokens` refuse a personal access token by design — those need a session token.
