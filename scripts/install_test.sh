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

assert_matches() {
    local actual="$1"
    local pattern="$2"
    local msg="${3:-value does not match the expected pattern}"
    if [[ "$actual" =~ $pattern ]]; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        echo "FAIL: $TEST_NAME - $msg"
    fi
}

path_mode() {
    local path="$1"
    local mode

    if mode=$(command stat -c '%a' -- "$path" 2>/dev/null); then
        builtin printf '%s\n' "$mode"
        return
    fi
    command stat -f '%Lp' "$path"
}

CAPTURED_CMD=""
CAPTURED_RELOAD_CMD=""
PROMPT_VALUES=()
PROMPT_INDEX=0
CAN_USE_SUDO_STUB=0
CURRENT_EUID_STUB=0

reset_state() {
    CAPTURED_CMD=""
    CAPTURED_RELOAD_CMD=""
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
    DOWNLOAD_DIR=""
    # shellcheck disable=SC2034
    VERIFIED_SHA256="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    # shellcheck disable=SC2034
    ROOT_STAGE_DIR=""
    # shellcheck disable=SC2034
    ROOT_STAGE_PATH=""
    # shellcheck disable=SC2034
    INSTALL_DIR="/usr/local/bin"
    CAN_USE_SUDO_STUB=1
    CURRENT_EUID_STUB=0
    unset SYS_BOOTSTRAP_LANG 2>/dev/null || true
    unset SYS_BOOTSTRAP_APT_MIRROR 2>/dev/null || true
    unset SYS_BOOTSTRAP_RUN_MODE 2>/dev/null || true
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
has_tty() { return 0; }

prompt_read() {
    local __var_name="$1"
    local __value="${PROMPT_VALUES[$PROMPT_INDEX]:-}"
    PROMPT_INDEX=$((PROMPT_INDEX + 1))
    printf -v "$__var_name" '%s' "$__value"
}

# Invoked indirectly by sourced installer functions.
# shellcheck disable=SC2329
run_with_tty() {
    CAPTURED_CMD="$*"
}

reload_current_shell() {
    CAPTURED_RELOAD_CMD="$(shell_reload_command)"
}

can_use_sudo() {
    return "$CAN_USE_SUDO_STUB"
}

current_euid() {
    echo "$CURRENT_EUID_STUB"
}

# Invoked indirectly by sourced installer functions.
# shellcheck disable=SC2329
run_as_root() {
    if [[ "$1" != /* ]]; then
        echo "unexpected PATH-resolved root command: $*" >&2
        return 1
    fi
    case "${1##*/}" in
        mktemp)
            "$1" -d "/tmp/sys-bootstrap.root.XXXXXX"
            ;;
        sha256sum|shasum)
            while IFS= read -r _; do :; done
            ;;
        rm|rmdir)
            "$@"
            ;;
        *)
            CAPTURED_CMD="run_as_root $*"
            ;;
    esac
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
    PROMPT_VALUES=("1" "1" "n")
    install_or_run >/dev/null
    assert_not_contains "$CAPTURED_CMD" "sudo"
    assert_contains "$CAPTURED_CMD" "/tmp/fake/sys-bootstrap"
    assert_contains "$CAPTURED_CMD" "SYS_BOOTSTRAP_RUN_MODE=user"
}

test_temp_full_mode_nonroot_uses_sudo() {
    TEST_NAME="temp run full mode non-root: uses sudo env"
    reset_state
    PROMPT_VALUES=("1" "2" "n")
    CURRENT_EUID_STUB=1000
    CAN_USE_SUDO_STUB=0
    install_or_run >/dev/null
    assert_contains "$CAPTURED_CMD" "sudo"
    assert_contains "$CAPTURED_CMD" "SYS_BOOTSTRAP_RUN_MODE=full"
    assert_matches "$CAPTURED_CMD" '/tmp/sys-bootstrap\.root\.[[:alnum:]]{6}/sys-bootstrap' \
        "root staging path must use a random temporary directory"
    assert_not_contains "$CAPTURED_CMD" "/tmp/fake/sys-bootstrap"
}

