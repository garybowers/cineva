# Cineva's container image. It packages a binary built elsewhere — by CI for
# a release, or locally with `make build OUT=dist/cineva-linux-amd64` — rather
# than building one: the static FFmpeg library takes most of an hour to
# compile, and the release binaries already exist.
#
#   docker buildx build --platform linux/amd64,linux/arm64 -t cineva .
#
# The image supplies what the binary takes from its host: glibc (Debian 13
# carries 2.41; release binaries need 2.39 or newer), the C++ runtime, and
# libva with the VA-API drivers, so hardware transcoding works once the GPU
# is passed in with --device /dev/dri.
FROM debian:trixie-slim

ARG TARGETARCH

# mesa-va-drivers covers AMD (and Intel before Broadwell); intel-media-va-
# driver covers Intel from Broadwell on, and exists for amd64 only.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates tzdata libstdc++6 libva2 libva-drm2 mesa-va-drivers \
      $([ "$TARGETARCH" = amd64 ] && echo intel-media-va-driver) \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 1000 --user-group --home-dir /data cineva \
 && mkdir -p /data && chown cineva:cineva /data

COPY --chmod=755 dist/cineva-linux-${TARGETARCH} /usr/local/bin/cineva

# The database, artwork cache and optional cineva.yaml live in /data, the
# working directory — kept apart from the binary, which Viper would
# otherwise try to read as a config file named "cineva".
WORKDIR /data
VOLUME /data
USER cineva
EXPOSE 4747

ENTRYPOINT ["cineva"]
CMD ["serve"]
