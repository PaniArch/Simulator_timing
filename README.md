# Vortex 周期级模拟器

本仓库是 Vortex GPU 周期级模拟器的开发仓库，用于实现和验证指令执行、
流水线、调度、访存、缓存以及延迟和仲裁等周期行为。功能执行层提供语义参考，
RTL 快照与配置文件用于约束周期模型的结构和参数。

## 仓库结构

- `timing/`：周期模型的核心开发区域。
- `isa/`：指令目录、译码规则、执行效果与 ISA 契约。
- `emu/`：功能执行模型，包括 Warp、Core、CTA、Device 和状态管理。
- `support/`：共享基础设施，包括内存模型和 Berkeley SoftFloat 封装。
- `Vortex_rtl/`：RTL 参考快照、硬件配置和生成后的配置定义。
- `internal/dependencycheck/`：第三方依赖的可用性和序列化往返测试。
- `vendor/`：Go 依赖的离线副本。
- `env/`：统一的开发环境入口。
- `scripts/`：环境检查、构建与验证脚本。
- `docs/`：架构、环境、依赖和开发约束文档。
- `Task_timing/`：周期模型开发任务说明。
- `logs/`：运行日志目录。

## 开发环境

项目使用固定工具链和离线依赖：

- Go 1.26.2
- GCC/G++ 9.4.0
- Berkeley SoftFloat Release 3e
- Akita v5.0.0-beta.10
- `go.yaml.in/yaml/v3` v3.0.5
- `github.com/pelletier/go-toml/v2` v2.4.3

本地环境胶囊位于 `.harness-environment/v1`；在 Harness 中可挂载为
`/opt/simulator-environment`。所有开发命令应先加载统一环境：

```bash
source env/env.sh
```

环境默认设置 `GOTOOLCHAIN=local`、`GOFLAGS=-mod=vendor`、`GOPROXY=off`
和 `GOSUMDB=off`。正常构建与测试仅使用固定工具链和仓库内的 `vendor/`，
不需要网络访问。

## 验证

运行完整验证：

```bash
./scripts/verify-all.sh
```

该命令检查 RTL 快照、固定环境、Go 模块、构建、测试、静态分析、格式以及
空缓存离线构建。仅验证离线依赖可用性时运行：

```bash
./scripts/verify-offline.sh
```
