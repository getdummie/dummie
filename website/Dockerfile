# Production image for the marketing site and prose docs. Two stages: bun
# prerenders every page to a static document, nginx serves them.
#
# Unlike the control plane's SPA, this is not embedded in a Go binary -- it is
# prose and it needs to be indexable, so nuxt.config.ts leaves SSR on and nitro
# prerenders the lot at build time.

# ---- build: prerender the site -----------------------------------------------
# Has to run under bun: @nuxt/content is pinned to the bun:sqlite connector, and
# the default one is a native node module this image cannot build.
FROM oven/bun:1.4.0-alpine AS build

WORKDIR /web
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY . ./

# The console links are real hyperlinks, so the URL is baked into the output. A
# sentinel is baked instead of a real host, and the entrypoint below replaces it
# at container start -- so this build is deployment-agnostic.
ENV CONSOLE_URL=__CONSOLE_URL__
RUN bun run generate

# ---- runtime -----------------------------------------------------------------
FROM nginx:1.29-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
# Run by the nginx image's own entrypoint, before nginx starts.
COPY --chmod=0755 docker-entrypoint.d/10-console-url.sh /docker-entrypoint.d/10-console-url.sh
COPY --from=build /web/.output/public /srv

# Points at the control plane. Overridden at run time -- that is the whole point
# of the sentinel above.
ENV CONSOLE_URL=http://localhost:1323

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