test_temp_full_mode_root_no_sudo() {
    TEST_NAME="temp run full mode root: no sudo"
    reset_state
    PROMPT_VALUES=("1" "2" "n")
    CURRENT_EUID_STUB=0
    install_or_run >/dev/null
    assert_not_contains "$CAPTURED_CMD" "sudo"
    assert_matches "$CAPTURED_CMD" '/tmp/sys-bootstrap\.root\.[[:alnum:]]{6}/sys-bootstrap' \
        "root staging path must use a random temporary directory"
    assert_not_contains "$CAPTURED_CMD" "/tmp/fake/sys-bootstrap"
}

test_temp_full_mode_no_sudo_available_dies() {
    TEST_NAME="temp run full mode: dies when sudo unavailable"
    reset_state
    PROMPT_VALUES=("1" "2")
    CURRENT_EUID_STUB=1000
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
    CURRENT_EUID_STUB=1000
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
    PROMPT_VALUES=("1" "1" "n")
    install_or_run >/dev/null
    assert_contains "$CAPTURED_CMD" "SYS_BOOTSTRAP_LANG=zh-CN"
}

test_env_vars_apt_mirror() {
    TEST_NAME="env vars: SYS_BOOTSTRAP_APT_MIRROR passed when set"
    reset_state
    APT_MIRROR="cernet"
    PROMPT_VALUES=("1" "1" "n")
    install_or_run >/dev/null
    assert_contains "$CAPTURED_CMD" "SYS_BOOTSTRAP_APT_MIRROR=cernet"
}

test_env_vars_full_mode_combined() {
    TEST_NAME="env vars: full mode passes all env vars through sudo env"
    reset_state
    # shellcheck disable=SC2034
    LANG_CHOICE="zh-CN"
    # shellcheck disable=SC2034
    APT_MIRROR="cernet"
    PROMPT_VALUES=("1" "2" "n")
    CURRENT_EUID_STUB=1000
    CAN_USE_SUDO_STUB=0
    install_or_run >/dev/null
    assert_contains "$CAPTURED_CMD" "sudo"
    assert_contains "$CAPTURED_CMD" "SYS_BOOTSTRAP_LANG=zh-CN"
    assert_contains "$CAPTURED_CMD" "SYS_BOOTSTRAP_APT_MIRROR=cernet"
    assert_contains "$CAPTURED_CMD" "SYS_BOOTSTRAP_RUN_MODE=full"
}

