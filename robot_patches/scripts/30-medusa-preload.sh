#!/bin/sh
#
# 30-medusa-preload.sh
# Bind-mounts patched medusa wrapper on boot to activate LD_PRELOAD firmware hooks.
#

WRAPPER="/data/rc.d/medusa.sh"
TARGET="/etc/rc.d/medusa.sh"

case "$1" in
  stop)
    umount "$TARGET" 2>/dev/null
    ;;
  *)
    if [ -f "$WRAPPER" ]; then
        # Check if already bind-mounted
        if ! mount | grep -q "$TARGET"; then
            mount --bind "$WRAPPER" "$TARGET"
        fi

        # Verify whether the running medusa process has LD_PRELOAD active
        MEDUSA_PID=$(pidof medusa | awk '{print $1}')
        if [ -n "$MEDUSA_PID" ]; then
            if ! grep -q "LD_PRELOAD" "/proc/$MEDUSA_PID/environ" 2>/dev/null; then
                # Restart medusa to attach patches
                killall -9 medusa 2>/dev/null
                rm -f /tmp/medusa.pid /tmp/lock.file 2>/dev/null
                sleep 1
                "$TARGET" start
            fi
        fi
    fi
    ;;
esac
