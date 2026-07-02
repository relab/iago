#!/bin/sh
# Run on bbchain1 as hgogada.
# Installs and loads missing netemu kernel modules on bbchain2..bbchain30.
set -eu

USER="hgogada"

# xt_MARK is not a loadable module on iptables-nft systems; MARK target works natively.
MODULES="sch_htb sch_netem cls_fw ifb act_mirred cls_u32"

PASS=0
FAIL=0

# If a node name is passed as argument, run on that node only; otherwise all nodes.
if [ $# -gt 0 ]; then
    NODES="$1"
else
    NODES=$(seq 2 30 | awk '{printf "bbchain%d ", $1}')
fi

for node in $NODES; do

    result=$(ssh -o BatchMode=yes -o ConnectTimeout=5 "${USER}@${node}" "
        set -e
        KERNEL=\$(uname -r)
        PKG=\"linux-modules-extra-\${KERNEL}\"
        FAILED=''

        # ensure modules-extra package is installed (provides xt_MARK.ko)
        if ! dpkg -l \"\$PKG\" 2>/dev/null | grep -q '^ii'; then
            echo \"  installing \$PKG...\"
            sudo apt-get install -y \"\$PKG\" 2>&1 || { echo \"FAIL apt-get install \$PKG failed\"; exit 1; }
        fi

        for mod in $MODULES; do
            if lsmod | grep -q \"^\${mod}\"; then
                continue
            fi
            if sudo modprobe \"\$mod\" 2>/dev/null; then
                echo \"  loaded: \$mod\"
            else
                FAILED=\"\$FAILED \$mod\"
            fi
        done

        if [ -n \"\$FAILED\" ]; then
            echo \"FAIL still missing:\$FAILED\"
        else
            echo 'OK'
        fi
    " 2>&1)

    if echo "$result" | grep -q '^OK'; then
        echo "OK   ${node}"
        PASS=$((PASS + 1))
    else
        echo "FAIL ${node}:"
        echo "$result" | sed 's/^/       /'
        FAIL=$((FAIL + 1))
    fi
done

echo ""
echo "results: ${PASS} ok, ${FAIL} failed out of $((PASS + FAIL)) nodes"
[ "$FAIL" -eq 0 ]
