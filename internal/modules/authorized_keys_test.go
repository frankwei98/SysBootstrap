package modules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendAuthorizedKeyLinesDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "authorized_keys")
	if err := os.Symlink(victim, keyFile); err != nil {
		t.Fatal(err)
	}

	if _, _, err := appendAuthorizedKeyLinesOnce(keyFile, []string{"ssh-ed25519 AAAA-new"}); err == nil {
		t.Fatal("symlink destination should be rejected")
	}

	victimContent, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(victimContent) != "keep me\n" {
		t.Fatalf("predictable temporary path overwrote another file: %q", victimContent)
	}
}

func TestAppendAuthorizedKeyLinesReturnsOnlyActualAdditions(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "authorized_keys")
	existing := "# keep\nssh-ed25519 AAAA-existing owner\n"
	if err := os.WriteFile(keyFile, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	added, retry, err := appendAuthorizedKeyLinesOnce(keyFile, []string{
		"ssh-ed25519 AAAA-existing duplicate",
		"ssh-rsa AAAA-new owner",
	})
	if err != nil || retry {
		t.Fatalf("append = (%v, retry=%t, %v)", added, retry, err)
	}
	if len(added) != 1 || added[0] != "ssh-rsa AAAA-new owner" {
		t.Fatalf("actual additions = %v", added)
	}
	content, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(content), existing) || strings.Count(string(content), "AAAA-existing") != 1 {
		t.Fatalf("existing content was not preserved/deduplicated: %q", content)
	}
}

func TestEnsureAuthorizedKeysFileLocalTightensPermissions(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	keyFile := filepath.Join(sshDir, "authorized_keys")
	if err := os.Mkdir(sshDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("# keep\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sshDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyFile, 0o666); err != nil {
		t.Fatal(err)
	}

	if err := ensureAuthorizedKeysFileLocal(home, sshDir, keyFile); err != nil {
		t.Fatalf("ensure authorized_keys: %v", err)
	}
	dirInfo, err := os.Stat(sshDir)
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf(".ssh mode = %o, want 700", got)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("authorized_keys mode = %o, want 600", got)
	}
}

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
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq key2\n"
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

func TestValidateKeyLines_RejectsMalformedPayload(t *testing.T) {
	_, err := validateKeyLines("ssh-ed25519 not-valid-base64\n")
	if err == nil {
		t.Fatal("expected malformed SSH key payload to be rejected")
	}
}

func TestValidateGitHubUsername(t *testing.T) {
	for _, username := range []string{"torvalds", "octo-cat", "A1"} {
		if !ValidateGitHubUsername(username) {
			t.Errorf("expected GitHub username %q to be valid", username)
		}
	}
	for _, username := range []string{"-octo", "octo-", "octo--cat", "octo/cat", ""} {
		if ValidateGitHubUsername(username) {
			t.Errorf("expected GitHub username %q to be invalid", username)
		}
	}
}

func TestPublicKeyFingerprints(t *testing.T) {
	input := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq test\n"
	fingerprints, err := PublicKeyFingerprints(input)
	if err != nil {
		t.Fatalf("PublicKeyFingerprints failed: %v", err)
	}
	if len(fingerprints) != 1 {
		t.Fatalf("fingerprint count = %d, want 1", len(fingerprints))
	}
	if !strings.HasPrefix(fingerprints[0], "SHA256:") {
		t.Errorf("fingerprint = %q, want SHA256 fingerprint", fingerprints[0])
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
