# Vortex 功能模拟器 Architecture Contract

> Contract 版本：T0 / 2026-08-24
>
> 事实基线：只读参考快照 `Vortex_rtl`，上游提交 `e2b9745b637ce8ac462be2f0e01b5d76542dc6c0`（见 [E-SRC-01]）

## 1. 文档角色与维护协议

本文是后续 T1–T7 全过程持续演化的架构真相源（Architecture Contract），不是 T0 后冻结不动的一次性设计说明。

- 每个 T1–T7 Task **开始前必须完整读取本文**，实现与本文冲突时不得静默绕过。
- 每个 Task **结束前必须同步更新本文**：加入新证据、实际实现结论、状态迁移、未决项和决策记录；代码与本文必须在同一个 Task 中恢复一致。
- RTL/config 事实必须使用第 11 节的证据格式。实现偏好不能伪装成 RTL 事实。
- 证据不足时必须登记为 `UNRESOLVED`；不得为了推进实现而猜测。
- 本文只约束架构功能。pipeline、cache timing、issue width、周期调度、性能计数精度以及 RTL 物理存储布局，除非以后被明确提升为契约，否则不是功能模拟器要求。

## 2. Contract 状态分类

每项重要结论使用以下一个状态：

| 状态 | 含义与使用门槛 | 允许的迁移 |
| --- | --- | --- |
| **FROZEN** | 已有充分依据，且属于所有后续 Task 必须遵守的核心架构约束。T0 仅把功能目标、冻结配置边界、单向层级依赖、唯一 canonical state owner、ISA 不持有长期状态等稳定原则放入此类。 | 只有新 RTL/config 证据证明基线改变，或所有受影响 Task 明确完成迁移时才能修改；必须写决策记录。 |
| **PROVISIONAL** | 当前推荐的逻辑分层、owner 映射、接口名称或设计方向；尚未经过对应实现阶段验证。 | 可保持 `PROVISIONAL`、经证据/实现验证转为 `RESOLVED`，或在发现歧义时转为 `UNRESOLVED`。 |
| **UNRESOLVED** | 当前证据不足以唯一决定；禁止把某个猜测实现成隐含契约。 | 补齐指定证据并在要求的最晚 Task 前转为 `RESOLVED`；若仍不足，必须阻止依赖该结论的实现。 |
| **RESOLVED** | 曾经的 provisional/unresolved 项已由指定 Task 的 RTL 研究或实现验证收敛。 | 保留原问题、解决 Task、证据和结论；基线变化时可重新打开为 `UNRESOLVED`。 |

状态描述的是**契约成熟度**。证据目录中的 `CONFIRMED` 只表示“从当前快照直接读到的事实”，不等价于把模拟器的具体实现方案 `FROZEN`。

## 3. 总体目标与冻结范围

### 3.1 功能目标

**[FROZEN]** 模拟器在下述冻结配置下接收 Kernel、程序启动/入口地址和输入数据，完成 Kernel 功能执行，并以 RTL/reference checker 可观察到的架构结果为正确性标准。它是功能模型：不建模周期、吞吐、cache 命中时序、pipeline hazard 或性能。参考包刻意不含 reference checker 实现，因此 checker 的逐字段比较协议仍见 U-08。[E-SRC-01][E-KRN-01]

### 3.2 冻结配置矩阵

**[FROZEN]** T1–T7 的默认目标只覆盖本表，不因 RTL 中存在其他可配置模块而扩展范围。

| 类别 | 冻结值/能力 | 状态与依据 |
| --- | --- | --- |
| 数据宽度 | RV32，`XLEN=32`；内存地址宽度 32；F 扩展下 `FLEN=32` | 启用；[E-SRC-01][E-CFG-02] |
| 拓扑 | 1 cluster、1 core、socket size 1 | [E-CFG-01][E-CFG-02] |
| SIMT | 4 warps/core、4 threads/warp（即 4 lanes）、8 barriers | [E-CFG-01][E-CFG-02] |
| 基础/标准 ISA | RV32I、M、F、Zicond、CSR/System | 启用；I/System 由 decode 的非条件 case 覆盖，M/F/Zicond 由配置宏启用；[E-ISA-01][E-ISA-02] |
| 标准 ISA 排除项 | D、C、A、虚拟内存 | 禁用；D 仅在 XLEN64 时启用，当前为 RV32；[E-CFG-01][E-CFG-02] |
| Vortex custom/SIMT | TMC、WSPAWN、SPLIT、JOIN、BAR、PRED、WSYNC、VOTE、SHFL、WGATHER，以及 decode 中的浮点 pack-load 形式 | 启用的 decode 路径；[E-ISA-02][E-ISA-03] |
| 可选加速/图形扩展 | TCU、DMA、DXA、TEX、RASTER、OM、RTU | 禁用；[E-CFG-01][E-CFG-02] |
| 内存能力 | instruction cache、data cache、local/shared memory（LMEM）启用；L2、L3 禁用；VM 地址转换禁用 | [E-CFG-01][E-CFG-02][E-MEM-01] |
| 内存尺度/地址图 | 32-bit 地址；64-byte memory block；LMEM 16 KiB/core（`LMEM_LOG_SIZE=14`）；user base `0x00010000`、LMEM/stack base `0xffff0000` | [E-CFG-01][E-MAP-01] |
| 原子能力 | A 扩展禁用；local/shared 路径明确不支持 AMO | [E-CFG-01][E-MEM-01] |

“启用 cache”只描述 RTL 配置和 instruction/data 请求边界；功能模型不需要复制 cache。L2/L3 的 passthrough 物理结构也不产生新的 architectural memory space。

## 4. 模拟器层级与单向依赖

**[FROZEN]** 主层级及依赖方向为：

`Device → Core → CTA → Warp → Lane → ISA`

上层拥有下层的集合和生命周期，下层只通过传入的窄 view/service 访问所需信息；下层不得反向引用或操纵上层 executor。Memory 与 CSR 是按作用域挂靠的辅助服务，不是打破层级的全局后门。