test_temp_full_mode_uses_verified_root_staging() {
    TEST_NAME="temp full mode: executes root-staged verified content"
    reset_state

    local saved_run_as_root saved_run_with_tty attack_dir executed_content=""
    local stage_dir_mode="" stage_file_mode=""
    saved_run_as_root="$(declare -f run_as_root)"
    saved_run_with_tty="$(declare -f run_with_tty)"
    attack_dir="$(mktemp -d)"
    DOWNLOAD_DIR="$attack_dir"
    DOWNLOAD_PATH="${attack_dir}/sys-bootstrap"
    printf '%s\n' "trusted binary" > "$DOWNLOAD_PATH"
    VERIFIED_SHA256="$(file_sha256 "$DOWNLOAD_PATH")"

    # Invoked indirectly by sourced installer functions.
    # shellcheck disable=SC2329
    run_as_root() {
        if [[ "$1" != /* ]]; then
            echo "unexpected PATH-resolved root command: $*" >&2
            return 1
        fi
        case "${1##*/}" in
            mktemp)
                command mktemp -d "/tmp/sys-bootstrap.root.XXXXXX"
                ;;
            install|chmod|rm|rmdir)
                command "$@"
                ;;
            sha256sum|shasum)
                command "$@"
                ;;
            *)
                echo "unexpected root command: $*" >&2
                return 1
                ;;
        esac
    }
    run_with_tty() {
        local privileged_path="${!#}"
        stage_dir_mode="$(path_mode "${privileged_path%/*}")"
        stage_file_mode="$(path_mode "$privileged_path")"
        printf '%s\n' "malicious replacement" > "$DOWNLOAD_PATH"
        executed_content="$(command head -n 1 -- "$privileged_path")"
    }

    PROMPT_VALUES=("1" "2" "n")
    CURRENT_EUID_STUB=1000
    CAN_USE_SUDO_STUB=0
    install_or_run >/dev/null

    eval "$saved_run_as_root"
    eval "$saved_run_with_tty"
    assert_equal "$executed_content" "trusted binary" \
        "privileged execution must use content bound to the verified download"
    assert_equal "$stage_dir_mode" "711" \
        "temporary full-run staging directory must be traversable but not writable"
    assert_equal "$stage_file_mode" "755" \
        "temporary full-run binary must be executable but not writable"
}

test_install_rejects_replacement_during_privileged_copy() {
    TEST_NAME="install mode: rejects replacement during privileged copy"
    reset_state

    local saved_run_as_root attack_dir install_dir
    saved_run_as_root="$(declare -f run_as_root)"
    attack_dir="$(mktemp -d)"
    install_dir="$(mktemp -d)"
    # Consumed by sourced installer functions.
    # shellcheck disable=SC2034
    DOWNLOAD_DIR="$attack_dir"
    DOWNLOAD_PATH="${attack_dir}/sys-bootstrap"
    # Consumed by sourced installer functions.
    # shellcheck disable=SC2034
    INSTALL_DIR="$install_dir"
    printf '%s\n' "trusted binary" > "$DOWNLOAD_PATH"
    # Consumed by sourced installer functions in the failure subprocess.
    # shellcheck disable=SC2034
    VERIFIED_SHA256="$(file_sha256 "$DOWNLOAD_PATH")"

    # Invoked indirectly by sourced installer functions.
    # shellcheck disable=SC2329
    run_as_root() {
        if [[ "$1" != /* ]]; then
            echo "unexpected PATH-resolved root command: $*" >&2
            return 1
        fi
        case "${1##*/}" in
            mktemp)
                command mktemp -d "/tmp/sys-bootstrap.root.XXXXXX"
                ;;
            install)
                if [[ "${*: -2:1}" == "$DOWNLOAD_PATH" ]]; then
                    printf '%s\n' "malicious replacement" > "$DOWNLOAD_PATH"
                fi
                command "$@"
                ;;
            sha256sum|shasum)
                command "$@"
                ;;
            rm|rmdir)
                command "$@"
                ;;
            *)
                echo "unexpected root command: $*" >&2
                return 1
                ;;
        esac
    }

    PROMPT_VALUES=("2")
    CURRENT_EUID_STUB=1000
    CAN_USE_SUDO_STUB=0
    if run_expect_fail install_or_run >/dev/null; then
        FAIL=$((FAIL + 1))
        echo "FAIL: $TEST_NAME - replacement should fail root-side verification"
    else
        PASS=$((PASS + 1))
    fi

    eval "$saved_run_as_root"
    command rm -rf -- "$attack_dir" "$install_dir"
}

test_install_staging_remains_private() {
    TEST_NAME="install mode: staging remains root-private"
    reset_state

    local saved_run_as_root attack_dir stage_dir_mode stage_file_mode
    saved_run_as_root="$(declare -f run_as_root)"
    attack_dir="$(mktemp -d)"
    # Consumed by sourced installer functions.
    # shellcheck disable=SC2034
    DOWNLOAD_DIR="$attack_dir"
    DOWNLOAD_PATH="${attack_dir}/sys-bootstrap"
    printf '%s\n' "trusted binary" > "$DOWNLOAD_PATH"
    # Consumed by sourced installer functions.
    # shellcheck disable=SC2034
    VERIFIED_SHA256="$(file_sha256 "$DOWNLOAD_PATH")"

    # Invoked indirectly by sourced installer functions.
    # shellcheck disable=SC2329
    run_as_root() {
        if [[ "$1" != /* ]]; then
            echo "unexpected PATH-resolved root command: $*" >&2
            return 1
        fi
        case "${1##*/}" in
            mktemp)
                command mktemp -d "/tmp/sys-bootstrap.root.XXXXXX"
                ;;
            install|sha256sum|shasum|rm|rmdir)
                command "$@"
                ;;
            *)
                echo "unexpected root command: $*" >&2
                return 1
                ;;
        esac
    }

    stage_verified_binary_as_root
    stage_dir_mode="$(path_mode "$ROOT_STAGE_DIR")"
    stage_file_mode="$(path_mode "$ROOT_STAGE_PATH")"
    cleanup_root_stage

    eval "$saved_run_as_root"
    command rm -rf -- "$attack_dir"
    assert_equal "$stage_dir_mode" "700" \
        "install staging directory must stay root-private"
    assert_equal "$stage_file_mode" "700" \
        "install staging binary must stay root-private"
}

