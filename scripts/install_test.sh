#!/usr/bin/env bash

set -euo pipefail

PASS=0
FAIL=0
TEST_NAME=""

assert_contains() {
    local haystack="$1"
    local needle="$2"
    local msg="${3:-expected output to contain the target text}"
    if [[ "$haystack" == *"$needle"* ]]; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        echo "FAIL: $TEST_NAME - $msg"
        echo "  output: $(echo "$haystack" | head -1)"
    fi
}

assert_not_contains() {
    local haystack="$1"
    local needle="$2"
    local msg="${3:-expected output to NOT contain the target text}"
    if [[ "$haystack" != *"$needle"* ]]; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        echo "FAIL: $TEST_NAME - $msg"
        echo "  output: $(echo "$haystack" | head -1)"
    fi
}

assert_equal() {
    local actual="$1"
    local expected="$2"
    local msg="${3:-values differ}"
    if [[ "$actual" == "$expected" ]]; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        echo "FAIL: $TEST_NAME - $msg"
    fi
}

CAPTURED_CMD=""
PROMPT_VALUES=()
PROMPT_INDEX=0
CAN_USE_SUDO_STUB=0

reset_state() {
    CAPTURED_CMD=""
    PROMPT_VALUES=()
    PROMPT_INDEX=0
    # These globals are consumed by the sourced installer logic.
    # shellcheck disable=SC2034
    LANG_CHOICE="en"
    # shellcheck disable=SC2034
    APT_MIRROR=""
    RUN_MODE=""
    # shellcheck disable=SC2034
    DOWNLOAD_PATH="/tmp/fake/sys-bootstrap"
    # shellcheck disable=SC2034
    INSTALL_DIR="/usr/local/bin"
    CAN_USE_SUDO_STUB=1
    unset SYS_BOOTSTRAP_LANG 2>/dev/null || true
    unset SYS_BOOTSTRAP_APT_MIRROR 2>/dev/null || true
    unset SYS_BOOTSTRAP_RUN_MODE 2>/dev/null || true
    unset SYS_BOOTSTRAP_TEST_EUID 2>/dev/null || true
}

run_expect_fail() {
    local output
    set +e
    output="$("$@" 2>&1)"
    local rc=$?
    set -e
    printf '%s\n' "$output"
    return "$rc"
}

source_real_script() {
    # shellcheck source=/dev/null
    source "$(dirname "$0")/install.sh"
}

source_real_script

info() { :; }
warn() { :; }
error() { :; }

prompt_read() {
    local __var_name="$1"
    local __value="${PROMPT_VALUES[$PROMPT_INDEX]:-}"
    PROMPT_INDEX=$((PROMPT_INDEX + 1))
    printf -v "$__var_name" '%s' "$__value"
}

run_with_tty() {
    CAPTURED_CMD="$*"
}

can_use_sudo() {
    return "$CAN_USE_SUDO_STUB"
}

run_as_root() {
    CAPTURED_CMD="run_as_root $*"
}

test_choose_run_mode_user() {
    TEST_NAME="choose_run_mode: selection 1 -> user"
    reset_state
    PROMPT_VALUES=("1")
    choose_run_mode >/dev/null
    assert_equal "$RUN_MODE" "user"
}

test_choose_run_mode_full() {
    TEST_NAME="choose_run_mode: selection 2 -> full"
    reset_state
    PROMPT_VALUES=("2")
    choose_run_mode >/dev/null
    assert_equal "$RUN_MODE" "full"
}

test_choose_run_mode_default() {
    TEST_NAME="choose_run_mode: empty input defaults to user"
    reset_state
    PROMPT_VALUES=("")
    choose_run_mode >/dev/null
    assert_equal "$RUN_MODE" "user"
}

test_temp_user_mode_no_sudo() {
    TEST_NAME="temp run user mode: no sudo"
    reset_state
    PROMPT_VALUES=("1" "1")
    install_or_run >/dev/null
    assert_not_contains "$CAPTURED_CMD" "sudo"
    assert_contains "$CAPTURED_CMD" "/tmp/fake/sys-bootstrap"
    assert_equal "${SYS_BOOTSTRAP_RUN_MODE:-}" "user"
}

