package io.onsim.gateway;

import android.app.Activity;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

public class SmsStatusReceiver extends BroadcastReceiver {
    public static final String SENT = "io.onsim.gateway.SMS_SENT";
    public static final String DELIVERED = "io.onsim.gateway.SMS_DELIVERED";

    @Override public void onReceive(Context context, Intent intent) {
        String clientId = intent.getStringExtra("clientId");
        String providerId = intent.getStringExtra("providerId");
        int subscriptionId = intent.getIntExtra("subscriptionId", -1);
        if (SENT.equals(intent.getAction())) {
            if (getResultCode() == Activity.RESULT_OK) {
                GatewayService.emitEvent("sms.sent", "", "", clientId, providerId, "", subscriptionId);
            } else {
                GatewayService.emitEvent("sms.failed", "", "", clientId, providerId,
                        "ANDROID_SMS_RESULT_" + getResultCode(), subscriptionId);
            }
        } else if (DELIVERED.equals(intent.getAction())) {
            if (getResultCode() == Activity.RESULT_OK) {
                GatewayService.emitEvent("sms.delivered", "", "", clientId, providerId, "", subscriptionId);
            } else {
                GatewayService.emitEvent("sms.failed", "", "", clientId, providerId,
                        "ANDROID_DELIVERY_RESULT_" + getResultCode(), subscriptionId);
            }
        }
    }
}
