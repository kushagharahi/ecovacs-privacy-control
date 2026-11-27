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
  - **Relocation Map Wipe**: In factory firmware (`plug_mmi_periph.so` `onMainTaskFinished`), if `Lift`/`DuringRunning` relocation (`reloc_type == 0`) fails (or succeeds with `> 1.5 m` jump), Medusa calls `MdsresetAll(ctx, 0)`, clears the RAM map and odometry to `(0, 0)`, and auto-launches Task `0` Auto Clean (or blind return search).
  - **RecoverMap Stub**: In `plug_medusa_plugin_wifiHandler.so` (`0x1a478`), the `<ctl td="RecoverMap"/>` command handler is an empty stub that only echoes `ret="ok"` without ever signaling SLAM or reloading storage.
- **Interception Mechanism**:
  - Hooks `plug_mmi_periph.so` PLT functions (`MdsLoadAndAnalyzeMap`, `MdsStartReloadMap`, `MdsresetAll`, `onMainTaskFinished`): on `Lift`/`Running` relocation failure with a valid map, inspects the active task queue (`MdsHasWorkToDo` / `MdsGetNextMoveTask`) so queued Auto cleans (`subtype` `1`/`2`) proceed via RAM-only fallback clean (preserving `/data/FILES/*`), while Room/Spot/Zone cleans (`subtype` `3`/`5`/`6`), Return-to-dock, and idle wheel-lifts stop cleanly and restore the map frame; on `Reload` final failure (`> 3` retries) with `ctx+0x14c != 0`, prevents `MdsSaveMapToFs` overwrite.
  - Parses `TASK_MAN` type `5` (`{ reloc_type, status }`) and allows `MAP` cmd `3` subcmds except unauthorized `subcmd == 4` (`clearWifiMap`).
  - Implements actual `RecoverMap` logic: unwraps `CtlCmd` `{ from, to, iks* }` via `iks_find_attrib` and triggers `MdsStartReloadMap` (or verifies `restoreMap.bin`/`restorePose.bin`).
  - Filesystem Protection: Intercepts `unlink()`, `remove()`, and firmware wipe routines (`rst_removeAllSavedFiles`, `rst_removeRbpfMapAndPose`) targeting `/data/FILES/` files unless explicit user `ClearMap` (`MdsresetAll(ctx, 1)`) is active.
- **Runtime Log**: `/tmp/persist_map.log`

### B. `libsilentgoto` (Pin & Go Silent Navigation)
- **Directory**: `robot_patches/libsilentgoto/`
- **Output Binary**: `libsilentgoto.so`
- **Problem Solved**:
  - In factory firmware, autonomous point-to-point transit is exclusively coupled to cleaning tasks (Task 9 `SpotArea`). Any transit command unconditionally engages cleaning suction and brush motors (11,000–15,000 RPM) via `MDSMotorControl` (`0x29724` in `plug_mmi_periph.so`) and announces voice lines ("I am starting the clean").
- **Interception Mechanism**:
  - Activated dynamically when `/tmp/silent_goto` exists on the robot filesystem (created during Go-To navigation by the HA bridge).
  - Intercepts `MDSMotorControl`: Forces motor mode to `2` (Idle / 0 RPM) during transit.
  - Intercepts `MdsMmiMotorElemCastMotorSetMmiMsg`: Zeroes out suction, main brush, and side brush speed targets in peripheral hardware packets.
  - Silences transit voice announcements (`0x0e, 0x13, 0x14, 0x15, 0x16, 0x18, 0x1b, 0x1d, 0x25, 0x26, 0x28, 0x2d` on `MdsPlayAudio` / `PLAYER cmd == 1`) without modifying stock audio files.
- **Runtime Log**: `/tmp/silent_goto.log`

