# Android clients

Standalone Android apps for Cineva live here (see CLAUDE.md: `clients/`
is for client apps, never the server's own UIs). None exists yet; the
toolchain does.

## The toolchain, in a container

`sdk/` builds an image with the JDK, the Android SDK command-line tools,
platform 35 with its build tools, and the emulator with a phone system
image, so nothing is installed on the host:

    docker build -t cineva-android clients/android/sdk

The emulator runs in one of two ways, under one container name, so
`make android` finds it either way. Run as your own user (the volume is
owned by uid 1000; build outputs then are too), from the repository root.

**Headless**, for adb, screenshots and agents:

    docker run -d --name cineva-android --network host --device /dev/kvm \
      --user 1000:1000 -e HOME=/root \
      -v cineva-android-home:/root -v "$PWD:/work" cineva-android

**With a window on the desktop**, to play with the app by hand (the
mouse is a finger; Hyprland's Xwayland lets the same uid in, which is why
the container runs as yours):

    docker run -d --name cineva-android --network host --device /dev/kvm \
      --user 1000:1000 -e HOME=/root -e WINDOW=1 -e DISPLAY=:0 \
      -v /tmp/.X11-unix:/tmp/.X11-unix \
      -v cineva-android-home:/root -v "$PWD:/work" cineva-android

Closing the emulator's window shuts it down; add `--restart unless-stopped`
to the run command and it comes straight back, and `docker stop
cineva-android` stops it for real. `docker rm cineva-android` before
switching modes. Either way `adb` inside the container reaches the device,
and the emulator reaches the host's Cineva at `10.0.2.2:4747`:

    docker exec cineva-android adb wait-for-device shell getprop sys.boot_completed
    docker exec cineva-android adb install -r /work/clients/android/app/build/outputs/apk/debug/app-debug.apk
    docker exec cineva-android adb exec-out screencap -p > screen.png

The app is built in the same container with the Gradle wrapper it ships,
which is what `make android` runs:

    docker exec -w /work/clients/android -e GRADLE_USER_HOME=/root/.gradle -e ANDROID_USER_HOME=/root/.android cineva-android ./gradlew assembleDebug

Both variables point Gradle's cache and the debug signing key at the
volume, whatever user the container runs as, so every build is signed
with the same key and installs over the last one.

The `/root` volume keeps the virtual device and the Gradle caches between
runs; `sdk/emulator.sh` starts the emulator and clears stale lock files.
