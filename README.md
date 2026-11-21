# ecovacs-privacy-control
Privacy first API control for Ecovacs MQTT based vaccums -- tested with Ecovacs ~~Deebot 900/901 series and~~ OZMO 930 _only_

Goal: Control Ecovacs MQTT based vaccum robots directly with a self-hosted local API that runs completely on the robot, no external services/server needed.

> [!WARNING]
> There is a lot of AI slop in this repo. Take a backup of your robots data before you start messing with it <insert how to do the backup>
> I've personally tested most of everything in here with my 930 OZMO but it's not well reviewed and very much vibe coded.


As it is now, you can run the controller and the bridge and HA will pick up the vacuum. You can setup map control with https://github.com/PiotrMachowski/lovelace-xiaomi-vacuum-map-card/ instructions to come

## Technicials

A bunch (all?) of Ecovacs bots do implement do certificate pinning, so they are vulnerable to MITM attacks and establishing your own local MQTT control.

Once setup, MQTT based Ecovacs robot vaccums directly connect to `mq-ww.ecouser.net`:8883 MQTT server. With a proper DNS setup and a self signed cert for `ecouser.net`, users can redirect bot MQTT traffic to a self-hosted MQTT server

ecovacs-privacy-control is a docker container that generates self-signed certificates for `ecouser.net` and launches Mosquitto (MQTT broker). In the future, the goal is to root ecovacs robots and directly communicate with them.

## Usage

- Setup WIFI credentials on robot
  - Hold reset button down robot until you here beep or prompt asking to you go to the app
  - Connect to robot WIFI
  - Send a REST request with `s` as the SSID you want the robot to connect to and `p` being the WIFI password for the SSID
  - `curl -X POST -v \
  http://192.168.0.1:8888/rcp.do \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: Dalvik/2.1.0 (Linux; U; Android 14; Android SDK built for arm64 Build/UE1A.230829.036.A1)' \
  -H 'Connection: Keep-Alive' \
  -H 'Accept-Encoding: gzip' \
  -d '{"td":"SetApConfig","s":"<SSID_HERE>","p":"<PASSWORD_HERE>","u":"ia1a0bodd640d6a2","sc":"b0"}'`
- Build/Run Docker container - Must expose container port 8883
- Point DNS for `ecouser.net` to server running container
- Restart robot -- Robot caches DNS response and to get a refreshed DNS you must restart

If the bot is successful in connecting, you should see something like this in the logs:

```
1637629641: New connection from <snip> on port 8883.
1637629641: New client connected from <snip> as <snip>-<snip>-<snip>-<snip>-<snip>@ls1ok3/<snip> (p2, c1, k120, u'<snip>').
```
## API

`:8000/getMapData` will return the stored map

![map data](/getMapData.jpg)

## TODO:

- Create MQTT Broker with TLS config needed for bot to connect to local server ✅
- Connect robot to WIFI without app ✅
- Document ~~MQTT~~ xmpp message schema/contents for sending bot commands ✅
- Document ~~MQTT~~ xmpp message schema/content for receiving bot info ✅
- Create local API to send/read MQTT messages ✅
- Gain root access ✅
- Fix pause button
- Ability to add rooms from HA
- Drive robot around without cleaning
- Run controller and HA mqtt bridge on robot itself, not requiring a server at all
- Strip off ecovacs phone homes (is this even worth doing?)
- Xiaomi map lovelace auto create map areas https://github.com/PiotrMachowski/lovelace-xiaomi-vacuum-map-card/ 


## Acknowledgements

This work is done on the shoulders of giants. Huge shout out to:
- Bumper: https://github.com/bmartin5692/bumper - Amazing documentation around doing a MITM attack to gain self hosting
- Dennis Giese: https://dontvacuum.me/talks/37c3-2023/37c3-vacuuming-and-mowing.html - Great talk about serial vulnerabilities of ecovacs robots