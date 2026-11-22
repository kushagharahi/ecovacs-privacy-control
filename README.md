# ecovacs-privacy-control
Privacy first API control for Ecovacs xmpp based vacs -- tested with Ecovacs ~~Deebot 900/901 series and~~ OZMO 930 _only_

Goal: Control Ecovacs MQTT based vaccum robots directly with a self-hosted local API that runs completely on the robot, no external services/server needed.

> [!WARNING]
> There is a lot of AI slop in this repo. Take a backup of your robot's data partition before you start messing with it:
> ```bash
> # Once rooted via Telnet:
> tar -czvf /tmp/data_backup.tar.gz /data
> # Or copy off to a connected USB OTG drive:
> tar -czvf /udisk/data_backup.tar.gz /data
> ```
> I've personally tested most of everything in here with my 930 OZMO, but it's not well reviewed and very much vibe coded.

As it is now, you can run the controller (`api_client`) and the Home Assistant bridge (`cmd/valetudo-bridge`) and Home Assistant will pick up the vacuum automatically via MQTT Discovery. You can set up map control with standard Home Assistant camera cards or the [lovelace-xiaomi-vacuum-map-card](https://github.com/PiotrMachowski/lovelace-xiaomi-vacuum-map-card/) (see [docs/ha_valetudo_integration.md](docs/ha_valetudo_integration.md)).

Eventually this will be all entirely enclosed in the robot and HA will connect to the services hosted within the robot.

## Root Access & Persistence (OZMO 930)

Persistent root access on the OZMO 930 can be gained without disassembling the robot or soldering UART connections:

1. **Micro-USB OTG Auto-Execution Vulnerability**:
   - The udev rules on the robot trigger `/usr/bin/udisk.sh` whenever a USB storage drive is connected.
   - `/usr/bin/udisk.sh` looks for `/dr930/dr930.sh` on FAT32 USB media and executes it directly as `root` (UID 0).
2. **Automated Exploit Payload (`root_payload/`)**:
   - Format a USB drive as FAT32.
   - Copy `root_payload/autorun.sh` to the root of the USB drive, and copy `root_payload/dr930/dr930.sh` into a `/dr930/` folder on the USB drive.
   - Plug the drive into the micro-USB service port under the top lid and turn the robot on.
   - The payload installs a persistent root telnet daemon into `/data/autostart/00-root-telnet.sh` (which `/etc/rc.sysinit` automatically runs on every boot).
   - Telnet opens immediately on ports `23` and `2323` (passwordless root shell). See [root_payload/README.md](root_payload/README.md) for full details.

---

## Quick Start (OZMO 930)

### 1. Provision WiFi & Point Robot to Controller
1. Hold down the reset button under the robot's top cover until it chimes and announces it is in pairing mode.
2. Connect your computer to the robot's AP network (`ECOVACS_xxxx`).
3. Run the provisioning script, specifying your local WiFi credentials and the IP of the machine running the controller:
   ```bash
   python3 ozmo-930-wifi.py --ssid "<YOUR_SSID>" --passphrase "<YOUR_WIFI_PASSWORD>" --host-ip <HOST_IP>
   ```
4. The robot will join your WiFi network and register `<HOST_IP>` as its discovery target.

### 2. Root the Robot (One-time setup)
1. Prepare a FAT32 USB drive with `root_payload/` as described above.
2. Plug it into the robot's micro-USB port and power-cycle.
3. Verify telnet access: `telnet <ROBOT_IP> 23`.

### 3. Run the Controller & Bridge
Run the controller:
```bash
cd api_client
go run .
```

Run the Home Assistant MQTT bridge:
```bash
cd cmd/valetudo-bridge
go run . -broker "tcp://<MQTT_BROKER_IP>:1883" -controller "http://localhost:8000"
```

Home Assistant will automatically discover:
- `vacuum.ecovacs_deebot_ozmo_930_...` (State, Clean, Pause, Return to Base, Locate)
- `camera.ecovacs_deebot_ozmo_930_ozmo_map` (Live rendered map)
- Sensors for battery level, main brush, side brushes, and dust filter
- Binary sensor for mopping water tank attachment
- Select entities for water flow rate and voice prompt soundboard
- Volume number slider

---

## Custom Firmware Patches & Deployment

Custom shared libraries inject into `medusa` via `LD_PRELOAD` to fix factory firmware quirks and enable quiet point-to-point transit:
- **Map Saver & Anti-Wipe (`libpersistmap.so`)**: Prevents the robot from deleting its own floor map! In factory Ecovacs firmware, if return-to-dock times out or the robot is picked up, it catastrophically purges the active map from memory, resets coordinates to `(0, 0)`, and blindly wanders into walls. This patch traps relocation timeouts, preserves the active map and coordinates, suppresses blind wandering, and enables true map restoration from storage.
- **Silent Drive / Quiet Navigation (`libsilentgoto.so`)**: Allows you to drive the robot to coordinates without the vacuum suction motor screaming. By default, the OZMO 930 hardware unconditionally runs the vacuum suction motor at full blast (11,000–15,000 RPM) during any autonomous navigation. This patch silences the cleaning motors whenever the robot is executing a "Go to" point-to-point transit command.

### Deploying Patches over Telnet

The deployment script ([robot_patches/deploy.py](robot_patches/deploy.py)) transfers patches directly over the root Telnet connection (port 23), verifies remote checksums, and configures persistent boot loading via bind mount (`mount --bind /data/rc.d/medusa.sh /etc/rc.d/medusa.sh`):

```bash
# Deploy all patches from default directory:
python3 robot_patches/deploy.py --robot-ip <ROBOT_IP>

# Deploy only a specific patch (and compile only that patch):
python3 robot_patches/deploy.py libsilentgoto --build --robot-ip <ROBOT_IP>

# Deploy from any custom patch directory:
python3 robot_patches/deploy.py /path/to/my_patch --robot-ip <ROBOT_IP>

# Check which patches are installed and verified on the robot:
python3 robot_patches/deploy.py --robot-ip <ROBOT_IP> --verify-only

# Deploy and apply immediately (restarts medusa without full reboot):
python3 robot_patches/deploy.py --robot-ip <ROBOT_IP> --restart-medusa
```

---

## API

The controller exposes a local REST API on port `8000`:

- `GET :8000/getMapData`: Returns the current SLAM map JSON.
- `GET :8000/map.png`: Returns a rendered 2D floor plan image with robot and dock positions.
- `POST :8000/clean`: Trigger auto cleaning (`{"act":"s"}`).
- `POST :8000/stop` / `POST :8000/pause` / `POST :8000/resume`
- `POST :8000/charge`: Return to charging dock.
- `POST :8000/spot_area`: Clean specific room segment (`{"mid":"0,1"}`).
- `POST :8000/custom_area`: Clean specific coordinate bounding box (`{"p":"x1,y1,x2,y2"}`).
- `POST :8000/goto`: Point-to-point transit to target coordinate (`{"x":100,"y":120}`).
- `POST :8000/play_sound`: Play built-in voice prompt or custom audio file (`{"name":"71"}` or `{"name":"custom_sound"}`).
- `POST :8000/water_flow`: Set electronic mopping water flow rate (`{"level":"low|medium|high|max"}`).

---

## Verified Protocol Rules (Reverse-Engineered from Firmware)

1. **Room (Segment) Cleaning**:
   - XML: `<clean act="s" type="SpotArea" mid="0,1" deep="1"/>`
   - **Mutual Exclusion**: The `p` attribute **must not** be present. If both `mid` and `p` are sent, the robot firmware aborts with `spotArea has mid and p` and rejects the task.
2. **Zone (Custom Area) Cleaning**:
   - XML: `<clean act="s" type="SpotArea" p="x1,y1,x2,y2" deep="1"/>`
   - Must use `type="SpotArea"`. Do **not** use `type="spot"` (which is single-point clean taking only `x,y` and causes navigation failure / redock).
   - The `mid` attribute **must not** be present.
3. **Audio DAC Latency**:
   - The Rockchip RK3188 audio DAC has ~150-250ms power-gate wake-up latency.
   - All custom audio files must be 16kHz mono Vorbis OGG with 350ms lead-in padding (`-af "adelay=350:all=1"`) to prevent initial syllable cutoff.
4. **Motor Control & Silent Navigation**:
   - Cleaning task types (Auto, Spot, Area, Custom Zone) unconditionally spin up the suction motor (11,000 RPM baseline / 15,000 RPM carpet boost).
   - Silent point-to-point transit is achieved using `libsilentgoto.so`.

---

## TODO:

- [x] Create MQTT Broker with TLS config needed for bot to connect to local server
- [x] Connect robot to WiFi without app (`ozmo-930-wifi.py`)
- [x] Document XMPP message schema & commands
- [x] Document XMPP telemetry & status responses
- [x] Create local API to send/read commands and retrieve maps
- [x] Gain persistent root access via USB OTG exploit (`root_payload`)
- [x] Fix map-wiping on relocation failure & provide map recovery (`libpersistmap.so`)
- [x] Drive robot around without suction motor (`libsilentgoto.so`)
- [x] Home Assistant MQTT Auto-Discovery & Valetudo bridge (`cmd/valetudo-bridge`)
- [x] Dynamic soundboard / custom audio playback support
- [x] Consumable lifespan sensors & mopping water tank detection
- [ ] Run controller and HA MQTT bridge directly on the robot itself (zero external server)
- [ ] Fix pause button quirks during certain navigation states
- [ ] Auto-create interactive map rooms/zones in Lovelace
- [x] One-click deployment script for custom firmware patches (`robot_patches/deploy.py`)
- [ ] Integration tests via firmware emulation in QEMU

---

## Technical Overview

### OZMO 930 (DR930 / DG3G / Class 115)
The OZMO 930 runs an embedded Linux system on a Rockchip RK3188+ SoC (ARMv7 Cortex-A9) managed by the internal `medusa` daemon. Unlike older models that communicate over raw MQTT, the OZMO 930 communicates using **XML over TLS XMPP** with an HTTP discovery service:

1. **WiFi Provisioning (TCP :9876)**:
   - When placed in AP mode (`ECOVACS_xxxx`), the robot listens on TCP port 9876.
   - Using `ozmo-930-wifi.py`, you can send WiFi credentials and point the robot's Load Balancer (`lb`) parameter directly to your self-hosted controller IP (`<HOST_IP>`). **No DNS redirection or certificate poisoning is required** when provisioned this way!
2. **HTTP Discovery Server (HTTP :8007)**:
   - On boot, the robot requests `POST http://<HOST_IP>:8007/lookup.do` to locate the message server (`EcoMsgNew`).
   - The controller replies with `{"result":"ok","ip":"<HOST_IP>","port":5223}`.
3. **TLS XMPP Command Channel (TLS :5223)**:
   - The robot connects over TLS to port 5223.
   - Commands (Clean, SpotArea, Charge, Stop, Sound, etc.) and telemetry (battery, charging state, brush lifespans, map updates) are exchanged via XML `<ctl td="..."/>` stanzas inside XMPP `<iq>` stanzas.

### Legacy Deebot 900 / 901 Series
Older models connect to `mq-ww.ecouser.net:8883` over MQTT. Because they do not enforce certificate pinning, pointing DNS for `ecouser.net` to a local Mosquitto broker running with a self-signed CA allows MITM control. (Historical configs and cert templates remain in `openssl-config/` and `mosquitto.conf`).

---

## Acknowledgements

This work is done on the shoulders of giants. Huge shout out to:
- **Bumper**: https://github.com/bmartin5692/bumper - Amazing documentation around doing a MITM attack to gain self hosting.
- **Dennis Giese**: https://dontvacuum.me/talks/37c3-2023/37c3-vacuuming-and-mowing.html - Great talk and research on serial/firmware vulnerabilities of Ecovacs robots.
