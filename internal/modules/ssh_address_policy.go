package modules

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/frankwei98/sys-bootstrap/internal/types"
)

type sshAddressPolicyConflict struct {
	path      string
	line      int
	directive string
}

type sshAddressPolicyScanner struct {
	includeRoot string
	wanted      map[string]bool
	stack       map[string]bool
}

func rejectAddressDependentSSHAuthPolicy(cfg *types.Config) error {
	if cfg == nil || (!cfg.SSHDisableRoot && !cfg.SSHDisablePass) {
		return nil
	}
	if _, err := os.Lstat(sshConfigPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot inspect SSH configuration %s: %w", sshConfigPath, err)
	}
	if _, err := os.Stat(sshConfigPath); err != nil {
		return fmt.Errorf("cannot inspect SSH configuration %s: %w", sshConfigPath, err)
	}
	wanted := make(map[string]bool)
	if cfg.SSHDisableRoot {
		wanted["permitrootlogin"] = true
	}
	if cfg.SSHDisablePass {
		wanted["passwordauthentication"] = true
		wanted["kbdinteractiveauthentication"] = true
		wanted["challengeresponseauthentication"] = true
		wanted["skeyauthentication"] = true
	}

	conflict, err := findSSHAddressPolicyConflict(sshConfigPath, wanted)
	if err != nil {
		return fmt.Errorf("cannot inspect address-dependent SSH authentication policy: %w", err)
	}
	if conflict == nil {
		return nil
	}
	return fmt.Errorf(
		"cannot safely enforce SSH authentication: Match Address controls %s at %s:%d; remove the address-dependent override or harden SSH manually",
		conflict.directive,
		conflict.path,
		conflict.line,
	)
}

func findSSHAddressPolicyConflict(rootPath string, wanted map[string]bool) (*sshAddressPolicyConflict, error) {
	scanner := sshAddressPolicyScanner{
		includeRoot: filepath.Dir(rootPath),
		wanted:      wanted,
		stack:       make(map[string]bool),
	}
	return scanner.scanFile(rootPath, false, 0)
}

func (s *sshAddressPolicyScanner) scanFile(path string, addressDependent bool, depth int) (*sshAddressPolicyConflict, error) {
	if depth > 16 {
		return nil, fmt.Errorf("too many recursive SSH Includes")
	}
	enclosingAddressDependent := addressDependent
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve SSH config path %s: %w", path, err)
	}
	if resolvedPath, resolveErr := filepath.EvalSymlinks(canonicalPath); resolveErr == nil {
		canonicalPath = resolvedPath
	} else if !os.IsNotExist(resolveErr) {
		return nil, fmt.Errorf("resolve SSH config symlinks for %s: %w", path, resolveErr)
	}
	if s.stack[canonicalPath] {
		return nil, fmt.Errorf("recursive SSH Include involving %s", canonicalPath)
	}
	s.stack[canonicalPath] = true
	defer delete(s.stack, canonicalPath)

	file, err := os.Open(canonicalPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		fields, err := splitSSHConfigFields(scanner.Text())
		if err != nil {
			return nil, fmt.Errorf("parse %s:%d: %w", canonicalPath, lineNumber, err)
		}
		if len(fields) == 0 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "match":
			dependsOnAddress, err := sshMatchDependsOnAddress(fields)
			if err != nil {
				return nil, fmt.Errorf("parse %s:%d: %w", canonicalPath, lineNumber, err)
			}
			addressDependent = enclosingAddressDependent || dependsOnAddress
		case "include":
			for _, pattern := range fields[1:] {
				if strings.HasPrefix(pattern, "~") {
					return nil, fmt.Errorf("tilde-based SSH Include %q at %s:%d cannot be inspected safely", pattern, canonicalPath, lineNumber)
				}
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(s.includeRoot, pattern)
				}
				matches, err := openSSHGlob(pattern)
				if err != nil {
					return nil, fmt.Errorf("expand SSH Include %q at %s:%d: %w", pattern, canonicalPath, lineNumber, err)
				}
				for _, match := range matches {
					conflict, err := s.scanFile(match, addressDependent, depth+1)
					if err != nil || conflict != nil {
						return conflict, err
					}
				}
			}
		default:
			keyword := strings.ToLower(fields[0])
			if addressDependent && s.wanted[keyword] {
				return &sshAddressPolicyConflict{path: canonicalPath, line: lineNumber, directive: fields[0]}, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SSH config %s: %w", canonicalPath, err)
	}
	return nil, nil
}

