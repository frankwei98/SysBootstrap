#!/usr/bin/env bash
# user.sh — 创建用户

set -euo pipefail

module_user() {
    log_info "=== 创建用户 ==="

    # 输入用户名
    local username
    username=$(gum input --placeholder "deploy" --prompt "用户名: ")

    if [[ -z "$username" ]]; then
        log_warn "跳过"
        return 0
    fi

    # 检查用户是否已存在
    if id "$username" &>/dev/null; then
        log_warn "用户 $username 已存在"
        return 0
    fi

    # 是否加入 sudo 组
    local add_sudo=false
    gum confirm "是否加入 sudo 组?" && add_sudo=true

    # 选择默认 shell
    local shell_choice
    shell_choice=$(gum choose "bash" "zsh" --header "选择默认 Shell")
    [[ -z "$shell_choice" ]] && shell_choice="bash"
    local user_shell="/bin/$shell_choice"

    # 是否写入 SSH 公钥
    local add_key=false
    gum confirm "是否写入 SSH 公钥?" && add_key=true

    # 创建用户
    log_info "创建用户 $username (shell: $user_shell) ..."
    useradd -m -s "$user_shell" "$username"
    log_success "用户 $username 已创建"

    # 加入 sudo
    if $add_sudo; then
        usermod -aG sudo "$username"
        log_success "$username 已加入 sudo 组"
    fi

    # 写入公钥
    if $add_key; then
        local ssh_dir="/home/${username}/.ssh"
        local key_file="${ssh_dir}/authorized_keys"

        echo ""
        log_info "请粘贴公钥内容:"
        local pubkey
        pubkey=$(gum write --placeholder "ssh-ed25519 AAAA..." --prompt "公钥: ")

        if [[ -n "$pubkey" ]] && [[ "$pubkey" =~ ^ssh-(rsa|ed25519|dss)|^ecdsa-sha2|^sk- ]]; then
            mkdir -p "$ssh_dir"
            echo "$pubkey" >> "$key_file"
            chmod 700 "$ssh_dir"
            chmod 600 "$key_file"
            chown -R "$username:$username" "$ssh_dir"
            log_success "公钥已写入"
        elif [[ -n "$pubkey" ]]; then
            log_error "公钥格式不正确，跳过"
        else
            log_warn "未输入公钥，跳过"
        fi
    fi

    # 提示设置密码
    echo ""
    log_info "为 $username 设置密码:"
    passwd "$username" || log_warn "密码设置已跳过"
}
