#!/bin/sh
# Run on bbchain1 as hgogada.
# Checks that all kernel modules required by netemu are present on bbchain2..bbchain30.
set -eu

USER="hgogada"

# xt_MARK is not a loadable module on iptables-nft systems; checked functionally below.
MODULES="sch_htb sch_netem cls_fw ifb act_mirred cls_u32"

PASS=0
FAIL=0

for i in $(seq 2 30); do
    node="bbchain${i}"
    missing=""

    for mod in $MODULES; do
        if ! ssh -o BatchMode=yes -o ConnectTimeout=5 "${USER}@${node}" \
            "lsmod | grep -q '^${mod}' || modinfo ${mod} > /dev/null 2>&1"; then
            missing="${missing} ${mod}"
        fi
    done

    # xt_MARK may be built-in: check the iptables target list instead
    if ! ssh -o BatchMode=yes -o ConnectTimeout=5 "${USER}@${node}" \
        "grep -qw MARK /proc/net/ip_tables_targets 2>/dev/null || \
         sudo iptables -t mangle -A OUTPUT -j MARK --set-mark 1 2>/dev/null && \
         sudo iptables -t mangle -D OUTPUT -j MARK --set-mark 1 2>/dev/null"; then
        missing="${missing} xt_MARK"
    fi

    if [ -z "$missing" ]; then
        echo "OK   ${node}"
        PASS=$((PASS + 1))
    else
        echo "FAIL ${node}: missing:${missing}"
        FAIL=$((FAIL + 1))
    fi
done

echo ""
echo "results: ${PASS} ok, ${FAIL} failed out of $((PASS + FAIL)) nodes"
[ "$FAIL" -eq 0 ]
