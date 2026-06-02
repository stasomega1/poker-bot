#!/bin/sh
set -eu

CERT_DIR="/etc/letsencrypt/live/${SERVER_NAME}"
TARGET="/etc/nginx/conf.d/default.conf"

if [ -f "${CERT_DIR}/fullchain.pem" ] && [ -f "${CERT_DIR}/privkey.pem" ]; then
  envsubst '$SERVER_NAME' < /etc/nginx/templates/https.conf.template > "${TARGET}"
else
  envsubst '$SERVER_NAME' < /etc/nginx/templates/http-only.conf.template > "${TARGET}"
fi

/bin/sh /reload-watch.sh &
exec nginx -g 'daemon off;'