| 层级 | 负责 | 不负责 |
| --- | --- | --- |
| Device | Kernel launch 输入、冻结配置、core 集合、device-global memory、全局完成汇总；将 CTA 工作分派给 core | 单指令 decode/语义；lane 寄存器；周期级互连/cache 仿真 |
| Core | resident CTA/warp 容量、warp manager/scheduler、active-warp 集合、CTA admission、core-scoped CSR/service | 某 lane 的算术语义；复制每个 warp 的 PC/GPR；device-global memory 的第二份副本 |
| CTA | CTA 身份/维度/线程坐标上下文、所属 warps、CTA local/shared-memory region、barrier coordination、CTA completion | 指令 decode；其他 CTA 的 lane/warp 状态；整个 device memory |
| Warp | 一个 SIMT warp 的 PC、active lane mask、活跃/终止状态、divergence/reconvergence、warp-scope control effects | Kernel/CTA 调度策略；每 lane GPR/FPR 的第二份副本；全局 memory 存储 |
| Lane | 单线程 GPR/FPR 和 lane/thread identity view；发起 lane 地址/数据访问 | warp PC、warp 调度、CTA barrier、跨指令的 decode 状态 |
| ISA | decode 和**单条指令**功能语义；读取 view 并产生 result/effect | 任何跨指令长期状态；scheduler、CTA、Core、Kernel executor 或 MGPUSim 知识；直接提交全局副作用 |

### 4.1 辅助模块位置

- **Memory [PROVISIONAL]**：`InstructionSource` 是 Device/global program image 的只读取指令 view；global memory 的 canonical store 位于 Device；每个 CTA 的 local/shared region 逻辑归 CTA，并可由 Core memory service 做地址路由。实现位置不等于 canonical owner。[E-MEM-01][E-MEM-02]
- **CSR [UNRESOLVED U-05；分 scope 方向 PROVISIONAL]**：已确认 storage/readback 分散在 warp、CTA、core/派生 context，不应建成一份无 scope 的全局 map；具体逐 CSR owner 在 U-05 收敛。[E-CSR-01][E-CSR-02]
- **Barrier [UNRESOLVED U-04；CTA coordinator 候选 PROVISIONAL]**：已确认 Core 负责让等待 warp 不可运行及重新激活；CTA/barrier coordinator 是当前逻辑 owner 候选，但 namespace 证据不足；跨 core global barrier 在冻结的单 core 配置中不形成额外功能需求。[E-BAR-01]

## 5. State Ownership

### 5.1 唯一 owner 原则

**[FROZEN]** 每一项长期 architectural state 必须恰有一个 canonical owner。其他层只能持有不可独立修改的 view、临时计算值或显式派生缓存；不得在多个层维护可分叉的“真值”。RTL 中 RAM/寄存器的物理模块位置仅是证据，不自动决定功能模型的逻辑 owner。

### 5.2 初始 ownership 映射

以下是功能模型的初始逻辑映射。`PROVISIONAL` 项须由对应实现 Task 验证，不能以“物理 RTL 在某 module 中”为由自动提升。状态列含 `UNRESOLVED U-xx` 时，表中的 owner 只是允许后续研究的候选边界，不是已经收敛的决定。

| Canonical state | 初始 owner | 状态 | RTL 观察 |
| --- | --- | --- | --- |
| GPR | Lane（按 core/warp/lane/integer-register 索引） | PROVISIONAL | RTL 的 `VX_opc_unit.gpr_ram` 地址包含 register、warp/SIMD/lane 维度；I/F register type 进入统一物理编号。[E-REG-01] |
| FPR | Lane（按 core/warp/lane/floating-register 索引） | PROVISIONAL | F 扩展增加独立 register type，但复用相同按线程寻址的物理 bank；这不使 FPR 成为 Core-owned state。[E-REG-01] |
| PC | Warp | PROVISIONAL | `VX_scheduler.warp_pcs` 是 per-warp 表；冻结配置下其写入来源为顺序推进、普通 branch、trap/MRET、JOIN、CTA dispatch 和 WSPAWN。TMC 不写 PC。[E-PC-01] |
| active lane mask | Warp | PROVISIONAL | `VX_scheduler.thread_masks` 是 per-warp 表；CTA dispatch、WSPAWN、TMC/SPLIT/JOIN 会更新它，MRET 在保存的 pre-trap mask 非零时恢复它。[E-PC-01] |
| active warp state | Core / Warp Manager | PROVISIONAL | `VX_scheduler.active_warps` 决定可调度 warp；TMC mask=0 退休 warp。[E-SCHED-01] |
| divergence / reconvergence stack | Warp | PROVISIONAL | `VX_split_join`/`VX_ipdom_stack` 按 `wid` 保存 mask、next PC 和 stack pointer。[E-DVG-01] |
| thread context | Lane，CTA 派生字段由 CTA Context 提供只读 view | PROVISIONAL | CTA dispatcher 为每 lane 展开 3D thread coordinates，CSR unit 组合 thread/warp/core id。[E-CTA-01][E-CSR-02] |
| warp context（wid、所属 CTA、launch residue） | Warp；映射/生命周期由 Core Warp Manager 维护 | PROVISIONAL | dispatcher 维护 wid→CTA slot 和 per-warp residue；scheduler 维护 warp execution state。[E-CTA-01] |
| CTA context | CTA | PROVISIONAL | dispatcher 的 `cta_ctx_ram` 保存 block/grid/entry/param/lmem 等，remaining-warps 决定 slot 生命周期。[E-CTA-01] |
| CSR | 候选为按 scope 拆分：已确认 FCSR/trap/mscratch 为 per-warp，CTA fields 为 CTA，常量/计数多为 core 派生 | **UNRESOLVED U-05**（已确认子集保持 PROVISIONAL） | 不存在一个能正确覆盖全部 CSR 的单一 owner；逐 CSR scope 尚未收敛。[E-CSR-01][E-CSR-02] |
| barrier state | 候选为 CTA Barrier Coordinator（Core 提供阻塞/解锁） | **UNRESOLVED U-04**（候选边界 PROVISIONAL） | RTL `VX_bar_unit` 保存 mask/count/events/phase 并向 scheduler 输出 unlock mask，但软件如何形成 CTA 隔离 key 仍缺证据。[E-BAR-01] |
| local/shared memory | CTA 的已分配 LMEM region | PROVISIONAL | 物理 LMEM 在 core；dispatcher 为 resident CTA 分配固定 stride slot/base。[E-MEM-01][E-MEM-02] |
| global memory | Device Memory | PROVISIONAL | LSU 将非-local lane 请求路由到 dcache/device memory 边界；I/D cache 不是额外 architectural owner。[E-MEM-01] |

