#!/bin/sh
# Run on bbchain1 as hgogada.
# SSHes to bbchain2..bbchain30 and grants hgogada passwordless sudo on each node.
set -eu

USER="hgogada"

printf "Enter sudo password for %s on remote nodes: " "$USER"
stty -echo
read -r SUDO_PASS
stty echo
echo

for i in $(seq 2 30); do
    node="bbchain${i}"
    printf "%s... " "$node"
    if ssh -o BatchMode=yes "${USER}@${node}" \
        "echo '${SUDO_PASS}' | sudo -S sh -c 'echo \"${USER} ALL=(ALL) NOPASSWD:ALL\" > /etc/sudoers.d/${USER} && chmod 440 /etc/sudoers.d/${USER}'" \
        2>/dev/null; then
        echo "done"
    else
        echo "FAILED"
    fi
done

echo "all nodes processed"
