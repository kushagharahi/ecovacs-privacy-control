#!/bin/sh
case "$1" in
  stop)
    killall telnetd 2>/dev/null
    ;;
  *)
    killall telnetd 2>/dev/null
    /usr/sbin/telnetd -l /bin/sh -b 0.0.0.0 &
    /usr/sbin/telnetd -p 2323 -l /bin/sh -b 0.0.0.0 &
    ;;
esac