临时 pipeline metadata、scoreboard bits、cache lines、fetch tags、pending request queues、性能计数和 arbitration state 默认不属于功能模拟器的 architectural state。某项若影响 reference checker 可见结果，必须先登记并重新分类。

## 6. ISA、State 与 Effect 边界

**[FROZEN]** ISA 负责 decode 和单指令功能语义；ISA 不拥有跨指令长期机器状态。ISA 不感知 scheduler、CTA、Core、Kernel executor 或 MGPUSim。State 由第 5 节对应层级拥有。

**[PROVISIONAL]** ISA 通过抽象边界协作：调用者提供 lane/warp/CSR/memory 的最小只读 `View`，ISA 返回显式 `Result` / `Effect`；owner 校验并应用 effect。这里的名称只表达职责，不预先冻结 Go interface、transaction 类型、effect struct、commit 顺序、rollback、版本号或并发算法。

### 6.1 Instruction Effect Ownership

以下映射把“当前快照直接确认的 effect 路径”与“功能模型建议的 canonical owner”分开：`CONFIRMED` 仅描述 RTL 事实，owner 方向仍为 `PROVISIONAL`；含 `UNRESOLVED` 的行不得据此定稿提交机制。

| 指令/效果 | 当前已确认的 RTL effect 路径 | canonical effect owner / 应用位置 | Contract 状态 |
| --- | --- | --- | --- |
| ADD / FADD | ALU/FPU 按 active lane 产生结果，经 commit/writeback 按 lane mask 写目标 register。[E-EXEC-01][E-REG-01] | 对应 Lane 的 GPR / FPR | RTL path CONFIRMED；owner PROVISIONAL |
| LW / FLW | LSU 按 lane 形成地址并返回 lane data；local/global route 由 memory attribute 决定。[E-EXEC-01][E-MEM-01] | Memory 负责读取；load result 由对应 Lane register owner 应用 | RTL path CONFIRMED；owner PROVISIONAL |
| branch / JAL / JALR / trap return | ALU 产生 taken/destination，scheduler 重定向 per-warp PC。[E-BR-01][E-PC-01] | Warp PC；trap CSR effect 交给其 scope owner | RTL path CONFIRMED；owner PROVISIONAL；divergent branch **UNRESOLVED U-01** |
| TMC | WCTL 形成 tmask；scheduler 更新 per-warp mask/active bit，mask=0 通知退休，不产生 PC effect。[E-WCTL-01][E-SCHED-01] | Warp active-lane mask；Core Warp Manager 处理 warp retirement | RTL path CONFIRMED；owner PROVISIONAL |
| SPLIT / JOIN | WCTL 形成 then/else masks；split/join stack 恢复 mask/PC。[E-WCTL-01][E-DVG-01] | Warp divergence/reconvergence state、mask、PC | RTL path CONFIRMED；owner PROVISIONAL |
| VOTE / SHFL / WGATHER | ALU 读取 active mask 和跨 lane operands，产生各目标 lane result。[E-ALU-01] | 跨 lane 读取由 Warp view 提供；结果由目标 Lane register owner 应用 | RTL path CONFIRMED；owner PROVISIONAL |
| WSPAWN | WCTL 形成 target warp mask/PC；scheduler 激活目标 warp、设置 lane-0 mask/PC 并复制 mscratch。[E-WCTL-01][E-SCHED-01] | Core Warp Manager 创建/激活目标 warps | 已观察路径 CONFIRMED；完整初始化 effect **UNRESOLVED U-02** |
| WSYNC | WCTL 等待该 warp 的 pipeline pending 状态排空，再由 scheduler 解锁。[E-WCTL-01] | Core/Warp Manager 控制该 warp 可运行性 | 已观察路径 CONFIRMED；无周期模型的功能语义 **UNRESOLVED U-03** |
| BAR / BAR.ARRIVE / BAR.WAIT | WCTL 解析 barrier request；bar unit 更新 mask/count/events/phase 并产生 unlock mask。[E-WCTL-01][E-BAR-01] | 候选为 CTA Barrier Coordinator 更新 state，Core 解锁 warps | 已观察路径 CONFIRMED；namespace/owner **UNRESOLVED U-04** |
| instruction memory / fetch | fetch 以 scheduled Warp PC 发出只读 instruction request。[E-FETCH-01] | Device Program Image / `InstructionSource` 提供指令；Warp 保持 PC | RTL path CONFIRMED；owner PROVISIONAL |
| global memory load/store | LSU switch 将非-local lane request 送 global/dcache path。[E-MEM-01] | Device Memory 保存 global bytes；load result 回 Lane | RTL route CONFIRMED；owner PROVISIONAL |
| local/shared memory load/store | LSU switch 将 local lane request 送物理 core LMEM；dispatcher 给 resident CTA 分配 region。[E-MEM-01][E-MEM-02] | CTA-owned LMEM region 保存 bytes；Core Memory service 只路由 | RTL route/allocation CONFIRMED；owner PROVISIONAL |
| CSR read/write/System | CSR unit 解析 read/modify/write，已知 storage 分散在 scheduler、CTA context 与 CSR data。[E-CSR-01][E-CSR-02] | 交给该 CSR scope 的 Warp/CTA/Core owner | 已观察路径 CONFIRMED；逐项 scope **UNRESOLVED U-05** |

## 7. Execution Model 骨架

**[FROZEN]** 稳定的功能数据流为：

`select warp → fetch → decode → read state/view → ISA semantics → result/effect → state update`

- `select warp` 只选择当前可运行的 active warp；具体公平性、优先级和周期行为不属于功能契约。
- `fetch` 以 Warp PC 从 `InstructionSource` 读取指令；不要求模拟 I-cache。
- `read state/view` 构造该指令所需的 lane/warp/CSR/memory 视图，不转移 state ownership。
- `ISA semantics` 必须是单指令作用域；所有长期变化显式返回。
- `state update` 由 effect 指向的唯一 owner 完成，并推动 termination/completion 状态。

