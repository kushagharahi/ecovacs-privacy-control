# Ecovacs Firmware Runtime Patches & Deployment Framework

This directory contains dynamic runtime interception patches (`LD_PRELOAD` shared libraries) for the Ecovacs Deebot OZMO 930 (`DR930` / `DG70`), compiled for Rockchip RK3188 ARMv7-A (`ARM:LE:32:v7`), along with the automated Telnet-based deployment tool.

---

## 1. Overview & Architecture

Because the core root filesystem on the robot is mounted from a read-only SquashFS (`/dev/mtdblock3`), permanent binary modifications cannot be written directly to `/usr/bin/medusa` or `/usr/lib/medusa/*.so`.

Instead, our patching architecture leverages dynamic `LD_PRELOAD` injection:
1. **Persistent Storage (`/data`)**: Patches are stored on the writable ext4 data partition at `/data/*.so`.
2. **Dynamic Daemon Wrapper (`/data/rc.d/medusa.sh`)**: An updated Medusa init script discovers all `.so` shared libraries in `/data/` and injects them into Medusa's process space via `LD_PRELOAD`.
3. **Boot Persistence (`/data/autostart/30-medusa-preload.sh`)**: The system autostart script executes on boot, bind-mounting `/data/rc.d/medusa.sh` over `/etc/rc.d/medusa.sh`. When the system watchdog or supervisor starts `medusa`, all active patches are loaded automatically.
4. **Telnet-Only Ingress (`deploy.py`)**: Streams compiled `.so` binaries over standard Telnet (port 23) using `base64 -d`, requiring zero host HTTP servers, extra open firewall ports, or complex reverse-NAT routing.

---

## 2. Active Patches

### A. `libpersistmap` (Map Preservation & Recovery)
- **Directory**: `robot_patches/libpersistmap/`
- **Output Binary**: `libpersistmap.so`
- **Problem Solved**:
  - **Relocation Map Wipe**: In factory firmware, if return-to-dock or wheel-lift relocation fails (`relocGoChgFail` state 9 or `relocLiftFail` state 3 in `task_relocation.so`), Medusa clears the active SLAM map from RAM, resets odometry to `(0, 0)`, and enters `docklostsearch` (circling blindly and ramming walls).
  - **RecoverMap Stub**: In `plug_medusa_plugin_wifiHandler.so` (`0x1a478`), the `<ctl td="RecoverMap"/>` command handler is an empty stub that only echoes `ret="ok"` without ever signaling SLAM or reloading storage.
- **Interception Mechanism**:
  - Intercepts `MDSElemCastMsg("TASK_MAN", ...)`: When a catastrophic relocation failure (state 3 or 9) occurs, flags the event and traps subsequent `MDSElemCastMsg("MAP", ClearMap)` calls to prevent RAM map erasure.
  - Intercepts `MDSElemSendMsg(..., "taskreturn", ...)`: Suppresses `docklostsearch` and `dockrandom` messages, preventing erratic obstacle collisions.
  - Implements actual `RecoverMap` logic: Reads `/data/FILES/restoreMap.bin`, reloads dock coordinates from `restorePose.bin`, and re-anchors the SLAM engine.
  - Filesystem Protection: Intercepts `unlink()`, `remove()`, and firmware wipe routines (`rst_removeAllSavedFiles`, `rst_removeRbpfMapAndPose`) targeting `/data/FILES/*.bin`.
- **Runtime Log**: `/tmp/persist_map.log`

### B. `libsilentgoto` (Pin & Go Silent Navigation)
- **Directory**: `robot_patches/libsilentgoto/`
- **Output Binary**: `libsilentgoto.so`
- **Problem Solved**:
  - In factory firmware, autonomous point-to-point transit is exclusively coupled to cleaning tasks (Task 9 `SpotArea`). Any transit command unconditionally engages cleaning suction and brush motors (11,000–15,000 RPM) via `MDSMotorControl` (`0x29724` in `plug_mmi_periph.so`) and announces voice lines ("I am starting the clean").
- **Interception Mechanism**:
  - Activated dynamically when `/tmp/silent_goto` exists on the robot filesystem (created during Go-To navigation by the Valetudo bridge).
  - Intercepts `MDSMotorControl`: Forces motor mode to `2` (Idle / 0 RPM) during transit.
  - Intercepts `MdsMmiMotorElemCastMotorSetMmiMsg`: Zeroes out suction, main brush, and side brush speed targets in peripheral hardware packets.
  - Silences transit voice announcements (events 55, 24, 80, 81, 82) without modifying stock audio files.
- **Runtime Log**: `/tmp/silent_goto.log`

