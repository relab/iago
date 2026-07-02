#!/bin/sh
# Run on bbchain1 as hgogada.
# Verifies passwordless SSH and passwordless sudo on bbchain2..bbchain30.
set -eu

USER="hgogada"
TOTAL=0
PASS=0
FAIL=0

for i in $(seq 2 30); do
    node="bbchain${i}"
    TOTAL=$((TOTAL + 1))

    # test passwordless SSH
    if ! ssh -o BatchMode=yes -o ConnectTimeout=5 "${USER}@${node}" true 2>/dev/null; then
        echo "FAIL ${node}: SSH login failed"
        FAIL=$((FAIL + 1))
        continue
    fi

    # test passwordless sudo
    if ! ssh -o BatchMode=yes "${USER}@${node}" "sudo -n true" 2>/dev/null; then
        echo "FAIL ${node}: passwordless sudo not working"
        FAIL=$((FAIL + 1))
        continue
    fi

    echo "OK   ${node}"
    PASS=$((PASS + 1))
done

echo ""
echo "results: ${PASS} ok, ${FAIL} failed out of ${TOTAL} nodes"
[ "$FAIL" -eq 0 ]