**[PROVISIONAL]** 后续按以下能力阶梯扩展，每一级只能依赖其左侧已验证能力：

`Complete ISA → Architectural State → Single-Lane Warp → SIMT Warp → Multi-Warp Core → CTA/Barrier → Kernel Execution`

此路径不预设 transaction framework、rollback、owner version、并发控制或 RTL pipeline 的提交算法。

## 8. 关键接口职责（名称均为 PROVISIONAL）

| 边界 | 负责/交换内容 | 明确不负责 |
| --- | --- | --- |
| `InstructionSource` | 输入 PC，返回指令字/取指错误；面向 program image | cache timing、改变 Warp PC |
| `Memory` | 按 space/address/mask/width 读写字节，返回 architectural fault/result | 持有 lane register、调度 warp |
| `LaneState / View`（工作名可写作 `LaneState` / `LaneView`） | GPR/FPR、lane identity；向 ISA 暴露最小读视图并应用 lane effect | Warp PC、barrier、scheduler |
| `WarpState / View`（工作名可写作 `WarpState` / `WarpView`） | PC、active mask、active/termination、divergence；提供跨 lane view | CTA admission、Kernel completion |
| `CSR Context`（工作名可写作 `CSRContext`） | 按请求者 lane/warp/CTA/core 解析 CSR，执行合法读写 | 无 scope 的全局 CSR map、指令调度 |
| `ISA Decoder / Evaluator`（工作名可写作 `ISADecoder` / `ISAEvaluator`） | 指令 decode 与单指令语义，输出 result/effect | 长期 state、直接调用 executor |
| `Architectural Result / Effect`（工作名可写作 `ArchitecturalResult` / `Effect`） | 描述寄存器、PC/mask/control、CSR、memory 等预期变化 | 自行持有/提交 canonical state |
| `Warp Executor`（工作名可写作 `WarpExecutor`） | 驱动 fetch→effect；收集 lane 语义和 warp-scope effect | 跨 CTA 调度、device launch |
| `Warp Manager / Scheduler`（工作名可写作 `WarpManager` / `Scheduler`） | active/runnable warp 集合、spawn/retire、选择下个 warp | 算术语义、复制 lane state |
| `Barrier Coordinator`（工作名可写作 `BarrierCoordinator`） | barrier arrival/wait/phase/count 与待解锁 warp 集合 | decode、global memory 存储 |
| `Kernel Launch` / `Kernel Executor`（工作名可写作 `KernelLaunch` / `KernelExecutor`） | 接收 image/PC/entry/input/launch dimensions，创建 Device 工作并判定整体完成 | lane 指令语义、周期模型 |

接口应按“上层拥有，下层借 view/返回 effect”的方向注入。具体 Go 名称、method、error 类型和 effect 表示必须等相应 Task 的实现证据后再收敛。

## 9. RTL 已确认结论

### 9.1 调查交叉核对矩阵

| 主题 | 交叉核对的 RTL/config/interface | 调查状态 |
| --- | --- | --- |
| PC 与普通 control flow | `VX_scheduler.sv`, `VX_alu_int.sv`, `VX_branch_ctl_if.sv`, `VX_schedule_if.sv` | RTL path CONFIRMED；model owner PROVISIONAL [E-PC-01][E-BR-01][E-IF-01] |
| SPLIT/JOIN divergence | `VX_wctl_unit.sv`, `VX_split_join.sv`, `VX_ipdom_stack.sv`, `VX_warp_ctl_if.sv` | stack/mask path CONFIRMED；software convention UNRESOLVED U-01 [E-DVG-01][E-WCTL-01][E-IF-01] |
| WSPAWN / WSYNC | `VX_wctl_unit.sv`, `VX_scheduler.sv`, `VX_warp_ctl_if.sv` | observed actions CONFIRMED；full functional semantics UNRESOLVED U-02/U-03 [E-WCTL-01][E-SCHED-01][E-IF-01] |
| barrier state | `VX_wctl_unit.sv`, `VX_bar_unit.sv`, `VX_scheduler.sv`, `VX_warp_ctl_if.sv`, `VX_config.toml` | store/update/fence path CONFIRMED；CTA namespace owner UNRESOLVED U-04 [E-BAR-01][E-WCTL-01][E-IF-01][E-CFG-01] |
| CSR scope | `VX_csr_data.sv`, `VX_csr_unit.sv`, `VX_scheduler.sv`, `VX_cta_dispatch.sv`, `VX_sched_csr_if.sv`, `VX_types.toml` | confirmed subsets plus UNRESOLVED U-05 [E-CSR-01][E-CSR-02][E-IF-01] |
| instruction/global/local memory | `VX_fetch.sv`, `VX_lsu_unit.sv`, `VX_lmem_switch.sv`, `VX_mem_unit.sv`, `VX_schedule_if.sv`, `VX_lsu_sched_if.sv`, `VX_config.toml` | routes CONFIRMED；logical owners PROVISIONAL [E-FETCH-01][E-EXEC-01][E-MEM-01][E-IF-01][E-CFG-01] |
| Warp/CTA/Kernel completion | `VX_scheduler.sv`, `VX_cta_dispatch.sv`, `VX_core.sv`, `VX_kmu.sv`, `Vortex.sv` | RTL chain PARTIAL；software/host contract UNRESOLVED U-06/U-07 [E-DONE-01][E-DONE-02] |

### 9.2 三类控制状态不得混同

