#!/bin/sh
set -eu

RELOAD_FLAG="/var/www/certbot/.nginx-reload"
CHECK_INTERVAL_SECONDS="${CHECK_INTERVAL_SECONDS:-43200}"

while true; do
  certbot renew \
    --webroot \
    -w /var/www/certbot \
    --quiet \
    --deploy-hook "touch ${RELOAD_FLAG}"
  sleep "${CHECK_INTERVAL_SECONDS}"
done