test_install_mode_persists_config_with_trusted_root_tools() {
    TEST_NAME="install mode: persists config through trusted root tools"
    reset_state

    local saved_run_as_root attack_dir install_dir persisted_config config_content
    saved_run_as_root="$(declare -f run_as_root)"
    attack_dir="$(mktemp -d)"
    install_dir="$(mktemp -d)"
    persisted_config="${attack_dir}/persisted-config.env"
    # Consumed by sourced installer functions.
    # shellcheck disable=SC2034
    DOWNLOAD_DIR="$attack_dir"
    DOWNLOAD_PATH="${attack_dir}/sys-bootstrap"
    # Consumed by sourced installer functions.
    # shellcheck disable=SC2034
    INSTALL_DIR="$install_dir"
    # Consumed by sourced installer functions.
    # shellcheck disable=SC2034
    LANG_CHOICE="zh-CN"
    # Consumed by sourced installer functions.
    # shellcheck disable=SC2034
    APT_MIRROR="cernet"
    printf '%s\n' "trusted binary" > "$DOWNLOAD_PATH"
    # Consumed by sourced installer functions.
    # shellcheck disable=SC2034
    VERIFIED_SHA256="$(file_sha256 "$DOWNLOAD_PATH")"

    # Invoked indirectly by sourced installer functions.
    # shellcheck disable=SC2329
    run_as_root() {
        if [[ "$1" != /* ]]; then
            echo "unexpected PATH-resolved root command: $*" >&2
            return 1
        fi
        case "${1##*/}" in
            mktemp)
                command mktemp -d "/tmp/sys-bootstrap.root.XXXXXX"
                ;;
            install|sha256sum|shasum|rm|rmdir)
                command "$@"
                ;;
            mkdir)
                # The production target is /etc/sys-bootstrap; keep this test
                # isolated while still exercising the privileged call.
                ;;
            cp)
                command cp "$2" "$persisted_config"
                ;;
            chmod)
                command chmod "$2" "$persisted_config"
                ;;
            *)
                echo "unexpected root command: $*" >&2
                return 1
                ;;
        esac
    }

    PROMPT_VALUES=("2")
    CURRENT_EUID_STUB=0
    install_or_run >/dev/null

    config_content="$(<"$persisted_config")"
    assert_contains "$config_content" "lang=zh-CN" \
        "install mode must persist the selected language"
    assert_contains "$config_content" "apt_mirror=cernet" \
        "install mode must persist the selected APT mirror"
    if [[ ! -x "${install_dir}/sys-bootstrap" ]]; then
        FAIL=$((FAIL + 1))
        echo "FAIL: $TEST_NAME - installed binary was not created"
    else
        PASS=$((PASS + 1))
    fi

    eval "$saved_run_as_root"
    command rm -rf -- "$attack_dir" "$install_dir"
}

