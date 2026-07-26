.PHONY: build build-all test lint lint-all shellcheck fmt fmt-all clean dev install release help

# 项目变量
APP_NAME    := nas-panel
SRC_DIR     := web
BUILD_DIR   := build
INSTALL_DIR := /opt/nas
BIN_PATH    := $(INSTALL_DIR)/nas-panel

# Go 构建变量
GO           := go
GOFLAGS      := -trimpath
LDFLAGS      := -s -w
BUILD_TARGET := $(SRC_DIR)/main.go

# 支持的平台
PLATFORMS := linux/amd64 linux/arm64 linux/arm/v7

## help: 显示帮助信息
help:
	@echo "NAS 家用存储系统 - 构建工具"
	@echo ""
	@echo "用法: make [目标]"
	@echo ""
	@echo "开发:"
	@echo "  build       编译当前平台二进制文件"
	@echo "  build-all   交叉编译全部平台"
	@echo "  dev         本地开发运行"
	@echo "  test        运行测试"
	@echo "  lint        Go 代码检查 (golangci-lint)"
	@echo "  lint-all    Go + Shell 全面检查 (golangci-lint + shellcheck)"
	@echo "  fmt         Go 格式化 (gofmt)"
	@echo "  fmt-all     Go + Shell + 前端全面格式化"
	@echo "  clean       清理构建产物"
	@echo ""
	@echo "部署:"
	@echo "  install     安装到系统 (需要 root)"
	@echo "  setup       一键部署全部服务 (需要 root)"
	@echo ""
	@echo "发布:"
	@echo "  release     构建发布版本 (交叉编译 + 打包)"
	@echo "  checksum    生成校验和"

## build: 编译当前平台
build:
	@echo "编译 $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
	cd $(SRC_DIR) && $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o ../$(BUILD_DIR)/$(APP_NAME) .

## build-all: 交叉编译全部平台
build-all:
	@echo "交叉编译全部平台..."
	@mkdir -p $(BUILD_DIR)
	@for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*} GOARCH=$${platform#*/}; \
		if [ "$${GOARCH%%/*}" = "arm" ]; then \
			GOARM=$${GOARCH##*/} GOARCH=arm; \
		else \
			GOARM=; \
		fi; \
		output=$(BUILD_DIR)/$(APP_NAME)-$$GOOS-$$GOARCH; \
		if [ "$$GOARCH" = "arm" ]; then output=$(BUILD_DIR)/$(APP_NAME)-$$GOOS-armv$$GOARM; fi; \
		echo "  -> $$output"; \
		cd $(SRC_DIR) && GOOS=$$GOOS GOARCH=$$GOARCH GOARM=$$GOARM $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o ../$$output . ; \
	done
	@echo "完成。产物在 $(BUILD_DIR)/"

## test: 运行测试
test:
	@echo "运行测试..."
	cd $(SRC_DIR) && $(GO) test ./... -v -count=1

## fmt: 格式化 Go 代码
fmt:
	@echo "格式化 Go 代码..."
	cd $(SRC_DIR) && $(GO) fmt ./...

## fmt-all: 全面格式化 (Go + Shell + 前端)
fmt-all: fmt
	@echo "格式化 Shell 脚本 (shfmt)..."
	@if command -v shfmt > /dev/null 2>&1; then \
		shfmt -l -w scripts/*.sh; \
	else \
		echo "  shfmt 未安装，跳过 (go install mvdan.cc/sh/v3/cmd/shfmt@latest)"; \
	fi

## lint: Go 代码检查
lint:
	@echo "运行 Go 代码检查..."
	@if command -v golangci-lint > /dev/null 2>&1; then \
		cd $(SRC_DIR) && golangci-lint run ./... ; \
	else \
		echo "golangci-lint 未安装，使用 go vet 代替..."; \
		cd $(SRC_DIR) && $(GO) vet ./... ; \
	fi

## lint-all: Go + Shell 全面检查
lint-all: lint
	@echo "Shell 脚本检查 (shellcheck)..."
	@if command -v shellcheck > /dev/null 2>&1; then \
		shellcheck scripts/*.sh; \
		echo "  ✓ Shell 检查通过"; \
	else \
		echo "  shellcheck 未安装，跳过 (apt-get install shellcheck)"; \
	fi

## shellcheck: 仅检查 Shell 脚本
shellcheck:
	@echo "Shell 脚本检查..."
	@if command -v shellcheck > /dev/null 2>&1; then \
		shellcheck scripts/*.sh; \
		echo "  ✓ Shell 检查通过"; \
	else \
		echo "  shellcheck 未安装，跳过 (apt-get install shellcheck)"; \
	fi

## clean: 清理构建产物
clean:
	@echo "清理构建产物..."
	rm -rf $(BUILD_DIR)
	@echo "完成。"

## dev: 本地开发运行
dev:
	@echo "开发模式启动 (端口 8090)..."
	cd $(SRC_DIR) && $(GO) run .

## install: 安装到系统
install: build
	@echo "安装 $(APP_NAME) 到 $(INSTALL_DIR)..."
	@if [ "$$(id -u)" != "0" ]; then \
		echo "错误: 需要 root 权限 (sudo make install)"; \
		exit 1; \
	fi
	mkdir -p $(INSTALL_DIR)
	cp $(BUILD_DIR)/$(APP_NAME) $(BIN_PATH)
	chmod 755 $(BIN_PATH)
	@if [ -f configs/nas-panel.service ]; then \
		cp configs/nas-panel.service /etc/systemd/system/; \
		systemctl daemon-reload; \
		systemctl enable nas-panel; \
		echo "systemd 服务已注册。使用: systemctl start nas-panel"; \
	fi
	@echo "安装完成。"

## setup: 一键部署全部服务
setup:
	@echo "运行一键部署脚本..."
	@if [ "$$(id -u)" != "0" ]; then \
		echo "错误: 需要 root 权限"; \
		exit 1; \
	fi
	bash scripts/setup.sh

## release: 构建发布版本
release: clean build-all checksum
	@echo ""
	@echo "========================================"
	@echo "  发布版本构建完成！"
	@echo "  产物目录: $(BUILD_DIR)/"
	@echo "========================================"
	@ls -lh $(BUILD_DIR)/

## checksum: 生成校验和
checksum:
	@echo "生成校验和..."
	cd $(BUILD_DIR) && sha256sum $(APP_NAME)-* > checksums.txt
	@echo "  -> $(BUILD_DIR)/checksums.txt"
