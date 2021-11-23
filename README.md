# ecovacs-privacy-control
Privacy first API control for Ecovacs MQTT based vaccums -- tested with Ecovacs Deebot 900/901 series _only_

Goal: Control Ecovacs MQTT based vaccum robots directly with a self-hosted local API.

## Usage

- Setup WIFI credentials on robot with Ecovacs app
- Build/Run Docker container
- Point DNS for `ecouser.net` to server running container
- Restart robot -- Robot caches DNS response and to get a refreshed DNS you must restart

If the bot is successful in connecting, you should see something like this in the logs:

```
1637629641: New connection from <snip> on port 8883.
1637629641: New client connected from <snip> as <snip>-<snip>-<snip>-<snip>-<snip>@ls1ok3/<snip> (p2, c1, k120, u'<snip>').
```

## Technicials

Once setup, MQTT based Ecovacs robot vaccums directly connect to `mq-ww.ecouser.net`:8883 MQTT server. With a proper DNS setup and a self signed cert for `ecouser.net`, users can redirect bot MQTT traffic to a self-hosted MQTT server

## Limitations

WIFI credentials must be setup on the robot with the Ecovacs app. Reverse engineering is required here. To avoid data leakage, internet data can be disabled while setting up bot.