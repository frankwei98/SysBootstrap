package modules

import (
	"context"
	"errors"
	"os"
	osuser "os/user"
	"path/filepath"
	"strings"
	"testing"
)

const testAuthorizedKeyPayload = "AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq"

func TestRunAuthorizedKeysHelperPreservesNotFoundIdentity(t *testing.T) {
	sudoStub := filepath.Join(t.TempDir(), "sudo")
	if err := os.WriteFile(sudoStub, []byte("#!/bin/sh\nprintf '%s\\n' '{\"error\":\"authorized_keys is missing\",\"not_found\":true}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	originalSudoPath := authorizedKeysSudoPathFn
	authorizedKeysSudoPathFn = func() (string, error) { return sudoStub, nil }
	t.Cleanup(func() { authorizedKeysSudoPathFn = originalSudoPath })

	_, err := runAuthorizedKeysHelper(context.Background(), "alice", "/home/alice", "rollback", authorizedKeysHelperRequest{})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("helper error = %v, want os.ErrNotExist identity", err)
	}
}

func TestPreflightAuthorizedKeysForUserRequiresSudo(t *testing.T) {
	originalLookup := lookupUser
	originalSudoPath := authorizedKeysSudoPathFn
	lookupUser = func(string) (*osuser.User, error) {
		return &osuser.User{Username: "alice", Uid: "1000"}, nil
	}
	authorizedKeysSudoPathFn = func() (string, error) {
		return "", errors.New("sudo not installed")
	}
	t.Cleanup(func() {
		lookupUser = originalLookup
		authorizedKeysSudoPathFn = originalSudoPath
	})

	if err := preflightAuthorizedKeysForUser("alice"); err == nil {
		t.Fatal("expected missing sudo to fail before user mutation")
	}
}

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

	if _, _, err := appendAuthorizedKeyLinesOnce(keyFile, []string{"ssh-ed25519 " + testAuthorizedKeyPayload}); err == nil {
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
	existing := "# keep\nssh-ed25519 " + testAuthorizedKeyPayload + " owner\n"
	if err := os.WriteFile(keyFile, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	added, retry, err := appendAuthorizedKeyLinesOnce(keyFile, []string{
		"ssh-ed25519 " + testAuthorizedKeyPayload + " duplicate",
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
	if !strings.HasPrefix(string(content), existing) || strings.Count(string(content), testAuthorizedKeyPayload) != 1 {
		t.Fatalf("existing content was not preserved/deduplicated: %q", content)
	}
}

func TestAppendAuthorizedKeyLinesRestoresFileAfterPartialWrite(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "authorized_keys")
	original := []byte("# keep exactly\n")
	if err := os.WriteFile(keyFile, original, 0o600); err != nil {
		t.Fatal(err)
	}

	ops := defaultAuthorizedKeysFileOps
	ops.write = func(f *os.File, payload string) (int, error) {
		n, err := f.WriteString(payload[:len(payload)/2])
		if err != nil {
			return n, err
		}
		return n, errors.New("injected write failure")
	}

	if added, _, err := appendAuthorizedKeyLinesOnceWithOps(keyFile, []string{"ssh-ed25519 " + testAuthorizedKeyPayload}, ops); err == nil {
		t.Fatal("partial write should fail")
	} else if len(added) != 0 {
		t.Fatalf("failed append attributed keys for rollback: %v", added)
	}
	got, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("failed append changed authorized_keys: %q", got)
	}
}

func TestAppendAuthorizedKeyLinesRestoresFileAfterSyncFailure(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "authorized_keys")
	original := []byte("# keep exactly\n")
	if err := os.WriteFile(keyFile, original, 0o600); err != nil {
		t.Fatal(err)
	}

	ops := defaultAuthorizedKeysFileOps
	syncCalls := 0
	ops.sync = func(f *os.File) error {
		syncCalls++
		if syncCalls == 1 {
			return errors.New("injected sync failure")
		}
		return f.Sync()
	}

	if added, _, err := appendAuthorizedKeyLinesOnceWithOps(keyFile, []string{"ssh-ed25519 " + testAuthorizedKeyPayload}, ops); err == nil {
		t.Fatal("sync failure should fail")
	} else if len(added) != 0 {
		t.Fatalf("failed append attributed keys for rollback: %v", added)
	}
	got, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("failed sync changed authorized_keys: %q", got)
	}
}

