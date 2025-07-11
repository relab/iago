#!/bin/bash
set -euo pipefail

mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"

if [ -z "${AUTHORIZED_KEYS:-}" ]; then
  echo "AUTHORIZED_KEYS is not set" >&2
  exit 1
fi

echo "$AUTHORIZED_KEYS" > "$HOME/.ssh/authorized_keys"
chmod 600 "$HOME/.ssh/authorized_keys"
service ssh start
exec "$@"