| 控制平面 | CONFIRMED RTL 事实 | 功能模型边界（PROVISIONAL） |
| --- | --- | --- |
| 普通 control-flow PC 更新 | fetch/顺序推进、branch/trap/MRET、JOIN、CTA dispatch 和 WSPAWN 最终都写 per-warp `warp_pcs`；TMC 不写 PC。branch interface 只携带 `wid/taken/dest/trap`，不携带 reconvergence stack。[E-PC-01][E-BR-01][E-IF-01] | Warp 是 PC 的唯一 owner；普通 branch effect 是 PC 重定向，不因此另建一份 divergence state。 |
| warp mask / reconvergence | CTA dispatch、WSPAWN、TMC/SPLIT/JOIN 以及有保存 mask 的 MRET 会更新 `thread_masks`；IPDOM stack 按 `wid` 保存 original mask 与 next PC。[E-PC-01][E-DVG-01] | active lane mask 与 reconvergence state 均归 Warp，但它们是不同 canonical fields；普通 PC 更新不能隐式改写二者。 |
| Core/CTA coordination | `active_warps/stalled_warps`、CTA slot/remaining-warps 和 barrier unlock mask 分别协调 runnable warp、CTA residency 与同步。[E-SCHED-01][E-CTA-01][E-BAR-01] | Core Warp Manager、CTA Context 和候选 Barrier Coordinator 只消费/应用相应 control effect；它们不执行 ISA 算术语义，也不复制 Warp PC/mask。 |

这些 owner 是从可观察职责推导出的逻辑边界，不是照抄 RTL module 层次。比如 GPR/FPR 物理 bank 在 core RTL、LMEM SRAM 也在 core RTL，但 canonical architectural values 分别仍映射为 Lane register 与 CTA-owned region；物理 placement 只进入证据，不产生第二 owner。[E-REG-01][E-MEM-02]

### 9.3 当前结论

1. **PC ownership [CONFIRMED RTL fact；PROVISIONAL model mapping]**：PC 在 RTL 中集中为 per-warp `warp_pcs`；顺序步进、普通 branch、JOIN、CTA dispatch、WSPAWN、trap/MRET 都更新该表。TMC 只更新 active-warp、lane-mask 和 scheduler stall state，不写 PC。因此当前功能模型方向把 PC 映射给 Warp，而不是 Lane 或 ISA。[E-PC-01][E-SCHED-01][E-IF-01]
2. **普通 branch 与 SPLIT/JOIN [PARTIAL / UNRESOLVED U-01]**：普通 branch 的最终 taken 位由最后一个 active lane 的比较结果产生，并重定向 per-warp PC；SPLIT 单独生成 then/else mask 并压入 IPDOM，JOIN 恢复 mask/PC。RTL 清楚显示两条硬件通路，但缺少编译器/runtime 资料，尚不能确认软件如何排列它们。[E-BR-01][E-DVG-01]
3. **WSPAWN [CONFIRMED observed actions；UNRESOLVED U-02]**：WSPAWN 生成目标 warp mask 和 PC；scheduler 等到当前只剩单 warp 时激活目标 warps，为其 lane 0 置 active、设置 PC，并复制 `mscratch`。将完整 spawn effect 交给 Core/Warp Manager 是 PROVISIONAL 方向，精确初始化集仍未收敛。[E-WCTL-01][E-SCHED-01]
4. **WSYNC [CONFIRMED drain path；UNRESOLVED U-03]**：WCTL 在该 warp 的 pending instruction 计数尚未到 almost-empty 时阻塞 WSYNC；到达条件后请求 scheduler 解除该 warp 的 stall。Core/Warp Manager ownership 是 PROVISIONAL；去掉周期细节后的 memory completion/visibility 保证仍未收敛。[E-WCTL-01][E-SCHED-01][E-IF-01]
5. **Barrier [CONFIRMED state/fence path；UNRESOLVED U-04]**：每个 Core 的 `VX_bar_unit` 保存 mask/count/events/phase，arrival/wait 改变状态并返回 unlock mask；BAR 和 BAR.ARRIVE 在进入 barrier 前由 `lsu_sched_drained` 等待 LSU 排空，interface 注释明确其 SMEM/GMEM fence 意图。当前单 core 配置使 global barrier hardware path 不成为必需的跨 core 行为。CTA Barrier Coordinator 只是 PROVISIONAL 候选，barrier address 与 CTA 隔离约定仍未收敛。[E-BAR-01][E-WCTL-01][E-IF-01]
6. **CSR scope [PARTIAL / UNRESOLVED U-05]**：FCSR、mscratch 和 machine trap CSR 是 per-warp；CTA dimensions/ids/lmem/entry 来自 CTA context；warp/core ids、active masks、配置常量为派生读取；cycles/instret/perf 是 core/运行派生。不能据此把所有 CSR 统一放到某一层。[E-CSR-01][E-CSR-02][E-IF-01]
7. **Memory boundaries [CONFIRMED routes；PROVISIONAL logical owners]**：fetch 通过独立 icache bus 取 instruction；LSU 的每 lane 请求按 `is_addr_local` 分为 LMEM 与 global path；CTA dispatcher 为 CTA 分配 LMEM base/stride。功能模型方向需要 instruction view、device-global bytes 和 CTA LMEM region，不需要 cache state。[E-FETCH-01][E-EXEC-01][E-MEM-01][E-MEM-02]
8. **Warp/CTA/Kernel completion [PARTIAL / UNRESOLVED U-06/U-07]**：TMC mask=0 使 warp inactive并产生 `warp_done`；CTA dispatcher 对该 slot 的 remaining-warps 递减，最后一个 warp 释放 CTA slot；device `busy` 汇总 KMU dispatch、core/scheduler 和未排空 memory activity。软件侧 termination/host completion 协议不在参考包内。[E-DONE-01][E-DONE-02]

## 10. UNRESOLVED 登记

