package io.onsim.gateway;

import android.telecom.Call;
import android.telecom.InCallService;

public class GatewayInCallService extends InCallService {
    private static volatile GatewayInCallService instance;
    private static volatile Call current;

    private final Call.Callback callback = new Call.Callback() {
        @Override public void onStateChanged(Call call, int state) {
            emitState(call, state);
        }
        @Override public void onDetailsChanged(Call call, Call.Details details) {
            emitState(call, call.getState());
        }
    };

    @Override public void onCreate() {
        super.onCreate();
        instance = this;
    }

    @Override public void onCallAdded(Call call) {
        current = call;
        call.registerCallback(callback);
        emitState(call, call.getState());
    }

    @Override public void onCallRemoved(Call call) {
        call.unregisterCallback(callback);
        GatewayService.emitCallEvent("call.ended", call, "removed");
        if (current == call) current = null;
    }

    @Override public void onDestroy() {
        instance = null;
        current = null;
        super.onDestroy();
    }

    private void emitState(Call call, int state) {
        String event;
        switch (state) {
            case Call.STATE_RINGING: event = "call.incoming"; break;
            case Call.STATE_DIALING:
            case Call.STATE_CONNECTING: event = "call.alerting"; break;
            case Call.STATE_ACTIVE: event = "call.active"; break;
            case Call.STATE_DISCONNECTED:
            case Call.STATE_DISCONNECTING: event = "call.ended"; break;
            default: return;
        }
        GatewayService.emitCallEvent(event, call, "");
    }

    public static Call currentCall() { return current; }
    public static void setGatewayMuted(boolean muted) {
        GatewayInCallService service = instance;
        if (service != null) service.setMuted(muted);
    }
}
