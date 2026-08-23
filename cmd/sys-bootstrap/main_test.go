package main

import (
	"reflect"
	"testing"
)

func TestExtractLangFlag_TwoArgForm(t *testing.T) {
	lang, args := extractLangFlag([]string{"--lang", "zh-CN", "help"})
	if lang != "zh-CN" {
		t.Errorf("lang = %q, want %q", lang, "zh-CN")
	}
	want := []string{"help"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestExtractLangFlag_EqualsForm(t *testing.T) {
	lang, args := extractLangFlag([]string{"--lang=zh-CN", "help"})
	if lang != "zh-CN" {
		t.Errorf("lang = %q, want %q", lang, "zh-CN")
	}
	want := []string{"help"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestExtractLangFlag_NoFlag(t *testing.T) {
	lang, args := extractLangFlag([]string{"run"})
	if lang != "" {
		t.Errorf("lang = %q, want empty", lang)
	}
	want := []string{"run"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestExtractLangFlag_FlagAtEnd(t *testing.T) {
	// --lang at end without value: should not consume anything
	lang, args := extractLangFlag([]string{"help", "--lang"})
	if lang != "" {
		t.Errorf("lang = %q, want empty (no value after --lang)", lang)
	}
	want := []string{"help", "--lang"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestExtractLangFlag_MultipleFlags(t *testing.T) {
	// Only first --lang should be consumed
	lang, args := extractLangFlag([]string{"--lang", "en", "plan", "--json"})
	if lang != "en" {
		t.Errorf("lang = %q, want %q", lang, "en")
	}
	want := []string{"plan", "--json"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestValidateTopLevelArgsRejectsIgnoredArguments(t *testing.T) {
	tests := [][]string{
		{"module", "node", "--dry-run"},
		{"plan", "--jsno"},
		{"plan", "--json", "extra"},
		{"run", "extra"},
		{"doctor", "extra"},
		{"version", "unexpected"},
		{"help", "unexpected"},
	}
	for _, args := range tests {
		if err := validateTopLevelArgs(args); err == nil {
			t.Errorf("validateTopLevelArgs(%v) succeeded, want error", args)
		}
	}
}

func TestValidateTopLevelArgsAcceptsSupportedForms(t *testing.T) {
	tests := [][]string{
		{"run"},
		{"plan"},
		{"plan", "--json"},
		{"doctor"},
		{"module", "node"},
		{"uninstall", "--dry-run"},
		{"config", "language", "en"},
		{"version"},
		{"--help"},
	}
	for _, args := range tests {
		if err := validateTopLevelArgs(args); err != nil {
			t.Errorf("validateTopLevelArgs(%v) failed: %v", args, err)
		}
	}
}
