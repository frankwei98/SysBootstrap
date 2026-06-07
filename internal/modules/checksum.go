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
	bunVersion = "v1.3.14"

	// Bun release asset SHA256 checksums (from GitHub release SHASUMS256.txt)
	bunLinuxX64SHA256         = "951ee2aee855f08595aeec6225226a298d3fea83a3dcd6465c09cbccdf7e848f"
	bunLinuxX64BaselineSHA256 = "a063908ae08b7852ca10939bbdc6ceed3ddabce8fb9402dce83d65d73b36e6c7"
	bunLinuxAarch64SHA256     = "a27ffb63a8310375836e0d6f668ae17fa8d8d18b88c37c821c65331973a19a3b"

	// nvm install script SHA256 (pinned version)
	nvmInstallSHA256 = "4b7412c49960c7d31e8df72da90c1fb5b8cccb419ac99537b737028d497aba4f"
)

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
