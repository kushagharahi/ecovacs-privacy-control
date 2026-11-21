# Comprehensive Reverse Engineering & Protocol Documentation: Ecovacs Deebot OZMO 930 (DR930 / DG70)

This document contains the complete, definitive record of reverse engineering analysis conducted on the Deebot OZMO 930 firmware binaries (`research/930_DUMP`), hardware architecture, and the decompiled official Android client application (`research/apps/com.eco.global.app(3).apk_Decompiler.com`).

**DO NOT RE-RUN REA / GHIDRA DECOMPILATION FOR THE PROCEDURES DOCUMENTED BELOW.** All symbol addresses, decompiled C pseudocode, switch statements, and protocol formats are preserved here.

---

## Table of Contents
1. [Executive Summary & Core Architectural Constraints](#1-executive-summary--core-architectural-constraints)
2. [Firmware Analysis: Command Dispatcher (`plug_medusa_plugin_wifiHandler.so`)](#2-firmware-analysis-command-dispatcher-plug_medusa_plugin_wifihandlerso)
   - [A. Command Dispatch Table (`0x31b20`)](#a-command-dispatch-table-0x31b20)
   - [B. Decompiled `Clean` Handler (`0x183c0`)](#b-decompiled-clean-handler-0x183c0)
   - [C. Decompiled `Move` Handler (`0x165d0`)](#c-decompiled-move-handler-0x165d0)
   - [D. Fan Speed Hardcoding (`0x18964` & `0x1d1a4`)](#d-fan-speed-hardcoding-0x18964--0x1d1a4)
3. [Firmware Analysis: Task Manager & Motor Control (`plug_mmi_periph.so`)](#3-firmware-analysis-task-manager--motor-control-plug_mmi_periphso)
   - [A. Decompiled `MdsStartTask` (`0x28bdc`)](#a-decompiled-mdsstarttask-0x28bdc)
   - [B. Decompiled `MDSMotorControl` (`0x29724`)](#b-decompiled-mdsmotorcontrol-0x29724)
   - [C. Motor Dispatch `FUN_00021ed4` (`0x21ed4`)](#c-motor-dispatch-fun_00021ed4-0x21ed4)
   - [D. Motor Baseline & Carpet Boost Speeds (`0x2e6e8`)](#d-motor-baseline--carpet-boost-speeds-0x2e6e8)
4. [Hardware & Peripheral Bus Architecture](#4-hardware--peripheral-bus-architecture)
   - [A. Hardware Layout & Linux System](#a-hardware-layout--linux-system)
   - [B. Serial UART Mapping (`deebot_dr930.conf`)](#b-serial-uart-mapping-deebot_dr930conf)
5. [Official Android App Reference (`com.eco.global.app`)](#5-official-android-app-reference-comecoglobalapp)
   - [A. DR930 Model Logic (`DR930.java`)](#a-dr930-model-logic-dr930java)
   - [B. Protocol XML Generator (`EcoRobotProtocol.java`)](#b-protocol-xml-generator-ecorobotprotocoljava)
6. [Why "It's Still Cleaning" Occurs on Go-To Navigation](#6-why-its-still-cleaning-occurs-on-go-to-navigation)
   - [A. The Firmware Invariant](#a-the-firmware-invariant)
   - [B. Practical Solution & Dual-Mode Configuration](#b-practical-solution--dual-mode-configuration)
7. [Audio System & Custom Sound Engineering](#7-audio-system--custom-sound-engineering)
   - [A. Audio Codec & Hardware DAC Delay](#a-audio-codec--hardware-dac-delay)
   - [B. Custom Audio Soundboard Table](#b-custom-audio-soundboard-table)
   - [C. Robot Filesystem Persistence](#c-robot-filesystem-persistence)

---

## 1. Executive Summary & Core Architectural Constraints

1. **No Software Fan Speed Control**: The OZMO 930 hardware uses an automatic carpet sensor. It lacks `SetCleanSpeed` / `GetCleanSpeed`. Baseline suction is fixed at 11,000 RPM; Carpet Pressurization Boost runs at 15,000 RPM. Reports hardcode `speed="standard"`.
2. **Autonomous Transit Requires Cleaning Task**: The robot's firmware has NO native "drive to (x, y) without cleaning" task. Point-to-point autonomous navigation is only performed under Task Type 1 (Spot Clean) or Task Type 9 (Custom Zone Clean), both of which immediately engage `MDSMotorControl` (suction and brushes).
3. **Silent Navigation Exists ONLY for Docking**: The only autonomous SLAM navigation that runs silently without suction is Task Type 3 (`MdsStartReturnTask` / return to dock).
4. **Room vs Zone Mutual Exclusion**:
   - Spot Area with Room IDs (`mid`): `<clean act="s" type="SpotArea" mid="0,1" deep="1"/>`. The `p` attribute MUST NOT be present.
   - Spot Area with Coordinates (`p`): `<clean act="s" type="SpotArea" p="x1,y1,x2,y2" deep="1"/>`. The `mid` attribute MUST NOT be present.
   - If both are present, firmware aborts with `spotArea has mid and p`.
   - Never use `type="spot"` for zones (`TaskType 1` only accepts `x,y` single point and causes navigation failure / redock).

---

## 2. Firmware Analysis: Command Dispatcher (`plug_medusa_plugin_wifiHandler.so`)

### A. Command Dispatch Table (`0x31b20`)
The dispatch table in `getCtlHandlerFromTdStz` (`0x1c224`) matches incoming `<ctl td="...">` stanzas against exactly 44 registered command names:
1. `FactoryReset`
2. `GetBatteryInfo`
3. `Move`
4. `GetChargeState`
5. `Charge`
6. `GetLifeSpan`
7. `ResetLifeSpan`
8. `GetCleanLogs`
9. `EmptyCleanLog`
10. `GetLogs`
11. `EmptyLog`
12. `GetSched`
13. `AddSched`
14. `ModSched`
15. `DelSched`
16. `GetError`
17. `Clean`
18. `GetCleanState`
19. `GetCleanSum`
20. `GetWaterBoxInfo`
21. `GetChargerPos`
22. `GetPos`
23. `SetMapAutoReport`
24. `ClearMap`
25. `GetMapSt`
26. `GetMapM`
27. `PullMP`
28. `RecoverMap`
29. `IfHaveMap`
30. `GetTrM`
31. `GetTr`
32. `GetMapSet`
33. `PullM`
34. `DelM`
35. `RenameM`
36. `AddM`
37. `SetM`
38. `GetSleepStatus`
39. `GetBlockTime`
40. `SetBlockTime`
41. `GetOnOff`
42. `SetOnOff`
43. `SetWaterPermeability`
44. `GetWaterPermeability`

### B. Decompiled `Clean` Handler (`0x183c0`)
Function signature: `void FUN_000183c0(undefined4 param_1, int param_2)`
Full decompiled logic:
```c
void FUN_000183c0(undefined4 param_1,int param_2) {
  // XML attribute lookup
  local_34 = iks_find(param_1, "clean");
  local_3c = (char *)iks_find_attrib(local_34, "act"); // 's', 'p', 'h', 'r'
  
  if (*local_3c == 's') { // act="s" (Start Clean)
    local_1c = 0;
    local_38 = (char *)iks_find_attrib(local_34, "type");
    if (strcmp(local_38, "auto") == 0) {
      local_18 = 0; // Task Type 0: Auto Clean
    }
    else if (strcmp(local_38, "spot") == 0) {
      local_18 = 1; // Task Type 1: Spot Clean (Single Point)
      local_40 = (char *)iks_find_attrib(local_34, "p");
      local_44 = strtok(local_40, ",");
      // parses x,y coordinates
    }
    else if (strcmp(local_38, "SpotArea") == 0) {
      local_48 = (char *)iks_find_attrib(local_34, "mid");
      local_4c = (char *)iks_find_attrib(local_34, "p");
      
      if (local_48 == NULL && local_4c != NULL) {
        // Task Type 9: Custom Area / Zone Clean
        local_18 = 9;
        local_50 = strtok(local_4c, ","); // x1
        local_54 = strtok(NULL, ",");     // y1
        local_58 = strtok(NULL, ",");     // x2
        local_5c = strtok(NULL, ",");     // y2
        // parses deep (clean passes: 1 or 2)
      }
      else if (local_48 != NULL && local_4c == NULL) {
        // Task Type 2: Spot Area Clean (Rooms)
        local_18 = 2;
        local_24 = strtok(local_48, ",");
        while (local_24 != NULL) {
          aiStack_e8[local_6c++] = atoi(local_24);
          local_24 = strtok(NULL, ",");
        }
      }
      else {
        // FATAL: Both mid and p present!
        fprintf(stdout, "[M][%s:%d] spotArea has mid and p\n", "mds_wifi_cmd_handler.c", 0x550);
        local_14 = 1; // Rejection error
      }
    }
  }
  else if (*local_3c == 'r') {
    local_1c = 3; // Resume clean
  }
  else if (*local_3c == 'p') {
    local_1c = 2; // Pause clean
  }
  else if (*local_3c == 'h') {
    local_1c = 1; // Halt / Stop clean
  }
  
  // Dispatch task to plug_mmi_periph
  if (local_14 == 0) {
    FUN_00013f10(*(undefined4 *)(local_28 + 0x48), local_18, local_1c, local_20, &local_68);
  }
}
```

### C. Decompiled `Move` Handler (`0x165d0`)
Incoming command: `<ctl td="Move" action="..."/>`
Allowed `action` parameters:
- `forward`
- `backward`
- `TurnAround`
- `SpinLeft`
- `SpinRight`
- `stop`
- `brake`

`Move` passes directly to `MdsStartRemoteControlTask` (`case 8`). It **only** executes raw directional motion. It possesses no SLAM path planning or target coordinate routing capabilities.

### D. Fan Speed Hardcoding (`0x18964` & `0x1d1a4`)
In `HandleCustomCleanReport` (`0x1d1a4`):
```c
local_20 = iks_insert(local_18, "clean");
iks_insert_attrib(local_20, "type", "auto");
iks_insert_attrib(local_20, "speed", "standard"); // Hardcoded string literal
```
In `GetCleanState` (`0x18964`):
```c
local_24 = iks_insert(local_20, "clean");
iks_insert_attrib(local_24, "type", acStack_1b4);
iks_insert_attrib(local_24, "speed", "standard"); // Hardcoded string literal
```

---

## 3. Firmware Analysis: Task Manager & Motor Control (`plug_mmi_periph.so`)

### A. Decompiled `MdsStartTask` (`0x28bdc`)
```c
int MdsStartTask(int param_1, undefined4 param_2, int param_3, undefined4 param_4) {
  int local_c;
  
  switch(param_3) {
  case 0: // Auto Clean
    local_c = MdsStartAutoTask(param_1);
    if (local_c != 0) return local_c;
    MdsStartRobotPreparation(param_1);
    FUN_00023248(param_1, 0x13);
    MdsStartWorkLight(param_1);
    break;
  case 1: // Spot Clean (Point)
    local_c = MdsStartSpotCleanTask(param_1, param_4);
    if (local_c != 0) return local_c;
    MdsStartRobotPreparation(param_1);
    FUN_00023248(param_1, 0x15);
    MdsStartWorkLight(param_1);
    break;
  case 2: // Area Clean (Rooms)
    local_c = MdsStartAreaCleanTask(param_1, param_4);
    if (local_c != 0) return local_c;
    MdsStartRobotPreparation(param_1);
    FUN_00023248(param_1, 0x14);
    MdsStartWorkLight(param_1);
    break;
  case 3: // Return to Dock (Charge)
    if (*(int *)(*(int *)(param_1 + 0x3c) + 0x2c) == 3) {
      MdsStartSimpleReturnTask(param_1);
    } else {
      if (*(int *)(param_1 + 400) == 0) {
        MdsStartExploreReturnTask(param_1);
      } else {
        MdsStartReturnTask(param_1, 1, param_4);
      }
    }
    MdsStartRobotPreparation(param_1);
    break;
  case 8: // Remote Control Manual Drive
    MdsStartRemoteControlTask(param_1, param_4);
    MdsStartRobotPreparation(param_1);
    break;
  case 9: // Custom Zone Clean
    local_c = MdsStartCustomCleanTask(param_1, param_4);
    if (local_c != 0) return local_c;
    MdsStartRobotPreparation(param_1);
    FUN_00023248(param_1, 0x14);
    MdsStartWorkLight(param_1);
    break;
  }
  
  local_c = MdsTaskManagerExecuteNextTask(param_1);
  MDSMotorControl(param_1); // ALWAYS EVALUATES MOTOR STATE AFTER STARTING TASK
  return local_c;
}
```

### B. Decompiled `MDSMotorControl` (`0x29724`)
```c
undefined4 MDSMotorControl(int param_1) {
  int *piVar1; // Main task struct
  int *piVar2; // Sub task struct
  undefined4 uVar3;
  
  piVar1 = (int *)getCurrentExecuteMainTask(*(undefined4 *)(param_1 + 0x30));
  piVar2 = (int *)getCurrentExecuteSubTask(piVar1);
  
  if (piVar1 == NULL) {
    uVar3 = FUN_00021ed4(param_1, 2); // Motors OFF (Idle)
  }
  else if (*(int *)(*piVar2 + 0x28) == 1) {
    // Task Type 0 (Auto), 1 (Spot), 2 (Area), 9 (Custom Zone)
    if (*(int *)(*piVar1 + 0x2c) == 0) {
      if (*(int *)(param_1 + 0x178) == 0) {
        uVar3 = FUN_00021ed4(param_1, 0); // Mode 0: STANDARD CLEAN (11,000 RPM)
      } else {
        uVar3 = FUN_00021ed4(param_1, 3); // Mode 3: CARPET BOOST (15,000 RPM)
      }
    }
    // Task Type 3: RETURN TO BASE / CHARGE
    else if (*(int *)(*piVar1 + 0x2c) == 3) {
      uVar3 = FUN_00021ed4(param_1, 1);   // Mode 1: RETURN MOTOR OFF (SILENT TRANSIT)
    }
    // Task Type 8: REMOTE CONTROL
    else if (*(int *)(*piVar1 + 0x2c) == 8) {
      uVar3 = FUN_00021ed4(param_1, 2);   // Mode 2: IDLE MOTOR OFF
    }
    else {
      uVar3 = FUN_00021ed4(param_1, 2);
    }
  }
  return uVar3;
}
```

### C. Motor Dispatch `FUN_00021ed4` (`0x21ed4`)
```c
undefined4 FUN_00021ed4(undefined4 param_1, undefined4 param_2) {
  undefined4 local_18 = param_2; // Motor mode (0=Clean, 1=Return, 2=Off, 3=Boost, 4=MopOnly)
  undefined4 local_14 = param_1;
  undefined4 local_10 = 0;
  return MDSElemCastMsg(param_1, "MOTOR", &local_10);
}
```

### D. Motor Baseline & Carpet Boost Speeds (`0x2e6e8`)
- Mode 0 (Standard Cleaning): `*(undefined2 *)(param_1 + 0x32) = 11000;` (11,000 RPM baseline)
- Mode 3 (Carpet Boost): `*(undefined2 *)(param_1 + 0x32) = 15000;` (15,000 RPM high boost)

---

## 4. Hardware & Peripheral Bus Architecture

### A. Hardware Layout & Linux System
- **SoC**: Rockchip RK3188 (Quad-Core Cortex-A9 ARMv7).
- **OS**: Linux 3.0.36+ (Custom Ecovacs Build).
- **Filesystem**: SquashFS root partition with read-write `/data` ext4 partition.
- **Main Daemon**: `/usr/bin/medusa` configured via `/etc/conf/medusa/deebot_dr930.conf`.

### B. Serial UART Mapping (`deebot_dr930.conf`)
- `/dev/ttyS0` (115200 baud, 8N1): **MMIV2 Protocol**. Connects to the auxiliary microcontroller (STM32) governing motor drivers (wheel, main brush, side brush, suction fan), battery ADC, bumper, cliff sensors, and physical top button.
- `/dev/ttyS1` (230400 baud, 8N1): **LDS UART**. Direct data stream from the 360° laser distance sensor (Lidar).
- `/dev/ttyFIQ0` (115200 baud): RK3188 fast interrupt hardware serial console (Linux getty shell).

---

## 5. Official Android App Reference (`com.eco.global.app`)

### A. DR930 Model Logic (`DR930.java`)
In `sources/com/ecovacs/lib_iot_client/robot/DR930.java`:
- Lines 630-670 define `Clean(...)`:
  - Enforces `cleanType == CleanType.SPOT_AREA` requires `mids` OR `position`.
  - Specifically throws runtime exception if both are provided: `"SpotArea Clean must appoint area's mids or position,choice one"`.
  - Spot Clean requires single point: `"Spot Clean must appoint point"`.
- There is **no** Go-To / point navigation function in `DR930.java`. The only top-level actions supported are Auto Clean, Area Clean (Rooms), Custom Clean (Zones), and Return to Dock (`Charge`).

### B. Protocol XML Generator (`EcoRobotProtocol.java`)
In `sources/com/ecovacs/lib_iot_client/robot/protocol/EcoRobotProtocol.java`:
- `Clean_Protocol`: formats `<clean act="s" type="SpotArea" mid="..." deep="..."/>` or `<clean act="s" type="SpotArea" p="x1,y1,x2,y2" deep="..."/>`.
- Confirms the absence of any `speed` or `suction` attributes in the client command stanzas.

---

## 6. Why "It's Still Cleaning" Occurs on Go-To Navigation

### A. The Firmware Invariant
When a user drops a "Go-To" pin on the map:
1. Valetudo generates target coordinates `(x, y)`.
2. Because the OZMO 930 firmware has no native `GoTo` command, the bridge dispatches a small 500x500mm custom clean zone centered at `(x, y)`:
   `<clean act="s" type="SpotArea" p="x1,y1,x2,y2" deep="1"/>`
3. As proven by `MdsStartTask` (`0x28bdc`) and `MDSMotorControl` (`0x29724`), the robot's firmware **unconditionally engages the suction motor and brush motor** for any custom clean task.
4. Hence, while driving to the target coordinate, the robot is **physically vacuuming** until it either arrives and is halted or finishes the zone.

### B. Practical Solution & Dual-Mode Configuration
To satisfy user expectations in Home Assistant Lovelace (`xiaomi-vacuum-map-card`):
1. **Mode A: Spot Clean (Pin & Clean)**:
   - Places a target pin and explicitly runs a 1000mm x 1000mm localized clean. The user expects vacuuming to occur.
2. **Mode B: Go-To Location (Transit & Park)**:
   - Places a target pin.
   - The bridge commands transit via Custom Area.
   - A background arrival watcher tracks physical robot coordinates (`RobotPosX`, `RobotPosY`).
   - The moment Euclidean distance `dist <= 400mm`, the bridge immediately executes `<clean act="h"/>` (`/clean?act=stop`), halting the robot in place so it parks at the destination.

---

## 7. Audio System & Custom Sound Engineering

### A. Audio Codec & Hardware DAC Delay
- **Format**: Ogg Vorbis, 16,000 Hz sample rate, 1 channel (Mono), 48 kbps bitrate.
- **Hardware Quirk**: The audio DAC/amplifier on the Rockchip RK3188 has a hardware power-gate wake-up latency of ~150-250 ms.
- **Mitigation**: All custom sounds **must** have 350 ms of silence prepended (`-af "adelay=350:all=1"`) to prevent the first syllable from being clipped.

### B. Custom Audio Soundboard Table
| Sound ID | Filename | Source / Description | Padding Applied | Bind-Mount Target |
|---|---|---|---|---|
| `98` | `do-not-come.ogg` | "Do not come" (Kamala Harris voice clip) | 350ms lead silence | `/media/music/EN/98.ogg` |
| `99` | `im-gunna-come.ogg` | "I'm gonna come" (Trump voice clip) | 350ms lead silence | `/media/music/EN/99.ogg` |

### C. Robot Filesystem Persistence
Because root `/media/music/EN/` is on the read-only SquashFS partition, custom audio is staged on the persistent `/data` partition (`/data/music/EN/`) and bind-mounted at boot time via `/data/autostart/20-custom-sounds.sh`:
```bash
#!/bin/sh
if [ -f /data/music/EN/98.ogg ]; then
    mount -o bind /data/music/EN/98.ogg /media/music/EN/98.ogg
fi
if [ -f /data/music/EN/99.ogg ]; then
    mount -o bind /data/music/EN/99.ogg /media/music/EN/99.ogg
fi
```

---

## 8. Firmware Interception: Silent Go-To Transit (`libsilentgoto.so`)

### A. Architectural Hook Strategy
Because OZMO 930 SLAM navigation cannot be decoupled from `MdsStartCustomCleanTask` at the high-level XML layer, silent point-to-point transit is achieved by preloading `libsilentgoto.so` into `/usr/bin/medusa`. When `/tmp/silent_goto` exists on the robot filesystem, `libsilentgoto.so` intercepts three critical dispatch points:

1. **Voice Prompt Suppression (`MDSElemCastMsg` @ `/usr/bin/medusa`)**:
   - In `FUN_00023248` (`0x23248` in `plug_mmi_periph.so`), task startup triggers:
     `MDSElemCastMsg(ctx, "PLAYER", &local_10)` with payload `{ 1, &audio_prompt_id }`.
   - Task 9 (Custom Clean) passes prompt ID `0x14` (20: "I am starting to clean").
   - Intercepting `MDSElemCastMsg` for `param_2 == "PLAYER"` and returning `0` completely drops the voice announcement.

2. **Motor Mode Override (`MDSElemCastMsg` @ `/usr/bin/medusa`)**:
   - In `FUN_00021ed4` (`0x21ed4` in `plug_mmi_periph.so`), motor state changes trigger:
     `MDSElemCastMsg(ctx, "MOTOR", &local_10)` where `param_3` is `&local_10`:
     - `((uint32_t *)param_3)[0] == 0`: Subcommand 0 (Set Motor Mode).
     - `((int **)param_3)[1]`: Pointer to `mode` variable (`int *`).
   - Mode definitions:
     - `0`: Standard Clean (11,000 RPM baseline suction).
     - `1`: Return to dock (0 RPM suction, 50% side brush).
     - `2`: Idle (0 RPM suction, 0 RPM main brush, 0 RPM side brush, 0 pump).
     - `3`: Carpet Pressurization Boost (15,000 RPM suction).
   - Hook dereferences `((int **)param_3)[1]` and overrides `*mode = 2` (Idle).

3. **Direct Speed Suppression (`MdsMotorControl` & `MdsMmiMotorElemCastMotorSetMmiMsg`)**:
   - `MdsMotorControl` (`0x2e6e8` / `0x1e6e9` in `plug_mmi_periph.so`) evaluates `*(int *)(ctx + 0x3c)`:
     Case 2 sets registers:
     `*(uint16_t *)(ctx + 0x2a) = 0` (main brush)
     `*(uint16_t *)(ctx + 0x2e) = 0` (side brush)
     `*(uint16_t *)(ctx + 0x32) = 0` (suction)
     `*(uint16_t *)(ctx + 0x36) = 0` (water pump)
     `*(uint8_t *)(ctx + 0x35) = 2`
   - `MdsMmiMotorElemCastMotorSetMmiMsg` (`0x2e528` / `0x1e529` in `plug_mmi_periph.so`) formats the 18-byte `'MA'` packet:
     `local_24 = 0x4d` ('M'), `uStack_23 = 0x41` ('A'), followed by the 16-bit motor targets, and transmits over `/dev/ttyS0` to the STM32 microcontroller.
   - Hooking both ensures that even if internal routines attempt to set non-zero speed, all cleaning motor speeds remain strictly 0 RPM while wheel driving motors navigate.

### B. Robot Persistence & Preload Mechanism
SquashFS root `/etc/rc.d/medusa.sh` launches `medusa`. Persistent injection is achieved via `/data`:
1. `/data/rc.d/medusa.sh`: Wrapper script executing `LD_PRELOAD=/data/libsilentgoto.so medusa -f /etc/conf/medusa/deebot_dr930.conf &`.
2. `/data/autostart/30-medusa-preload.sh`: Boot script bind-mounting `/data/rc.d/medusa.sh` over `/etc/rc.d/medusa.sh`.


---

## 9. SLAM Navigation Engine & Point Transit Analysis (`TASK_GOPT`)

### A. Element Registration & Callbacks (`plug_medusa_plugin_task_ergodic.so`)
The SLAM coverage and ergodic navigation plugin registers `MdsTaskGoPtElemClass` (`0x4f128`):
- Element Name: `"TASK_GOPT"` (`0x41bc0`)
- Source file: `src/medusa_plugin_task_ergodic/mds_task_goto_point.c`
- Message handler: `FUN_0002b330` (`0x1b330`)
- Subtask init dispatcher: `FUN_0002af60` (`0x1af60`)

### B. Decompiled `TASK_GOPT` Start Routine (`0x1af60`)
```c
undefined4 FUN_0002af60(int param_1, int *param_2) {
  adjustSpeed(0, 0);
  MdsTaskUpdateAdapterParameters(param_1, 1);
  if (*param_2 == 0) {
    local_c = (undefined4 *)param_2[1];
    *(undefined4 *)(param_1 + 0xb4) = *local_c;
    *(undefined4 *)(param_1 + 0xbc) = 0;
    switch(*local_c) {
    case 0: // Single-point transit (Spot clean navigation)
      local_10 = (undefined4 *)local_c[1];
      erg_setTwoTimesFlagDone(0);
      erg_spotLocationRequestWithPoint(*local_10, local_10[1], 0, 0);
      uVar2 = erg_getSpotLocationP();
      *(undefined4 *)(param_1 + 0xb8) = uVar2;
      uVar2 = erg_getWorkingStatesP();
      *(undefined4 *)(param_1 + 0xbc) = uVar2;
      local_20 = 0;
      FUN_0002a444(param_1, &local_20);
      break;
    case 1: // Charger front transit (Return to dock)
      erg_setGotoOriginalTestTypeByValue(0);
      erg_setInitTimes_gotoOriginalLocationInit(0);
      erg_setTwoTimesFlagDone(0);
      local_14 = (undefined4 *)local_c[1];
      puVar1 = (undefined4 *)erg_getChargerFrontPose();
      *puVar1 = *local_14;
      puVar1[1] = local_14[1];
      puVar1[2] = local_14[2];
      erg_gotoOriginalLocationInit();
      uVar2 = erg_getChargerFrontPose();
      *(undefined4 *)(param_1 + 0xb8) = uVar2;
      uVar2 = erg_getWorkingStatesP();
      *(undefined4 *)(param_1 + 0xbc) = uVar2;
      break;
    case 2: // Charger transit with virtual walls
      erg_updateGridMapAndReducedMapWithVirtualWall();
      erg_gotoOriginalLocationInit();
      break;
    case 3: // Explore return
      erg_gotoOriginalLocationInitForExplore();
      break;
    }
    // Starts navigation thread and periodic 20ms trajectory timer
    CFTimerModTime(*(undefined4 *)(param_1 + 0xa4), 0, 20000000, 0, 20000000);
  }
}
```

### C. Why "Moving Like It's Vacuuming" Occurred at Destination
1. The DR930 firmware does not support a dedicated "Go-To" command from the mobile app; it only exposes `auto`, `spot`, `SpotArea` (mid), and `SpotArea` (p) (Custom Clean `TaskType 9`).
2. When the bridge initiates Go-To via a 500x500mm custom clean zone:
   - **Phase 1 (Transit)**: Path planning drives the robot to the zone coordinate boundary. `libsilentgoto.so` ensures this transit is silent.
   - **Phase 2 (Ergodic Sweep)**: Upon entering the zone bounding box, the firmware transitions from transit to area coverage, executing zig-zag back-and-forth S-curves inside the 500x500mm box.
3. In `valetudo-bridge`, the arrival watcher was checking:
   `if st.CleanSpeed == "stop" || st.CleanSpeed == "idle" || st.ChargingState == "SlotCharging"`
   Because the robot takes 4–6 seconds to leave the charging contacts and begin rolling, `st.ChargingState` was still `"SlotCharging"` on the watcher's very first tick (at 2.5s). The watcher immediately hit `return` and exited, never tracked distance, and never issued `/clean?act=stop`.
4. **Resolution**:
   - The watcher now incorporates `hasStartedMoving`: it waits up to 12s for the robot to undock before evaluating stop conditions.
   - Upon arriving within 450mm of the target coordinate, the bridge immediately calls `/clean?act=stop` (halts the clean task) AND `/move?act=stop` (sends `<ctl td="Move" action="stop"/>` to freeze STM32 wheel drivers immediately).

---

## 10. Dock Localization & SLAM Stale Coordinate Mechanics

### A. Binary Structure of Stored Charger Stand Files
The DR930 Linux filesystem stores the dock reference in `/data/FILES/`:
- `/data/FILES/restorePose.bin`: 12 bytes (`<fff`, Little-Endian IEEE 754):
  - `float32 x` (Robot docked X position in mm)
  - `float32 y` (Robot docked Y position in mm)
  - `float32 theta` (Robot orientation in radians)
- `/data/FILES/restoreChargerStand.bin`: 12 bytes (`<fff`):
  - `float32 x` (Infrared charger base station X in mm)
  - `float32 y` (Infrared charger base station Y in mm)
  - `float32 theta` (Charger base orientation in radians)

### B. Root Cause of Docking Failure (`179, 52` vs `179, 133`)
1. On initial map generation, the dock position was recorded at `(-1486.3 mm, -3396.0 mm)` $\rightarrow$ grid pixel `(179, 52)`.
2. When the physical charging dock was moved to `(179, 133)` (4.05 meters away along the same wall), the firmware never updated `restoreChargerStand.bin` because `rst_storeChargerStand` is only invoked during full map creation.
3. When `return_to_base` (`/charge`) is called, `MdsStartReturnTask` navigates the robot to the stale flash coordinates `(179, 52)`. Unable to find the infrared beacon at the phantom position, the robot times out and switches to `Idle`.
4. **Calibration**:
   - Updating `/data/FILES/restorePose.bin` and `/data/FILES/restoreChargerStand.bin` with `y = +700.0 mm` directly calibrates the dock coordinates on the robot filesystem to `(179, 133)`.
   - The controller and bridge also support dynamic environment variable overrides (`ROBOT_DOCK_PIXEL_Y=133`).
