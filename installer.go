package antigravityacp

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

const (
	GithubRepo = "google-antigravity/antigravity-cli"
	AgyVersion = "1.0.13"
)

type Release struct {
	Asset  string
	Sha256 string
	Kind   string
}

var Releases = map[string]Release{
	"darwin-arm64": {
		Asset:  "agy_cli_mac_arm64.tar.gz",
		Sha256: "c00b3aa10d4eee821f7ddaf3185942c88511ebbe425663692f7732d8dd1e83c2",
		Kind:   "tar.gz",
	},
	"darwin-x64": {
		Asset:  "agy_cli_mac_x64.tar.gz",
		Sha256: "53e23ef3f54d0212df7fc73ea1eb99c34e4c97bffa1f886afe565fd142c9ab89",
		Kind:   "tar.gz",
	},
	"linux-arm64": {
		Asset:  "agy_cli_linux_arm64.tar.gz",
		Sha256: "e2f062ff8a573d2da54c03c8f0b66e130a563a08c87b6db174953a9afdd21235",
		Kind:   "tar.gz",
	},
	"linux-x64": {
		Asset:  "agy_cli_linux_x64.tar.gz",
		Sha256: "6bf990458c114af3b3173dcbc1b0fb9ab93bea91c53b605fdd69aedd29a21cd9",
		Kind:   "tar.gz",
	},
	"win32-arm64": {
		Asset:  "agy_cli_windows_arm64.zip",
		Sha256: "e6a6fb4c9703cdd51c5d2c107b724e5bc5654a2850c8e1d737ee906ed5facdf8",
		Kind:   "zip",
	},
	"win32-x64": {
		Asset:  "agy_cli_windows_x64.zip",
		Sha256: "ca397c0f07157b6f38a2e11a3c9e97ef56c24ec4238a0deef6a5dd390dee1836",
		Kind:   "zip",
	},
}

func ReleaseURL(asset string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", GithubRepo, AgyVersion, asset)
}

func extractTarGz(archive io.Reader, destDir string) error {
	gr, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, header.FileInfo().Mode())
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

func extractZip(archiveBytes []byte, destDir string) error {
	zr, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		target := filepath.Join(destDir, f.Name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		src, err := f.Open()
		if err != nil {
			dst.Close()
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			dst.Close()
			return err
		}
		src.Close()
		dst.Close()
	}
	return nil
}

func findBinary(dir, name string, maxDepth int) string {
	if maxDepth < 0 {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		full := filepath.Join(dir, entry.Name())
		if !entry.IsDir() && entry.Name() == name {
			return full
		}
		if entry.IsDir() {
			found := findBinary(full, name, maxDepth-1)
			if found != "" {
				return found
			}
		}
	}
	return ""
}

type InstallOptions struct {
	DestDir string
	Log     func(msg string)
	Warn    func(msg string)
}

func EnsureAgy(opts InstallOptions) error {
	if os.Getenv("AGY_SKIP_DOWNLOAD") == "1" {
		opts.Log("[agy-acp] skipping agy download (AGY_SKIP_DOWNLOAD=1)")
		return nil
	}
	if os.Getenv("AGY_BIN") != "" {
		opts.Log(fmt.Sprintf("[agy-acp] using $AGY_BIN=%s, skipping download", os.Getenv("AGY_BIN")))
		return nil
	}

	platformKey := runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		platformKey = "darwin-x64"
	} else if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		platformKey = "linux-x64"
	} else if runtime.GOOS == "windows" {
		if runtime.GOARCH == "amd64" {
			platformKey = "win32-x64"
		} else if runtime.GOARCH == "arm64" {
			platformKey = "win32-arm64"
		}
	}

	release, exists := Releases[platformKey]
	if !exists {
		opts.Warn(fmt.Sprintf("[agy-acp] WARN: unsupported platform %s. Set $AGY_BIN to your agy binary path.", platformKey))
		return nil
	}

	exeName := "agy"
	if runtime.GOOS == "windows" {
		exeName = "agy.exe"
	}
	dest := filepath.Join(opts.DestDir, exeName)

	if fi, err := os.Stat(dest); err == nil && !fi.IsDir() {
		// already present, executable check not strictly necessary on windows, on Unix we can check mode but usually os.Stat existence is fine
		opts.Log(fmt.Sprintf("[agy-acp] agy already present (%s)", dest))
		return nil
	}

	url := ReleaseURL(release.Asset)
	opts.Log(fmt.Sprintf("[agy-acp] agy not found — downloading v%s for %s...", AgyVersion, platformKey))

	resp, err := http.Get(url)
	if err != nil {
		opts.Warn(fmt.Sprintf("[agy-acp] WARN: network error downloading agy: %v. Set $AGY_BIN as a workaround.", err))
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		opts.Warn(fmt.Sprintf("[agy-acp] WARN: HTTP %d downloading agy from %s. Set $AGY_BIN as a workaround.", resp.StatusCode, url))
		return nil
	}

	archiveBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		opts.Warn(fmt.Sprintf("[agy-acp] WARN: error reading archive: %v. Set $AGY_BIN as a workaround.", err))
		return nil
	}

	h := sha256.New()
	h.Write(archiveBytes)
	actualHash := fmt.Sprintf("%x", h.Sum(nil))

	if actualHash != release.Sha256 {
		opts.Warn(fmt.Sprintf("[agy-acp] WARN: SHA256 mismatch for %s\n  expected: %s\n  got:      %s\n  Refusing to install — set $AGY_BIN as a workaround.", release.Asset, release.Sha256, actualHash))
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "agy-acp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	extractDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return err
	}

	if release.Kind == "tar.gz" {
		if err := extractTarGz(bytes.NewReader(archiveBytes), extractDir); err != nil {
			opts.Warn(fmt.Sprintf("[agy-acp] WARN: tar extract error: %v. Set $AGY_BIN as a workaround.", err))
			return nil
		}
	} else {
		if err := extractZip(archiveBytes, extractDir); err != nil {
			opts.Warn(fmt.Sprintf("[agy-acp] WARN: zip extract error: %v. Set $AGY_BIN as a workaround.", err))
			return nil
		}
	}

	found := findBinary(extractDir, exeName, 2)
	if found == "" {
		opts.Warn(fmt.Sprintf("[agy-acp] WARN: could not locate %s inside %s. Set $AGY_BIN as a workaround.", exeName, release.Asset))
		return nil
	}

	if err := os.MkdirAll(opts.DestDir, 0755); err != nil {
		return err
	}

	srcFile, err := os.Open(found)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dest, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	opts.Log(fmt.Sprintf("[agy-acp] agy v%s installed → %s", AgyVersion, dest))
	return nil
}
