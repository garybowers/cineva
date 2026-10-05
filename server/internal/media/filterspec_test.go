package media

import "testing"

func TestVideoFilterSpec(t *testing.T) {
	tests := []struct {
		name        string
		hdr         string
		wantScale   string
		wantTonemap bool
		wantTin     string
	}{
		{name: "sdr is a plain scale", hdr: "", wantScale: "scale=1920:1080"},
		{name: "pq tone maps from smpte2084", hdr: "pq", wantScale: "scale=1920:1080", wantTonemap: true, wantTin: "tin=smpte2084"},
		{name: "hlg tone maps from arib-std-b67", hdr: "hlg", wantScale: "scale=1920:1080", wantTonemap: true, wantTin: "tin=arib-std-b67"},
		{name: "unknown transfer is treated as sdr", hdr: "dolbyvision", wantScale: "scale=1920:1080"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := videoFilterSpec(tc.hdr, 1920, 1080)
			if !contains(got, tc.wantScale) {
				t.Errorf("spec %q missing %q", got, tc.wantScale)
			}
			if tc.wantTonemap {
				for _, want := range []string{tc.wantTin, "tonemap=hable", "desat=0", "zscale=p=bt709", "t=bt709:m=bt709:r=tv"} {
					if !contains(got, want) {
						t.Errorf("spec %q missing %q", got, want)
					}
				}
				// The scale must come last so the tone map runs at full
				// resolution and the downscale averages mapped values.
				if got[len(got)-len(tc.wantScale):] != tc.wantScale {
					t.Errorf("spec %q does not end in the scale", got)
				}
			} else if got != tc.wantScale {
				t.Errorf("spec = %q, want exactly %q", got, tc.wantScale)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
