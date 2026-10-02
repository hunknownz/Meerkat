package web

import (
	"io/fs"
	"testing"
)

func TestAssetsContainBundles(t *testing.T) {
	for _, name := range []string{"app/index.html", "mount/meerkat-ui.js", "mount/meerkat-ui.css"} {
		if b, err := fs.ReadFile(Assets(), name); err != nil || len(b) == 0 {
			t.Fatalf("missing embedded asset %s: %v", name, err)
		}
	}
}
