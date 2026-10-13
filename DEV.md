
# Setup

## Local control plane and website

This workflow runs the control server and Nuxt apps on the host for hot reload.
Docker runs PostgreSQL, ClickHouse, and RustFS. You need Docker, Go 1.26.5 or
newer, and Bun 1.4. The database ports are bound to localhost; PostgreSQL uses
5433 so it can coexist with another local server on 5432.

Create the ignored `.rustfs.env` first, with a locally generated secret:

```sh
printf 'RUSTFS_ACCESS_KEY=dummielocal\nRUSTFS_SECRET_KEY=%s\n' \
  "$(python3 -c 'import secrets; print(secrets.token_urlsafe(48))')" > .rustfs.env
chmod 600 .rustfs.env
```

```sh
docker compose -f docker-compose.local.yml up -d --wait

cd control/web-client && bun install --frozen-lockfile && cd ../..
cd website && bun install --frozen-lockfile --ignore-scripts && bun --bun run postinstall && cd ..

cp -n control/.env.example control/.env
```

In the RustFS console at `http://localhost:9001`, sign in with the two values
from `.rustfs.env` and create a bucket named `dummie`. Set the following values
in `control/.env`. Use the same storage credentials, and replace `JWT_SECRET`
and `PROXY_AUTH_SECRET` with independent random values. Keep the file local;
`control/.gitignore` excludes it.

```dotenv
DATABASE_URL=postgres://control:control@127.0.0.1:5433/control?sslmode=disable
CLICKHOUSE_URL=clickhouse://control:control@127.0.0.1:9010/dummie
API_HOST=127.0.0.1
CONTROL_URL=http://localhost:1323
CONSOLE_URL=http://localhost:1323/
JWT_SECRET=replace-with-a-random-local-secret
PROXY_AUTH_SECRET=replace-with-another-random-local-secret
S3_BUCKET=dummie
S3_REGION=us-east-1
S3_ENDPOINT=http://127.0.0.1:9000
S3_PUBLIC_ENDPOINT=http://127.0.0.1:9000
S3_FORCE_PATH_STYLE=true
S3_ACCESS_KEY_ID=copy-from-.rustfs.env
S3_SECRET_ACCESS_KEY=copy-from-.rustfs.env
LLM_KEY_ENCRYPTION_KEY=replace-with-base64-encoded-32-random-bytes
```

Generate the LLM key with `openssl rand -base64 32` and put the output in
`control/.env`. Keep the same value across control server restarts: changing it
makes stored LLM provider keys unreadable. Restart the control server after
setting it.

In development mode the database migrations are explicit. Apply both sets, then
start the control server from `control/`; it also starts the console Nuxt server
and proxies it at `http://localhost:1323`.

```sh
cd control
go run . migrate up
go run . migrate-clickhouse up
go run . serve
```

In another terminal, start the public website at `http://localhost:3001`.
The website uses Bun's SQLite connector, so run Nuxt under Bun:

```sh
cd website
CONSOLE_URL=http://localhost:1323 bun --bun run dev --port 3001
```

Check the API at `http://localhost:1323/api/v1/health`. Then open
`http://localhost:1323` and sign up. On a fresh database, the first
account becomes the admin and can open the **Admin** sections used below.

Stop the apps with Ctrl-C and the backing services with
`docker compose -f docker-compose.local.yml down`. The Compose volumes keep
your development data; add `-v` to `down` if you want to remove them.

This starts the control plane and website. [Local VM setup](LOCAL_VMS.md)
covers the prebuilt kernels, QEMU host, enrollment, and a sandbox smoke test.

## Python SDK

```sh
just sdk-python
just sdk-install
```
