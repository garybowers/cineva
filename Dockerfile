# syntax=docker/dockerfile:1.7
# Cineva's container image, built from source in stages:
#
#   ffmpeg   the static FFmpeg library with our hwaccel patch (CLAUDE.md).
#            The slow stage, most of an hour the first time; cached on the
#            contents of third_party/, so it runs again only when the
#            submodule or the patch changes.
#   ui       the web client, the admin UI and the API reference.
#   build    the Go binary with the UIs embedded.
#   prebuilt a binary built elsewhere, from dist/ (what CI packages: it
#            builds and caches the library per architecture already).
#   final    Debian with the runtime libraries, from one of the two above.
#
#   docker build -t garybowers/cineva .                     from source
#   docker build --build-arg SOURCE=prebuilt -t ... .       CI, from dist/
#   --build-arg VERSION=v0.2.0                              what `cineva version` prints
#   --secret id=tmdb_key,env=TMDB_KEY                       bakes the project's TMDB key in,
#                                                           never in a layer or the history
#
# The final image supplies what the binary takes from its host: glibc
# (Debian 13 carries 2.41; the binary is linked against it here, and
# release binaries need 2.39 or newer), the C++ runtime, and libva with the
# VA-API drivers, so hardware transcoding works once the GPU is passed in
# with --device /dev/dri.

ARG GO_IMAGE=golang:1.27-trixie
ARG NODE_IMAGE=node:22-bookworm-slim
ARG SOURCE=build

# ---- ffmpeg: the static library ----
FROM ${GO_IMAGE} AS ffmpeg
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      yasm nasm meson ninja-build gperf python3 cmake pkg-config curl ca-certificates libssl-dev \
      autoconf automake libtool autopoint gettext texinfo xz-utils bzip2 unzip \
 && rm -rf /var/lib/apt/lists/*
# Rust with cargo-c, for the codecs the builder compiles from Rust.
ENV CARGO_HOME=/usr/local/cargo RUSTUP_HOME=/usr/local/rustup PATH=/usr/local/cargo/bin:$PATH
RUN curl -fsSL https://sh.rustup.rs | sh -s -- -y --profile minimal --no-modify-path \
 && cargo install cargo-c --locked
WORKDIR /src/third_party/ffmpeg-statigo
COPY third_party/patches/ffmpeg-statigo-hwaccel.patch /src/third_party/patches/
COPY third_party/ffmpeg-statigo .
# The submodule's .git is a pointer into the parent repository, which is
# not in the context, so it goes before git apply runs (git apply works on
# a plain tree). Re-applying is a no-op, as the Makefile's rule has it: a
# checkout the host already patched passes the reverse check.
RUN rm -f .git \
 && (git apply --reverse --check /src/third_party/patches/ffmpeg-statigo-hwaccel.patch 2>/dev/null \
     || git apply /src/third_party/patches/ffmpeg-statigo-hwaccel.patch)
RUN --mount=type=cache,target=/go/pkg/mod go run ./internal/builder

# ---- ui: the three front ends ----
FROM ${NODE_IMAGE} AS ui
WORKDIR /src/server/ui
COPY server/ui/client/package.json server/ui/client/package-lock.json client/
COPY server/ui/admin/package.json server/ui/admin/package-lock.json admin/
COPY server/ui/apidocs/package.json server/ui/apidocs/package-lock.json apidocs/
RUN --mount=type=cache,target=/root/.npm \
    npm --prefix client ci && npm --prefix admin ci && npm --prefix apidocs ci
COPY server/ui .
COPY server/docs/openapi.yaml /src/server/docs/openapi.yaml
RUN npm --prefix client run build && npm --prefix admin run build && npm --prefix apidocs run build

# ---- build: the binary ----
FROM ${GO_IMAGE} AS build
ARG VERSION=dev
ARG TARGETOS TARGETARCH
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
COPY third_party/ffmpeg-statigo /src/third_party/ffmpeg-statigo
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY server .
COPY --from=ui /src/server/ui/client/dist ui/client/dist
COPY --from=ui /src/server/ui/admin/dist ui/admin/dist
COPY --from=ui /src/server/ui/apidocs/dist ui/apidocs/dist
COPY --from=ffmpeg /src/third_party/ffmpeg-statigo/lib /src/third_party/ffmpeg-statigo/lib
# The key is read from the secret at link time only; it never lands in a
# layer. Without the secret the binary needs --tmdb-key, the env var or the
# config file, as a plain `make build` does.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=secret,id=tmdb_key \
    key=$(cat /run/secrets/tmdb_key 2>/dev/null || true) \
 && CGO_ENABLED=1 go build -buildvcs=false \
      -ldflags "-X github.com/garybowers/cineva/server/internal/metadata.defaultAPIKey=$key \
                -X github.com/garybowers/cineva/server/internal/version.Version=${VERSION}" \
      -o /cineva .

# ---- prebuilt: a binary from dist/ ----
FROM scratch AS prebuilt
ARG TARGETARCH
COPY dist/cineva-linux-${TARGETARCH} /cineva

# ---- binary: whichever of the two SOURCE names ----
FROM ${SOURCE} AS binary

# ---- final ----
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

COPY --from=binary --chmod=755 /cineva /usr/local/bin/cineva
COPY --chmod=755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# The database, artwork cache and optional cineva.yaml live in /data, the
# working directory — kept apart from the binary, which Viper would
# otherwise try to read as a config file named "cineva". The entrypoint
# starts as root, makes /data belong to PUID:PGID (1000:1000 by default)
# and drops to that user, so a bind-mounted folder owned by root works
# without a chown by hand.
WORKDIR /data
VOLUME /data
EXPOSE 4747

ENTRYPOINT ["docker-entrypoint.sh", "cineva"]
CMD ["serve"]
