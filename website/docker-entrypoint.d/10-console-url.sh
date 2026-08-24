#!/bin/sh
# Points the built site at this deployment's control plane.
#
# The console links are baked into the prerendered HTML rather than redirected,
# so the URL is in the output as a literal. The image is built with a sentinel in
# its place; this swaps the sentinel for CONSOLE_URL before nginx starts, which
# is what keeps one image usable by every deployment without a rebuild.
#
# The nginx image runs everything in /docker-entrypoint.d in order at container
# start. Note this writes to /srv, so the image cannot run with a read-only
# root filesystem.
set -eu

: "${CONSOLE_URL:?CONSOLE_URL is not set}"

# A trailing slash would produce `https://host//signin`.
url="${CONSOLE_URL%/}"

# Both .html and .js: the URL is in the prerendered markup and in the payload the
# client hydrates from, and they have to agree or Vue reports a mismatch.
find /srv -type f \( -name '*.html' -o -name '*.js' -o -name '*.json' \) \
  -exec sed -i "s|__CONSOLE_URL__|${url}|g" {} +

echo "10-console-url.sh: console links point at ${url}"