### C. `libdirectpath` (Direct Global A*, LOS Path Smoothing & Stuck Heat-Map)
- **Directory**: `robot_patches/libdirectpath/`
- **Output Binary**: `libdirectpath.so`
- **Problem Solved**:
  - In factory firmware, `astar_exe_G2G_NextRoom02` (`0x251c0` in `plug_medusa_plugin_task_ergodic.so`) runs a hierarchical Gate-to-Gate (G2G) path planner that queries `rma_getDoorPathNodesByGraphSearch`.
  - In open-concept spaces (such as open kitchen/living room layouts), auto-segmentation creates partition boundaries where no physical walls exist. The G2G solver treats these boundaries as abstract rooms requiring transit through a synthetic "door gate" coordinate on the boundary line, forcing the robot to steer completely out of its way to pass through an imaginary doorway before continuing to the target.
  - Raw grid-quantized A* paths also exhibit 45°/90° stair-stepping across open spaces and can fail when a start or transit goal lands 1–2 cells inside an inflated obstacle boundary.
- **Interception Mechanism**:
  - Intercepts `findpathInt400` and `ast_astarInt400WitCoef` (alongside bounded `astar_exe_G2G_NextRoom02`) via `LD_PRELOAD`.
  - Runs single-pass soft-inflated 8-dir A* on the `400x400` (`100mm/cell`) grid with `MAX_ASTAR_POPS = 120000`, `coef <= 0.6f` tight-clearance scoping, and `MAX_RAW_ASTAR_NODES = 999` / `MAX_WAYPOINTS = 50` caps.
  - Applies clearance-aware supercover Bresenham **Line-of-Sight (LOS) string-pulling** (`smooth_path_los`) to collapse 45°/90° staircases into straight any-angle paths while guaranteeing safe wall clearance (`>= 200mm` normal, `>= 100mm` tight transit).
  - Performs spiral nearest-free snapping for wedged start cells and blocked `G2G`/`witCoef` goals (while keeping upfront fast-fail for unreachable zig-zag sweep probes).
  - Exports `nav_record_stuck_cell()` to maintain a decaying 10-minute spatial stuck heat-map (`+heat * 60` A* step penalty) fed by `libnavboost`.
- **Runtime Log**: `/tmp/direct_path.log`

### D. `libwifiguard` (WiFi Power-Gate Protection & True Link LED Sync)
- **Directory**: `robot_patches/libwifiguard/`
- **Output Binary**: `libwifiguard.so`
- **Problem Solved**:
  - Low-battery MCU warning (`CH_SIG` `10` in `MdsTaskManagerProcess` `0x29d2c`) and power-switch toggles while sitting on live dock contacts (`KEY (0, 3)` in `MdsKeyMsgHandler` `0x2940c`) invoke `/etc/conf/medusa/disable_wifi.sh`, killing `wifi_service`/`wpa_supplicant`/`wifi_daemon`/`dhcpcd`, unloading `wlan.ko`, and pulling `gpio274` low with no re-enable path upon docking.
  - `/etc/conf/medusa/xmpp_server_status.sh` drives the top WiFi LED solid whenever XMPP is connected—even when connected only to the loopback on-bot controller (`127.0.0.1:5223`) while `wlan0` is disconnected.
- **Interception Mechanism**:
  - Intercepts `system()` in `medusa` to block `disable_wifi.sh` and gate `xmpp_server_status.sh` solid LED status on actual `wlan0` station IPv4 (`SIOCGIFFLAGS` + `SIOCGIFADDR`, excluding `169.254.x.x` and AP `192.168.0.1`).
  - Hooks `MDSElemCastMsg` (`xcRestart` from `nt0` and `LED(2,0,0)`) to sync the WiFi LED solid immediately on `wlan0` reconnect and prevent blanking while online, plus `onRobotEnterCharger` (`0x29264`) to self-heal `gpio274` and WiFi daemons if ever disabled externally.
- **Runtime Log**: `/tmp/wifi_guard.log`