func TestAppendAuthorizedKeyLinesSnapshotsSizeAfterLease(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "authorized_keys")
	original := []byte("# original\n")
	concurrent := []byte("# opened-before-lease\n")
	if err := os.WriteFile(keyFile, original, 0o600); err != nil {
		t.Fatal(err)
	}

	ops := defaultAuthorizedKeysFileOps
	ops.acquireLease = func(*os.File) error {
		other, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return err
		}
		if _, err := other.Write(concurrent); err != nil {
			_ = other.Close()
			return err
		}
		return other.Close()
	}
	ops.write = func(f *os.File, payload string) (int, error) {
		n, err := f.WriteString(payload[:len(payload)/2])
		if err != nil {
			return n, err
		}
		return n, errors.New("injected write failure")
	}

	if _, _, err := appendAuthorizedKeyLinesOnceWithOps(keyFile, []string{"ssh-ed25519 " + testAuthorizedKeyPayload}, ops); err == nil {
		t.Fatal("partial write should fail")
	}
	got, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte(nil), original...), concurrent...)
	if string(got) != string(want) {
		t.Fatalf("failure recovery removed a pre-lease append: got %q, want %q", got, want)
	}
}

func TestAppendAuthorizedKeyLinesFailsBeforeWriteWithoutLease(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "authorized_keys")
	original := []byte("# keep exactly\n")
	if err := os.WriteFile(keyFile, original, 0o600); err != nil {
		t.Fatal(err)
	}

	ops := defaultAuthorizedKeysFileOps
	ops.acquireLease = func(*os.File) error { return errors.New("lease unavailable") }
	writeCalled := false
	ops.write = func(*os.File, string) (int, error) {
		writeCalled = true
		return 0, nil
	}

	if _, _, err := appendAuthorizedKeyLinesOnceWithOps(keyFile, []string{"ssh-ed25519 " + testAuthorizedKeyPayload}, ops); err == nil {
		t.Fatal("missing write lease should fail")
	}
	if writeCalled {
		t.Fatal("append attempted a write without the lease")
	}
	got, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("lease failure changed authorized_keys: %q", got)
	}
}

func TestRollbackAuthorizedKeyLinesFailsBeforeWriteWithoutLease(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "authorized_keys")
	line := "ssh-ed25519 " + testAuthorizedKeyPayload + " transaction"
	original := []byte(line + "\n")
	if err := os.WriteFile(keyFile, original, 0o600); err != nil {
		t.Fatal(err)
	}

	ops := defaultAuthorizedKeysFileOps
	ops.acquireLease = func(*os.File) error { return errors.New("lease unavailable") }
	changed, _, err := rollbackAuthorizedKeyLinesOnceWithOps(keyFile, map[string]int{line: 1}, ops)
	if err == nil {
		t.Fatal("missing write lease should fail")
	}
	if changed {
		t.Fatal("rollback reported a change without the lease")
	}
	got, readErr := os.ReadFile(keyFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("lease failure changed authorized_keys: %q", got)
	}
}

