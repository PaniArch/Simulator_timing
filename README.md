# Vortex 功能型与周期级模拟器

本仓库实现冻结单 Core Vortex 配置下的功能模拟器和周期级模拟器。功能层负责
RV32/SIMT 架构语义，周期层依据仓库内 RTL 快照和 Timing IR 表达流水线、Multi-Warp、
CTA/Kernel 生命周期、L1 Cache、LMEM、访存合并、背压及可见性。

原生 Vortex runtime 可通过同一 `simtiming` driver 选择两种后端：

```text
Vortex host benchmark → native runtime → simtiming
                                      ├─ functional → emu/
                                      └─ timing     → timing/runner + timing/memsys
                                                       ├─ fixed（默认）
                                                       └─ rtlsim-dram → DramSim / Ramulator
```

## 当前状态

- 已实现 Timing IR、周期流水线、四 Warp 调度、CTA/Kernel 执行闭环，以及
  L1 I/D Cache、LMEM、访存合并、背压、flush 和 backing visibility。
- 冻结拓扑为 RV32、1 Core、4 Warp、每 Warp 4 lane；当前配置关闭 L2/L3。
- 功能型与周期型后端均已接入原生 Vortex runtime。周期后端现支持直接复用 RTLSim 的
  `DramSim/Ramulator`；固定 100 周期后端仍保留为默认选项。
- DRAM 桥接已按物理 memory bus bank 保持返回 FIFO 顺序，支持跨 bank 返回及背压；
  同一 DramSim 实例跨 Kernel launch 和 cache flush 复用。
- 2026-09-17 最新修复版完成 31 种 benchmark、63 组输入的 Timing/RTLSim 配对回归，
  全部功能通过、指令总数一致。周期结果见下表。
- 当前不支持 L2/L3、多 Core coherence 和 VM/TLB；外部桥接尚未逐拍复现完整 RTL
  socket 仲裁与响应寄存级，因此不宣称全系统逐周期等价。

## DRAM 与外部存储后端

通过 `SIMTIMING_MEMORY_BACKEND` 选择周期模型的外部存储路径：

| 选项 | 行为 | 依赖 |
| --- | --- | --- |
| `fixed`（默认） | 请求实际接受后固定 100 cycles 服务延迟；排队和返回背压另计 | 无需 Ramulator |
| `rtlsim-dram` | 直接编译外部 Vortex 的 `sim/common/dram_sim.cpp`，调用原有 Ramulator；不叠加固定 100 cycles | Vortex 源码、Ramulator 库、DramSim 插件 |

`rtlsim-dram` 使用冻结平台的 2 channels、64-byte bus、clock ratio 1；HBM2/FRFCFS、
刷新、地址映射及请求拆分由原 DramSim 实现。桥接默认最多 16 个在途请求、每 Core 周期
接受 1 个请求并返回 1 个响应，这些是桥接限制，不是 DRAM controller 参数。

必须通过 `SIMTIMING_DRAM_LIBRARY` 指定插件的绝对路径；加载失败会明确报错，不会回退
到固定延迟。`functional` 模式不加载 DRAM。原 DramSim 的写回调表示 Ramulator 接受写请求，
不表示物理 DRAM 已排空。完整语义见 [DramSim 接入说明](integration/dramsim/README.md)。

## 当前验证结果

以下为物理 bus bank 返回排序和公共 runtime 分配器均修复后的结果，使用 `rtlsim-dram`，
比较两端原始最终累计 PERF 周期，不扣启动周期、不使用延迟重放。

| 测试集合 | 双端通过 | 等权 MAPE | 最大绝对误差 |
| --- | ---: | ---: | ---: |
| 小规模 | 31/31 | 1.474% | 3.689% |
| 扩大规模 | 30/30 | 0.694% | 3.630% |
| 全部（含 2 组额外 io_addr 诊断输入） | 63/63 | 1.168% | 3.689% |

