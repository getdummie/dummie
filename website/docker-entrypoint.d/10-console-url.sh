#!/bin/sh
set -eu

SENTINEL="https://console-url.invalid"

: "${CONSOLE_URL:?CONSOLE_URL is not set}"

url="${CONSOLE_URL%/}"

find /srv -type f \( -name '*.html' -o -name '*.js' -o -name '*.json' \) \
  -exec sed -i "s|${SENTINEL}|${url}|g" {} +

if grep -rql "$SENTINEL" /srv 2>/dev/null; then
  echo "10-console-url.sh: $SENTINEL still present in /srv after substitution" >&2
  exit 1
fi

echo "10-console-url.sh: console links point at ${url}"
