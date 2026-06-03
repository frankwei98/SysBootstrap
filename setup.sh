#!/usr/bin/env bash
# setup.sh — OneLineSetup 主入口
#
# 用法:
#   sudo bash setup.sh                     # 交互式 (gum)
#   sudo bash setup.sh --modules ssh,node  # 指定模块
#   sudo bash setup.sh --all               # 安装所有模块
#
# 支持的系统: Debian 11+ / Ubuntu 22+

set -euo pipefail

# ==================== 初始化 ====================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${SCRIPT_DIR}/lib/common.sh"
source "${SCRIPT_DIR}/lib/detect.sh"

for module_file in "${SCRIPT_DIR}"/modules/*.sh; do
    [[ -f "$module_file" ]] && source "$module_file"
done

# 所有可用模块 (按依赖顺序)
AVAILABLE_MODULES=(
    base
    gum
    ssh
    node
    ai
    user
    ssh_keygen
)

# ==================== 参数解析 ====================

MODULES=()
RUN_ALL=false

parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --modules)
                [[ -z "${2:-}" ]] && die "--modules 需要参数"
                IFS=',' read -ra MODULES <<< "$2"
                shift 2
                ;;
            --all)
                RUN_ALL=true
                shift
                ;;
            -h|--help)
                echo "用法: sudo bash setup.sh [--modules mod1,mod2] [--all]"
                echo ""
                echo "选项:"
                echo "  --modules mod1,mod2  指定要安装的模块 (逗号分隔)"
                echo "  --all                安装所有模块"
                echo "  -h, --help           显示帮助"
                echo ""
                echo "可用模块: ${AVAILABLE_MODULES[*]}"
                exit 0
                ;;
            *)
                die "未知参数: $1 (使用 --help 查看帮助)"
                ;;
        esac
    done
}

# ==================== 交互式流程 ====================

# 初始设置 (必须步骤)
initial_setup() {
    module_ssh
    echo ""

    gum confirm "是否安装 AI CLI 工具 (Claude Code / Codex)?" && {
        module_node
        echo ""
        module_ai
    }
    echo ""
}

# 菜单循环
menu_loop() {
    while true; do
        local choice
        choice=$(gum choose \
            "创建用户" \
            "生成 SSH 密钥" \
            "退出" \
            --header "选择操作")

        case "$choice" in
            "创建用户")
                module_user
                ;;
            "生成 SSH 密钥")
                module_ssh_keygen
                ;;
            *)
                break
                ;;
        esac
        echo ""
    done
}

interactive_setup() {
    initial_setup
    menu_loop
}

# ==================== 主流程 ====================

main() {
    parse_args "$@"

    # root 检查 (最先)
    require_root

    echo ""
    echo "=============================="
    echo "  OneLineSetup — 服务器开荒"
    echo "=============================="
    echo ""

    # 系统检测
    validate_os

    # 安装基础依赖 (始终执行)
    module_base
    echo ""
    module_gum
    echo ""

    # 执行模块
    if $RUN_ALL; then
        # --all: 跳过已执行的 base/gum，运行其余模块
        for mod in "${AVAILABLE_MODULES[@]}"; do
            [[ "$mod" == "base" || "$mod" == "gum" ]] && continue
            log_info ">>> ${mod}"
            "module_${mod}"
            echo ""
        done
    elif [[ ${#MODULES[@]} -gt 0 ]]; then
        # --modules: 运行指定模块
        for mod in "${MODULES[@]}"; do
            if ! type "module_${mod}" &>/dev/null; then
                die "未知模块: ${mod}"
            fi
            log_info ">>> ${mod}"
            "module_${mod}"
            echo ""
        done
    else
        # 无参数: 交互式
        interactive_setup
    fi

    echo "=============================="
    log_success "全部完成!"
    echo "=============================="
}

main "$@"
