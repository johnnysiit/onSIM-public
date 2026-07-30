SKIPUNZIP=0

ui_print "- Installing onSIM privileged Android gateway"

MIXER_SOURCE=/vendor/etc/mixer_paths_tasha.xml
MIXER_TARGET="$MODPATH/system/vendor/etc/mixer_paths_tasha.xml"
if [ -f "$MIXER_SOURCE" ]; then
  ui_print "- Installing OnePlus 5 in-call music uplink routes"
  mkdir -p "${MIXER_TARGET%/*}"
  cp "$MIXER_SOURCE" "$MIXER_TARGET"
  if ! sed -n '/<path name="afe-proxy-playback afe-proxy">/,/<\/path>/p' "$MIXER_TARGET" |
      grep -q 'Incall_Music Audio Mixer MultiMedia2'; then
    sed -i '/<path name="afe-proxy-playback afe-proxy">/a\
        <ctl name="Incall_Music Audio Mixer MultiMedia2" value="1" />' "$MIXER_TARGET"
  fi
  if ! grep -q '<path name="incall_music_uplink">' "$MIXER_TARGET"; then
    sed -i '/<\/mixer>/i\
    <!-- onSIM: Qualcomm telephony uplink routes omitted by HydrogenOS tasha config -->\
    <path name="afe-proxy-playback afe-proxy">\
    </path>\
\
    <path name="incall_music_uplink">\
        <ctl name="Incall_Music Audio Mixer MultiMedia2" value="1" />\
    </path>\
\
    <path name="voice-tx">\
    </path>\
' "$MIXER_TARGET"
  fi
  if ! grep -q '<path name="afe-proxy-playback">' "$MIXER_TARGET"; then
    sed -i '/<\/mixer>/i\
    <path name="afe-proxy-playback">\
    </path>\
' "$MIXER_TARGET"
  fi
  grep -q '<path name="incall_music_uplink">' "$MIXER_TARGET" ||
    abort "! Could not patch mixer_paths_tasha.xml"
  set_perm "$MIXER_TARGET" 0 0 0644
else
  ui_print "! mixer_paths_tasha.xml not present; audio uplink route was not installed"
fi

# Android caches parsed metadata for persistent system apps. Invalidate only
# that regenerable cache so an updated module APK is recognized after reboot.
if [ -d /data/system/package_cache ]; then
  ui_print "- Refreshing Android system package metadata"
  rm -rf /data/system/package_cache/*
fi

set_perm_recursive "$MODPATH/system/priv-app/OnSIMGW" 0 0 0755 0644
set_perm "$MODPATH/system/etc/permissions/privapp-permissions-onsim.xml" 0 0 0644
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/uplink-service.sh" 0 0 0755
set_perm "$MODPATH/uplink-handler.sh" 0 0 0755
set_perm "$MODPATH/system.prop" 0 0 0644
