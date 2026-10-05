package media

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The shim is what satisfies the vendored ffmpeg's undefined libva
// references, which is how the binary avoids a DT_NEEDED entry on libva.
//
// A *missing* forwarder is already caught by the linker — removing one fails
// the build with "undefined reference", verified. What this adds is the
// readable version of that invariant: it names every symbol involved and
// counts them, so when the vendored ffmpeg is rebuilt the diff in this
// test's output says what changed rather than leaving someone to read a
// linker error. It also catches the reverse, which nothing else does:
// forwarders kept for symbols no longer referenced.
//
// Skipped where `nm` or the archive is absent, so a bare checkout still runs
// the suite.
func TestLibvaShimCoversEveryReferencedSymbol(t *testing.T) {
	archive := filepath.Join("..", "..", "..", "third_party", "ffmpeg-statigo", "lib", "linux_amd64", "libffmpeg.a")
	if _, err := exec.LookPath("nm"); err != nil {
		t.Skip("nm not available")
	}
	out, err := exec.Command("nm", "-u", archive).Output()
	if err != nil {
		t.Skipf("cannot read %s: %v", archive, err)
	}

	symRe := regexp.MustCompile(`\bva[A-Z][A-Za-z0-9]*`)
	needed := map[string]bool{}
	for _, m := range symRe.FindAllString(string(out), -1) {
		needed[m] = true
	}
	if len(needed) == 0 {
		t.Skip("archive references no libva symbols")
	}

	shim, err := exec.Command("grep", "-oE", `VA_FORWARD\(va[A-Za-z0-9]+`, "libva_shim.c").Output()
	if err != nil {
		t.Fatalf("read shim: %v", err)
	}
	have := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(shim)), "\n") {
		if name := strings.TrimPrefix(line, "VA_FORWARD("); name != "" {
			have[name] = true
		}
	}

	var missing []string
	for sym := range needed {
		if !have[sym] {
			missing = append(missing, sym)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("libva_shim.c does not forward %d symbol(s) the vendored ffmpeg references: %s\n"+
			"Add a VA_FORWARD line for each, with the right failure value.",
			len(missing), strings.Join(missing, ", "))
	}

	// Stale forwarders are harmless but misleading: they suggest the shim
	// supports more than ffmpeg actually asks for.
	var unused []string
	for sym := range have {
		if !needed[sym] {
			unused = append(unused, sym)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Logf("shim forwards %d symbol(s) the archive no longer references: %s",
			len(unused), strings.Join(unused, ", "))
	}
	t.Logf("%d libva symbols referenced, %d forwarded", len(needed), len(have))
}
