#!/usr/bin/env bash
# ssh_keygen.sh — 为当前用户生成 SSH 密钥对

set -euo pipefail

module_ssh_keygen() {
    log_info "=== 生成 SSH 密钥 ==="

    # 选择算法
    local algo
    algo=$(gum choose \
        "ed25519 (推荐)" \
        "rsa" \
        --header "选择密钥算法")

    [[ -z "$algo" ]] && { log_warn "跳过"; return 0; }

    local key_type="ed25519"
    [[ "$algo" == "rsa" ]] && key_type="rsa"

    # 文件名
    local key_file="${HOME}/.ssh/id_${key_type}"

    # 检查是否已存在
    if [[ -f "$key_file" ]]; then
        log_warn "密钥已存在: $key_file"
        gum confirm "是否覆盖?" || { log_warn "跳过"; return 0; }
    fi

    # 注释 (可选)
    local comment
    comment=$(gum input \
        --placeholder "$(whoami)@$(hostname)" \
        --prompt "密钥注释 (可选): ")

    [[ -z "$comment" ]] && comment="$(whoami)@$(hostname)"

    # 生成密钥
    log_warn "密钥将不设密码短语 (适合自动化场景)"
    mkdir -p "$HOME/.ssh"
    chmod 700 "$HOME/.ssh"

    if [[ "$key_type" == "ed25519" ]]; then
        ssh-keygen -t ed25519 -C "$comment" -f "$key_file" -N ""
    else
        ssh-keygen -t rsa -b 4096 -C "$comment" -f "$key_file" -N ""
    fi

    chmod 600 "$key_file"
    chmod 644 "${key_file}.pub"

    log_success "密钥已生成:"
    log_info "  私钥: $key_file"
    log_info "  公钥: ${key_file}.pub"

    # 显示公钥
    echo ""
    log_info "公钥内容:"
    cat "${key_file}.pub"
}