func openSSHGlob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	patternParts := strings.Split(filepath.Clean(pattern), string(filepath.Separator))
	filtered := matches[:0]
	for _, match := range matches {
		matchParts := strings.Split(filepath.Clean(match), string(filepath.Separator))
		if len(matchParts) != len(patternParts) {
			continue
		}
		included := true
		for index := range matchParts {
			if strings.HasPrefix(matchParts[index], ".") && !strings.HasPrefix(patternParts[index], ".") {
				included = false
				break
			}
		}
		if included {
			filtered = append(filtered, match)
		}
	}
	return filtered, nil
}

func sshMatchDependsOnAddress(fields []string) (bool, error) {
	if len(fields) < 2 {
		return false, fmt.Errorf("Match requires criteria")
	}
	if len(fields) == 2 && (strings.EqualFold(fields[1], "all") || strings.EqualFold(fields[1], "invalid-user")) {
		return false, nil
	}
	addressDependent := false
	for index := 1; index < len(fields); {
		criterion := strings.ToLower(fields[index])
		if criterion == "invalid-user" {
			index++
			continue
		}
		if index+1 >= len(fields) {
			return false, fmt.Errorf("Match criterion %s requires a pattern", fields[index])
		}
		switch criterion {
		case "user", "group", "host", "localaddress", "localport", "version", "rdomain":
		case "address":
			addressDependent = true
		default:
			return false, fmt.Errorf("unsupported Match criterion %s", fields[index])
		}
		index += 2
	}
	return addressDependent, nil
}

func splitSSHConfigFields(line string) ([]string, error) {
	content := strings.TrimLeftFunc(line, unicode.IsSpace)
	if content == "" || strings.HasPrefix(content, "#") {
		return nil, nil
	}
	keywordEnd := len(content)
	separator := rune(0)
	for index, char := range content {
		if char == '=' || unicode.IsSpace(char) {
			keywordEnd = index
			separator = char
			break
		}
	}
	keyword := content[:keywordEnd]
	if keyword == "" {
		return nil, fmt.Errorf("missing keyword")
	}
	arguments := content[keywordEnd:]
	if separator == '=' {
		arguments = arguments[1:]
	} else if separator != 0 {
		arguments = strings.TrimLeftFunc(arguments, unicode.IsSpace)
		arguments = strings.TrimPrefix(arguments, "=")
	}
	arguments = strings.TrimLeftFunc(arguments, unicode.IsSpace)

	fields, err := splitSSHConfigArguments(arguments)
	if err != nil {
		return nil, err
	}
	return append([]string{keyword}, fields...), nil
}

func splitSSHConfigArguments(line string) ([]string, error) {
	var fields []string
	var field strings.Builder
	inQuotes := false
	escaped := false
	hasField := false
	flush := func() {
		if !hasField {
			return
		}
		fields = append(fields, field.String())
		field.Reset()
		hasField = false
	}
	for _, char := range line {
		if escaped {
			field.WriteRune(char)
			hasField = true
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			hasField = true
			continue
		}
		if char == '"' {
			inQuotes = !inQuotes
			hasField = true
			continue
		}
		if char == '#' && !inQuotes {
			break
		}
		if unicode.IsSpace(char) && !inQuotes {
			flush()
			continue
		}
		field.WriteRune(char)
		hasField = true
	}
	if escaped {
		return nil, fmt.Errorf("unfinished escape")
	}
	if inQuotes {
		return nil, fmt.Errorf("unterminated quote")
	}
	flush()
	return fields, nil
}
