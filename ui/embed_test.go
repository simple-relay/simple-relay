package ui

import (
	"strings"
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

func TestIndexHTML_VersionAnnotation(t *testing.T) {
	data, err := FS.ReadFile("index.html")
	if err != nil {
		t.Fatalf("failed to read index.html: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "<!-- x-release-please-version -->") {
		t.Errorf("expected index.html to contain release-please version annotation marker")
	}
	if strings.Contains(content, "v0.2.0") {
		t.Errorf("expected hardcoded v0.2.0 to be removed from index.html")
	}
}