test_temp_full_mode_nonroot_uses_sudo() {
    TEST_NAME="temp run full mode non-root: uses sudo env"
    reset_state
    PROMPT_VALUES=("1" "2")
    SYS_BOOTSTRAP_TEST_EUID=1000
    CAN_USE_SUDO_STUB=0
    install_or_run >/dev/null
    assert_contains "$CAPTURED_CMD" "sudo"
    assert_contains "$CAPTURED_CMD" "SYS_BOOTSTRAP_RUN_MODE=full"
    assert_contains "$CAPTURED_CMD" "/tmp/fake/sys-bootstrap"
}

test_temp_full_mode_root_no_sudo() {
    TEST_NAME="temp run full mode root: no sudo"
    reset_state
    PROMPT_VALUES=("1" "2")
    SYS_BOOTSTRAP_TEST_EUID=0
    install_or_run >/dev/null
    assert_not_contains "$CAPTURED_CMD" "sudo"
    assert_contains "$CAPTURED_CMD" "/tmp/fake/sys-bootstrap"
}

test_temp_full_mode_no_sudo_available_dies() {
    TEST_NAME="temp run full mode: dies when sudo unavailable"
    reset_state
    PROMPT_VALUES=("1" "2")
    SYS_BOOTSTRAP_TEST_EUID=1000
    CAN_USE_SUDO_STUB=1
    if run_expect_fail install_or_run >/dev/null; then
        FAIL=$((FAIL + 1))
        echo "FAIL: $TEST_NAME - should fail when sudo unavailable"
    else
        PASS=$((PASS + 1))
    fi
}

test_install_requires_root() {
    TEST_NAME="install to /usr/local/bin: fails without root"
    reset_state
    PROMPT_VALUES=("2")
    SYS_BOOTSTRAP_TEST_EUID=1000
    CAN_USE_SUDO_STUB=1
    if run_expect_fail install_or_run >/dev/null; then
        FAIL=$((FAIL + 1))
        echo "FAIL: $TEST_NAME - should fail when root unavailable"
    else
        PASS=$((PASS + 1))
    fi
}

test_env_vars_zh_cn() {
    TEST_NAME="env vars: SYS_BOOTSTRAP_LANG=zh-CN"
    reset_state
    LANG_CHOICE="zh-CN"
    PROMPT_VALUES=("1" "1")
    install_or_run >/dev/null
    assert_equal "${SYS_BOOTSTRAP_LANG:-}" "zh-CN"
}

test_env_vars_apt_mirror() {
    TEST_NAME="env vars: SYS_BOOTSTRAP_APT_MIRROR passed when set"
    reset_state
    APT_MIRROR="cernet"
    PROMPT_VALUES=("1" "1")
    install_or_run >/dev/null
    assert_equal "${SYS_BOOTSTRAP_APT_MIRROR:-}" "cernet"
}

test_env_vars_full_mode_combined() {
    TEST_NAME="env vars: full mode passes all env vars through sudo env"
    reset_state
    # shellcheck disable=SC2034
    LANG_CHOICE="zh-CN"
    # shellcheck disable=SC2034
    APT_MIRROR="cernet"
    PROMPT_VALUES=("1" "2")
    # shellcheck disable=SC2034
    SYS_BOOTSTRAP_TEST_EUID=1000
    CAN_USE_SUDO_STUB=0
    install_or_run >/dev/null
    assert_contains "$CAPTURED_CMD" "sudo"
    assert_contains "$CAPTURED_CMD" "SYS_BOOTSTRAP_LANG=zh-CN"
    assert_contains "$CAPTURED_CMD" "SYS_BOOTSTRAP_APT_MIRROR=cernet"
    assert_contains "$CAPTURED_CMD" "SYS_BOOTSTRAP_RUN_MODE=full"
}

echo "Running install.sh tests..."
echo ""

test_choose_run_mode_user
test_choose_run_mode_full
test_choose_run_mode_default
test_temp_user_mode_no_sudo
test_temp_full_mode_nonroot_uses_sudo
test_temp_full_mode_root_no_sudo
test_temp_full_mode_no_sudo_available_dies
test_install_requires_root
test_env_vars_zh_cn
test_env_vars_apt_mirror
test_env_vars_full_mode_combined

echo ""
echo "Results: $PASS passed, $FAIL failed"
if [[ $FAIL -gt 0 ]]; then
    exit 1
fi
