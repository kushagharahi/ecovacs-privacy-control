# `libpersistmap`: Ecovacs Deebot OZMO 930 Map Preservation & Recovery Patch

A dedicated firmware patch that solves two critical architectural bugs in Ecovacs Deebot OZMO 930 (DR930 / DG70) robot vacuum firmware:

1. **Bug 1: Catastrophic Map-Wiping on Relocation Failure (`relocGoChgFail` / `relocLiftFail`)**
   - When a robot is cleaning or returning to dock and relocation times out (60s), Ecovacs firmware purges the active SLAM map from RAM, resets odometry coordinates to `(0, 0)` in the middle of the room, and enters `docklostsearch` (blindly driving in arcs into walls).
   - This patch traps relocation failure events, blocks the map wipe, preserves the last known valid pose, and suppresses blind wandering.

2. **Bug 2: Fake Stub `RecoverMap` Handler (`0x1a478`)**
   - In `plug_medusa_plugin_wifiHandler.so` at address `0x1a478`, `<ctl td="RecoverMap"/>` is an empty stub that only echoes `ret="ok"` without ever signaling the SLAM engine or reloading map storage.
   - This patch intercepts `<ctl td="RecoverMap"/>` and executes a true reload from `/data/FILES/restoreMap.bin`, `restorePose.bin`, and `restoreChargerStand.bin`.

---

## Technical Reverse Engineering Findings

- **Command Dispatcher**: `getCtlHandlerFromTdStz` (`0x1c224` in `plug_medusa_plugin_wifiHandler.so`)
- **Decompiled `RecoverMap` Stub (`0x1a478`)**:
  ```c
  void handleRecoverMap(undefined4 param_1, int param_2) {
      int id = iks_find_attrib(param_1, "id");
      // Echoes <ctl id="..." ret="ok"/> without contacting SLAM!
  }
  ```
- **Relocation States (`HandleRelocation2Report`)**:
  - `relocLiftStart` (1), `relocLiftOk` (2), `relocLiftFail` (3)
  - `relocOnChgStart` (4), `relocOnChgOk` (5), `relocOnChgFail` (6)
  - `relocGoChgStart` (7), `relocGoChgOk` (8), `relocGoChgFail` (9)

---

## Interception Logic

`libpersistmap.so` hooks into Medusa via `LD_PRELOAD`:

1. **`MDSElemCastMsg(ctx, "TASK_MAN", payload)`**:
   - Detects state 3 (`relocLiftFail`) and state 9 (`relocGoChgFail`).
   - Flags `g_reloc_failure_trapped = 1`.
   - Prevents downstream elements from treating the map as corrupted.

2. **`MDSElemCastMsg(ctx, "MAP", payload)`**:
   - Drops any automatic map-clearing instruction triggered by relocation timeout.

3. **`MDSElemCastMsg(ctx, "CtlCmd", payload)`**:
   - Intercepts `<ctl td="RecoverMap"/>` and reads persistent map files from `/data/FILES/`.

4. **`MDSElemSendMsg(src, "taskreturn", "docklostsearch", payload)`**:
   - Blocks transition to `docklostsearch` / `dockrandom` so the robot does not blindly drive in random arcs into walls.

---

## Compilation

Builds inside Docker using the `arm-builder` image:

```bash
cd robot_patches/libpersistmap
make
```

Produces `libpersistmap.so` (ARMv7 32-bit ELF).

---

## Deployment to Robot

Copy to the robot's `/data/` partition via SCP or Telnet:

```bash
scp libpersistmap.so root@<ROBOT_IP>:/data/
```

Add to `/etc/init.d/S99medusa` or `/etc/rc.d/medusa.sh`:

```bash
export LD_PRELOAD="/data/libpersistmap.so /data/libsilentgoto.so"
```
