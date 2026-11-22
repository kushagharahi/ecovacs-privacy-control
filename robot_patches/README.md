# Ecovacs Firmware Patch Deployment Tool (Telnet Only)

This tool automatically discovers any `.so` shared library patches in a given directory and deploys them directly to the robot over Telnet without hardcoding filenames or requiring extra open network ports.

---

## How It Works

1. **Dynamic Discovery**: Scans whatever directory you pass (or defaults to `robot_patches/`) for all `.so` libraries.
2. **Telnet Base64 Transfer**: Streams each `.so` directly across the existing Telnet connection (`port 23`) using `base64 -d`, requiring zero host HTTP servers, extra firewall rules, or complex network routing back to your laptop.
3. **Remote Checksum Verification**: Calculates local MD5 sums and compares them directly against `md5sum` on the robot.
4. **Dynamic Preload Wrapper**: Generates `/data/rc.d/medusa.sh` with the exact `LD_PRELOAD` string containing all discovered libraries.
5. **Autostart Boot Hook**: Deploys `/data/autostart/30-medusa-preload.sh`, which automatically bind-mounts `/data/rc.d/medusa.sh` over `/etc/rc.d/medusa.sh` on every system boot.

---

## Usage

### 1. Deploy Patches from Default Directory
```bash
python3 robot_patches/deploy.py --robot-ip <ROBOT_IP>
```

### 2. Deploy Patches from Any Custom Directory
```bash
python3 robot_patches/deploy.py /path/to/my_patches --robot-ip <ROBOT_IP>
```

### 3. Verify Existing Installation
```bash
python3 robot_patches/deploy.py --robot-ip <ROBOT_IP> --verify-only
```

### 4. Deploy and Apply Immediately
```bash
python3 robot_patches/deploy.py --robot-ip <ROBOT_IP> --restart-medusa
```
*(By default, patches are installed to `/data` and take effect on next boot. Use `--restart-medusa` to restart the daemon immediately).*

---

## Compilation

To compile the patches in `robot_patches/` before deploying:

```bash
make -C robot_patches all
```
The Makefile dynamically discovers and builds all subdirectories containing a Makefile.
