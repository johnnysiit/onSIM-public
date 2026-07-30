package io.onsim.gateway;

import android.Manifest;
import android.app.Activity;
import android.app.role.RoleManager;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Bundle;
import android.provider.Settings;
import android.telecom.TelecomManager;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;

import java.util.ArrayList;
import java.util.List;

public class GatewayActivity extends Activity {
    private TextView status;
    private EditText number;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(32, 48, 32, 32);
        root.setGravity(Gravity.CENTER_HORIZONTAL);
        status = new TextView(this);
        status.setTextSize(18);
        number = new EditText(this);
        number.setHint("电话号码");
        number.setInputType(android.text.InputType.TYPE_CLASS_PHONE);
        Button dial = button("拨号", v -> placeCall());
        Button answer = button("接听", v -> {
            if (GatewayInCallService.currentCall() != null) {
                GatewayInCallService.currentCall().answer(android.telecom.VideoProfile.STATE_AUDIO_ONLY);
            }
        });
        Button hangup = button("挂断", v -> {
            if (GatewayInCallService.currentCall() != null) GatewayInCallService.currentCall().disconnect();
        });
        Button setup = button("重新检查默认应用和权限", v -> provisionRoles());
        root.addView(status, match());
        root.addView(number, match());
        root.addView(dial, match());
        root.addView(answer, match());
        root.addView(hangup, match());
        root.addView(setup, match());
        setContentView(root);

        Intent intent = getIntent();
        if (intent != null && intent.getData() != null && "tel".equals(intent.getData().getScheme())) {
            number.setText(intent.getData().getSchemeSpecificPart());
        }
        Intent gateway = new Intent(this, GatewayService.class);
        if (getIntent() != null && getIntent().hasExtra("token")) {
            gateway.putExtra("token", getIntent().getStringExtra("token"));
        }
        startForegroundService(gateway);
        provisionRoles();
    }

    @Override protected void onResume() {
        super.onResume();
        TelecomManager tm = getSystemService(TelecomManager.class);
        String dialer = tm == null ? "" : tm.getDefaultDialerPackage();
        RoleManager rm = getSystemService(RoleManager.class);
        boolean sms = rm != null && rm.isRoleHeld(RoleManager.ROLE_SMS);
        status.setText("onSIM Gateway\n默认电话：" + getPackageName().equals(dialer) +
                "\n默认短信：" + sms + "\nUSB/ADB 服务：" + (GatewayService.instance() != null ? "运行中" : "启动中"));
    }

    private void provisionRoles() {
        List<String> permissions = new ArrayList<>();
        String[] wanted = {
                Manifest.permission.CALL_PHONE, Manifest.permission.ANSWER_PHONE_CALLS,
                Manifest.permission.READ_PHONE_STATE, Manifest.permission.READ_PHONE_NUMBERS,
                Manifest.permission.READ_CALL_LOG, Manifest.permission.WRITE_CALL_LOG,
                Manifest.permission.SEND_SMS, Manifest.permission.RECEIVE_SMS,
                Manifest.permission.READ_SMS, Manifest.permission.RECORD_AUDIO
        };
        for (String p : wanted) if (checkSelfPermission(p) != PackageManager.PERMISSION_GRANTED) permissions.add(p);
        if (!permissions.isEmpty()) requestPermissions(permissions.toArray(new String[0]), 20);

        RoleManager roles = getSystemService(RoleManager.class);
        if (roles != null && roles.isRoleAvailable(RoleManager.ROLE_DIALER) && !roles.isRoleHeld(RoleManager.ROLE_DIALER)) {
            startActivityForResult(roles.createRequestRoleIntent(RoleManager.ROLE_DIALER), 21);
            return;
        }
        if (roles != null && roles.isRoleAvailable(RoleManager.ROLE_SMS) && !roles.isRoleHeld(RoleManager.ROLE_SMS)) {
            startActivityForResult(roles.createRequestRoleIntent(RoleManager.ROLE_SMS), 22);
        }
        try {
            Intent battery = new Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
                    Uri.parse("package:" + getPackageName()));
            startActivity(battery);
        } catch (Exception ignored) {}
    }

    @Override protected void onActivityResult(int request, int result, Intent data) {
        super.onActivityResult(request, result, data);
        if (request == 21) provisionRoles();
    }

    private void placeCall() {
        String value = number.getText().toString().trim();
        if (value.isEmpty()) return;
        TelecomManager tm = getSystemService(TelecomManager.class);
        if (tm != null && checkSelfPermission(Manifest.permission.CALL_PHONE) == PackageManager.PERMISSION_GRANTED) {
            tm.placeCall(Uri.fromParts("tel", value, null), new Bundle());
        }
    }

    private Button button(String text, View.OnClickListener listener) {
        Button b = new Button(this);
        b.setText(text);
        b.setOnClickListener(listener);
        return b;
    }
    private LinearLayout.LayoutParams match() {
        return new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT,
                LinearLayout.LayoutParams.WRAP_CONTENT);
    }
}
