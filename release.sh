#!/bin/bash

# Go 模块通用发布脚本
# 支持任意 Go mod 工程的版本发布，包括手动指定版本号或自动递增版本号
#
# 用法:
#   ./release.sh [版本号] [发布说明]           # 手动指定版本
#   ./release.sh init [发布说明]              # 自动递增补丁版本
#   ./release.sh minor [发布说明]             # 自动递增次版本
#   ./release.sh major [发布说明]             # 自动递增主版本
#
# 示例:
#   ./release.sh v1.0.1 "修复日志级别问题"     # 手动指定版本
#   ./release.sh init "修复bug"               # 自动递增补丁版本
#   ./release.sh minor "新增功能"             # 自动递增次版本
#   ./release.sh major "重大更新"             # 自动递增主版本

set -e  # 遇到错误立即退出

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 日志函数
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 检查是否在 git 仓库中
check_git_repo() {
    if ! git rev-parse --git-dir > /dev/null 2>&1; then
        log_error "当前目录不是 git 仓库"
        exit 1
    fi
    log_info "Git 仓库检查通过"
}

# 检查是否为 Go 模块
check_go_module() {
    if [[ ! -f "go.mod" ]]; then
        log_error "当前目录不是 Go 模块，缺少 go.mod 文件"
        exit 1
    fi

    local module_name=$(grep "^module " go.mod | awk '{print $2}')
    if [[ -z "$module_name" ]]; then
        log_error "无法从 go.mod 文件中读取模块名称"
        exit 1
    fi

    log_info "Go 模块检查通过: $module_name"
}

# 检查工作目录是否干净
check_clean_working_dir() {
    if ! git diff-index --quiet HEAD --; then
        log_error "工作目录有未提交的更改，请先提交"
        git status --porcelain
        exit 1
    fi
}

# 检查是否在主分支
check_main_branch() {
    current_branch=$(git branch --show-current)
    if [[ "$current_branch" != "main" && "$current_branch" != "master" ]]; then
        log_warning "当前不在主分支 ($current_branch)，是否继续? (y/N)"
        read -r response
        if [[ ! "$response" =~ ^[Yy]$ ]]; then
            log_info "已取消发布"
            exit 0
        fi
    fi
}

# 验证版本号格式
validate_version() {
    local version=$1
    if [[ ! $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9]+)?$ ]]; then
        log_error "版本号格式错误，应为 vX.Y.Z 或 vX.Y.Z-suffix 格式"
        log_info "示例: v1.0.0, v1.2.3, v2.0.0-beta"
        exit 1
    fi
}

# 检查版本号是否已存在
check_version_exists() {
    local version=$1
    if git tag -l | grep -q "^$version$"; then
        log_error "版本 $version 已存在"
        git tag -l | grep "^v" | sort -V | tail -5
        exit 1
    fi
}

# 获取最新版本
get_latest_version() {
    local latest=$(git tag -l | grep "^v" | sort -V | tail -1)
    if [[ -z "$latest" ]]; then
        echo ""  # 返回空字符串表示没有版本
    else
        echo "$latest"
    fi
}

# 检查是否为首次发布
is_first_release() {
    local latest=$(get_latest_version)
    [[ -z "$latest" ]]
}

# 递增版本号
increment_version() {
    local version=$1
    local type=$2

    # 如果是首次发布，返回初始版本
    if [[ -z "$version" ]]; then
        case $type in
            "major")
                echo "v1.0.0"
                ;;
            "minor")
                echo "v0.1.0"
                ;;
            "patch"|"init"|*)
                echo "v0.0.1"
                ;;
        esac
        return
    fi

    # 移除 v 前缀
    version=${version#v}

    # 分割版本号
    IFS='.' read -ra PARTS <<< "$version"
    major=${PARTS[0]}
    minor=${PARTS[1]}
    patch=${PARTS[2]}

    case $type in
        "major")
            major=$((major + 1))
            minor=0
            patch=0
            ;;
        "minor")
            minor=$((minor + 1))
            patch=0
            ;;
        "patch"|"init"|*)
            patch=$((patch + 1))
            ;;
    esac

    echo "v$major.$minor.$patch"
}

# 检查是否为自动版本递增模式
is_auto_increment() {
    local param=$1
    [[ "$param" == "init" || "$param" == "patch" || "$param" == "minor" || "$param" == "major" ]]
}

