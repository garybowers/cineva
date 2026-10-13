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
LDFLAGS := -X github.com/garybowers/cineva/server/internal/metadata.defaultAPIKey=$(TMDB_KEY)

# The recipe is silenced with @ because $(LDFLAGS) carries the TMDB key:
# make echoes every command it runs, which puts the key in the terminal, in
# scrollback, and in any CI log that ever builds this.
build: $(LIB)
	@echo "building bin/cineva"
	@cd server && go build -ldflags "$(LDFLAGS)" -o ../bin/cineva .

run: $(LIB)
	@cd server && go run -ldflags "$(LDFLAGS)" . serve

test: $(LIB)
	cd server && go test ./...

lint: $(LIB)
	cd server && out=$$(gofmt -l .); [ -z "$$out" ] || { echo "$$out"; exit 1; }
	cd server && go vet ./...

ui:
	npm --prefix server/ui/client run build
	npm --prefix server/ui/admin run build

setup: $(LIB)
	npm --prefix server/ui/client ci
	npm --prefix server/ui/admin ci

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
