package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func createTestTarball(binaryContent []byte) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	header := &tar.Header{
		Name:     "simple-relay",
		Mode:     0755,
		Size:     int64(len(binaryContent)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(header); err != nil {
		return nil, err
	}
	if _, err := tw.Write(binaryContent); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func TestInstallScript_MockServer(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("install.sh is designed for Linux and macOS")
	}

	mockBinary := []byte("#!/bin/sh\nif [ \"$1\" = \"--help\" ]; then echo \"mock simple-relay ok\"; exit 0; fi\necho \"running\"; exit 0;\n")
	tarballBytes, err := createTestTarball(mockBinary)
	if err != nil {
		t.Fatalf("failed to create test tarball: %v", err)
	}

	h := sha256.New()
	h.Write(tarballBytes)
	tarballHash := hex.EncodeToString(h.Sum(nil))

	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		t.Skip("install.sh supports amd64 and arm64")
	}

	archiveName := fmt.Sprintf("simple-relay_1.2.3_%s_%s.tar.gz", runtime.GOOS, arch)
	checksumContent := fmt.Sprintf("%s  %s\n", tarballHash, archiveName)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/simple-relay/simple-relay/releases/latest":
			http.Redirect(w, r, "/simple-relay/simple-relay/releases/tag/v1.2.3", http.StatusFound)
		case r.URL.Path == "/simple-relay/simple-relay/releases/tag/v1.2.3":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/simple-relay/simple-relay/releases/download/v1.2.3/checksums.txt":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(checksumContent))
		case r.URL.Path == "/simple-relay/simple-relay/releases/download/v1.2.3/"+archiveName:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(tarballBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Locate install.sh relative to repo root
	installScriptPath := filepath.Join("..", "..", "install.sh")
	absScript, err := filepath.Abs(installScriptPath)
	if err != nil {
		t.Fatalf("failed to resolve install.sh: %v", err)
	}

	t.Run("successful install and symlink", func(t *testing.T) {
		tempBinDir := t.TempDir()

		cmd := exec.Command("sh", absScript)
		cmd.Env = append(os.Environ(),
			"GITHUB_HOST="+server.URL,
			"GITHUB_REPO=simple-relay/simple-relay",
			"BINDIR="+tempBinDir,
		)

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("install.sh failed: %v\nOutput: %s", err, string(output))
		}

		outStr := string(output)
		if !strings.Contains(outStr, "installed successfully") {
			t.Errorf("expected success message in output, got: %s", outStr)
		}

		// Verify binary was installed and is executable
		installedBin := filepath.Join(tempBinDir, "simple-relay")
		fi, err := os.Stat(installedBin)
		if err != nil {
			t.Fatalf("expected binary at %s, error: %v", installedBin, err)
		}
		if fi.Mode()&0111 == 0 {
			t.Errorf("binary %s is not executable", installedBin)
		}

		// Verify symlink was created
		symlink := filepath.Join(tempBinDir, "relay")
		symInfo, err := os.Lstat(symlink)
		if err != nil {
			t.Fatalf("expected symlink at %s, error: %v", symlink, err)
		}
		if symInfo.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is not a symlink", symlink)
		}

		target, err := os.Readlink(symlink)
		if err != nil {
			t.Fatalf("failed to read symlink target: %v", err)
		}
		if target != "simple-relay" && target != installedBin {
			t.Errorf("expected symlink target 'simple-relay', got '%s'", target)
		}
	})

	t.Run("checksum failure aborts", func(t *testing.T) {
		tempBinDir := t.TempDir()

		badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/simple-relay/simple-relay/releases/latest":
				http.Redirect(w, r, "/simple-relay/simple-relay/releases/tag/v1.2.3", http.StatusFound)
			case r.URL.Path == "/simple-relay/simple-relay/releases/tag/v1.2.3":
				w.WriteHeader(http.StatusOK)
			case r.URL.Path == "/simple-relay/simple-relay/releases/download/v1.2.3/checksums.txt":
				w.WriteHeader(http.StatusOK)
				// Corrupted checksum
				_, _ = w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  " + archiveName + "\n"))
			case r.URL.Path == "/simple-relay/simple-relay/releases/download/v1.2.3/"+archiveName:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(tarballBytes)
			default:
				http.NotFound(w, r)
			}
		}))
		defer badServer.Close()

		cmd := exec.Command("sh", absScript)
		cmd.Env = append(os.Environ(),
			"GITHUB_HOST="+badServer.URL,
			"GITHUB_REPO=simple-relay/simple-relay",
			"BINDIR="+tempBinDir,
		)

		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected install.sh to fail on bad checksum, but succeeded!\nOutput: %s", string(output))
		}
		if !strings.Contains(string(output), "checksum") {
			t.Errorf("expected checksum error in output, got: %s", string(output))
		}
	})
}
