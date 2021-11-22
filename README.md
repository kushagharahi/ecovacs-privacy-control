# ecovacs-privacy-control
Privacy first API control for Ecovacs MQTT based vaccums

Goal: Control Ecovacs MQTT based vaccum robots directly with a self-hosted local API. 

# Technicials

Once setup, MQTT based Ecovacs robot vaccums directly connect to `mq-ww.ecouser.net`:8883 MQTT server. With a proper DNS setup and a self signed cert, users can redirect bot MQTT traffic to a self-hosted MQTT server

# Limitations

WIFI credentials must be setup on the robot with the Ecovacs app. Reverse engineering is required here.