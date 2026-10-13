//go:build linux

package media

/*
// Declared here rather than taken from <va/va.h>, which is not part of the
// build: these resolve to the forwarders in libva_shim.c, so they reach the
// host's libva when it is present and fail cleanly when it is not.
int vaMaxNumProfiles(void *dpy);
int vaQueryConfigProfiles(void *dpy, int *profiles, int *num);
int vaMaxNumEntrypoints(void *dpy);
int vaQueryConfigEntrypoints(void *dpy, int profile, int *entrypoints, int *num);

#include <stdlib.h>

#define CINEVA_VA_ENTRYPOINT_VLD 1

// cineva_va_decode_profiles writes the profiles the driver can decode
// (those with a VLD entrypoint) into out, returning how many, or -1 when
// the driver cannot be asked.
static int cineva_va_decode_profiles(void *dpy, int *out, int max) {
	int np = vaMaxNumProfiles(dpy);
	int ne = vaMaxNumEntrypoints(dpy);
	if (np <= 0 || ne <= 0) return -1;
	int *profiles = calloc((size_t)np, sizeof(int));
	int *eps = calloc((size_t)ne, sizeof(int));
	if (!profiles || !eps) { free(profiles); free(eps); return -1; }
	int n = 0, found = 0;
	if (vaQueryConfigProfiles(dpy, profiles, &n) != 0) { free(profiles); free(eps); return -1; }
	for (int i = 0; i < n && found < max; i++) {
		int m = 0;
		if (vaQueryConfigEntrypoints(dpy, profiles[i], eps, &m) != 0) continue;
		for (int j = 0; j < m; j++) {
			if (eps[j] == CINEVA_VA_ENTRYPOINT_VLD) { out[found++] = profiles[i]; break; }
		}
	}
	free(profiles);
	free(eps);
	return found;
}
*/
import "C"

import (
	"slices"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// VAProfile values from <va/va.h>, for the profiles a library is likely to
// hold. The baseline profile of each codec is the one asked about: a chip
// that has it decodes the common case, and a stream in a profile it lacks
// (HEVC Main 10 on an older part, say) is caught at decode time and handed
// to software by the decoder's probation.
var vaDecodeProfiles = map[ffmpeg.AVCodecID][]int{
	ffmpeg.AVCodecIdMpeg2Video: {0, 1},     // MPEG2Simple, MPEG2Main
	ffmpeg.AVCodecIdH264:       {6, 7, 13}, // H264Main, H264High, H264ConstrainedBaseline
	ffmpeg.AVCodecIdVp8:        {14},       // VP8Version0_3
	ffmpeg.AVCodecIdHevc:       {17},       // HEVCMain
	ffmpeg.AVCodecIdVp9:        {19},       // VP9Profile0
	ffmpeg.AVCodecIdAV1:        {32},       // AV1Profile0
}

// vaapiDecodeCodecs asks the VAAPI driver behind an opened device which of
// the codecs above it can decode.
//
// Whether this build has an hwaccel for a codec says nothing about whether
// the chip has the silicon for it: an older GPU has the H.264 and HEVC
// blocks and no AV1 one. Without asking, the startup summary would claim
// AV1 on such a part, and every AV1 playback would try the device, fail on
// the first frame and only then fall back. Returns nil when the driver
// cannot be asked, which callers treat as "unknown" rather than "nothing".
func vaapiDecodeCodecs(ref *ffmpeg.AVBufferRef) map[ffmpeg.AVCodecID]bool {
	if ref == nil || ref.Data() == nil {
		return nil
	}
	// ref.Data() is the AVHWDeviceContext; the binding only wraps such a
	// pointer from an array of them, so it is handed one.
	devPtr := ref.Data()
	dev := ffmpeg.ToAVHWDeviceContextArray(unsafe.Pointer(&devPtr)).Get(0)
	if dev == nil || dev.Hwctx() == nil {
		return nil
	}
	// AVVAAPIDeviceContext begins with its VADisplay.
	display := *(*unsafe.Pointer)(dev.Hwctx())
	if display == nil {
		return nil
	}
	const max = 128
	out := (*C.int)(C.calloc(max, C.size_t(unsafe.Sizeof(C.int(0)))))
	if out == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(out))
	n := int(C.cineva_va_decode_profiles(display, out, max))
	if n < 0 {
		return nil
	}
	have := map[int]bool{}
	for _, p := range unsafe.Slice(out, n) {
		have[int(p)] = true
	}
	codecs := map[ffmpeg.AVCodecID]bool{}
	for id, profiles := range vaDecodeProfiles {
		codecs[id] = slices.ContainsFunc(profiles, func(p int) bool { return have[p] })
	}
	return codecs
}
