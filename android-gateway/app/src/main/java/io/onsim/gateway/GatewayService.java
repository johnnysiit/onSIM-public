package io.onsim.gateway;

import android.Manifest;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.ContentValues;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.media.AudioDeviceInfo;
import android.media.AudioFormat;
import android.media.AudioManager;
import android.media.AudioRecord;
import android.net.LocalServerSocket;
import android.net.LocalSocket;
import android.net.Uri;
import android.os.BatteryManager;
import android.os.Build;
import android.os.Bundle;
import android.os.IBinder;
import android.provider.Telephony;
import android.database.Cursor;
import android.telecom.Call;
import android.telecom.TelecomManager;
import android.telephony.PhoneStateListener;
import android.telephony.SignalStrength;
import android.telephony.SmsManager;
import android.telephony.SubscriptionInfo;
import android.telephony.SubscriptionManager;
import android.telephony.TelephonyManager;
import android.util.Log;

import org.json.JSONException;
import org.json.JSONArray;
import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.BufferedWriter;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.io.OutputStreamWriter;
import java.lang.reflect.Method;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

public class GatewayService extends Service {
    private static final String TAG = "OnSIMGateway";
    private static final int PROTOCOL_VERSION = 1;
    // MediaRecorder.AudioSource.VOICE_DOWNLINK is a hidden system constant in
    // parts of the public SDK. The platform value is stable and the app is a
    // privileged CAPTURE_AUDIO_OUTPUT holder.
    private static final int AUDIO_SOURCE_VOICE_DOWNLINK = 3;
    private static final String PREFS = "gateway";
    private static final String TOKEN_KEY = "token";
    private static final int ROOT_UPLINK_PORT = 47601;
    private static volatile GatewayService service;
    private static final ConcurrentLinkedQueue<JSONObject> pendingEvents = new ConcurrentLinkedQueue<>();

    private final ExecutorService executor = Executors.newCachedThreadPool();
    private final Object audioSessionLock = new Object();
    private volatile BufferedWriter controlWriter;
    private volatile boolean running;
    private volatile boolean audioEnabled;
    private volatile boolean injectedMuted;
    private volatile boolean rootUplinkActive;
    private volatile boolean audioDownlinkOK;
    private volatile boolean audioUplinkOK;
    private volatile long audioDownlinkFrames;
    private volatile long audioUplinkFrames;
    private volatile long audioUplinkBytes;
    private volatile String audioLastError = "";
    private volatile int signalDbm = -1;
    private volatile int signalLevel = -1;
    private LocalServerSocket controlServer;
    private LocalServerSocket audioServer;
    private PhoneStateListener signalListener;

    public static GatewayService instance() { return service; }

