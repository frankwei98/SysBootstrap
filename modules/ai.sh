#!/usr/bin/env bash
# ai.sh — AI CLI 工具 (Claude Code / Codex)

set -euo pipefail

module_ai() {
    log_info "=== AI CLI 工具 ==="

    # 确保 nvm 和 node 可用
    export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
    # shellcheck source=/dev/null
    [ -s "$NVM_DIR/nvm.sh" ] && source "$NVM_DIR/nvm.sh"

    if ! command -v node &>/dev/null; then
        die "Node.js 未安装，请先运行 node 模块"
    fi

    # 选择要安装的工具
    local choices
    choices=$(gum choose --no-limit \
        "Claude Code" \
        "Codex" \
        --header "选择要安装的 AI 工具 (空格选择，回车确认)")

    if [[ -z "$choices" ]]; then
        log_warn "未选择任何工具，跳过"
        return 0
    fi

    # 检测可用的包管理器 (优先 pnpm)
    local pm="npm"
    command -v pnpm &>/dev/null && pm="pnpm"
    log_info "使用 $pm 安装"

    # 安装选中的工具
    while IFS= read -r choice; do
        case "$choice" in
            "Claude Code")
                log_info "安装 Claude Code ..."
                "$pm" install -g @anthropic-ai/claude-code
                log_success "Claude Code 安装完成"
                ;;
            "Codex")
                log_info "安装 Codex ..."
                "$pm" install -g @openai/codex
                log_success "Codex 安装完成"
                ;;
        esac
    done <<< "$choices"

    log_success "AI 工具安装完成"
}
