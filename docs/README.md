# 文档导航

文档按用途而不是按开发时间组织。权威契约保留在对应代码目录旁；环境、项目来源、
runtime 操作和历史任务集中在本目录。

## 阅读顺序

1. [仓库 README](../README.md)：范围、当前状态和快速开始。
2. [功能模拟器架构契约](../emu/docs/architecture.md)：架构语义、状态 owner 和 effect
   边界的权威来源。
3. [周期模型入口](../timing/README.md)：Timing IR、周期组件、Kernel 与 memory system。
4. [runtime 接入](runtime/vortex-runtime-integration.md)：原生 benchmark 如何选择功能型或
   周期型后端。
5. [runtime 测试与周期评估](runtime/runtime-error-diagnosis-20260910.md)：支持集结果、
   性能瓶颈和 RTLSIM 对比。

## 分类

### 项目来源与冻结输入

- [周期模型基线](project/baseline.md)
- [冻结 RTL 快照](project/rtl-snapshot.md)

这些文档解释仓库从何处建立及 RTL evidence 的版本边界，不描述最新运行接口。

### 开发环境与依赖

- [开发环境](development/environment.md)
- [依赖清单](development/dependencies.md)
- [依赖策略](development/dependency-policy.md)
- [基础支撑包](development/support.md)

### 功能与周期设计

- [功能模拟器 Living Architecture Contract](../emu/docs/architecture.md)
- [Timing IR 与周期实现索引](../timing/README.md)
- [结构化 Timing IR](../timing/ir.yaml)
- [周期 Kernel API 与生命周期](../timing/kernel-usage.md)
- [memory system 子系统](../timing/memsys/README.md)
- [runner 子系统](../timing/runner/README.md)
- [effects 适配层](../timing/effects/README.md)

`timing/*.md` 中的契约和实施记录被 `timing/ir.yaml` 作为 evidence 引用，因此保留在
原路径；它们在 [timing/README.md](../timing/README.md) 中进一步分为权威事实、当前
接口和历史里程碑证据。

### Runtime、测试与诊断

- [原生 Vortex runtime 接入](runtime/vortex-runtime-integration.md)
- [支持集 Slurm 测试方法](runtime/runtime-supported-tests.md)
- [扩展测试、性能及 RTLSIM 周期分析](runtime/runtime-error-diagnosis-20260910.md)
- [RTLSIM 与 Timing 双侧 trace 证据及小规模复测](runtime/rtlsim-timing-trace-analysis-20260914.md)
- [修复后全支持集小规模回归与 trace 对比（31 项）](runtime/rtlsim-timing-small-regression-20260917.md)

诊断文档是按日期累积的实验记录；其中的作业状态是历史快照，当前结论以文末最新章节
和保存的 result/summary 文件为准。

### 历史任务定义

- [T8：初版 Timing IR](history/tasks/T8.md)
- [T9：周期流水线框架](history/tasks/T9.md)
- [T10：Multi-Warp Scheduling](history/tasks/T10.md)
- [T11：Kernel 执行闭合](history/tasks/T11.md)
- [T12：L1 Memory System](history/tasks/T12.md)

这些文件保存当时的任务输入和完成边界，不作为最新 API 文档。当前实现以代码、Timing
IR、架构契约、Kernel 使用说明及 T12 交付记录为准。

## 文档状态约定

| 类型 | 含义 |
| --- | --- |
| 权威契约 | 功能 architecture、Timing IR 及带 RTL evidence 的规则；实现变化必须同步更新 |
| 当前指南 | README、Kernel/runtime 使用文档；应反映当前可运行入口 |
| 历史证据 | T8–T12 任务和 milestone progress；保留验收上下文，不回写成最新设计 |
| 实验报告 | Slurm、诊断和 RTLSIM 对比；记录可复查事实，不替代契约或自动化测试 |

新增文档应先选择上述分类。临时运行输出、二进制和大日志放入忽略的 `.cache/`，不要在
仓库根目录新增孤立 Markdown；确需入库的实验说明统一从本索引链接。