### E. `libnavboost` (VFH+ Lidar Obstacle Steering, Pose-Stall Recovery & Smooth Motion)
- **Directory**: `robot_patches/libnavboost/`
- **Output Binary**: `libnavboost.so`
- **Problem Solved**:
  - Factory Ozmo 930 relies almost exclusively on physical bumper impacts for unmapped dynamic obstacles (shoes, chair legs), commands instantaneous wheel speed jumps that cause wheel slip / odometry drift, spins endlessly in one direction on jammed bumpers, and lacks high-centering (zero-displacement) stall recovery when the bumper is not pressed.
- **Interception Mechanism**:
  - **VFH+ 72-Sector Polar Histogram Steering (`adjustSpeed` hook)**: Reads live 360° Lidar points from `pRobotLdsPic` (`+0x2be8` count, `+0xa28` array) and, when `>= 3` confirming rays enter the forward danger corridor (`155–460mm` ahead, `|y| <= 205mm`), builds a robot-radius-inflated (`222mm`) 72-sector polar histogram to smoothly steer around obstacles before impact (automatically paused during dock IR alignment).
  - **Zero-Displacement Pose-Stall & Wheel-Current Recovery**: Monitors `pAdapterDeebot_9` pose (`+0x2c08/+0x2c0c`), `_alert_isWheelLargeCurrent`, and `_alert_isSmallWheelStoped`; if commanded forward `> 2.5s` with `< 30mm` movement or sustained current/caster stall `> 1.2s`, injects a `320ms` alternating reverse-arc unhook pulse and reports the location to `libdirectpath`'s stuck heat-map.
  - **Smooth Acceleration Slew-Rate Limiter**: Caps forward/turn wheel speed jumps (`4.5` units / 20ms tick) while passing immediate stops `(0, 0)` and reverse commands straight through with zero lag.
  - **Bump-Cluster Heat-Map & Anti-Deadlock Pulse (`dwb_bumpUpdateInGridMapAndReduced`, `dtp_DeTrapInit`, `bumpStuckCheckAndHandle`)**: Feeds repeated bump clusters (`>= 3` bumps within `300mm` in `15s`) and detrap entries to `libdirectpath`, and pulses a `240ms` reverse nudge if a jammed bumper spins `> 1.6s`.
- **Runtime Log**: `/tmp/nav_boost.log`

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

Patches are deployed directly to the robot over Telnet (port 23):

```bash
# Deploy all patches in robot_patches/ and restart medusa
python3 robot_patches/deploy.py --robot-ip <ROBOT_IP> --restart-medusa

# Deploy and compile specific patches in a single step
python3 robot_patches/deploy.py libdirectpath libpersistmap libsilentgoto --build --robot-ip <ROBOT_IP> --restart-medusa
```

---

## 5. Verification & Runtime Diagnostics

### Check Patch Installation Without Modifying Files
```bash
python3 robot_patches/deploy.py --robot-ip <ROBOT_IP> --verify-only
```

### Verify Loaded Patches in Medusa Memory
```bash
(echo 'grep -E "libdirectpath|libpersistmap|libsilentgoto" /proc/$(pidof medusa)/maps'; sleep 1; echo 'exit') | telnet <ROBOT_IP> 23
```
*Expected output: Memory mappings for `/data/libdirectpath.so`, `/data/libpersistmap.so`, and `/data/libsilentgoto.so` in `r-xp` segments.*

### Inspect Runtime Patch Logs
```bash
(echo 'tail -n 20 /tmp/direct_path.log'; echo 'tail -n 20 /tmp/persist_map.log'; echo 'tail -n 20 /tmp/silent_goto.log'; sleep 1; echo 'exit') | telnet <ROBOT_IP> 23
```

### Clean Medusa Restart (Without Rebooting Robot)
```bash
(echo '/data/rc.d/medusa.sh restart'; sleep 2; echo 'pidof medusa'; sleep 1; echo 'exit') | telnet <ROBOT_IP> 23
```