func TestRollbackAuthorizedKeyLinesDoesNotReplayAfterInodeReplacement(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "authorized_keys")
	replacement := filepath.Join(dir, "replacement")
	line := "ssh-ed25519 " + testAuthorizedKeyPayload + " transaction"
	content := []byte(line + "\n")
	if err := os.WriteFile(keyFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, content, 0o600); err != nil {
		t.Fatal(err)
	}

	ops := defaultAuthorizedKeysFileOps
	replaced := false
	ops.sync = func(f *os.File) error {
		if err := f.Sync(); err != nil {
			return err
		}
		if !replaced {
			replaced = true
			return os.Rename(replacement, keyFile)
		}
		return nil
	}

	changed, err := rollbackAuthorizedKeyLinesWithOps(keyFile, []string{line}, ops)
	if err == nil {
		t.Fatal("inode replacement after rollback should make attribution ambiguous")
	}
	if !changed {
		t.Fatal("rollback should report that it changed the detached inode")
	}
	got, readErr := os.ReadFile(keyFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(content) {
		t.Fatalf("rollback replayed onto the replacement inode: %q", got)
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

func TestContainsKeyMatchesRestrictedAuthorizedKey(t *testing.T) {
	const payload = "AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq"
	existing := []string{
		`from="10.0.0.1",command="echo ssh-ed25519 is restricted" ssh-ed25519 ` + payload + ` owner`,
	}

	if !containsKey(existing, "ssh-ed25519 "+payload+" requested") {
		t.Fatal("same public key with authorized_keys options must be deduplicated")
	}
}

func TestContainsKeyRejectsUnknownAuthorizedKeyOption(t *testing.T) {
	const payload = "AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq"
	existing := []string{"garbage ssh-ed25519 " + payload + " ignored-by-openssh"}

	if containsKey(existing, "ssh-ed25519 "+payload+" requested") {
		t.Fatal("an OpenSSH-invalid option prefix must not satisfy key installation")
	}
}

func TestParseAuthorizedKeyLineRejectsMalformedOptions(t *testing.T) {
	const payload = "AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq"
	invalid := []string{
		`unknown="value" ssh-ed25519 ` + payload,
		`from="unterminated ssh-ed25519 ` + payload,
		`from=unquoted ssh-ed25519 ` + payload,
		`from="ok", ssh-ed25519 ` + payload,
	}
	for _, line := range invalid {
		if key, ok := parseAuthorizedKeyLine(line); ok {
			t.Errorf("parseAuthorizedKeyLine(%q) = %q, want rejection", line, key)
		}
	}
}

func TestBuildAuthorizedKeysContentDoesNotBypassExistingOptions(t *testing.T) {
	const payload = "AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq"
	existing := []string{`from="10.0.0.1" ssh-ed25519 ` + payload + ` restricted`}
	requested := []string{"ssh-ed25519 " + payload + " unrestricted"}

	result := buildAuthorizedKeysContent(existing, requested)
	if strings.Count(result, payload) != 1 {
		t.Fatalf("restricted key was duplicated as an unrestricted key: %q", result)
	}
}

func TestBuildAuthorizedKeysContentDoesNotTreatCAAsLoginKey(t *testing.T) {
	const payload = "AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq"
	existing := []string{"cert-authority ssh-ed25519 " + payload + " user-ca"}
	requested := []string{"ssh-ed25519 " + payload + " direct-login"}

	result := buildAuthorizedKeysContent(existing, requested)
	if strings.Count(result, payload) != 2 {
		t.Fatalf("CA entry incorrectly suppressed the direct login key: %q", result)
	}
}

func TestSelectAuthorizedKeySkipsCommentsAndParsesOptions(t *testing.T) {
	const payload = "AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq"
	content := []byte("# operator note\n\n" +
		`from="10.0.0.1",command="echo restricted key" ssh-ed25519 ` + payload + " owner\n")

	got, err := selectAuthorizedKey(content, "ssh-ed25519 "+payload+" requested")
	if err != nil {
		t.Fatalf("selectAuthorizedKey failed: %v", err)
	}
	if got != "ssh-ed25519 "+payload {
		t.Fatalf("selected key = %q", got)
	}
}

func TestSelectAuthorizedKeyPrefersRequestedKey(t *testing.T) {
	const first = "AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq"
	const requested = "AAAAC3NzaC1lZDI1NTE5AAAAIGZvb29vb29vb29vb29vb29vb29vb29vb29vb29vb29v"
	content := []byte("ssh-ed25519 " + first + " first\nssh-ed25519 " + requested + " second\n")

	got, err := selectAuthorizedKey(content, "ssh-ed25519 "+requested+" reviewed")
	if err != nil {
		t.Fatalf("selectAuthorizedKey failed: %v", err)
	}
	if got != "ssh-ed25519 "+requested {
		t.Fatalf("selected key = %q, want requested key", got)
	}
}

func TestSelectAuthorizedKeyRejectsCAAsDirectLoginKey(t *testing.T) {
	const payload = "AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq"
	content := []byte("cert-authority ssh-ed25519 " + payload + " user-ca\n")

	if got, err := selectAuthorizedKey(content, "ssh-ed25519 "+payload+" direct-login"); err == nil {
		t.Fatalf("CA entry selected as a direct login key: %q", got)
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
