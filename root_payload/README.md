# Ecovacs OZMO 930 (DR930 / DG3G) USB Root Exploit & Persistence

This directory contains the root payload and persistence scripts used to gain persistent root access on the Ecovacs Deebot OZMO 930.

---

## 1. Vulnerability Overview

The OZMO 930 firmware (`/usr/lib/udev/rules.d/usb-storage.rules`) executes `/usr/bin/udisk.sh start %k` as `root` (UID 0) whenever a USB storage device is inserted via the micro-USB OTG port.

In `/usr/bin/udisk.sh`:
```sh
PRODUCT=dr930
if [ -f ${USB_MNT}/${PRODUCT}/${PRODUCT}.sh ]; then
    sh ${USB_MNT}/${PRODUCT}/${PRODUCT}.sh $1
fi
```
The script searches for `/udisk/dr930/dr930.sh` (or `autorun.sh` / `pwn.sh` / `115/115.sh`) on the root of any connected FAT32 USB drive and executes it directly as `root`.

---

## 2. Exploitation Steps

1. Format a USB flash drive as FAT32 (label: `VACROOT`).
2. Copy `autorun.sh` to the root of the USB drive.
3. Copy `dr930/dr930.sh` into a `dr930/` folder on the USB drive.
4. Plug the USB flash drive into the vacuum's micro-USB OTG port under the top cover.
5. Power on or reboot the vacuum.
6. The exploit executes automatically:
   - Starts root telnet daemons on ports `23` and `2323` (no password required).
   - Installs persistent boot script to `/data/autostart/00-root-telnet.sh`.
   - Dumps partition table, CPU info, and `/etc` configs to `/dump/` on the USB drive.
   - Plays an audible chime when successful.

---

## 3. Persistent Boot Services (`/data/autostart/`)

On every boot, `/etc/rc.sysinit` runs `/etc/rc.d/autostart.sh`, which executes all scripts in `/data/autostart/*.sh` as `root`. The `/data` ext4 partition is fully writable.

### Installed Autostart Script:

- **`00-root-telnet.sh`**:
  - Launches `/usr/sbin/telnetd -l /bin/sh -b 0.0.0.0` on port 23 and port 2323.
  - Binds to all network interfaces, providing immediate root shell access whether in AP mode (`192.168.0.1`) or station mode.