| ID / 最晚解决阶段 | 当前已知 | 缺失信息 | 后续必须检查 |
| --- | --- | --- | --- |
| **U-01 — before T5 (entering Multi-Warp Core)** | branch 更新 Warp PC；SPLIT/JOIN 单独维护 IPDOM/mask。[E-BR-01][E-DVG-01] | 编译器如何围绕 divergent branch 发射 SPLIT/JOIN，普通 branch 选择 last active lane 的完整控制流约定 | `hw/rtl/core/VX_decode.sv`, `hw/rtl/core/VX_alu_int.sv`, `hw/rtl/core/VX_wctl_unit.sv`, `hw/rtl/core/VX_split_join.sv`, `hw/rtl/core/VX_ipdom_stack.sv`，以及当前包缺失的 compiler/runtime control-flow lowering |
| **U-02 — before T5 (Multi-Warp Core)** | WSPAWN 设置目标 PC、lane0 mask 并复制 mscratch。[E-WCTL-01][E-SCHED-01] | 功能模型是否还须复制 GPR/FPR/CSR/divergence state；软件在何时调用 | `hw/rtl/core/VX_wctl_unit.sv`, `hw/rtl/core/VX_scheduler.sv`, `hw/rtl/core/VX_opc_unit.sv`，以及当前包缺失的 startup/runtime WSPAWN intrinsic |
| **U-03 — before T5** | WSYNC 等待该 warp 先前 pending instructions committed 后解锁；RTL 用 almost-empty 计数表达。[E-WCTL-01][E-SCHED-01] | 无周期模型时它是否只是 instruction ordering point，是否额外包含 memory completion/visibility 语义 | `hw/rtl/core/VX_wctl_unit.sv`, `hw/rtl/core/VX_scheduler.sv`, `hw/rtl/core/VX_commit.sv`, `hw/rtl/core/VX_lsu_unit.sv`, `hw/rtl/interfaces/VX_warp_ctl_if.sv`，以及当前包缺失的软件 WSYNC 使用点 |
| **U-04 — before T6 (CTA/Barrier)** | barrier store key 宽度为 warp-id bits + barrier-id bits；state 含 wait mask/count/events/phase；BAR/BAR.ARRIVE 先 drain LSU。[E-BAR-01][E-WCTL-01][E-IF-01] | rs1 低位如何编码 CTA 内 warp base、并发 CTA 的 barrier namespace 如何隔离；BAR.ARRIVE/WAIT 软件协议 | `hw/rtl/core/VX_wctl_unit.sv`, `hw/rtl/core/VX_bar_unit.sv`, `hw/rtl/core/VX_cta_dispatch.sv`, `hw/rtl/interfaces/VX_warp_ctl_if.sv`，以及当前包缺失的 runtime barrier intrinsics |
| **U-05 — before T1 确认 ISA-visible 语义；最晚 before T6 收敛 CTA scope** | 已确认若干 per-warp/CTA/core/派生 CSR 以及 wid/cta-id read selectors。[E-CSR-01][E-CSR-02][E-IF-01] | 每个受支持 CSR 的合法读写、lane broadcast/选择、reset、trap、readonly 行为和最终 canonical scope | `hw/VX_types.vh`, `hw/rtl/core/VX_csr_unit.sv`, `hw/rtl/core/VX_csr_data.sv`, `hw/rtl/core/VX_scheduler.sv`, `hw/rtl/core/VX_cta_dispatch.sv`, `hw/rtl/interfaces/VX_sched_csr_if.sv`，以及当前包缺失的 CSR runtime/tests |
| **U-06 — before T6** | TMC mask=0 是 warp retirement；remaining-warps=1 时 CTA done 并释放 slot。[E-DONE-01] | 异常/非法指令是否也能终止 warp/CTA；barrier waiting 时的失败语义 | `hw/rtl/core/VX_scheduler.sv`, `hw/rtl/core/VX_cta_dispatch.sv`, `hw/rtl/core/VX_alu_int.sv`, `hw/rtl/core/VX_bar_unit.sv`，以及当前包缺失的软件 warp/CTA exit path |
| **U-07 — before T7 (Kernel Execution)** | KMU 停止表示 CTA 全部发出；top `busy` 还汇总 core 与 memory drain。[E-DONE-02] | host/kernel completion 的精确 API、cache flush/IO exit/reference checker 观察点 | `hw/rtl/Vortex.sv`, `hw/rtl/VX_kmu.sv`, `hw/rtl/core/VX_core.sv`, `hw/rtl/core/VX_cta_dispatch.sv`, `hw/rtl/cp/`，以及当前包缺失的 host runtime/tests |
| **U-08 — before T7** | 目标要求与 RTL/reference checker 的结果一致；参考包声明 checker、`sim/`、`tests/` 被排除。[E-SRC-01] | checker 比较哪些 architectural fields、浮点 NaN/flags、fault/termination 与 memory 范围 | `README.md` 的排除清单；取得授权后补充当前包缺失的 `sim/`, `tests/` 和 reference checker evidence；未补齐前不得声称逐字段兼容 |
| **U-09 — before T1 (Complete ISA)** | 配置/decoder 确认 ISA family 和 custom op 路径。[E-ISA-01][E-ISA-02][E-ISA-03] | 每条指令的完整编码、非法编码、corner case 和浮点舍入/flags 清单尚未在 T0 审计 | `hw/rtl/core/VX_decode.sv`, `hw/rtl/core/VX_alu_int.sv`, `hw/rtl/core/VX_lsu_unit.sv`, `hw/rtl/fpu/`, `hw/rtl/core/VX_sfu_unit.sv`, `hw/rtl/VX_gpu_pkg.sv`, `hw/VX_types.vh` |

## 11. 证据规范与目录

### 11.1 引用格式

格式为：`[E-领域-NN] 仓库相对路径 — module/signal/macro/config key：支持的结论`。

- 路径一律相对只读参考根 `Vortex_rtl/`，不可写绝对机器路径作为长期证据。
- config 证据写 TOML section/key 或生成 macro；RTL 证据写 module 及 signal/instance/function。
- 结论必须能由引用内容直接支持；推导出的功能模型设计另标 Contract 状态。

### 11.2 Evidence catalog

