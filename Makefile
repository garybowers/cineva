# A fresh clone needs the vendored ffmpeg submodule and its static library
# (not in git) before anything builds; every target below depends on it.
# `make setup` additionally installs the npm deps needed by `make ui`.
#
# The binary goes in bin/, never in server/: Viper probes the extensionless
# name "cineva" in the working directory, so a binary at server/cineva gets
# parsed as the config file and the server dies on startup.

STATIGO := third_party/ffmpeg-statigo
LIB := $(STATIGO)/lib/$(shell go env GOOS)_$(shell go env GOARCH)/libffmpeg.a
PATCH := $(abspath third_party/patches/ffmpeg-statigo-hwaccel.patch)

# The project's TMDB key is injected at build time rather than committed, so
# the repository holds no credential. Release builds set it; a plain `make
# build` leaves it empty and the server then needs --tmdb-key, the env var,
# or the config file. Pass it as `make build TMDB_KEY=...`.
# Credentials live in local.mk, which is gitignored — never in this
# file, which is committed.
-include local.mk
TMDB_KEY ?=
# What `cineva version` prints: the tag being built (v0.2.0), or the nearest
# tag plus commits and a dirty marker between releases. CI passes the tag.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/garybowers/cineva/server/internal/metadata.defaultAPIKey=$(TMDB_KEY) \
	-X github.com/garybowers/cineva/server/internal/version.Version=$(VERSION)
# CI builds one binary per architecture and names them apart.
OUT ?= bin/cineva
GOLANGCI_LINT := github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

# The recipe is silenced with @ because $(LDFLAGS) carries the TMDB key:
# make echoes every command it runs, which puts the key in the terminal, in
# scrollback, and in any CI log that ever builds this.
build: $(LIB) ui
	@echo "building $(OUT) $(VERSION)"
	@cd server && go build -ldflags "$(LDFLAGS)" -o $(abspath $(OUT)) .

run: $(LIB)
	@cd server && go run -ldflags "$(LDFLAGS)" . serve --log-level debug

test: $(LIB)
	cd server && go test ./...

lint: $(LIB)
	cd server && out=$$(gofmt -l .); [ -z "$$out" ] || { echo "$$out"; exit 1; }
	cd server && go vet ./...
	cd server && go run $(GOLANGCI_LINT) run ./...

# The built UIs are not committed; `make build` runs this first so the binary
# embeds them.
ui:
	npm --prefix server/ui/client run build
	npm --prefix server/ui/admin run build
	npm --prefix server/ui/apidocs run build

setup: $(LIB)
	npm --prefix server/ui/client ci
	npm --prefix server/ui/admin ci
	npm --prefix server/ui/apidocs ci

# Built from source rather than downloaded: upstream enables no hwaccels on
# Linux, so its prebuilt library cannot hardware-decode on VAAPI (AMD, and
# Intel via VAAPI). $(PATCH) adds them. Re-applying is a no-op, so this
# survives a rebuild without needing a clean submodule.
$(LIB):
	git submodule update --init
	git -C $(STATIGO) apply --reverse --check $(PATCH) 2>/dev/null || git -C $(STATIGO) apply $(PATCH)
	cd $(STATIGO) && go run ./internal/builder

clean:
	rm -rf bin

.PHONY: build run test lint ui setup clean
