package io.onsim.gateway;

import android.content.BroadcastReceiver;
import android.content.ContentValues;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.provider.Telephony;
import android.telephony.SmsMessage;
import android.telephony.SubscriptionManager;

public class SmsReceiver extends BroadcastReceiver {
    @Override public void onReceive(Context context, Intent intent) {
        SmsMessage[] messages = Telephony.Sms.Intents.getMessagesFromIntent(intent);
        if (messages == null || messages.length == 0) return;
        String number = messages[0].getOriginatingAddress();
        StringBuilder body = new StringBuilder();
        long timestamp = messages[0].getTimestampMillis();
        for (SmsMessage message : messages) body.append(message.getMessageBody());

        String providerId = "";
        try {
            ContentValues values = new ContentValues();
            values.put(Telephony.Sms.ADDRESS, number);
            values.put(Telephony.Sms.BODY, body.toString());
            values.put(Telephony.Sms.DATE, timestamp);
            values.put(Telephony.Sms.READ, 0);
            values.put(Telephony.Sms.SEEN, 0);
            Uri row = context.getContentResolver().insert(Telephony.Sms.Inbox.CONTENT_URI, values);
            if (row != null) providerId = row.getLastPathSegment();
        } catch (Exception ignored) {}
        int subscriptionId = intent.getIntExtra(SubscriptionManager.EXTRA_SUBSCRIPTION_INDEX, -1);
        GatewayService.emitEvent("sms.received", number, body.toString(), "", providerId, "", subscriptionId);
    }
}