- **[E-SRC-01]** `README.md` — snapshot commit、`XLEN=32`，并明确排除了 `sw/`、`sim/`、`tests/`、reference semantics/DPI C++ 和既有功能模型。
- **[E-CFG-01]** `VX_config.toml` — `[platform] VX_CFG_NUM_CLUSTERS/NUM_CORES/SOCKET_SIZE`, cache/LMEM enable；`[isa] VX_CFG_EXT_*`, VM, FLEN；`[pipeline] VX_CFG_NUM_WARPS/NUM_THREADS/NUM_BARRIERS`；`[memory] VX_CFG_MEM_ADDR_WIDTH/MEM_BLOCK_SIZE`；`[lmem] VX_CFG_LMEM_LOG_SIZE`。
- **[E-CFG-02]** `hw/VX_config.vh` — generated `VX_CFG_*` macro defaults/mirrors confirm 1/1/4/4/8, enabled M/F/Zicond/I$/D$/LMEM and disabled C/A/VM/D/L2/L3/optional accelerators under RV32.
- **[E-MAP-01]** `VX_types.toml` — `[memmap] VX_MEM_USER_BASE_ADDR`, `VX_MEM_STACK_BASE_ADDR`, `VX_MEM_LMEM_BASE_ADDR` and `[vm]` RV32 alternatives.
- **[E-ISA-01]** `hw/rtl/core/VX_decode.sv` — module `VX_decode`, opcode cases `INST_I/R/L/S/B/JAL/JALR/FENCE/SYS`, gated M/F/Zicond/A/C paths。
- **[E-ISA-02]** `hw/rtl/core/VX_decode.sv` — `INST_EXT1` decode for TMC/WSPAWN/SPLIT/JOIN/BAR/PRED/WSYNC and VOTE/SHFL; `INST_EXT2` WGATHER; pack-load decode。
- **[E-ISA-03]** `hw/rtl/VX_gpu_pkg.sv` — `INST_SFU_*`, `INST_VOTE_*`, `INST_SHFL_*`, `inst_sfu_is_wctl` definitions。
- **[E-EXEC-01]** `hw/rtl/core/VX_alu_int.sv` — lane-indexed `result_if.data.data`；`hw/rtl/fpu/VX_fpu_unit.sv` — lane operands/results and `commit_if`；`hw/rtl/core/VX_lsu_unit.sv` — per-lane LSU execute/result path；`hw/rtl/core/VX_commit.sv` — `tmask`-derived writeback byte enables and lane data。
- **[E-ALU-01]** `hw/rtl/core/VX_alu_int.sv` — VOTE `vote_true/vote_false` under active tmask, SHFL cross-lane source selection, WGATHER cross-lane result and lane write mask。
- **[E-REG-01]** `hw/rtl/core/VX_opc_unit.sv` — module `VX_opc_unit`, `src_regs`, `gpr_wr_addr`, `gpr_rd_addr`, generate block `g_gpr_rams`, instance `gpr_ram`; `hw/rtl/VX_gpu_pkg.sv` — `REG_TYPES = 1 + VX_CFG_EXT_F_ENABLED`。
- **[E-PC-01]** `hw/rtl/core/VX_scheduler.sv` — module `VX_scheduler`；冻结配置下 `warp_pcs` 的 CTA dispatch/WSPAWN/JOIN/branch/trap/MRET/advance 写入路径，以及 `thread_masks` 的 CTA dispatch/WSPAWN/TMC/SPLIT/JOIN/MRET 写入路径。
- **[E-SCHED-01]** `hw/rtl/core/VX_scheduler.sv` — `active_warps`, `ready_warps`, WSPAWN/TMC handling and `cta_warp_done`。
- **[E-DVG-01]** `hw/rtl/core/VX_split_join.sv` — module `VX_split_join`, `ipdom_stack`, `orig_tmask`, `next_pc`, `join_tmask`；`hw/rtl/core/VX_ipdom_stack.sv` — per-`wid` pointer/storage。
- **[E-BR-01]** `hw/rtl/core/VX_alu_int.sv` — module `VX_alu_int`, `last_tid`, `br_result`, `br_taken`, `branch_ctl_if`；`hw/rtl/core/VX_scheduler.sv` — branch PC redirect。
- **[E-WCTL-01]** `hw/rtl/core/VX_wctl_unit.sv` — module `VX_wctl_unit`, `wspawn.wmask/pc`, `wsync_drain`, `bar_drain`, `wctl_bar_addr`, `bar.*` and `warp_ctl_if` effects；`hw/rtl/core/VX_core.sv` — `warp_ctl_if.lsu_sched_drained = &lsu_sched_empty`。
- **[E-BAR-01]** `hw/rtl/core/VX_bar_unit.sv` — module `VX_bar_unit`, `mask/count/events/phase`, `barrier_state_store`, `barrier_phase_store`, unlock outputs and `USE_GBAR`；`hw/rtl/VX_gpu_pkg.sv` — `BAR_ADDR_BITS = NW_BITS + NB_BITS`。
- **[E-CSR-01]** `hw/rtl/core/VX_csr_data.sv` — module `VX_csr_data`, per-warp `fcsr`, scheduler-owned mscratch/trap CSR interface, CSR read cases and optional per-core `satp`。
- **[E-CSR-02]** `hw/rtl/core/VX_csr_unit.sv` — requester `read_wid`/lane IDs；`hw/rtl/core/VX_cta_dispatch.sv` — `cta_ctx_ram`, `cta_warp_ram`, `cta_id_per_warp_r` and CTA CSR readback。
- **[E-IF-01]** `hw/rtl/interfaces/VX_branch_ctl_if.sv` — per-warp branch/trap fields；`hw/rtl/interfaces/VX_warp_ctl_if.sv` — WSPAWN/TMC/SPLIT/JOIN/BAR/WSYNC effects, pending/drain status and explicit barrier fence comment；`hw/rtl/interfaces/VX_sched_csr_if.sv` — wid/cta-id selected CSR context；`hw/rtl/interfaces/VX_schedule_if.sv` — scheduler/fetch boundary；`hw/rtl/interfaces/VX_lsu_sched_if.sv` — LSU request/response boundary。
- **[E-CTA-01]** `hw/rtl/core/VX_cta_dispatch.sv` — module `VX_cta_dispatch`, warp allocation, wid→CTA mappings, per-lane coordinate expansion, context stores and remaining-warps table。
- **[E-FETCH-01]** `hw/rtl/core/VX_fetch.sv` — module `VX_fetch`, `schedule_if.data.PC` to `icache_bus_if` and response tag store。
- **[E-MEM-01]** `hw/rtl/mem/VX_lmem_switch.sv` — module `VX_lmem_switch`, per-lane `is_addr_local_mask`, global/local split and local AMO assertion；`hw/rtl/core/VX_mem_unit.sv` — LMEM-enabled routing and `VX_local_mem` instance。
- **[E-MEM-02]** `hw/rtl/core/VX_cta_dispatch.sv` — fixed-stride LMEM slot allocation, `cur_lmem_base_r`, `cta_csrs.lmem_addr`；`hw/rtl/mem/VX_local_mem.sv` — physical banked store。
- **[E-KRN-01]** `VX_types.toml` — `[dcr_kmu]` startup/entry/arg/grid/block/LMEM keys；`hw/rtl/VX_kmu.sv` — `dcr_PC`, `dcr_entry`, `dcr_param`, grid walk and `kmu_bus_if`。
- **[E-DONE-01]** `hw/rtl/core/VX_scheduler.sv` — `cta_warp_done` from TMC mask zero；`hw/rtl/core/VX_cta_dispatch.sv` — `rem_warps_ram`, `cta_done`, slot release。
- **[E-DONE-02]** `hw/rtl/VX_kmu.sv` — `running`/`busy` and CTA dispatch completion；`hw/rtl/core/VX_scheduler.sv` — active/pending busy；`hw/rtl/core/VX_core.sv` — scheduler/LSU/memory busy aggregation；`hw/rtl/Vortex.sv` — KMU/cluster/device `busy` aggregation。

