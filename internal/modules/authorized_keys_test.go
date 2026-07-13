package modules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateKeyLines_Valid(t *testing.T) {
	input := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq test\n"
	got, err := validateKeyLines(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 key, got %d", len(got))
	}
}

func TestValidateKeyLines_MultiKey(t *testing.T) {
	input := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq key1\n" +
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCx8+key2\n"
	got, err := validateKeyLines(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(got))
	}
}

func TestValidateKeyLines_Empty(t *testing.T) {
	_, err := validateKeyLines("")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestValidateKeyLines_BlankLines(t *testing.T) {
	input := "\n\nssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq test\n\n"
	got, err := validateKeyLines(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 key, got %d", len(got))
	}
}

func TestValidateKeyLines_MalformedLine(t *testing.T) {
	input := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq good\n" +
		"not-a-valid-key\n"
	_, err := validateKeyLines(input)
	if err == nil {
		t.Fatal("expected error for malformed key line")
	}
}

func TestContainsKey(t *testing.T) {
	keys := []string{
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq test1",
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCx8+key2 test2",
	}

	if !containsKey(keys, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq different-comment") {
		t.Error("expected match by payload, ignoring comment")
	}
	if containsKey(keys, "ssh-ed25519 DIFFERENTPAYLOAD") {
		t.Error("expected no match for different payload")
	}
	if containsKey(keys, "") {
		t.Error("expected false for empty string")
	}
}

func TestBuildAuthorizedKeysContent_Dedup(t *testing.T) {
	existing := []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq existing"}
	newValid := []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq new-comment"}

	result := buildAuthorizedKeysContent(existing, newValid)
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (deduped), got %d: %q", len(lines), lines)
	}
}

func TestBuildAuthorizedKeysContent_AppendNew(t *testing.T) {
	existing := []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq existing"}
	newValid := []string{"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCx8+newkey"}

	result := buildAuthorizedKeysContent(existing, newValid)
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), lines)
	}
}

func TestReadExistingKeys_NotExists(t *testing.T) {
	tmp := t.TempDir()
	keys, err := readExistingKeys(filepath.Join(tmp, "nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(keys))
	}
}

func TestReadExistingKeys_WithComments(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "authorized_keys")
	content := "# comment\nssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq key1\n\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	keys, err := readExistingKeys(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
}

func TestRejectAuthorizedKeysPath_SymlinkSSHDir(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create symlink .ssh -> /tmp/other
	linkTarget := filepath.Join(tmp, "other_target")
	sshLink := filepath.Join(home, ".ssh")
	if err := os.Symlink(linkTarget, sshLink); err != nil {
		t.Fatal(err)
	}

	err := rejectAuthorizedKeysPath(home, ".ssh")
	if err == nil {
		t.Fatal("expected error for symlinked .ssh")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

func TestRejectAuthorizedKeysPath_SymlinkAuthorizedKeys(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Create symlink authorized_keys -> /dev/null
	linkTarget := filepath.Join(tmp, "other_keys")
	if err := os.Symlink(linkTarget, filepath.Join(sshDir, "authorized_keys")); err != nil {
		t.Fatal(err)
	}

	err := rejectAuthorizedKeysPath(home, ".ssh", "authorized_keys")
	if err == nil {
		t.Fatal("expected error for symlinked authorized_keys")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

func TestRejectAuthorizedKeysPath_NoSymlink(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(sshDir, "authorized_keys")
	if err := os.WriteFile(keyFile, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := rejectAuthorizedKeysPath(home, ".ssh", "authorized_keys")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRejectAuthorizedKeysPath_NonExistentOK(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")

	// .ssh doesn't exist yet — should be OK
	err := rejectAuthorizedKeysPath(home, ".ssh", "authorized_keys")
	if err != nil {
		t.Fatalf("unexpected error for non-existent path: %v", err)
	}
}

func TestResolveHome_EmptyUsername(t *testing.T) {
	_, err := resolveHome("")
	if err == nil {
		t.Fatal("expected error for empty username")
	}
}