test_privileged_staging_avoids_user_path_tools() {
    TEST_NAME="privileged staging: never resolves root tools through user PATH"
    reset_state

    local script script_dir staging_code
    script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    script="$(<"${script_dir}/install.sh")"
    if [[ "$script" != *"trusted_system_command() {"* ]]; then
        FAIL=$((FAIL + 1))
        echo "FAIL: $TEST_NAME - trusted_system_command marker is missing"
        return
    fi
    if [[ "$script" != *"# --- Language Selection ---"* ]]; then
        FAIL=$((FAIL + 1))
        echo "FAIL: $TEST_NAME - staging section end marker is missing"
        return
    fi
    staging_code="${script#*trusted_system_command() \{}"
    staging_code="${staging_code%%# --- Language Selection ---*}"

    assert_not_contains "$staging_code" "run_as_root mktemp"
    assert_not_contains "$staging_code" "run_as_root install"
    assert_not_contains "$staging_code" "run_as_root chmod"
    assert_not_contains "$staging_code" "run_as_root rm"
    assert_not_contains "$staging_code" "run_as_root rmdir"
    assert_not_contains "$script" "run_as_root apt-get"
    assert_not_contains "$script" "run_as_root mkdir"
    assert_not_contains "$script" "run_as_root cp"
    assert_not_contains "$script" "run_as_root chmod"
    assert_not_contains "$staging_code" "| awk"
    assert_contains "$staging_code" "builtin printf"
}

test_temp_run_reload_shell_declined() {
    TEST_NAME="temp run: decline shell reload"
    reset_state
    PROMPT_VALUES=("1" "1" "n")
    install_or_run >/dev/null
    assert_equal "$CAPTURED_RELOAD_CMD" ""
}

test_temp_run_reload_shell_default_yes() {
    TEST_NAME="temp run: shell reload defaults to yes"
    reset_state
    PROMPT_VALUES=("1" "1" "")
    install_or_run >/dev/null
    assert_contains "$CAPTURED_RELOAD_CMD" "exec "
    assert_contains "$CAPTURED_RELOAD_CMD" " -l"
}

test_shell_reload_command_is_manual_friendly() {
    TEST_NAME="shell_reload_command: manual command omits /dev/tty redirect"
    reset_state
    local cmd
    cmd="$(shell_reload_command)"
    assert_contains "$cmd" "exec "
    assert_contains "$cmd" " -l"
    assert_not_contains "$cmd" "/dev/tty"
}

test_no_test_euid_in_production() {
    TEST_NAME="production install.sh has no SYS_BOOTSTRAP_TEST_EUID"
    reset_state
    local script_dir
    script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    if grep -q "SYS_BOOTSTRAP_TEST_EUID" "${script_dir}/install.sh"; then
        FAIL=$((FAIL + 1))
        echo "FAIL: $TEST_NAME - SYS_BOOTSTRAP_TEST_EUID found in production install.sh"
    else
        PASS=$((PASS + 1))
    fi
}

test_resolve_version_falls_back_from_jsdelivr() {
    TEST_NAME="resolve_version: optional jsDelivr failure falls back to GitHub"
    reset_state
    # shellcheck disable=SC2034
    REGION="china"
    JSDELIVR_API="https://jsdelivr.invalid/metadata"
    GITHUB_API="https://github.invalid/releases/latest"
    # shellcheck disable=SC2317,SC2329
    curl() {
        local arg
        for arg in "$@"; do
            if [[ "$arg" == "$JSDELIVR_API" ]]; then
                return 22
            fi
            if [[ "$arg" == "$GITHUB_API" ]]; then
                printf '%s\n' '{"tag_name":"v9.8.7"}'
                return 0
            fi
        done
        return 1
    }
    local resolved
    resolved="$(resolve_version)"
    unset -f curl
    assert_equal "$resolved" "v9.8.7"
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
test_temp_full_mode_uses_verified_root_staging
test_install_rejects_replacement_during_privileged_copy
test_install_staging_remains_private
test_install_mode_persists_config_with_trusted_root_tools
test_privileged_staging_avoids_user_path_tools
test_temp_run_reload_shell_declined
test_temp_run_reload_shell_default_yes
test_shell_reload_command_is_manual_friendly
test_no_test_euid_in_production
test_resolve_version_falls_back_from_jsdelivr

echo ""
echo "Results: $PASS passed, $FAIL failed"
if [[ $FAIL -gt 0 ]]; then
    exit 1
fi
