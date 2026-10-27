#!/bin/sh
# Starts the emulator headless on KVM, making the virtual device first if
# the volume has none. AVD names the device; IMAGE the system image it runs.
set -e
# The SDK tools take the Java user home for the AVD folder, which for a
# container run as another uid is not $HOME: name it outright so the
# device is made and found on the volume whoever runs this.
export ANDROID_USER_HOME="$HOME/.android"
export ANDROID_AVD_HOME="$HOME/.android/avd"
mkdir -p "$ANDROID_AVD_HOME"
AVD=${AVD:-cineva}
IMAGE=${IMAGE:-"system-images;android-35;google_apis;x86_64"}
if ! avdmanager list avd 2>/dev/null | grep -q "Name: $AVD\$"; then
  echo no | avdmanager create avd -n "$AVD" -k "$IMAGE" -d pixel_6 > /dev/null
fi
# A container stopped without shutting the emulator down leaves lock
# files on the volume that make the next start refuse "a second emulator".
rm -f "$HOME/.android/avd/$AVD.avd/"*.lock 2>/dev/null
# WINDOW=1 shows the emulator on the host's desktop (the X socket is
# mounted and DISPLAY set by the run command in the README) for playing
# with an app by hand; otherwise it runs headless for adb and screenshots.
if [ "${WINDOW:-0}" = "1" ]; then
  exec emulator -avd "$AVD" -no-audio -no-boot-anim -no-snapshot \
    -gpu swiftshader_indirect -accel on -memory 2048 -cores 4
fi
exec emulator -avd "$AVD" -no-window -no-audio -no-boot-anim -no-snapshot \
  -gpu swiftshader_indirect -accel on -memory 2048 -cores 4
