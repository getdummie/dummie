# Production image for the control server. Three stages, one artifact: the final
# image is a single static binary with the SPA, the OpenAPI spec and both
# migration sets embedded in it.

# ---- web: the Nuxt SPA -------------------------------------------------------
# The console only. nuxt.config.ts sets ssr:false, so `generate` emits a static
# SPA into .output/public. The landing page and prose docs are not here -- they
# are their own image, built from website/.
FROM oven/bun:1.3.14-alpine AS web

WORKDIR /web
COPY web-client/package.json web-client/bun.lock ./
RUN bun install --frozen-lockfile
COPY web-client/ ./
RUN bun run generate

# ---- build: the Go binary ----------------------------------------------------
FROM golang:1.26.5-trixie AS build

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN go install "github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc5"

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# web/ is dockerignored so a stale local build cannot leak in; the embed still
# needs something there until the real SPA arrives below.
RUN mkdir -p web && touch web/placeholder.txt

# docs/ is go:embed'd, so the spec has to be regenerated before the build or the
# binary ships whatever was last committed. Before the SPA lands in web/ so swag
# has fewer files to walk.
RUN swag init --v3.1 -g server.go -o docs --outputTypes json

COPY --from=web /web/.output/public ./web

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/control .

# ---- runtime -----------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/control /control

EXPOSE 1323
ENTRYPOINT ["/control"]
CMD ["serve"]
