package modules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyFileSHA256(t *testing.T) {
	// Create a temp file with known content
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := []byte("hello world\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// SHA256 of "hello world\n"
	expected := "a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447"

	// Correct hash should pass
	if err := verifyFileSHA256(path, expected); err != nil {
		t.Errorf("verifyFileSHA256 with correct hash: unexpected error: %v", err)
	}

	// Wrong hash should fail
	if err := verifyFileSHA256(path, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Error("verifyFileSHA256 with wrong hash: expected error, got nil")
	}

	// Non-existent file should fail
	if err := verifyFileSHA256(filepath.Join(dir, "nonexistent"), expected); err == nil {
		t.Error("verifyFileSHA256 with missing file: expected error, got nil")
	}
}

func TestBunAssetForArch(t *testing.T) {
	tests := []struct {
		arch      string
		wantAsset string
		wantErr   bool
	}{
		{"amd64", "bun-linux-x64.zip", false},
		{"linux/amd64", "bun-linux-x64.zip", false},
		{"x86_64", "bun-linux-x64.zip", false},
		{"arm64", "bun-linux-aarch64.zip", false},
		{"linux/arm64", "bun-linux-aarch64.zip", false},
		{"aarch64", "bun-linux-aarch64.zip", false},
		{"darwin/arm64", "", true},
		{"386", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.arch, func(t *testing.T) {
			asset, _, err := bunAssetForArch(tt.arch)
			if (err != nil) != tt.wantErr {
				t.Errorf("bunAssetForArch(%q) error = %v, wantErr %v", tt.arch, err, tt.wantErr)
				return
			}
			if asset != tt.wantAsset {
				t.Errorf("bunAssetForArch(%q) asset = %q, want %q", tt.arch, asset, tt.wantAsset)
			}
		})
	}
}

func TestBunAssetForNormalizedArchBaseline(t *testing.T) {
	asset, checksum, err := bunAssetForNormalizedArch("amd64", true)
	if err != nil {
		t.Fatalf("bunAssetForNormalizedArch baseline failed: %v", err)
	}
	if asset != "bun-linux-x64-baseline.zip" {
		t.Fatalf("asset = %q, want baseline", asset)
	}
	if checksum != bunLinuxX64BaselineSHA256 {
		t.Fatalf("checksum = %q, want baseline checksum", checksum)
	}
}

func TestCPUInfoHasAVX2(t *testing.T) {
	if !cpuInfoHasAVX2("flags\t: fpu sse avx avx2 bmi1") {
		t.Fatal("cpuInfoHasAVX2 should detect avx2 flag")
	}
	if cpuInfoHasAVX2("flags\t: fpu sse avx avx512") {
		t.Fatal("cpuInfoHasAVX2 should not match partial flag names")
	}
}

func TestRejectSymlinkPaths(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real")
	if err := os.Mkdir(realPath, 0o755); err != nil {
		t.Fatalf("mkdir real path: %v", err)
	}
	if err := rejectSymlinkPaths(realPath, filepath.Join(dir, "missing")); err != nil {
		t.Fatalf("rejectSymlinkPaths should allow real and missing paths: %v", err)
	}

	linkPath := filepath.Join(dir, "link")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}
	if err := rejectSymlinkPaths(linkPath); err == nil {
		t.Fatal("rejectSymlinkPaths should reject symlink")
	}
}

func TestNvmInstallSHA256Constant(t *testing.T) {
	// Verify the constant is a valid 64-char hex string
	if len(nvmInstallSHA256) != 64 {
		t.Errorf("nvmInstallSHA256 length = %d, want 64", len(nvmInstallSHA256))
	}
	for _, c := range nvmInstallSHA256 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("nvmInstallSHA256 contains non-hex char: %c", c)
			break
		}
	}
}

func TestBunSHA256Constants(t *testing.T) {
	constants := map[string]string{
		"bunLinuxX64SHA256":         bunLinuxX64SHA256,
		"bunLinuxX64BaselineSHA256": bunLinuxX64BaselineSHA256,
		"bunLinuxAarch64SHA256":     bunLinuxAarch64SHA256,
	}
	for name, val := range constants {
		t.Run(name, func(t *testing.T) {
			if len(val) != 64 {
				t.Errorf("%s length = %d, want 64", name, len(val))
			}
			for _, c := range val {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Errorf("%s contains non-hex char: %c", name, c)
					break
				}
			}
		})
	}
}
