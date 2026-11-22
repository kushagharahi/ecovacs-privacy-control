#!/bin/sh
#
# start/stop medusa with LD_PRELOAD firmware patches
#
# Injected patches:
# - libpersistmap.so: Traps relocation failure and prevents catastrophic map-wiping; true RecoverMap
# - libsilentgoto.so: Silences suction motors during point-to-point transit
#

stop_script(){
	PID_WK=`pidof mds_wukong_get_best_iot_svr.sh`
	if [ $? == 0 ]
	then
		kill $PID_WK
		sleep 0.5
	fi	
}

load () {
	LD_PRELOAD="/data/libpersistmap.so /data/libsilentgoto.so" medusa -f /etc/conf/medusa/deebot_dr930.conf &
}

unload () {
	stop_script
    kill -SIGTERM `cat /tmp/medusa.pid 2>/dev/null` 2>/dev/null
    killall wget 2>/dev/null
}

case "$1" in
      start)
            load
             ;;
       stop)
            unload
             ;;
       restart)
            unload          
            load
             ;;
        *)
             echo "$0 <start/stop/restart>"
             ;;
esac
