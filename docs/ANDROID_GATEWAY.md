# Android gateway

[简体中文](ANDROID_GATEWAY.zh-CN.md)

The Android provider uses a dedicated phone as a cellular gateway. Control and
16 kHz PCM audio travel over private ADB forwards. The Companion becomes the
default phone and SMS application and needs privileged system permissions.

## Important warnings

- Unlocking a bootloader normally erases the phone.
- Flashing the wrong boot image can prevent the phone from booting.
- Patch a boot image on the same target phone with the official Magisk app.
- Keep the stock boot image, its hash, and a tested recovery path.
- Never commit OTA files, boot images, APK signing keys, ADB keys, or tokens.
- A normal unrooted Android app cannot capture and inject cellular call audio.

The provided scripts target the verified OnePlus 5 Android 10 layout. Treat
other devices as a porting project, not a drop-in installation.

## 1. Confirm the exact build

```sh
adb devices -l
export ONSIM_ANDROID_SERIAL=REPLACE_WITH_ADB_SERIAL
adb -s "$ONSIM_ANDROID_SERIAL" shell getprop ro.build.fingerprint
adb -s "$ONSIM_ANDROID_SERIAL" shell getprop gsm.version.baseband
```

Obtain the matching stock OTA from a legitimate source and verify its checksum.
Never use an OTA or `boot.img` from a different fingerprint.

```sh
export ONSIM_ONEPLUS_ROM=/absolute/path/to/matching-stock-ota.zip
scripts/prepare-oneplus5.sh
```

Generated firmware material is stored under `data/` and ignored by Git.

## 2. Build the Companion

```sh
scripts/build-android-gateway.sh
```

Artifacts are written to `dist/android/`. The first build generates a private
signing key under `data/android-signing/`. Back it up securely; upgrades must
use the same signing identity.

## 3. Establish Magisk

Use the official Magisk application on the target phone to patch the extracted
stock boot image. Pull the result and validate temporary boot before persistent
flash:

```sh
scripts/root-oneplus5.sh manager
scripts/root-oneplus5.sh pull
scripts/root-oneplus5.sh test
scripts/root-oneplus5.sh flash
```

Read each prompt. Do not skip temporary boot validation. To restore the saved
stock image:

```sh
scripts/recover-oneplus5.sh
```

## 4. Provision

```sh
scripts/provision-oneplus5.sh
```

This installs the signed Companion and Magisk module, provisions the private
control token, assigns phone/SMS roles, and enables the privileged service.

After reboot:

```sh
adb -s "$ONSIM_ANDROID_SERIAL" shell su -c id
adb -s "$ONSIM_ANDROID_SERIAL" shell dumpsys telecom
adb -s "$ONSIM_ANDROID_SERIAL" shell dumpsys role
```

## 5. Configure onSIM

In `~/.config/onsim/onsim.env`:

```dotenv
ONSIM_GATEWAY_MODE=android
ONSIM_ANDROID_SERIAL=REPLACE_WITH_ADB_SERIAL
ONSIM_ANDROID_SUBSCRIPTION_ID=auto
```

Restart:

```sh
systemctl --user restart onsim.service
podman exec onsim adb -P 5038 devices -l
podman exec onsim adb -P 5038 forward --list
```

## Dual SIM and multiple phones

With one phone, `auto` selects the only ready subscription. If multiple active
subscriptions exist, choose the phone/SIM in the Web UI.

Multiple phones use JSON:

```dotenv
ONSIM_ANDROID_GATEWAYS=[{"id":"phone-a","serial":"REPLACE_WITH_FIRST_ADB_SERIAL"},{"id":"phone-b","serial":"REPLACE_WITH_SECOND_ADB_SERIAL"}]
```

Each `id` and `serial` must be unique. onSIM allocates independent control/audio
forward ports when addresses are omitted.

## Acceptance checklist

- Companion reconnects after phone and container reboot.
- The expected SIM and operator appear in **Information**.
- IMS/VoLTE remains registered during a call when the carrier supports it.
- Incoming and outgoing SMS survive reconnects without duplicate events.
- Incoming and outgoing calls have clear audio in both directions.
- Hold, resume, DTMF, mute, remote hangup, and voicemail work.
- Locking the phone does not stop the gateway service.

Audio health must represent actual Telephony Tx/Rx routing, not merely a
successfully created Android `AudioTrack`.
