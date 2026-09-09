FROM oven/bun:1.4.0-alpine AS build

WORKDIR /web
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY . ./

ENV CONSOLE_URL=https://console-url.invalid
RUN bun run generate

# ---- social card -------------------------------------------------------------
# public/og.svg is the source of truth; og:image has to be a raster, so the PNG
# is built here instead of being committed. The two families are the ones the
# site loads at runtime — without them the card renders in a fallback face.
FROM alpine:3.22 AS og

RUN apk add --no-cache rsvg-convert fontconfig ca-certificates

ADD https://raw.githubusercontent.com/google/fonts/main/ofl/archivo/Archivo%5Bwdth%2Cwght%5D.ttf \
    /usr/share/fonts/truetype/Archivo.ttf
ADD https://raw.githubusercontent.com/google/fonts/main/ofl/ibmplexmono/IBMPlexMono-Regular.ttf \
    /usr/share/fonts/truetype/IBMPlexMono-Regular.ttf
ADD https://raw.githubusercontent.com/google/fonts/main/ofl/ibmplexmono/IBMPlexMono-SemiBold.ttf \
    /usr/share/fonts/truetype/IBMPlexMono-SemiBold.ttf
RUN chmod 0644 /usr/share/fonts/truetype/*.ttf && fc-cache -f

COPY public/og.svg /og.svg
RUN rsvg-convert -w 1200 -h 630 /og.svg -o /og.png

# ---- runtime -----------------------------------------------------------------
FROM nginx:1.29-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --chmod=0755 docker-entrypoint.d/10-console-url.sh /docker-entrypoint.d/10-console-url.sh
COPY --from=build /web/.output/public /srv
COPY --from=og /og.png /srv/og.png

ENV CONSOLE_URL=http://localhost:1323

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
