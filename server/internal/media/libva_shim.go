//go:build linux

package media

/*
#cgo linux LDFLAGS: -ldl
int cineva_libva_present(void);
*/
import "C"

// Hardware transcoding needs the *host's* libva, never a bundled one: a
// VA-API driver is version-locked to the libva that loads it (the driver
// exports __vaDriverInit_<major>_<minor> and libva looks up exactly that
// name), so the copy inside the vendored static ffmpeg only matches by luck
// and fails with "unknown libva error" on anything newer.
//
// Linking -lva directly fixes that but makes libva a hard DT_NEEDED entry,
// and the dynamic loader resolves those before main runs: on a host without
// libva the binary does not start at all — no API, no direct play, not even
// `cineva version`. That is the wrong trade for a GPU optimisation, because
// the deployments least likely to have libva (headless servers, slim
// containers) are exactly this server's main targets.
//
// So libva_shim.c defines the entry points ffmpeg references and forwards
// them to a dlopen'd libva at first use. There is no DT_NEEDED entry, the
// binary starts anywhere, the host's own libva is used when present, and
// when it is absent the forwarders report failure so DetectHW falls back to
// software like any other machine without a GPU.
//
// The forwarders resolve against our object, so the linker never pulls the
// bundled libva members out of the archive — an archive member is only
// pulled in to satisfy an *undefined* symbol.

// libvaPresent reports whether the host has a usable libva. Detection
// already copes without it; this exists so the admin UI can say why no
// hardware was found instead of just "none".
func libvaPresent() bool { return C.cineva_libva_present() != 0 }