## 12. T0 最终一致性审计

### 12.1 八类要求独立覆盖

| T0 要求 | 本文独立覆盖位置 | 审计结果 |
| --- | --- | --- |
| 1. 总体目标与模拟器层级 | 第 3、4 节 | 目标、冻结范围、`Device → Core → CTA → Warp → Lane → ISA`、辅助模块和正反职责均已给出 |
| 2. State Ownership | 第 5 节 | 唯一 owner 原则和完整 state 表已给出；未收敛 CSR/barrier owner 显式指向 U-05/U-04 |
| 3. ISA 与 State 基本边界 | 第 6 节 | decode/单指令语义、无长期 state、无上层感知和 View/Result/Effect 边界均已给出 |
| 4. Instruction Effect Ownership | 第 6.1 节 | lane、control-flow、SIMT、spawn/sync、barrier、CSR 和三类 memory space 均已映射 |
| 5. Execution Model 骨架 | 第 7 节 | 稳定数据流和七阶段扩展顺序均已给出 |
| 6. 关键接口边界 | 第 8 节 | 十一类接口逐项说明交换内容与非职责；名称/类型均为 PROVISIONAL |
| 7. RTL 结论与未决问题 | 第 9–11 节 | 关键结论、交叉核对矩阵、带 deadline 的 U-01–U-09 和相对路径证据目录均已给出 |
| 8. Contract 状态分类 | 第 2、12.2、13 节 | 四态门槛/迁移、T0 状态审计和后续变更记录规则均已给出 |

因此 `docs/architecture.md` 自身即可解释本 Contract；T0 不依赖额外的一次性设计文档。

### 12.2 状态、owner 与范围审计

- **状态标签：PASS。** 核心约束使用 `FROZEN`；实现方向使用 `PROVISIONAL`；证据不足项使用 `UNRESOLVED U-xx`；RTL 直接观察使用 `CONFIRMED RTL fact/path`，不把它冒充已冻结的功能模型设计。`RESOLVED` 目前只定义迁移规则，T0 没有伪造已解决项。
- **FROZEN 最小化：PASS。** T0 只冻结功能目标/冻结配置边界、层级与依赖方向、唯一 canonical owner 原则、ISA 不拥有长期 state、稳定功能数据流；具体 owner 映射、接口名/type 和提交机制均未冻结。
- **canonical owner 唯一性：PASS。** 第 5.2 节每项只出现一个 owner 或一个明确的 unresolved 候选；物理 register bank、LMEM SRAM、scheduler table 与 cache 不产生第二 canonical owner。CSR/barrier 在收敛前禁止落成多个真值源。
- **三类控制边界：PASS。** 第 9.2 节分别处理普通 PC 更新、warp mask/reconvergence 和 Core/CTA coordination；没有用一个 scheduler/module 的物理位置把三者合并。
- **PROVISIONAL/UNRESOLVED 措辞：PASS。** 第 6.1、9 节把已观察 RTL 路径与 model owner 分列；未决表明确“当前已知/缺失/后续位置/deadline”。
- **无过度设计：PASS。** 本文没有规定 pipeline/cache timing、issue width、周期 scheduler、transaction/rollback/versioning、并发控制、最终 Go interface 或 commit 算法。
- **交付范围：PASS。** T0 仅交付本架构文档，不包含 ISA、State、Warp executor、scheduler、Kernel execution 或其他模拟器代码实现。

## 13. 决策与变更记录

| Task | 状态迁移/变更 | 证据与影响 |
| --- | --- | --- |
| T0 | 建立 Contract；冻结功能目标、配置边界、层级依赖、唯一 owner 和 ISA 无长期状态原则；其余设计保持 provisional/unresolved | 本文第 3–11 节；未实现 ISA、State、scheduler 或 executor 代码 |
| T0 / ownership-and-boundaries 审计 | GPR/FPR 分项；CSR/barrier 未收敛 owner 显式标记 UNRESOLVED；effect 表拆分 CONFIRMED RTL path 与 PROVISIONAL owner，并按 instruction/global/local memory space 列项 | [E-EXEC-01][E-ALU-01] 及第 5、6、8 节；未规定具体 Go 类型或 commit 算法 |
| T0 / rtl-findings-and-audit | 交叉核对 core RTL/interfaces/config；显式分离 PC、mask/reconvergence、Core/CTA coordination；统一 unresolved deadline；完成八类要求、状态、owner 与无代码交付审计 | [E-IF-01] 及第 9–12 节；所有 T0 结论可由本文独立理解 |
| T0 / verifier repair | 修正 PC 写入来源集合：移除 TMC、补入 WSPAWN，并明确 TMC 只产生 active-warp/lane-mask/scheduler-stall effect | [E-PC-01][E-SCHED-01]；同步修正 ownership、effect、控制平面、RTL 结论与证据目录 |

后续每个 Task 必须在此表增加一行；`RESOLVED` 项必须指出原 ID、解决 Task、证据和受影响的接口/owner。
