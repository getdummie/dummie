#!/bin/sh
# Points the built site at this deployment's control plane.
#
# The console links are baked into the prerendered HTML rather than redirected,
# so the URL is in the output as a literal. The image is built against a sentinel
# host; this swaps it for CONSOLE_URL before nginx starts, which is what keeps one
# image usable by every deployment without a rebuild.
#
# The nginx image runs everything in /docker-entrypoint.d in order at container
# start. Note this writes to /srv, so the image cannot run with a read-only
# root filesystem.
set -eu

# Must match the value baked in by the Dockerfile's build stage.
SENTINEL="https://console-url.invalid"

: "${CONSOLE_URL:?CONSOLE_URL is not set}"

# A trailing slash would produce `https://host//signin`.
url="${CONSOLE_URL%/}"

# Both .html and .js: the URL is in the prerendered markup and in the payload the
# client hydrates from, and they have to agree or Vue reports a mismatch.
find /srv -type f \( -name '*.html' -o -name '*.js' -o -name '*.json' \) \
  -exec sed -i "s|${SENTINEL}|${url}|g" {} +

# A miss would ship links to a domain that cannot resolve, which is worth failing
# on rather than serving.
if grep -rql "$SENTINEL" /srv 2>/dev/null; then
  echo "10-console-url.sh: $SENTINEL still present in /srv after substitution" >&2
  exit 1
fi

echo "10-console-url.sh: console links point at ${url}"
