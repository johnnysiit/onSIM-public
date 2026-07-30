#!/system/bin/sh

MODDIR=${0%/*}
PKG=io.onsim.gateway

until [ "$(getprop sys.boot_completed)" = "1" ]; do
  sleep 2
done

"$MODDIR/uplink-service.sh" >/data/local/tmp/onsim-uplink.log 2>&1 &

cmd role add-role-holder --user 0 android.app.role.DIALER "$PKG" 0 >/dev/null 2>&1
cmd role add-role-holder --user 0 android.app.role.SMS "$PKG" 0 >/dev/null 2>&1
pm grant "$PKG" android.permission.CALL_PHONE >/dev/null 2>&1
pm grant "$PKG" android.permission.ANSWER_PHONE_CALLS >/dev/null 2>&1
pm grant "$PKG" android.permission.READ_PHONE_STATE >/dev/null 2>&1
pm grant "$PKG" android.permission.READ_PHONE_NUMBERS >/dev/null 2>&1
pm grant "$PKG" android.permission.READ_CALL_LOG >/dev/null 2>&1
pm grant "$PKG" android.permission.WRITE_CALL_LOG >/dev/null 2>&1
pm grant "$PKG" android.permission.SEND_SMS >/dev/null 2>&1
pm grant "$PKG" android.permission.RECEIVE_SMS >/dev/null 2>&1
pm grant "$PKG" android.permission.READ_SMS >/dev/null 2>&1
pm grant "$PKG" android.permission.RECORD_AUDIO >/dev/null 2>&1
cmd deviceidle whitelist +"$PKG" >/dev/null 2>&1
am start -n "$PKG/.GatewayActivity" >/dev/null 2>&1
