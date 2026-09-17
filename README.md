# Vortex 功能型与周期级模拟器

本仓库实现冻结单 Core Vortex 配置下的功能模拟器和周期级模拟器。功能层负责
RV32/SIMT 架构语义，周期层依据仓库内 RTL 快照和 Timing IR 表达流水线、Multi-Warp、
CTA/Kernel 生命周期、L1 Cache、LMEM、访存合并、背压及可见性。

原生 Vortex runtime 可通过同一 `simtiming` driver 选择两种后端：

```text
Vortex host benchmark → native runtime → simtiming
                                      ├─ functional → emu/
                                      └─ timing     → timing/runner + timing/memsys
```

## 当前状态

- T8–T12 的基础模型结构已经完成：Timing IR、周期流水线、四 Warp 调度、CTA/Kernel
  执行闭环以及 L1/LMEM 存储路径均已接入。
- 冻结拓扑为 RV32、1 Core、4 Warp、每 Warp 4 lane；当前配置关闭 L2/L3。
- Timing 模型的 L1 miss 进入确定性的 external backend，默认从请求实际接受起延迟
  100 cycles；它不是 DRAM 微架构模型。
- 功能型与周期型后端均已连接原生 runtime。2026-09-17 修复后小规模回归覆盖正式支持
  集 28 项及 basic/wsync/bfs，共 31 项 Timing/RTLSIM 双侧 PASS；PERF 周期等权 MAPE
  为 28.48%（两侧外部 memory backend 不同）。这不代表原规模全部通过或逐周期 RTL 等价。
- DRAM controller、L2/L3、多 Core coherence、VM/TLB 和 RTLSIM trace 精度收敛不在
  当前基础模型范围内。

详细范围与证据从 [文档导航](docs/README.md) 开始阅读。

## 快速开始

项目使用仓库内 vendor 和固定工具链。所有 Go 构建、测试及验证先加载环境：

```bash
source env/env.sh
./scripts/verify-all.sh
```

仅验证 Timing IR 和周期契约：

```bash
./scripts/verify-timing.sh
```

构建原生 runtime backend 并运行小型 benchmark：

```bash
./scripts/build-vortex-runtime.sh
module load compilers/gcc-12.2.0
./scripts/run-vortex-benchmark.sh --mode timing vecadd -n16
./scripts/run-vortex-benchmark.sh --mode functional vecadd -n16
./scripts/test-vortex-runtime-smoke.sh
```

默认从仓库相邻的 `../vortex` 和 `../build` 查找 Vortex 源码与构建产物，也可通过
`VORTEX_HOME`、`VORTEX_BUILD` 指定。完整使用和可见性语义见
[runtime 接入说明](docs/runtime/vortex-runtime-integration.md)。

## 仓库结构

| 路径 | 职责 |
| --- | --- |
| `emu/` | 功能执行、Warp/Core/CTA/Device 状态与唯一架构 owner |
| `timing/` | Timing IR、周期组件、调度、effects、runner 与 memory system |
| `isa/` | 指令目录、译码和功能效果 |
| `integration/` | 原生 Vortex runtime 到两种模拟器后端的适配 |
| `cmd/` | timing 诊断、示例和 runtime C ABI 入口 |
| `support/` | 内存与 SoftFloat 等共享基础设施 |
| `Vortex_rtl/` | 冻结 RTL、配置和来源清单；作为只读建模证据 |
| `docs/` | 项目、开发、runtime 和历史任务文档索引 |
| `scripts/` | 环境、验证、runtime 构建和 benchmark/Slurm 工具 |
| `vendor/` | 离线 Go 依赖 |
| `logs/` | 需要入库的实验日志说明；临时产物位于忽略的 `.cache/` |

## 文档入口

- [完整文档导航](docs/README.md)
- [功能模拟器架构契约](emu/docs/architecture.md)
- [周期模型与 Timing IR](timing/README.md)
- [周期 Kernel 使用说明](timing/kernel-usage.md)
- [runtime 接入说明](docs/runtime/vortex-runtime-integration.md)
- [benchmark 与 RTLSIM 周期评估](docs/runtime/runtime-error-diagnosis-20260910.md)
- [双侧 trace 对比与全通过集小规模复测](docs/runtime/rtlsim-timing-trace-analysis-20260914.md)
- [修复后 31 项小规模回归与 trace 证据](docs/runtime/rtlsim-timing-small-regression-20260917.md)

## 验证原则

`scripts/verify-all.sh` 检查冻结 RTL manifest、功能与周期代码、离线依赖、测试和静态
分析。Timing PASS 还必须结合 host oracle、launch/finish、CTA 生命周期及 backing
visibility 判断；Slurm 状态或单独的进程退出码不能替代这些证据。不能静态确认的 RTL
行为继续在 Timing IR 中保留为 `UNRESOLVED`，不得为实现方便自行补定。
