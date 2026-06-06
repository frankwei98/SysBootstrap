package i18n

import (
	"fmt"
	"os"
	"strings"
)

// Lang represents a supported language.
type Lang string

const (
	LangEN   Lang = "en"
	LangZhCN Lang = "zh-CN"
)

// current holds the active language for the process.
var current Lang = LangEN

// SetLang sets the active language. Call once at startup.
func SetLang(l Lang) {
	switch l {
	case LangZhCN:
		current = LangZhCN
	default:
		current = LangEN
	}
}

// GetLang returns the active language.
func GetLang() Lang {
	return current
}

// DetectLang determines language from flag, env, and config.
// Priority: explicit flag > SYS_BOOTSTRAP_LANG env > cfgLang (from settings) > default en.
func DetectLang(flagValue string, cfgLang string) Lang {
	if flagValue != "" {
		return NormalizeLang(flagValue)
	}
	if env := os.Getenv("SYS_BOOTSTRAP_LANG"); env != "" {
		return NormalizeLang(env)
	}
	if cfgLang != "" {
		return NormalizeLang(cfgLang)
	}
	return LangEN
}

// NormalizeLang converts user input to a canonical Lang.
func NormalizeLang(s string) Lang {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "zh", "zh-cn", "zh_cn", "zh_cn.utf-8", "zh_cn.utf8", "chinese":
		return LangZhCN
	default:
		return LangEN
	}
}

// T returns the translated string for the given key in the current language.
// Falls back to English if the key is missing in the current language.
func T(key string) string {
	if table, ok := translations[current]; ok {
		if msg, ok := table[key]; ok {
			return msg
		}
	}
	// fallback to English
	if table, ok := translations[LangEN]; ok {
		if msg, ok := table[key]; ok {
			return msg
		}
	}
	return key
}

// Tf returns the translated string with fmt.Sprintf formatting.
func Tf(key string, args ...interface{}) string {
	return fmt.Sprintf(T(key), args...)
}

// translations maps language -> key -> message.
var translations = map[Lang]map[string]string{
	LangEN:   enMessages,
	LangZhCN: zhCNMessages,
}
