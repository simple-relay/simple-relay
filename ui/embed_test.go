package ui

import (
	"testing"
)

func TestEmbeddedAssets(t *testing.T) {
	requiredFiles := []string{
		"index.html",
		"static/js/alpine.min.js",
		"static/css/tailwind.min.css",
	}

	for _, file := range requiredFiles {
		data, err := FS.ReadFile(file)
		if err != nil {
			t.Fatalf("failed to read embedded file %q: %v", file, err)
		}
		if len(data) == 0 {
			t.Errorf("embedded file %q is empty", file)
		}
	}
}