### C. `libdirectpath` (Direct Global A* & Phantom Doorway Bypass)
- **Directory**: `robot_patches/libdirectpath/`
- **Output Binary**: `libdirectpath.so`
- **Problem Solved**:
  - In factory firmware, `astar_exe_G2G_NextRoom02` (`0x251c0` in `plug_medusa_plugin_task_ergodic.so`) runs a hierarchical Gate-to-Gate (G2G) path planner that queries `rma_getDoorPathNodesByGraphSearch`.
  - In open-concept spaces (such as open kitchen/living room layouts), auto-segmentation creates partition boundaries where no physical walls exist. The G2G solver treats these boundaries as abstract rooms requiring transit through a synthetic "door gate" coordinate on the boundary line, forcing the robot to steer completely out of its way to pass through an imaginary doorway before continuing to the target.
- **Interception Mechanism**:
  - Intercepts `astar_exe_G2G_NextRoom02` via `LD_PRELOAD`.
  - Runs native unconstrained 2D Euclidean A* across the full 800x800 grid (`ast_astarInt400`) directly from start coordinates to destination coordinates.
  - On success, commits waypoints to the motion controller via native `setAstartWalkPoint02`, completely bypassing synthetic room boundary gates.
  - If direct A* fails (e.g. search budget exceeded or separated by real obstacles), gracefully falls back to stock `astar_exe_G2G_NextRoom02`.
- **Runtime Log**: `/tmp/direct_path.log`

---

## 3. Compilation

Patches are cross-compiled for ARMv7 using the local Docker cross-compiler container (`arm-builder` with `arm-linux-gnueabihf-gcc`):

### Compile All Patches
```bash
make -C robot_patches all
```

### Compile a Specific Patch
```bash
make -C robot_patches/libdirectpath
# or
make -C robot_patches/libpersistmap
# or
make -C robot_patches/libsilentgoto
```

---

## 4. Deployment (`deploy.py`)

### A. Deployment via Kubernetes Probe Pod (Isolated Subnet)
Because the robot resides on an isolated subnet (`192.168.10.0/24`) not directly routed to the development host, deployment is executed via the `multitool-probe` pod in the `iot-jail` namespace:

```bash
# 1. Copy deployment script and compiled patches to the probe pod
kubectl cp -n iot-jail robot_patches/deploy.py multitool-probe:/tmp/deploy.py
kubectl cp -n iot-jail robot_patches/libdirectpath/libdirectpath.so multitool-probe:/tmp/libdirectpath.so
kubectl cp -n iot-jail robot_patches/libpersistmap/libpersistmap.so multitool-probe:/tmp/libpersistmap.so
kubectl cp -n iot-jail robot_patches/libsilentgoto/libsilentgoto.so multitool-probe:/tmp/libsilentgoto.so

# 2. Deploy patches and restart medusa immediately
kubectl exec -n iot-jail multitool-probe -- python3 /tmp/deploy.py \
  /tmp/libdirectpath.so \
  /tmp/libpersistmap.so \
  /tmp/libsilentgoto.so \
  --robot-ip 192.168.10.224 \
  --restart-medusa
```

### B. Direct Deployment (When Directly Routed)
```bash
# Deploy all patches in robot_patches/ and restart medusa
python3 robot_patches/deploy.py --robot-ip 192.168.10.224 --restart-medusa

# Deploy and compile in a single step
python3 robot_patches/deploy.py libdirectpath libpersistmap libsilentgoto --build --robot-ip 192.168.10.224 --restart-medusa
```

---

## 5. Verification & Runtime Diagnostics

### Check Patch Installation Without Modifying Files
```bash
kubectl exec -n iot-jail multitool-probe -- python3 /tmp/deploy.py --robot-ip 192.168.10.224 --verify-only
```

### Verify Loaded Patches in Medusa Memory
```bash
kubectl exec -n iot-jail multitool-probe -- sh -c \
  "(echo 'grep -E \"libdirectpath|libpersistmap|libsilentgoto\" /proc/\$(pidof medusa)/maps'; sleep 1; echo 'exit') | telnet 192.168.10.224 23"
```
*Expected output: Memory mappings for `/data/libdirectpath.so`, `/data/libpersistmap.so`, and `/data/libsilentgoto.so` in `r-xp` segments.*

### Inspect Runtime Patch Logs
```bash
kubectl exec -n iot-jail multitool-probe -- sh -c \
  "(echo 'tail -n 20 /tmp/direct_path.log'; echo 'tail -n 20 /tmp/persist_map.log'; echo 'tail -n 20 /tmp/silent_goto.log'; sleep 1; echo 'exit') | telnet 192.168.10.224 23"
```

### Clean Medusa Restart (Without Rebooting Robot)
```bash
kubectl exec -n iot-jail multitool-probe -- sh -c \
  "(echo '/data/rc.d/medusa.sh restart'; sleep 2; echo 'pidof medusa'; sleep 1; echo 'exit') | telnet 192.168.10.224 23"
```
