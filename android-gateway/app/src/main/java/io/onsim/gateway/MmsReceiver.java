package io.onsim.gateway;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

public class MmsReceiver extends BroadcastReceiver {
    @Override public void onReceive(Context context, Intent intent) {
        GatewayService.emitEvent("mms.received", "", "", "", "", "MMS_NOT_SUPPORTED");
    }
}