# 运行测试
run_tests() {
    log_info "运行完整质量门禁（不提供跳过选项）..."
    local gate_dir
    gate_dir=$(mktemp -d "${TMPDIR:-/tmp}/mlog-release.XXXXXX")
    log_info "质量检查证据: $gate_dir"
    GOTOOLCHAIN=go1.24.13 bash scripts/quality_gate.sh "$gate_dir/quality"
    GOTOOLCHAIN=go1.27.1 bash scripts/security_gate.sh "$gate_dir/security"
    RELEASE_VALIDATED_SHA=$(git rev-parse HEAD)
    test "$(cat "$gate_dir/quality/validated-sha.txt")" = "$RELEASE_VALIDATED_SHA"
    test "$(cat "$gate_dir/security/validated-sha.txt")" = "$RELEASE_VALIDATED_SHA"
}

# 运行代码检查
run_lint() {
    local unformatted_files
    unformatted_files=$(gofmt -l .)
    if [[ -n "$unformatted_files" ]]; then
        log_error "代码格式不规范，请修复并提交后重试（发布流程不会自动修改代码）"
        printf '%s\n' "$unformatted_files"
        exit 1
    fi
}

# 更新版本信息
update_version_info() {
    local version=$1
    local version_without_v=${version#v}
    local module_name=$(grep "^module " go.mod | awk '{print $2}')

    # 如果存在 version.go 文件，更新版本信息
    if [[ -f "version.go" ]]; then
        log_info "更新 version.go 文件中的版本信息..."
        sed -i.bak "s/const Version = \".*\"/const Version = \"$version_without_v\"/" version.go
        rm -f version.go.bak
        git add version.go
        log_success "version.go 文件已更新"
    fi

    # 更新 README.md 中的版本信息（如果存在）
    if [[ -f "README.md" ]] && grep -q "go get" README.md 2>/dev/null; then
        log_info "更新 README.md 中的版本信息..."
        # 更新 go get 命令中的版本引用
        sed -i.bak "s|go get $module_name@v[0-9]*\.[0-9]*\.[0-9]*|go get $module_name@$version|g" README.md
        rm -f README.md.bak
        git add README.md
        log_success "README.md 文件已更新"
    fi
}

# 生成变更日志
 generate_changelog() {
    local version=$1
    local message=$2
    local latest_version=$(get_latest_version)

    log_info "生成变更日志..."

    # 创建变更日志文件
    echo "## $version ($(date +%Y-%m-%d))" > /tmp/changelog.md
    echo "" >> /tmp/changelog.md
    echo "$message" >> /tmp/changelog.md
    echo "" >> /tmp/changelog.md

    # 添加提交记录
    if [[ -n "$latest_version" ]]; then
        echo "### 提交记录:" >> /tmp/changelog.md
        if git log --oneline "$latest_version"..HEAD > /dev/null 2>&1; then
            git log --oneline "$latest_version"..HEAD >> /tmp/changelog.md
        else
            log_warning "无法获取从 $latest_version 到 HEAD 的提交记录，使用所有提交"
            git log --oneline --max-count=10 >> /tmp/changelog.md
        fi
    else
        echo "### 提交记录 (首次发布):" >> /tmp/changelog.md
        git log --oneline --max-count=10 >> /tmp/changelog.md
    fi

    log_success "变更日志生成完成"
}

# 创建标签和发布
create_release() {
    local version=$1
    local message=$2
    local current_branch
    current_branch=$(git branch --show-current)
    test -n "${RELEASE_VALIDATED_SHA:-}"
    test "$(git rev-parse HEAD)" = "$RELEASE_VALIDATED_SHA"
    git diff --exit-code HEAD --
    git tag -a "$version" "$RELEASE_VALIDATED_SHA" -m "$message"
    if git remote | grep -q .; then
        # Never publish a tag if updating its branch fails.
        git push --atomic origin "HEAD:refs/heads/$current_branch" "refs/tags/$version"
    else
        log_warning "未配置远程仓库，经过完整验证的标签仅在本地创建"
    fi
}

# 显示发布信息
show_release_info() {
    local version=$1
    local module_name=$(grep "^module " go.mod | awk '{print $2}')

    echo ""
    log_success "🎉 版本 $version 发布成功!"
    echo ""
    echo "发布信息:"
    echo "- 模块名称: $module_name"
    echo "- 版本: $version"
    echo "- 时间: $(date)"
    echo "- 分支: $(git branch --show-current)"
    echo "- 提交: $(git rev-parse --short HEAD)"
    echo ""
    echo "使用方法:"
    echo "  go get $module_name@$version"
    echo ""
    echo "下一步操作:"
    echo "1. 检查 GitHub/GitLab 上的发布页面"
    echo "2. 更新文档和示例"
    echo "3. 通知团队成员"
    echo "4. 验证模块可以正常下载: go get $module_name@$version"
    echo ""
}

# 显示用法说明
show_usage() {
    local module_name=""
    if [[ -f "go.mod" ]]; then
        module_name=$(grep "^module " go.mod | awk '{print $2}')
    fi

    echo "Go 模块通用发布脚本"
    echo ""
    if [[ -n "$module_name" ]]; then
        echo "当前模块: $module_name"
        echo ""
    fi
    echo "用法:"
    echo "  $0 [版本号] [发布说明]           # 手动指定版本"
    echo "  $0 init [发布说明]              # 自动递增补丁版本"
    echo "  $0 minor [发布说明]             # 自动递增次版本"
    echo "  $0 major [发布说明]             # 自动递增主版本"
    echo ""
    echo "参数:"
    echo "  版本号    - 手动指定版本号，格式为 vX.Y.Z"
    echo "  init      - 自动递增补丁版本号（推荐用于bug修复）"
    echo "  minor     - 自动递增次版本号（用于新功能）"
    echo "  major     - 自动递增主版本号（用于重大更新）"
    echo "  发布说明  - 可选的发布说明文本"
    echo ""
    echo "示例:"
    echo "  $0 v1.0.1 '修复日志级别问题'     # 手动指定版本"
    echo "  $0 init '修复bug'               # 自动递增补丁版本"
    echo "  $0 minor '新增功能'             # 自动递增次版本"
    echo "  $0 major '重大更新'             # 自动递增主版本"
    echo ""
    echo "功能特性:"
    echo "- 支持任意 Go 模块项目"
    echo "- 自动检测首次发布"
    echo "- 智能处理测试和代码检查"
    echo "- 自动更新版本文件"
    echo "- 生成详细的变更日志"
    echo "- 安全的远程推送处理"
    echo ""
}

# 主函数
main() {
    local param1=$1
    local param2=$2

    # 检查帮助参数
    if [[ "$param1" == "-h" || "$param1" == "--help" ]]; then
        show_usage
        exit 0
    fi

    log_info "开始 Go 模块版本发布流程..."

    local version=""
    local message=""

    # 判断是自动递增还是手动指定版本
    if is_auto_increment "$param1"; then
        # 自动递增模式
        local current_version=$(get_latest_version)
        version=$(increment_version "$current_version" "$param1")
        message="$param2"

        if [[ -n "$current_version" ]]; then
            log_info "当前版本: $current_version"
        else
            log_info "首次发布，无当前版本"
        fi
        log_info "新版本: $version"
    else
        # 手动指定版本模式
        version="$param1"
        message="$param2"

        # 检查参数
        if [[ -z "$version" ]]; then
            log_error "请提供版本号或递增类型"
            show_usage
            exit 1
        fi

        # 验证版本号格式
        validate_version "$version"
    fi

    # 设置默认消息
    if [[ -z "$message" ]]; then
        message="Release $version"
    fi

    # 执行基础检查
    check_git_repo
    check_go_module
    check_clean_working_dir
    check_main_branch
    check_version_exists "$version"

    # 只检查格式，不在测试后偷偷改写源码
    run_lint

    # 更新版本信息
    update_version_info "$version"

    # 如果有版本文件更新，提交更改
    if ! git diff-index --quiet HEAD --; then
        log_info "提交版本信息更新..."
        git commit -m "chore: bump version to $version"
    fi

    # 在版本变更提交之后验证最终待发布内容；失败不创建或推送标签
    run_tests

    # 生成变更日志
    generate_changelog "$version" "$message"

    # 显示变更日志预览
    echo ""
    log_info "变更日志预览:"
    echo "----------------------------------------"
    cat /tmp/changelog.md
    echo "----------------------------------------"
    echo ""

    # 确认发布
    log_warning "确认发布版本 $version? (y/N)"
    read -r response
    if [[ ! "$response" =~ ^[Yy]$ ]]; then
        log_info "已取消发布"
        exit 0
    fi

    # 创建发布
    create_release "$version" "$message"

    # 显示发布信息
    show_release_info "$version"

    # 清理临时文件
    rm -f /tmp/changelog.md
}

# 脚本入口
main "$@"
