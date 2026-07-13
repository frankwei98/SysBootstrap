package modules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	zellijVersion = "v0.44.3"

	// Zellij release asset SHA256 checksums (from GitHub release .sha256sum files)
	zellijLinuxX64SHA256     = "397481870c4fc3bae646cd7613cde3a1cebdc204558a6cb9a7c603d4c852fc90"
	zellijLinuxAarch64SHA256 = "439ed44da5df3cd70e578dc4aef5a67dc7b81eabdddec27969d84a6be380b2f0"

	bunVersion = "v1.3.14"

	// Bun release asset SHA256 checksums (from GitHub release SHASUMS256.txt)
	bunLinuxX64SHA256         = "951ee2aee855f08595aeec6225226a298d3fea83a3dcd6465c09cbccdf7e848f"
	bunLinuxX64BaselineSHA256 = "a063908ae08b7852ca10939bbdc6ceed3ddabce8fb9402dce83d65d73b36e6c7"
	bunLinuxAarch64SHA256     = "a27ffb63a8310375836e0d6f668ae17fa8d8d18b88c37c821c65331973a19a3b"

	// nvm install script SHA256 (pinned version)
	nvmInstallSHA256 = "4b7412c49960c7d31e8df72da90c1fb5b8cccb419ac99537b737028d497aba4f"
)

// zellijAssetForArch maps Go arch to the correct Zellij asset name.
func zellijAssetForArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "zellij-x86_64-unknown-linux-musl.tar.gz"
	case "arm64":
		return "zellij-aarch64-unknown-linux-musl.tar.gz"
	default:
		return ""
	}
}

// zellijSHA256ForArch returns the pinned SHA-256 checksum for the given arch.
func zellijSHA256ForArch(goarch string) string {
	switch goarch {
	case "amd64":
		return zellijLinuxX64SHA256
	case "arm64":
		return zellijLinuxAarch64SHA256
	default:
		return ""
	}
}

// verifyFileSHA256 computes the SHA256 of a file and compares it to the expected hash.
func verifyFileSHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open file for checksum: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("cannot compute checksum: %w", err)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

// shellQuote wraps a string in single quotes for safe use in shell scripts.
// Embedded single quotes are escaped with the standard close-quote/backslash/reopen sequence.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
