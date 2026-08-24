# website

The public site: landing page, case studies and prose docs. Served on its own
domain separately from the console, which lives in `control/web-client` and is
embedded in the control server binary.

Unlike the console this is **not** an SPA — `nuxt.config.ts` leaves SSR on and
nitro prerenders every page to a real document, because this content needs to be
readable by a crawler.

## Development

Either directly:

```sh
bun install
bun run dev
```

or through compose, which builds `Dockerfile.dev` and hot-reloads on change
because the source tree is bind-mounted:

```sh
docker compose up website     # http://localhost:3000
```

Content lives in `content/`: `casestudies/*.md` and `docs/**/*.md`, with the
schemas in `content.config.ts`. A docs page needs `section` in its frontmatter —
that is what groups it in the sidebar — and its numeric filename prefix is what
orders it. Adding a page needs no code change.

## The console link

Links into the console — "Sign in", "Open dashboard", "API reference" — are real
hyperlinks built from `runtimeConfig.public.consoleUrl`, so hovering one shows
where it actually goes. They are plain `<a href>` rather than `<NuxtLink>`
because they leave this origin.

Locally the default (`http://localhost:1323`) is usually right. Override it with
`CONSOLE_URL` — the same name in `.env`, at build time, and in the container. A
trailing slash is stripped, so both forms work:

```sh
CONSOLE_URL=https://console.example.com bun run generate
```

Because the URL is baked into the prerendered HTML, a **prebuilt image** would
otherwise be stuck with whatever it was built against. So the image builds
against the sentinel host `https://console-url.invalid`, and
`docker-entrypoint.d/10-console-url.sh` swaps it for `$CONSOLE_URL` before nginx
starts — the nginx image runs anything in that directory at container start. One
image, any deployment, no rebuild:

```sh
docker build -t dummie-website .
docker run -e CONSOLE_URL=https://console.example.com -p 8080:8080 dummie-website
```

The substitution covers `.html`, `.js` and `.json`, since the URL appears both in
the prerendered markup and in the payload the client hydrates from — they have to
agree or Vue reports a mismatch. It also means the container writes to `/srv` on
boot, so it cannot run with a read-only root filesystem.

The sentinel is an absolute URL rather than a bare token on purpose: the
prerender crawler treats a schemeless value as a same-origin path and fails
trying to render it. `.invalid` is reserved by RFC 2606, so a missed substitution
can never resolve to a real host — and the entrypoint refuses to start nginx if
it finds the sentinel left in `/srv`.

## Production

```sh
bun run generate    # -> .output/public
```

The output is plain files, so it can also go on any static host; you then need
that host's equivalent of the redirects in `Caddyfile` (a `_redirects` file, for
instance).
