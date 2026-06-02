#!/bin/sh
set -eu

RELOAD_FLAG="/var/www/certbot/.nginx-reload"

while true; do
  if [ -f "${RELOAD_FLAG}" ]; then
    rm -f "${RELOAD_FLAG}"
    nginx -s reload || true
  fi
  sleep 30
done
