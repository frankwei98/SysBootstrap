package i18n

import (
	"os"
	"testing"
)

func TestNormalizeLang(t *testing.T) {
	tests := []struct {
		input string
		want  Lang
	}{
		{"en", LangEN},
		{"EN", LangEN},
		{"zh-CN", LangZhCN},
		{"zh-cn", LangZhCN},
		{"zh_cn", LangZhCN},
		{"zh", LangZhCN},
		{"chinese", LangZhCN},
		{"ZH_CN.UTF-8", LangZhCN},
		{"fr", LangEN}, // unknown falls back to en
		{"", LangEN},
	}
	for _, tt := range tests {
		got := NormalizeLang(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeLang(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDetectLang_FlagOverride(t *testing.T) {
	// Flag takes priority over env and config
	os.Setenv("SYS_BOOTSTRAP_LANG", "en")
	defer os.Unsetenv("SYS_BOOTSTRAP_LANG")

	got := DetectLang("zh-CN", "en")
	if got != LangZhCN {
		t.Errorf("DetectLang with flag override = %q, want %q", got, LangZhCN)
	}
}

func TestDetectLang_EnvVar(t *testing.T) {
	os.Setenv("SYS_BOOTSTRAP_LANG", "zh-CN")
	defer os.Unsetenv("SYS_BOOTSTRAP_LANG")

	got := DetectLang("", "en")
	if got != LangZhCN {
		t.Errorf("DetectLang from env = %q, want %q", got, LangZhCN)
	}
}

func TestDetectLang_ConfigFallback(t *testing.T) {
	os.Unsetenv("SYS_BOOTSTRAP_LANG")

	got := DetectLang("", "zh-CN")
	if got != LangZhCN {
		t.Errorf("DetectLang from config = %q, want %q", got, LangZhCN)
	}
}

func TestDetectLang_Default(t *testing.T) {
	os.Unsetenv("SYS_BOOTSTRAP_LANG")
	got := DetectLang("", "")
	if got != LangEN {
		t.Errorf("DetectLang default = %q, want %q", got, LangEN)
	}
}

func TestDetectLang_FlagOverConfig(t *testing.T) {
	os.Unsetenv("SYS_BOOTSTRAP_LANG")
	got := DetectLang("en", "zh-CN")
	if got != LangEN {
		t.Errorf("DetectLang flag should override config: got %q, want %q", got, LangEN)
	}
}

func TestSetAndGetLang(t *testing.T) {
	SetLang(LangZhCN)
	if GetLang() != LangZhCN {
		t.Errorf("GetLang() = %q, want %q", GetLang(), LangZhCN)
	}
	SetLang(LangEN)
	if GetLang() != LangEN {
		t.Errorf("GetLang() = %q, want %q", GetLang(), LangEN)
	}
}

func TestT_EN(t *testing.T) {
	SetLang(LangEN)
	got := T("app_title")
	if got == "" || got == "app_title" {
		t.Errorf("T(app_title) returned key fallback: %q", got)
	}
}

func TestT_ZhCN(t *testing.T) {
	SetLang(LangZhCN)
	got := T("app_title")
	if got == "" || got == "app_title" {
		t.Errorf("T(app_title) returned key fallback: %q", got)
	}
	// Verify it's actually Chinese
	if got != "sys-bootstrap — 服务器初始化工具" {
		t.Errorf("T(app_title) = %q, want Chinese translation", got)
	}
}

func TestT_FallbackToEN(t *testing.T) {
	SetLang(LangZhCN)
	// A key that doesn't exist should fall back to English, then to the key itself
	got := T("nonexistent_key_xyz")
	if got != "nonexistent_key_xyz" {
		t.Errorf("T(nonexistent_key_xyz) = %q, want key itself as fallback", got)
	}
}

func TestTf(t *testing.T) {
	SetLang(LangEN)
	got := Tf("runner_starting", "Base Environment")
	if got != "Starting Base Environment..." {
		t.Errorf("Tf(runner_starting) = %q", got)
	}
}

func TestAllENKeysExistInZhCN(t *testing.T) {
	for key := range enMessages {
		if _, ok := zhCNMessages[key]; !ok {
			t.Errorf("Key %q exists in English but not in zh-CN", key)
		}
	}
}

func TestAllZhCNKeysExistInEN(t *testing.T) {
	for key := range zhCNMessages {
		if _, ok := enMessages[key]; !ok {
			t.Errorf("Key %q exists in zh-CN but not in English", key)
		}
	}
}
