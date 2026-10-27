# The Android toolchain in a container: a JDK, the SDK command-line tools,
# a platform and build tools, and the emulator with one phone system image,
# so an app can be built and run without installing any of it on the host.
#
#   docker build -t cineva-android clients/android/sdk
#   docker run -d --name cineva-android --device /dev/kvm --network host \
#     -v cineva-android-home:/root -v "$PWD:/work" cineva-android
#   docker exec cineva-android adb wait-for-device shell getprop sys.boot_completed
#   docker exec cineva-android adb exec-out screencap -p > screen.png
#
# The emulator runs headless (no window) on KVM, so the host passes
# /dev/kvm; without it the image boots at a crawl. The /root volume keeps
# the AVD and Gradle caches between runs. Accepting the SDK licences is
# part of the build: the tools are Google's, under their own terms, and are
# a development tool here, not a part of Cineva.
FROM eclipse-temurin:21-jdk

ENV ANDROID_HOME=/opt/android-sdk \
    ANDROID_SDK_ROOT=/opt/android-sdk \
    PATH=/opt/android-sdk/cmdline-tools/latest/bin:/opt/android-sdk/platform-tools:/opt/android-sdk/emulator:$PATH

# What the emulator binary links against, even headless.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl unzip git \
      libpulse0 libgl1 libnss3 libxcomposite1 libxcursor1 libxi6 libxtst6 \
      libasound2t64 libxkbfile1 libxrandr2 libxdamage1 libxcb-cursor0 libxcb-xinerama0 \
      libxcb-icccm4 libxcb-image0 libxcb-keysyms1 libxcb-render-util0 libxcb-shape0 libfontconfig1 \
 && rm -rf /var/lib/apt/lists/*

ARG CMDLINE_TOOLS=11076708
RUN mkdir -p $ANDROID_HOME/cmdline-tools \
 && curl -fsSL -o /tmp/tools.zip https://dl.google.com/android/repository/commandlinetools-linux-${CMDLINE_TOOLS}_latest.zip \
 && unzip -q /tmp/tools.zip -d $ANDROID_HOME/cmdline-tools \
 && mv $ANDROID_HOME/cmdline-tools/cmdline-tools $ANDROID_HOME/cmdline-tools/latest \
 && rm /tmp/tools.zip

ARG PLATFORM=35
ARG BUILD_TOOLS=35.0.0
ARG SYSTEM_IMAGE="system-images;android-35;google_apis;x86_64"
RUN yes | sdkmanager --licenses > /dev/null \
 && sdkmanager "platform-tools" "platforms;android-${PLATFORM}" "build-tools;${BUILD_TOOLS}" \
      "emulator" "${SYSTEM_IMAGE}" \
 && rm -rf $ANDROID_HOME/.downloadIntermediates

# For the window on a desktop (WINDOW=1): the session libraries the
# emulator's Qt loads only then. A layer of its own so adding them did not
# fetch the SDK again.
RUN apt-get update \
 && apt-get install -y --no-install-recommends libsm6 libice6 libxkbcommon-x11-0 libxcb-xkb1 libegl1 libdbus-1-3 \
 && rm -rf /var/lib/apt/lists/*

COPY emulator.sh /usr/local/bin/emulator.sh
RUN chmod +x /usr/local/bin/emulator.sh

WORKDIR /work
EXPOSE 5555
CMD ["emulator.sh"]
