FROM oven/bun:1.4.0-alpine AS build

WORKDIR /web
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY . ./

ENV CONSOLE_URL=https://console-url.invalid
RUN bun run generate

# ---- runtime -----------------------------------------------------------------
FROM nginx:1.29-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --chmod=0755 docker-entrypoint.d/10-console-url.sh /docker-entrypoint.d/10-console-url.sh
COPY --from=build /web/.output/public /srv

ENV CONSOLE_URL=http://localhost:1323

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