    @Override public void onCreate() {
        super.onCreate();
        service = this;
        createNotification();
        startForeground(7600, notification("等待 USB/ADB 连接"));
        qualifyAudio();
        registerSignalListener();
        running = true;
        executor.execute(this::serveControl);
        executor.execute(this::serveAudio);
    }

    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent != null) {
            String token = intent.getStringExtra("token");
            if (token != null && token.length() >= 32) {
                getSharedPreferences(PREFS, MODE_PRIVATE).edit().putString(TOKEN_KEY, token).apply();
            }
        }
        return START_STICKY;
    }

    @Override public void onDestroy() {
        running = false;
        closeQuietly(controlServer);
        closeQuietly(audioServer);
        executor.shutdownNow();
        service = null;
        super.onDestroy();
    }

    @Override public IBinder onBind(Intent intent) { return null; }

    private void serveControl() {
        try {
            controlServer = new LocalServerSocket("onsim-control");
            while (running) {
                LocalSocket socket = controlServer.accept();
                executor.execute(() -> handleControl(socket));
            }
        } catch (IOException e) {
            Log.e(TAG, "Control server socket failed", e);
            if (running) emitEvent("device.error", "", "", "", "", "CONTROL_SOCKET:" + e.getMessage());
        }
    }

    private void handleControl(LocalSocket socket) {
        try (LocalSocket ignored = socket;
             BufferedReader reader = new BufferedReader(new InputStreamReader(socket.getInputStream(), StandardCharsets.UTF_8));
             BufferedWriter writer = new BufferedWriter(new OutputStreamWriter(socket.getOutputStream(), StandardCharsets.UTF_8))) {
            String first = reader.readLine();
            if (first == null) return;
            JSONObject auth = new JSONObject(first);
            if (auth.optInt("version") != PROTOCOL_VERSION ||
                    !validHmac("control", auth.optString("nonce"), auth.optString("mac"))) {
                Log.w(TAG, "Rejected control client authentication");
                writer.write(error("", "AUTHENTICATION_FAILED").toString());
                writer.newLine();
                writer.flush();
                return;
            }
            controlWriter = writer;
            Log.i(TAG, "ADB control client authenticated");
            writer.write(response("", new JSONObject().put("authenticated", true)).toString());
            writer.newLine();
            writer.flush();
            flushPending();
            syncRecentMessages();
            emitStatus();
            String line;
            while ((line = reader.readLine()) != null) {
                JSONObject request = new JSONObject(line);
                if (!"request".equals(request.optString("type"))) continue;
                handleRequest(request, writer);
            }
        } catch (Exception error) {
            Log.e(TAG, "Control client failed", error);
        } finally {
            controlWriter = null;
        }
    }

    private void handleRequest(JSONObject request, BufferedWriter writer) {
        String id = request.optString("id");
        String action = request.optString("action");
        JSONObject data = request.optJSONObject("data");
        if (data == null) data = new JSONObject();
        JSONObject reply;
        try {
            switch (action) {
                case "status":
                    reply = response(id, status());
                    break;
                case "dial":
                    dial(data.getString("number"), data.optString("subscriptionId", "auto"));
                    reply = response(id, new JSONObject());
                    break;
                case "answer":
                    requireCall().answer(android.telecom.VideoProfile.STATE_AUDIO_ONLY);
                    reply = response(id, new JSONObject());
                    break;
                case "hangup":
                    requireCall().disconnect();
                    reply = response(id, new JSONObject());
                    break;
                case "dtmf":
                    String key = data.getString("key");
                    if (key.length() != 1 || "0123456789*#ABCD".indexOf(key.charAt(0)) < 0) {
                        throw new IllegalArgumentException("INVALID_DTMF");
                    }
                    requireCall().playDtmfTone(key.charAt(0));
                    executor.execute(() -> {
                        try { Thread.sleep(180); } catch (InterruptedException ignored) {}
                        Call call = GatewayInCallService.currentCall();
                        if (call != null) call.stopDtmfTone();
                    });
                    reply = response(id, new JSONObject());
                    break;
                case "mute":
                    injectedMuted = data.getBoolean("muted");
                    // While the root bridge owns uplink, Telecom mute remains
                    // forced on to suppress the handset microphone. The web
                    // mute control silences only the injected PCM stream.
                    if (!rootUplinkActive) GatewayInCallService.setGatewayMuted(injectedMuted);
                    reply = response(id, new JSONObject());
                    break;
                case "audio.enable":
                    audioEnabled = data.getBoolean("enabled");
                    if (audioEnabled) {
                        qualifyAudio();
                    } else {
                        // Mute is scoped to one browser/SIP media session. A
                        // later handset-only call must not inherit that state.
                        injectedMuted = false;
                        if (!rootUplinkActive) GatewayInCallService.setGatewayMuted(false);
                    }
                    reply = response(id, new JSONObject());
                    break;
                case "sms.send":
                    sendSMS(data.getString("clientId"), data.getString("number"), data.getString("body"),
                            data.optString("subscriptionId", "auto"));
                    reply = response(id, new JSONObject());
                    break;
                case "sms.delete":
                    int deleted = getContentResolver().delete(
                            Uri.withAppendedPath(Telephony.Sms.CONTENT_URI, Integer.toString(data.getInt("id"))),
                            null, null);
                    reply = response(id, new JSONObject().put("deleted", deleted));
                    break;
                default:
                    reply = error(id, "UNKNOWN_ACTION");
            }
        } catch (Exception e) {
            reply = error(id, e.getMessage() == null ? e.getClass().getSimpleName() : e.getMessage());
        }
        try {
            synchronized (writer) {
                writer.write(reply.toString());
                writer.newLine();
                writer.flush();
            }
        } catch (IOException ignored) {}
    }

    private void serveAudio() {
        try {
            audioServer = new LocalServerSocket("onsim-audio");
            while (running) {
                LocalSocket socket = audioServer.accept();
                executor.execute(() -> handleAudio(socket));
            }
        } catch (IOException e) {
            Log.e(TAG, "Audio server socket failed", e);
            if (running) emitEvent("audio.failed", "", "", "", "", "AUDIO_SOCKET:" + e.getMessage());
        }
    }

    private void handleAudio(LocalSocket socket) {
        synchronized (audioSessionLock) {
            handleAudioSession(socket);
        }
    }

    private void handleAudioSession(LocalSocket socket) {
        AudioRecord record = null;
        Socket uplink = null;
        boolean initialized = false;
        boolean physicalMicMuted = false;
        AtomicBoolean routeFailed = new AtomicBoolean(false);
        try (LocalSocket ignored = socket) {
            String supplied = readLineUnbuffered(socket.getInputStream(), 256);
            int separator = supplied.indexOf(':');
            if (separator <= 0 || !validHmac("audio", supplied.substring(0, separator),
                    supplied.substring(separator + 1))) return;
            if (!audioEnabled || GatewayInCallService.currentCall() == null) {
                throw new IllegalStateException("CALL_AUDIO_NOT_ACTIVE");
            }
            int inSize = Math.max(1280, AudioRecord.getMinBufferSize(16000,
                    AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT));
            // VOICE_CALL records both directions on Qualcomm and feeds the
            // injected web microphone back to the browser. Capture only the
            // baseband downlink so the local user hears the remote party but
            // never their own uplink.
            record = new AudioRecord(AUDIO_SOURCE_VOICE_DOWNLINK, 16000,
                    AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT, inSize);

            AudioManager manager = getSystemService(AudioManager.class);
            AudioDeviceInfo telephonyIn = findDevice(manager, AudioManager.GET_DEVICES_INPUTS, AudioDeviceInfo.TYPE_TELEPHONY);
            if (telephonyIn != null) record.setPreferredDevice(telephonyIn);
            if (record.getState() != AudioRecord.STATE_INITIALIZED) {
                throw new IllegalStateException("TELEPHONY_AUDIO_INITIALIZATION_FAILED");
            }
            // Qualcomm's device_mute parameter suppresses the physical
            // handset microphone without muting Incall_Music. Voice/Telecom
            // mute cannot be used here because it also mutes injected PCM.
            // Send the parameter through AudioFlinger rather than writing the
            // dynamic mixer array with tinymix: on this ROM that control only
            // has a valid range after the voice session is fully established.
            physicalMicMuted = setPhysicalMicMuted(true);
            if (!physicalMicMuted) {
                Log.w(TAG, "Physical microphone isolation unavailable; continuing root uplink");
            }

            // Android's Java AudioTrack API maps TYPE_TELEPHONY to Qualcomm's
            // afe-proxy-playback usecase on this ROM. It never requests
            // AUDIO_OUTPUT_FLAG_INCALL_MUSIC, although getRoutedDevice()
            // misleadingly reports Telephony Tx. The Magisk root bridge opens
            // MultiMedia2 and the Incall_Music mixer directly; this is the
            // hardware path verified to reach the VoLTE baseband.
            uplink = openRootUplink();
            rootUplinkActive = true;
            record.startRecording();
            initialized = true;
            audioDownlinkOK = true;
            audioUplinkOK = false;
            audioLastError = "";
            final OutputStream activeUplink = uplink.getOutputStream();
            InputStream input = socket.getInputStream();
            executor.execute(() -> copyUplink(input, activeUplink, socket, routeFailed));
            OutputStream output = socket.getOutputStream();
            byte[] frame = new byte[640];
            while (running && audioEnabled) {
                int read = record.read(frame, 0, frame.length, AudioRecord.READ_BLOCKING);
                if (read < 0) throw new IOException("AudioRecord error " + read);
                if (read > 0) {
                    output.write(frame, 0, read);
                    output.flush();
                    audioDownlinkFrames++;
                }
            }
        } catch (Exception e) {
            Log.e(TAG, "Telephony audio failed", e);
            audioLastError = e.getMessage() == null ? e.getClass().getSimpleName() : e.getMessage();
            if (initialized && !routeFailed.get()) {
                // A browser/WebRTC or ADB disconnect closes the local socket
                // with EPIPE after the HAL has already been proven healthy.
                // Keep the device qualified so the next media owner can
                // reconnect without restarting the Companion.
                qualifyAudio();
                emitEvent("audio.closed", "", "", "", "", e.getMessage());
            } else {
                audioDownlinkOK = false;
                audioUplinkOK = false;
                emitEvent("audio.failed", "", "", "", "", audioLastError);
            }
        } finally {
            if (record != null) {
                try { record.stop(); } catch (Exception ignored) {}
                record.release();
            }
            if (uplink != null) try { uplink.close(); } catch (IOException ignored) {}
            rootUplinkActive = false;
            if (physicalMicMuted) {
                setPhysicalMicMuted(false);
                physicalMicMuted = false;
            }
            // If media disconnects while the call remains alive, restore the
            // explicit user mute state rather than leaving the handset muted.
            GatewayInCallService.setGatewayMuted(injectedMuted);
        }
    }

    private boolean setPhysicalMicMuted(boolean muted) {
        String parameters = "device_mute=" + muted + ";direction=tx";
        try {
            Class<?> audioSystem = Class.forName("android.media.AudioSystem");
            Method setParameters = audioSystem.getDeclaredMethod("setParameters", String.class);
            setParameters.setAccessible(true);
            Object result = setParameters.invoke(null, parameters);
            if (result instanceof Integer && ((Integer) result) != 0) {
                Log.e(TAG, "AudioSystem rejected " + parameters + " status=" + result);
                return false;
            }
            Log.i(TAG, "Qualcomm physical microphone mute=" + muted + " through AudioSystem");
            return true;
        } catch (Exception audioSystemError) {
            try {
                AudioManager manager = getSystemService(AudioManager.class);
                Method setParameters = AudioManager.class.getDeclaredMethod("setParameters", String.class);
                setParameters.setAccessible(true);
                setParameters.invoke(manager, parameters);
                Log.i(TAG, "Qualcomm physical microphone mute=" + muted + " through AudioManager");
                return true;
            } catch (Exception managerError) {
                Log.e(TAG, "Unable to set Qualcomm physical microphone mute=" + muted,
                        managerError);
                return false;
            }
        }
    }

    private Socket openRootUplink() throws IOException {
        Socket root = new Socket();
        root.setTcpNoDelay(true);
        root.connect(new InetSocketAddress("127.0.0.1", ROOT_UPLINK_PORT), 3000);
        OutputStream output = root.getOutputStream();
        output.write((token() + "\n").getBytes(StandardCharsets.UTF_8));
        output.flush();
        String reply = readLineUnbuffered(root.getInputStream(), 128);
        if (!"READY".equals(reply)) {
            root.close();
            throw new IOException("ROOT_UPLINK_" + (reply.isEmpty() ? "NO_RESPONSE" : reply));
        }
        output.write(streamingWaveHeader());
        output.flush();
        return root;
    }

    static byte[] streamingWaveHeader() {
        byte[] header = new byte[44];
        putASCII(header, 0, "RIFF");
        putLE32(header, 4, 0x7ffffff7);
        putASCII(header, 8, "WAVEfmt ");
        putLE32(header, 16, 16);
        putLE16(header, 20, 1);
        putLE16(header, 22, 2);
        putLE32(header, 24, 16000);
        putLE32(header, 28, 64000);
        putLE16(header, 32, 4);
        putLE16(header, 34, 16);
        putASCII(header, 36, "data");
        putLE32(header, 40, 0x7fffffd3);
        return header;
    }

    private static void putASCII(byte[] out, int offset, String value) {
        byte[] raw = value.getBytes(StandardCharsets.US_ASCII);
        System.arraycopy(raw, 0, out, offset, raw.length);
    }

    private static void putLE16(byte[] out, int offset, int value) {
        out[offset] = (byte) value;
        out[offset + 1] = (byte) (value >>> 8);
    }

    private static void putLE32(byte[] out, int offset, int value) {
        out[offset] = (byte) value;
        out[offset + 1] = (byte) (value >>> 8);
        out[offset + 2] = (byte) (value >>> 16);
        out[offset + 3] = (byte) (value >>> 24);
    }

    private void copyUplink(InputStream input, OutputStream uplink,
                            LocalSocket socket, AtomicBoolean routeFailed) {
        byte[] mono = new byte[640];
        byte[] stereo = new byte[1280];
        boolean routeQualified = false;
        try {
            while (running && audioEnabled) {
                int offset = 0;
                while (offset < mono.length) {
                    int n = input.read(mono, offset, mono.length - offset);
                    if (n < 0) return;
                    offset += n;
                }
                prepareUplinkFrame(mono, stereo, injectedMuted);
                uplink.write(stereo);
                uplink.flush();
                if (!routeQualified) {
                    routeQualified = true;
                    audioUplinkOK = true;
                    audioLastError = "";
                    emitEvent("audio.ready", "", "", "", "", "");
                    Log.i(TAG, "Uplink writing through root MultiMedia2 Incall_Music bridge");
                }
                audioUplinkFrames++;
                audioUplinkBytes += mono.length;
            }
        } catch (IOException e) {
            routeFailed.set(true);
            audioUplinkOK = false;
            audioLastError = e.getMessage() == null ? "TELEPHONY_TX_ROUTE_FAILED" : e.getMessage();
            emitEvent("audio.failed", "", "", "", "", audioLastError);
            try { socket.close(); } catch (IOException ignored) {}
        }
    }

    static void monoToStereo(byte[] mono, byte[] stereo) {
        if (mono.length % 2 != 0 || stereo.length != mono.length * 2) {
            throw new IllegalArgumentException("PCM_FRAME_SIZE_INVALID");
        }
        for (int sample = 0; sample < mono.length / 2; sample++) {
            byte low = mono[sample * 2];
            byte high = mono[sample * 2 + 1];
            int target = sample * 4;
            stereo[target] = low;
            stereo[target + 1] = high;
            stereo[target + 2] = low;
            stereo[target + 3] = high;
        }
    }

    static void prepareUplinkFrame(byte[] mono, byte[] stereo, boolean muted) {
        if (muted) {
            if (stereo.length != mono.length * 2) {
                throw new IllegalArgumentException("PCM_FRAME_SIZE_INVALID");
            }
            Arrays.fill(stereo, (byte) 0);
            return;
        }
        monoToStereo(mono, stereo);
    }

    private void dial(String number, String requestedSub) throws Exception {
        SubscriptionInfo sub = selectSubscription(requestedSub);
        TelecomManager telecom = getSystemService(TelecomManager.class);
        if (telecom == null) throw new IllegalStateException("TELECOM_UNAVAILABLE");
        Bundle extras = new Bundle();
        if (sub != null) {
            for (android.telecom.PhoneAccountHandle handle : telecom.getCallCapablePhoneAccounts()) {
                android.telecom.PhoneAccount account = telecom.getPhoneAccount(handle);
                if (account != null && account.hasCapabilities(android.telecom.PhoneAccount.CAPABILITY_SIM_SUBSCRIPTION)) {
                    String id = handle.getId();
                    if (id != null && id.contains(Integer.toString(sub.getSubscriptionId()))) {
                        extras.putParcelable(TelecomManager.EXTRA_PHONE_ACCOUNT_HANDLE, handle);
                        break;
                    }
                }
            }
        }
        telecom.placeCall(Uri.fromParts("tel", number, null), extras);
    }

    private void sendSMS(String clientId, String number, String body, String requestedSub) throws Exception {
        SubscriptionInfo sub = selectSubscription(requestedSub);
        if (sub == null) throw new IllegalStateException("NO_ACTIVE_SUBSCRIPTION");
        ContentValues values = new ContentValues();
        values.put(Telephony.Sms.ADDRESS, number);
        values.put(Telephony.Sms.BODY, body);
        values.put(Telephony.Sms.DATE, System.currentTimeMillis());
        values.put(Telephony.Sms.READ, 1);
        Uri row = getContentResolver().insert(Telephony.Sms.Outbox.CONTENT_URI, values);
        String providerId = row == null ? "" : row.getLastPathSegment();

        SmsManager manager = SmsManager.getSmsManagerForSubscriptionId(sub.getSubscriptionId());
        ArrayList<String> parts = manager.divideMessage(body);
        ArrayList<PendingIntent> sent = new ArrayList<>();
        ArrayList<PendingIntent> delivered = new ArrayList<>();
        for (int i = 0; i < parts.size(); i++) {
            int requestCode = (clientId.hashCode() & 0x3fffffff) + i;
            Intent sentIntent = new Intent(this, SmsStatusReceiver.class)
                    .setAction(SmsStatusReceiver.SENT)
                    .putExtra("clientId", clientId).putExtra("providerId", providerId)
                    .putExtra("subscriptionId", sub.getSubscriptionId());
            Intent deliveredIntent = new Intent(this, SmsStatusReceiver.class)
                    .setAction(SmsStatusReceiver.DELIVERED)
                    .putExtra("clientId", clientId).putExtra("providerId", providerId)
                    .putExtra("subscriptionId", sub.getSubscriptionId());
            sent.add(PendingIntent.getBroadcast(this, requestCode, sentIntent,
                    PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE));
            delivered.add(PendingIntent.getBroadcast(this, requestCode + 0x40000000, deliveredIntent,
                    PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE));
        }
        manager.sendMultipartTextMessage(number, null, parts, sent, delivered);
    }

    private SubscriptionInfo selectSubscription(String requested) {
        SubscriptionManager manager = getSystemService(SubscriptionManager.class);
        if (manager == null || checkSelfPermission(Manifest.permission.READ_PHONE_STATE) != PackageManager.PERMISSION_GRANTED) {
            throw new IllegalStateException("SUBSCRIPTION_PERMISSION_DENIED");
        }
        List<SubscriptionInfo> active = manager.getActiveSubscriptionInfoList();
        if (active == null || active.isEmpty()) throw new IllegalStateException("NO_ACTIVE_SUBSCRIPTION");
        if (!"auto".equals(requested) && !requested.isEmpty()) {
            int id = Integer.parseInt(requested);
            for (SubscriptionInfo info : active) if (info.getSubscriptionId() == id) return info;
            throw new IllegalStateException("SUBSCRIPTION_NOT_ACTIVE");
        }
        if (active.size() != 1) throw new IllegalStateException("MULTIPLE_ACTIVE_SUBSCRIPTIONS");
        return active.get(0);
    }

    private JSONObject status() throws JSONException {
        JSONObject out = new JSONObject();
        JSONArray subscriptions = new JSONArray();
        List<SubscriptionInfo> activeSubscriptions = new ArrayList<>();
        SubscriptionManager subscriptionManager = getSystemService(SubscriptionManager.class);
        if (subscriptionManager != null &&
                checkSelfPermission(Manifest.permission.READ_PHONE_STATE) == PackageManager.PERMISSION_GRANTED) {
            List<SubscriptionInfo> active = subscriptionManager.getActiveSubscriptionInfoList();
            if (active != null) activeSubscriptions.addAll(active);
            if (active != null) for (SubscriptionInfo info : active) {
                TelephonyManager subscriptionPhone = getSystemService(TelephonyManager.class);
                if (subscriptionPhone != null) {
                    subscriptionPhone = subscriptionPhone.createForSubscriptionId(info.getSubscriptionId());
                }
                JSONObject item = new JSONObject();
                item.put("id", info.getSubscriptionId());
                item.put("simSlot", info.getSimSlotIndex());
                item.put("displayName", info.getDisplayName() == null ? "" : info.getDisplayName().toString());
                item.put("carrierName", info.getCarrierName() == null ? "" : info.getCarrierName().toString());
                item.put("phoneNumber", info.getNumber() == null ? "" : info.getNumber());
                item.put("imei", privilegedString(subscriptionPhone, "getImei"));
                item.put("ready", true);
                subscriptions.put(item);
            }
        }
        out.put("subscriptions", subscriptions);
        SubscriptionInfo sub = null;
        try { sub = selectSubscription("auto"); } catch (Exception ignored) {}
        // The aggregate status may use one representative SIM, while dial and
        // SMS still require an explicit subscription when DSDS has >1 active.
        if (sub == null && !activeSubscriptions.isEmpty()) sub = activeSubscriptions.get(0);
        TelephonyManager base = getSystemService(TelephonyManager.class);
        TelephonyManager phone = base;
        if (base != null && sub != null) phone = base.createForSubscriptionId(sub.getSubscriptionId());
        out.put("simReady", sub != null);
        out.put("registered", phone != null && phone.getServiceState() != null &&
                phone.getServiceState().getState() == android.telephony.ServiceState.STATE_IN_SERVICE);
        out.put("voiceReady", out.optBoolean("registered"));
        out.put("operator", phone == null ? "" : phone.getNetworkOperatorName());
        int dataNetworkType = phone == null ? TelephonyManager.NETWORK_TYPE_UNKNOWN : phone.getDataNetworkType();
        out.put("accessTechnology", networkName(dataNetworkType));
        out.put("signal", signalLevel);
        out.put("signalDbm", signalDbm);
        out.put("phoneNumber", sub == null || sub.getNumber() == null ? "" : sub.getNumber());
        out.put("iccid", sub == null || sub.getIccId() == null ? "" : sub.getIccId());
        out.put("imsi", privilegedString(phone, "getSubscriberId"));
        out.put("manufacturer", Build.MANUFACTURER);
        out.put("model", Build.MODEL);
        out.put("imei", privilegedString(phone, "getImei"));
        out.put("androidVersion", Build.VERSION.RELEASE);
        out.put("buildId", Build.DISPLAY);
        out.put("securityPatch", Build.VERSION.SECURITY_PATCH);
        out.put("basebandVersion", Build.getRadioVersion());
        Intent battery = registerReceiver(null, new IntentFilter(Intent.ACTION_BATTERY_CHANGED));
        int level = battery == null ? -1 : battery.getIntExtra(BatteryManager.EXTRA_LEVEL, -1);
        int scale = battery == null ? 100 : battery.getIntExtra(BatteryManager.EXTRA_SCALE, 100);
        int plugged = battery == null ? 0 : battery.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0);
        out.put("batteryLevel", scale > 0 ? level * 100 / scale : -1);
        out.put("batteryCharging", plugged != 0);
        out.put("subscriptionId", sub == null ? -1 : sub.getSubscriptionId());
        out.put("simSlot", sub == null ? -1 : sub.getSimSlotIndex());
        boolean imsRegistered = reflectedBoolean(phone, "isImsRegistered");
        // HydrogenOS 10 exposes IMS registration correctly but its hidden
        // isVolteAvailable() implementation returns false while an LTE IMS
        // voice bearer is active. Treat IMS-on-LTE as VoLTE capable; call
        // acceptance still verifies that IMS remains registered in-call.
        boolean volte = reflectedBoolean(phone, "isVolteAvailable") ||
                (imsRegistered && dataNetworkType == TelephonyManager.NETWORK_TYPE_LTE);
        out.put("imsRegistered", imsRegistered);
        out.put("volte", volte);
        out.put("companionVersion", BuildConfig.VERSION_NAME);
        out.put("audioDownlinkOk", audioDownlinkOK);
        out.put("audioUplinkOk", audioUplinkOK);
        out.put("audioDownlinkFrames", audioDownlinkFrames);
        out.put("audioUplinkFrames", audioUplinkFrames);
        out.put("audioUplinkBytes", audioUplinkBytes);
        out.put("audioLastError", audioLastError);
        return out;
    }

    private void emitStatus() {
        try {
            JSONObject msg = base("status").put("data", status());
            writeEvent(msg);
        } catch (Exception ignored) {}
    }

    public static void emitCallEvent(String event, Call call, String reason) {
        String number = "";
        int subscriptionId = -1;
        try {
            if (call != null && call.getDetails() != null && call.getDetails().getHandle() != null) {
                number = call.getDetails().getHandle().getSchemeSpecificPart();
            }
            String accountId = call == null || call.getDetails() == null ||
                    call.getDetails().getAccountHandle() == null ? "" :
                    call.getDetails().getAccountHandle().getId();
            GatewayService current = service;
            SubscriptionManager manager = current == null ? null :
                    current.getSystemService(SubscriptionManager.class);
            List<SubscriptionInfo> active = manager == null ? null :
                    manager.getActiveSubscriptionInfoList();
            if (active != null) for (SubscriptionInfo info : active) {
                if (accountId.contains(Integer.toString(info.getSubscriptionId()))) {
                    subscriptionId = info.getSubscriptionId();
                    break;
                }
            }
        } catch (Exception ignored) {}
        emitEvent(event, number, "", "", "", reason, subscriptionId);
    }

    public static void emitEvent(String event, String number, String body,
                                 String clientId, String providerId, String reason) {
        emitEvent(event, number, body, clientId, providerId, reason, -1);
    }

    public static void emitEvent(String event, String number, String body,
                                 String clientId, String providerId, String reason,
                                 int subscriptionId) {
        try {
            JSONObject data = new JSONObject().put("event", event);
            if (number != null && !number.isEmpty()) data.put("number", number);
            if (body != null && !body.isEmpty()) data.put("body", body);
            if (clientId != null && !clientId.isEmpty()) data.put("clientId", clientId);
            if (providerId != null && !providerId.isEmpty()) data.put("providerId", providerId);
            if (reason != null && !reason.isEmpty()) data.put("reason", reason);
            if (subscriptionId >= 0) data.put("subscriptionId", subscriptionId);
            JSONObject message = base("event").put("data", data);
            GatewayService current = service;
            if (current == null || !current.writeEvent(message)) {
                while (pendingEvents.size() >= 100) pendingEvents.poll();
                pendingEvents.offer(message);
            }
        } catch (JSONException ignored) {}
    }

    private boolean writeEvent(JSONObject message) {
        BufferedWriter writer = controlWriter;
        if (writer == null) return false;
        try {
            synchronized (writer) {
                writer.write(message.toString());
                writer.newLine();
                writer.flush();
            }
            return true;
        } catch (IOException e) {
            return false;
        }
    }

    private void flushPending() {
        JSONObject message;
        while ((message = pendingEvents.poll()) != null) {
            if (!writeEvent(message)) {
                pendingEvents.offer(message);
                return;
            }
        }
    }

    private void qualifyAudio() {
        boolean privileged = checkSelfPermission("android.permission.CAPTURE_AUDIO_OUTPUT") == PackageManager.PERMISSION_GRANTED;
        // Telephony Rx/Tx ports may only be enumerated while a call is active.
        // The permission is the pre-call qualification; handleAudio performs
        // the real initialization check and fails health closed.
        audioDownlinkOK = privileged;
        audioUplinkOK = privileged;
    }

    private boolean validHmac(String scope, String nonce, String supplied) {
        String secret = token();
        if (secret.length() < 32 || nonce.length() != 32 || supplied.length() != 64) return false;
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(secret.getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
            byte[] digest = mac.doFinal((scope + ":" + nonce).getBytes(StandardCharsets.UTF_8));
            StringBuilder expected = new StringBuilder(digest.length * 2);
            for (byte value : digest) expected.append(String.format("%02x", value & 0xff));
            return constantTimeEquals(expected.toString(), supplied);
        } catch (Exception ignored) {
            return false;
        }
    }

    private void syncRecentMessages() {
        if (checkSelfPermission(Manifest.permission.READ_SMS) != PackageManager.PERMISSION_GRANTED) return;
        try (Cursor cursor = getContentResolver().query(
                Telephony.Sms.Inbox.CONTENT_URI,
                new String[]{Telephony.Sms._ID, Telephony.Sms.ADDRESS, Telephony.Sms.BODY},
                null, null, Telephony.Sms.DEFAULT_SORT_ORDER + " LIMIT 50")) {
            if (cursor == null) return;
            int id = cursor.getColumnIndexOrThrow(Telephony.Sms._ID);
            int address = cursor.getColumnIndexOrThrow(Telephony.Sms.ADDRESS);
            int body = cursor.getColumnIndexOrThrow(Telephony.Sms.BODY);
            while (cursor.moveToNext()) {
                emitEvent("sms.received", cursor.getString(address), cursor.getString(body),
                        "", cursor.getString(id), "");
            }
        } catch (Exception ignored) {}
    }

    private void registerSignalListener() {
        TelephonyManager manager = getSystemService(TelephonyManager.class);
        if (manager == null || checkSelfPermission(Manifest.permission.READ_PHONE_STATE) != PackageManager.PERMISSION_GRANTED) return;
        signalListener = new PhoneStateListener() {
            @Override public void onSignalStrengthsChanged(SignalStrength strength) {
                signalLevel = strength == null ? -1 : strength.getLevel();
                if (strength == null || strength.getCellSignalStrengths().isEmpty()) {
                    signalDbm = -1;
                } else {
                    signalDbm = strength.getCellSignalStrengths().get(0).getDbm();
                }
                emitStatus();
            }
        };
        manager.listen(signalListener, PhoneStateListener.LISTEN_SIGNAL_STRENGTHS);
    }

    private static AudioDeviceInfo findDevice(AudioManager manager, int direction, int type) {
        if (manager == null) return null;
        for (AudioDeviceInfo device : manager.getDevices(direction)) if (device.getType() == type) return device;
        return null;
    }

    private static Call requireCall() {
        Call call = GatewayInCallService.currentCall();
        if (call == null) throw new IllegalStateException("NO_ACTIVE_CALL");
        return call;
    }

    private String token() {
        return getSharedPreferences(PREFS, MODE_PRIVATE).getString(TOKEN_KEY, "");
    }

    private static String readLineUnbuffered(InputStream input, int max) throws IOException {
        byte[] data = new byte[max];
        int length = 0;
        while (length < max) {
            int value = input.read();
            if (value < 0) break;
            if (value == '\n') break;
            if (value != '\r') data[length++] = (byte) value;
        }
        return new String(data, 0, length, StandardCharsets.UTF_8);
    }

    private static boolean constantTimeEquals(String expected, String actual) {
        if (expected == null || actual == null) return false;
        byte[] a = expected.getBytes(StandardCharsets.UTF_8);
        byte[] b = actual.getBytes(StandardCharsets.UTF_8);
        int diff = a.length ^ b.length;
        for (int i = 0; i < Math.max(a.length, b.length); i++) {
            diff |= (i < a.length ? a[i] : 0) ^ (i < b.length ? b[i] : 0);
        }
        return diff == 0;
    }

    private static JSONObject base(String type) throws JSONException {
        return new JSONObject().put("version", PROTOCOL_VERSION).put("type", type);
    }
    private static JSONObject response(String id, JSONObject data) {
        try { return base("response").put("id", id).put("data", data); }
        catch (JSONException impossible) { return new JSONObject(); }
    }
    private static JSONObject error(String id, String message) {
        try { return base("response").put("id", id).put("error", message); }
        catch (JSONException impossible) { return new JSONObject(); }
    }

    private static String privilegedString(TelephonyManager manager, String method) {
        if (manager == null) return "";
        try {
            Object result = TelephonyManager.class.getMethod(method).invoke(manager);
            return result == null ? "" : result.toString();
        } catch (Exception ignored) { return ""; }
    }
    private static boolean reflectedBoolean(TelephonyManager manager, String method) {
        if (manager == null) return false;
        try {
            Method target = TelephonyManager.class.getDeclaredMethod(method);
            target.setAccessible(true);
            Object result = target.invoke(manager);
            return result instanceof Boolean && (Boolean) result;
        } catch (Exception ignored) { return false; }
    }
    private static String networkName(int type) {
        switch (type) {
            case TelephonyManager.NETWORK_TYPE_LTE: return "LTE";
            case TelephonyManager.NETWORK_TYPE_HSPAP: return "HSPA+";
            case TelephonyManager.NETWORK_TYPE_UMTS: return "UMTS";
            case TelephonyManager.NETWORK_TYPE_EDGE: return "EDGE";
            case TelephonyManager.NETWORK_TYPE_GPRS: return "GPRS";
            case TelephonyManager.NETWORK_TYPE_CDMA: return "CDMA";
            default: return type == TelephonyManager.NETWORK_TYPE_UNKNOWN ? "" : "RAT-" + type;
        }
    }

    private void createNotification() {
        NotificationManager manager = getSystemService(NotificationManager.class);
        if (manager != null) manager.createNotificationChannel(new NotificationChannel(
                "gateway", "onSIM Gateway", NotificationManager.IMPORTANCE_LOW));
    }
    private Notification notification(String text) {
        PendingIntent open = PendingIntent.getActivity(this, 1,
                new Intent(this, GatewayActivity.class),
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        return new Notification.Builder(this, "gateway")
                .setSmallIcon(io.onsim.gateway.R.drawable.ic_gateway)
                .setContentTitle("onSIM Gateway").setContentText(text)
                .setOngoing(true).setContentIntent(open).build();
    }
    private static void closeQuietly(LocalServerSocket socket) {
        if (socket != null) try { socket.close(); } catch (IOException ignored) {}
    }
}