63 组绝对误差均在 5% 内；这是当前冻结配置与指定输入集的结果，不是任意 workload 的误差
保证，也不代表所有 benchmark 的默认或最大规模已通过。逐项参数、周期和修复对照见
[回归报告第 15.6–15.7 节](docs/runtime/rtlsim-timing-small-regression-20260917.md#156-最终回归结果)。
早期固定延迟后端的 28.48% MAPE 和修复前 DRAM 结果仅作为历史记录保留。

当前重点是正确性和周期建模，运行速度仍需优化：报告第 16 节的 3 个样本中，Timing 比关闭
详细 trace 的 RTLSim 慢约 12.26–14.56 倍；该小样本不作为全支持集速度结论。

详细范围与证据从 [文档导航](docs/README.md) 开始阅读。

## 快速开始

项目使用 Go 1.26.2、cgo、固定 SoftFloat 和仓库内 `vendor/`。固定环境需位于
`/opt/simulator-environment` 或 `.harness-environment/v1`；后者被 Git 忽略，克隆仓库不会
自动获得工具链和 SoftFloat。准备方式与路径约定见 [开发环境](docs/development/environment.md)。
在仓库根目录加载环境并验证：

```bash
source env/env.sh
./scripts/verify-all.sh
```

仅验证 Timing IR 和周期契约：

```bash
./scripts/verify-timing.sh
```

运行原生 benchmark 还需已构建的 Vortex runtime、benchmark 和 `kernel.vxbin`。
默认从相邻的 `../vortex` 和 `../build` 查找源码及构建产物，也可设置 `VORTEX_HOME`、
`VORTEX_BUILD`。这些外部依赖不包含在本仓库的冻结 `Vortex_rtl/` 快照中。

构建 runtime backend，并使用固定延迟后端或功能模式运行：

```bash
./scripts/build-vortex-runtime.sh
module load compilers/gcc-12.2.0
SIMTIMING_MEMORY_BACKEND=fixed ./scripts/run-vortex-benchmark.sh --mode timing vecadd -n16
./scripts/run-vortex-benchmark.sh --mode functional vecadd -n16
./scripts/test-vortex-runtime-smoke.sh
```

在已有 Vortex/Ramulator 构建的环境中启用 DRAM：

```bash
source env/env.sh
./scripts/build-vortex-runtime.sh
module load compilers/gcc-12.2.0
make -C integration/dramsim CXX="$(command -v g++)"
export SIMTIMING_MEMORY_BACKEND=rtlsim-dram
export SIMTIMING_DRAM_LIBRARY="$PWD/.cache/vortex-runtime/libsimtiming-dram.so"
./scripts/run-vortex-benchmark.sh --mode timing vecadd -n32
```

`module load` 是当前集群的 C++ 运行库配置方式。DramSim 构建可覆盖 `VORTEX_HOME`、
`RAMULATOR_HOME` 和 `OUT_DIR`；执行节点也必须能加载对应 Ramulator 与 C++ 动态库。
复现最新精度结果还需使用包含低地址 reserve 分配修复的外部公共 runtime `libvortex.so`，
仅重建本仓库的 Go backend 不足以包含该修复，详见报告第 15.4–15.6 节。

完整运行接口见 [runtime 接入说明](docs/runtime/vortex-runtime-integration.md)。
扩大配对测试工具及其已验证输入快照要求见 [DramSim 测试集说明](integration/dramsim/README.md#expanded-paired-suite)。

## 仓库结构

| 路径 | 职责 |
| --- | --- |
| `emu/` | 功能执行、Warp/Core/CTA/Device 状态与唯一架构 owner |
| `timing/` | Timing IR、周期组件、调度、effects、runner 与 memory system |
| `isa/` | 指令目录、译码和功能效果 |
| `integration/` | 原生 Vortex runtime 到两种模拟器后端的适配 |
| `integration/dramsim/` | 原 RTLSim DramSim 的动态库桥接、构建和配对测试说明 |
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
- [DramSim / Ramulator 构建与接口契约](integration/dramsim/README.md)
- [benchmark 与 RTLSIM 周期评估](docs/runtime/runtime-error-diagnosis-20260910.md)
- [双侧 trace 对比与全通过集小规模复测](docs/runtime/rtlsim-timing-trace-analysis-20260914.md)
- [回归与诊断报告：DRAM 接入、63 组最终结果及三方速度测试](docs/runtime/rtlsim-timing-small-regression-20260917.md)

诊断报告按实验顺序追加，最新结论以文末对应章节为准。原始日志、库快照和完整 JSON/CSV
位于被忽略的 `.cache/`，不会随 GitHub 仓库发布；仓库内保留汇总表、方法和证据路径。

## 验证原则

`scripts/verify-all.sh` 检查冻结 RTL manifest、功能与周期代码、离线依赖、测试和静态
分析。Timing PASS 还必须结合 host oracle、launch/finish、CTA 生命周期及 backing
visibility 判断；Slurm 状态或单独的进程退出码不能替代这些证据。不能静态确认的 RTL
行为继续在 Timing IR 中保留为 `UNRESOLVED`，不得为实现方便自行补定。
