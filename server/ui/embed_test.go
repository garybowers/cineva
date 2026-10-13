package ui

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBuilt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{"built", fstest.MapFS{"app/dist/.gitkeep": {}, "app/dist/index.html": {Data: []byte("<p>app")}}, "<p>app"},
		{"only the .gitkeep", fstest.MapFS{"app/dist/.gitkeep": {}}, "make ui"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := fs.ReadFile(built(tc.files, "app/dist"), "index.html")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tc.want) {
				t.Fatalf("index.html = %q, want it to contain %q", data, tc.want)
			}
		})
	}
}
