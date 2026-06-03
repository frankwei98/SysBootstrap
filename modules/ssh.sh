#!/usr/bin/env bash
# ssh.sh — SSH 端口修改

set -euo pipefail

SSH_CONFIG="/etc/ssh/sshd_config"
DEFAULT_PORT="22122"

module_ssh() {
    log_info "=== SSH 端口配置 ==="

    # 交互式输入端口号
    local port
    port=$(gum input \
        --placeholder "$DEFAULT_PORT" \
        --prompt "SSH 端口号: " \
        --value "$DEFAULT_PORT")

    # 用户取消
    if [[ -z "$port" ]]; then
        log_warn "跳过 SSH 端口配置"
        return 0
    fi

    # 校验端口号
    if ! [[ "$port" =~ ^[0-9]+$ ]] || (( port < 1 || port > 65535 )); then
        die "无效端口号: $port (范围 1-65535)"
    fi
    if (( port == 22 )); then
        die "端口 22 是默认值，无需修改"
    fi

    # 确认
    gum confirm "将 SSH 端口修改为 $port，确认?" || {
        log_warn "已取消"
        return 0
    }

    # 备份原配置
    local backup_file="${SSH_CONFIG}.bak.$(date +%Y%m%d%H%M%S)"
    cp "$SSH_CONFIG" "$backup_file"
    log_success "已备份: $backup_file"

    # 注释掉已有的 Port 行，追加新端口
    sed -i 's/^[[:space:]]*Port /#&/' "$SSH_CONFIG"
    echo "Port $port" >> "$SSH_CONFIG"
    log_success "已写入: Port $port"

    # 校验配置 (sshd -t)
    log_info "校验 sshd 配置..."
    if ! sshd -t 2>/dev/null; then
        log_error "sshd 配置校验失败，已恢复原配置"
        cp "$backup_file" "$SSH_CONFIG"
        die "配置无效，已回滚。请手动检查 $SSH_CONFIG"
    fi
    log_success "配置校验通过"

    # 检测 ufw 并提示
    if command -v ufw &>/dev/null && ufw status | grep -q "active"; then
        log_warn "检测到 ufw 防火墙已启用"
        gum confirm "是否放行端口 $port ?" && {
            ufw allow "$port"/tcp
            log_success "ufw 已放行 $port/tcp"
        }
    fi

    # 重启 sshd (检测服务名)
    local svc="sshd"
    systemctl list-unit-files | grep -q "^ssh.service" && svc="ssh"
    systemctl restart "$svc"
    log_success "$svc 已重启"

    echo ""
    log_warn "请用新端口测试连接: ssh -p $port user@host"
    log_warn "确认新端口可用后，再关闭旧端口"

    # 检查 authorized_keys
    check_authorized_keys
}

# 检查用户的 SSH 公钥
check_authorized_keys() {
    log_info "=== 检查 SSH 公钥 ==="

    # 收集需要检查的用户 (root + 有登录shell的普通用户)
    local users=("root")
    while IFS=: read -r user _ uid _ _ home shell; do
        if (( uid >= 1000 )) && [[ "$shell" != "/usr/sbin/nologin" && "$shell" != "/bin/false" ]]; then
            users+=("$user")
        fi
    done < /etc/passwd

    local missing_keys=()

    for user in "${users[@]}"; do
        local home_dir
        if [[ "$user" == "root" ]]; then
            home_dir="/root"
        else
            home_dir="/home/$user"
        fi

        local key_file="${home_dir}/.ssh/authorized_keys"

        if [[ ! -f "$key_file" ]] || [[ ! -s "$key_file" ]]; then
            missing_keys+=("$user")
            log_warn "$user: 无 SSH 公钥"
        else
            local key_count
            key_count=$(grep -c '^ssh-\|^ecdsa-\|^sk-' "$key_file" 2>/dev/null || echo 0)
            log_success "$user: $key_count 个公钥"
        fi
    done

    # 如果有用户缺少公钥，提示添加
    if [[ ${#missing_keys[@]} -gt 0 ]]; then
        echo ""
        log_warn "以下用户没有 SSH 公钥: ${missing_keys[*]}"
        gum confirm "是否添加公钥?" || return 0

        for user in "${missing_keys[@]}"; do
            local home_dir
            if [[ "$user" == "root" ]]; then
                home_dir="/root"
            else
                home_dir="/home/$user"
            fi

            local ssh_dir="${home_dir}/.ssh"
            local key_file="${ssh_dir}/authorized_keys"

            echo ""
            log_info "为 $user 添加公钥"
            log_info "请粘贴公钥内容 (ssh-ed25519 AAAA... 或 ssh-rsa AAAA...):"

            local pubkey
            pubkey=$(gum write --placeholder "ssh-ed25519 AAAA..." --prompt "公钥: ")

            if [[ -z "$pubkey" ]]; then
                log_warn "$user: 跳过"
                continue
            fi

            # 简单校验公钥格式
            if ! [[ "$pubkey" =~ ^ssh-(rsa|ed25519|dss)|^ecdsa-sha2|^sk- ]]; then
                log_error "公钥格式不正确，跳过 $user"
                continue
            fi

            mkdir -p "$ssh_dir"
            echo "$pubkey" >> "$key_file"
            chmod 700 "$ssh_dir"
            chmod 600 "$key_file"
            [[ "$user" != "root" ]] && chown -R "$user:$user" "$ssh_dir"

            log_success "$user: 公钥已添加"
        done
    else
        log_success "所有用户都有 SSH 公钥"
    fi
}
