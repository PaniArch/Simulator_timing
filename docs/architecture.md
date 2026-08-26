# Vortex 功能模拟器 Living Architecture Contract

## 1. 文档职责

本文档是 Vortex 功能模拟器在整个开发周期内持续维护的 **Living Architecture Contract**，不是 T0 的一次性分析记录。后续每个 Task 必须在开始实现前完整读取本文档，并在结束时同步更新它：新增证据、状态 ownership、模块边界、effect/control 边界和待决问题都必须与代码一起落盘。禁止只修改代码而让本文档失真。

T0 只建立契约和证据基线，不实现 ISA、State、Warp、SIMT、scheduler、CTA、Kernel execution 或其他模拟器功能代码，也不提前固定 Go 类型、transaction/rollback 机制或调度算法。

## 2. 契约状态与治理

每项架构结论必须带下列状态之一；没有状态的描述只用于背景说明，不构成实现契约。

| 状态 | 含义 | 使用门槛 |
| --- | --- | --- |
| `FROZEN` | 当前冻结数据集下，已有配置和 RTL 的直接、相互一致证据，后续实现必须遵守的稳定契约。 | 必须列出可复查的冻结输入；不能仅凭经验、模块名或历史实现推断。 |
| `PROVISIONAL` | 为分解后续工作提出的当前设计方向，尚未经实现和对照验证。 | 可以指导原型和接口讨论，但不得作为 reference semantics；允许被后续任务修改。 |
| `UNRESOLVED` | 当前允许的证据不足以唯一作答。 | 禁止猜测、静默补默认值或伪装成实现决定；必须注明最晚闭合阶段。 |
| `RESOLVED` | 某个原 `UNRESOLVED` 项已经在后续阶段由明确证据和验证闭合的审计记录。 | 必须保留问题、决定、证据、验证和闭合 Task；它本身不自动等于 `FROZEN`。 |

状态迁移规则：

1. 新发现若是事实且证据充分，可直接进入 `FROZEN`；设计选择先进入 `PROVISIONAL`；证据不足则进入 `UNRESOLVED`。
2. `UNRESOLVED` 获得答案后转为 `RESOLVED` 并保留审计轨迹。若该答案还要成为稳定实现契约，须另行通过对照验证后写入相应 `FROZEN` 条目。
3. `PROVISIONAL` 只有在实现、定向测试和 RTL 证据三者一致后才可升为 `FROZEN`；失败时修改或退回 `UNRESOLVED`。
4. 修改 `FROZEN` 项必须有冻结配置/RTL 变化或原证据被反证，记录变更原因及受影响实现；不得为了迁就现有代码静默改约。
5. T0 没有实际 `RESOLVED` 项。下文列出的未知事项全部保持 `UNRESOLVED`，不得虚构为已闭合。

## 3. 冻结证据基线

### 3.1 唯一语义参考范围

`FROZEN`：模拟器语义分析的唯一外部参考是只读目录 `Vortex_rtl` 内的配置、生成头文件和 RTL。具体基线为：

- `Vortex_rtl/README.md` 声明该输入包来自上游快照 `e2b9745b637ce8ac462be2f0e01b5d76542dc6c0`，并明确冻结配置使用 `XLEN=32`。
- 配置源为 `Vortex_rtl/VX_config.toml` 与 `Vortex_rtl/VX_types.toml`；生成结果以 `Vortex_rtl/hw/VX_config.vh` 和 `Vortex_rtl/hw/VX_types.vh` 交叉核对。
- ISA 可达范围和状态语义继续由 `Vortex_rtl/hw/rtl/` 下当前配置实际包含的 decode、execute、scheduler、CSR、SIMT control 和 memory 路径核对。
- 不以仓库外文件、常识中的 RISC-V/Vortex 行为或旧模拟器实现填补空白。RTL 的物理模块划分是证据，不自动成为功能模拟器的实现划分。

`FROZEN`：输入包刻意排除了 `sw/`、`sim/`、`tests/`、reference checker、DPI C++ reference semantics、日志和 Git metadata。因此 Kernel/host ABI、软件生成序列、精确浮点 reference 行为及最终比较协议不能由缺失内容推断。

### 3.2 冻结配置矩阵

下表由 `VX_config.toml`、`VX_types.toml`、`hw/VX_config.vh`/`hw/VX_types.vh` 和 decode 条件编译路径交叉核对，均为 `FROZEN`。

| 领域 | 冻结值/范围 | 直接含义 |
| --- | --- | --- |
| 字长 | `XLEN=32`，`FLEN=32`，memory address width 32 | 冻结执行能力只包含 RV32 数据通路和单精度 F；RV64/W 类及 D 执行能力不在范围内。T1 严格 decoder 已将通用 F case 泄漏的 D-format encoding 确定为 illegal。 |
| 拓扑 | 1 cluster，1 core，socket size 1，因而 1 socket | 功能模型仍保留命名空间边界，但不需支持多实例配置。 |
| SIMT 宽度 | 4 warps/core，4 threads/warp，SIMD width 4 | 每 warp 最多四个 lane；架构效果必须携带并遵守该指令定义的 lane write mask，通常为 active thread mask，WGATHER 例外见 E-LANE-01。 |
| barrier | 8 barrier IDs，`MAX_BAR_EVENTS=32` | barrier 状态是跨指令状态；精确 CTA namespace/visibility 仍见待决项。 |
| 基础存储 | I-cache、D-cache、local memory 启用；L2、L3 关闭 | 功能上必须有指令/数据/本地内存语义；cache timing 和 cache 内容不是 canonical architecture state。 |
| 标准 ISA | I、M、F、Zicond 启用；System decode 存在 | 实现范围由下节列出的实际 decode 路径约束，不能只按扩展名称补指令。 |
| 关闭的标准能力 | D、C、A、VM 关闭 | 不实现双精度、压缩指令、原子/LR-SC/AMO、地址转换或页表执行语义。 |
| 关闭的可选加速器 | TCU、DMA、DXA、TEX、RASTER、OM、RTU 全部关闭 | 即使 TOML/types/RTL 中保留定义，也不是本冻结模拟器的可执行路径。 |

说明：`VX_config.toml` 中 D 的表达式依赖 `XLEN=64`；冻结 README 指定 XLEN 32，生成头的 `VX_CFG_EXT_D_ENABLED` 因而为 0、FLEN 为 32。I/M/F 也由生成的 `VX_CFG_MISA_STD` 位和 `VX_decode.sv` 路径互证。配置能力关闭与 decode 是否显式拒绝某个 encoding 是两件事：关闭的扩展不能因为源树中仍保留 decode 字段或 downstream 逻辑而被视作启用，其 encoding 的非法/fault 行为也不能在未审计时擅自补定。

### 3.3 冻结 ISA/decode 表面

以下是必须由后续 ISA 层覆盖的 decode 类别，不等同于尚未审计的逐 encoding 合法性表。

| 类别 | `FROZEN` 可达表面 | 主要 RTL 证据 |
| --- | --- | --- |
| RV32I integer/control | immediate/register ALU、LUI/AUIPC、JAL/JALR、六类条件分支、byte/half/word load/store、FENCE | `VX_gpu_pkg.sv` opcode/op 枚举；`core/VX_decode.sv` 的 `INST_I/R/LUI/AUIPC/JAL/JALR/B/L/S/FENCE` cases |
| M | MUL/MULH/MULHSU/MULHU、DIV/DIVU、REM/REMU | `VX_CFG_EXT_M_ENABLE` 保护的 `m_type` 与 R-type decode |
| F（冻结执行能力仅 S） | FLW/FSW；FADD/FSUB/FMUL/FDIV/FSQRT、四类 fused multiply-add、sign/min/max、compare、class、move、RV32 integer/float convert；FCSR/FRM/FFLAGS effects。通用 F decode 仍传播 S/D format 位，不构成 D 能力已启用。 | `VX_CFG_EXT_F_ENABLE` cases；`VX_CFG_FLEN_32`；`VX_csr_data.sv` |
| Zicond | CZERO.EQZ、CZERO.NEZ | `VX_CFG_EXT_ZICOND_ENABLE` 下的 R-type funct7 decode |
| System | CSRRW/CSRRS/CSRRC 及 immediate variants；ECALL、EBREAK、URET、SRET、MRET decode 路径 | `INST_SYS` case、`VX_csr_unit.sv`、scheduler trap CSR path |
| custom SIMT control | TMC、WSPAWN、SPLIT、JOIN、BAR（同步与 arrive/wait 形式）、PRED、WSYNC | unconditional `INST_EXT1`, funct7 `0x00` decode；`VX_wctl_unit.sv`、`VX_scheduler.sv`、`VX_split_join.sv`、`VX_bar_unit.sv` |
| custom lane/data | VOTE、SHFL、WGATHER，以及 `vx_packlb_f`/`vx_packlh_f` packed loads | `INST_EXT1` funct7 `0x01/0x04` 与 `INST_EXT2` WGATHER decode；ALU/LSU downstream paths |

`FROZEN`：A 关闭时 `INST_AMO` case 被条件编译移除；C 关闭时没有 RVC decode 且普通 PC 前进量是 4 bytes；TCU/DXA/TEX/RASTER/OM/RTU cases 受各自关闭宏保护；VM 关闭时不发生地址翻译。D 必须区别处理：配置和 `FLEN=32` 明确关闭 D/FLEN64 执行能力，但 `INST_FL`/`INST_FS` 与 `INST_FMADD`/`INST_FCI` 的通用 F decode 只受 `VX_CFG_EXT_F_ENABLE` 保护；其中 fused/common arithmetic 仍把 `funct2[0]` 传播为 S/D format，只有 FCVT.S.D/FCVT.D.S 的 F2F case 受 `VX_CFG_FLEN_64` 保护。T1 以冻结能力为上界，将这些宽松 case 接受的 D-format word 全部判为 illegal；后续 fault effect/priority 由 U-FAULT-01 管理。

`FROZEN（T1/01-catalog-decode）`：ISA decoder 不复制 RTL 的 default/`x` 行为。`isa/catalog.go` 将冻结配置能力与 `VX_decode.sv` 的可达 case、`VX_gpu_pkg.sv` 的 opcode/op 枚举及相应 execute consumer 交叉核对后，形成唯一 enabled manifest；`isa.Decode` 仅接受 manifest 中满足 match/mask 和字段约束的 32-bit word。具体严格规则为：RV32 shift/funct7、JALR/branch/load/store funct3、System 的 rd/rs1 固定零位、FENCE 的 fm/rd/rs1、custom 未使用字段均必须合法；F32 只接受 S-format，需 rounding 的指令只接受 rm=0..4/7，FSQRT/move/class/convert 的 rs2/宽度字段必须为 RV32F 组合。D-format、RV64/W、C、A、vector 以及关闭的 TCU/DXA/TEX/RASTER/OM/RTU 编码均确定性返回 `IllegalInstructionError`。此决定闭合 U-ISA-01 的 encoding/decode 部分；misalignment、fault effect 与 trap 优先级仍保留为 U-FAULT-01。

## 4. 功能架构边界

### 4.1 层级与依赖方向

以下模块分解为 `PROVISIONAL`，用于避免把 RTL pipeline 机械照搬为模拟器结构；职责边界是后续实现的起点而非具体类型承诺。

`PROVISIONAL` 主链可概括为 **Device → Core → CTA → Warp → Lane → ISA**：Device 驱动 Core，Core 管理 CTA，CTA 组织 Warp，Warp 暴露 active Lane view，而 Warp executor 以该最小 view 调用 ISA decode/evaluate。ISA 是无长期状态的叶子语义依赖，不是 Lane 的可变子对象，也不能反向调度 Warp、CTA 或 Core。

| 层/模块 | 职责 | 只依赖 |
| --- | --- | --- |
| Harness / Input adapter | 接收 Kernel image、入口、参数/数据，建立初始内存和启动请求；导出最终可比较结果。 | Device 公共入口；不直接改 Core/Warp 私有状态。 |
| Device | 持有冻结配置、全局内存、DCR/launch 上下文和设备完成聚合；创建唯一 cluster。 | Cluster、Memory、launch 接口。 |
| Cluster / Socket | 保留 RTL identity/地址和本地内存作用域边界；冻结单实例时主要做结构组合，不复制架构状态。 | Core、Memory。 |
| Core | CTA 接纳与回收、warp 集合、选择 runnable warp，并把单步 effect 路由到 owner；挂接 CSR 与 Barrier 服务。 | CTA/Warp manager、ISA/Execution、受控服务；不得反向依赖 Harness。 |
| CTA manager | 将 launch 描述映射为 CTA metadata、warp membership、lane thread coordinates 和 LMEM allocation；汇报 CTA 完成。 | 冻结 launch 描述与 Core/Warp 接口。 |
| Warp / SIMT control | 唯一管理 warp PC、active mask、lifecycle、divergence/reconvergence 与 barrier wait 状态。 | ISA 产生的 control effect、CTA metadata、barrier service。 |
| Lane view | 按 lane id 暴露该 lane 的 GPR/FPR 和由 CTA context 派生的 thread identity；普通指令不对 inactive lane 产生写 effect，指令明确覆盖 write mask 的例外必须显式表达。 | Warp register/state view 与只读 CTA context；不拥有 Warp/CTA 状态副本。 |
| ISA decoder | 将 32-bit instruction 解码成与状态存储无关的操作描述，声明输入、结果和潜在 effects；验证 frozen ISA 范围。 | 冻结配置与 ISA 常量；不得拥有持久状态。 |
| Execution semantics | 对显式 operand/context 计算 ALU/M/F/lane/SIMT/CSR/LSU 请求或 control effect。 | ISA 描述、只读 state view、Memory 接口；不得私自写 canonical state。 |
| Effect router/applier | 在一条架构指令边界校验 lane mask/scope，并把寄存器、PC、CSR、SIMT、memory、trap 和 lifecycle effects 交给对应 owner。 | 各 canonical state owner；T0 不规定 transaction、rollback 或 commit 算法。 |
| Memory | 提供取指、load/store、LMEM/global 地址空间和必要可见性语义；内存字节只有一个 canonical 副本。 | 冻结 memory map/config；cache 可作为透明优化但不是语义 owner。 |
| CSR service | 用显式 core/warp/lane/CTA context 执行 CSR lookup/read-modify-write 与 trap CSR effect。 | CSR canonical state 与只读身份 view；不能直接调度 Core/CTA。 |
| Barrier coordinator | 用显式 CTA/core/warp/barrier identity 处理 arrive/wait/release/event/phase。 | Barrier canonical state；只能返回 block/release effects，不能通过全局变量修改 Warp。 |

`FROZEN` RTL 观察：顶层为 `Vortex -> VX_cluster -> VX_socket -> VX_core`；Core 内连接 scheduler、fetch、decode、issue/operands、execute、commit/writeback、CSR/SIMT 和 memory 路径。此观察仅证明功能职责存在，不冻结上表的具体代码组织。

依赖原则为输入适配器 → Device → Cluster/Socket → Core → CTA/Warp orchestration → ISA/Execution → effect router → state owners。Instruction source/Memory 挂在 Device 的 memory spaces，CSR 和 Barrier 挂在 Core 的显式 service ports；调用只能沿主链向下并由 effect 向指定 owner 返回。服务不得持有上层 manager 引用、回调 Harness、遍历全局 Device singleton，或以 package-global map/隐式可变 context 形成反向依赖和全局后门。低层语义不得偷偷修改状态，decode/evaluate 不得持有跨指令 canonical state。

### 4.2 State Ownership（状态所有权）与长期状态

`PROVISIONAL` canonical owner 原则：每个跨指令事实只能有一个可写真值。其他层只能持有 stable identity/key、不可变 view 或由 owner 即时派生的结果；cache、mirror、snapshot 和 blocked/runnable 等派生值不得成为第二写源。跨 owner 更新只能由显式 effect 发起并由目标 owner 校验。RTL 物理存储位置仅是证据，不自动等于模拟器 owner。

下表中的“状态需要存在”来自 `FROZEN` RTL 观察；owner 是模拟器设计候选。证据足够时标 `PROVISIONAL`，scope/软件约定不足时明确标 `UNRESOLVED owner candidate`，后续不得在多个候选处先行重复维护真值。

| State（跨指令持续） | Scope | 唯一 owner 状态 | 证据/去重约束 |
| --- | --- | --- | --- |
| 冻结配置、device/core identity、memory map | Device | `PROVISIONAL`: immutable Device/config view | TOML 和生成头；运行时指令不可改写。 |
| global/device memory bytes | Device address space | `PROVISIONAL`: Memory/global space | fetch/load/store 共用一个 byte truth；instruction view 和 cache 不能另存可写副本。 |
| DCR/Kernel launch 描述 | Device/launch | `PROVISIONAL`: Kernel launch state | `VX_types.toml` KMU DCR；`VX_kmu.sv` 形成 PC/entry/dimensions/param/LMEM 请求。 |
| CTA context：CTA id/rank/size、block/grid dimensions/index、entry、param、LMEM allocation | CTA | `PROVISIONAL`: CTA state | `VX_cta_dispatch.sv` 有 per-CTA/per-warp tables和 CTA CSR readback；Warp/Lane 仅持 CTA key/view。 |
| warp membership 与 warp→CTA link | CTA/Core | `PROVISIONAL`: CTA manager owns membership；Warp 仅持稳定 CTA key | 禁止 CTA list 和 Warp backlink 同时可写并各自声称真值。 |
| Lane/thread/warp/CTA identity 与 thread coordinates | Lane view over CTA/Warp | `FROZEN（T2/01-canonical-state-views）`: Warp id 与四个 lane id 在 `WarpState` 初始化时校验；core/CTA identity 与 thread coordinates 只在 `ReadContext` 中按次传入并复制到 snapshot | `VX_csr_unit.sv` 从 wid/lane/CTA tables形成 thread/hart/CTA CSR；`state/view.go` 不长期保存 CTA/Core context。 |
| warp PC、active lane mask | Warp | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 是唯一 owner | `VX_scheduler.sv` 的 warp_pcs/thread_masks；`state.WarpSnapshot` 只复制值，ISA input 不含 owner 引用。 |
| warp active、blocked/wait reason、runnable、termination | Warp/Core scheduling scope | `FROZEN（T2/01-canonical-state-views）` running/inactive lifecycle 与 lane mask 由 `WarpState` 一致持有；blocked/wait/runnable 仍属于未来 owner且不在 T2 state 中预存 | `active_warps`、stall/control/retire 路径证明事实分层；`state.WarpLifecycle` 只表达当前已闭合的 running/inactive。 |
| GPR x0..x31（每 lane） | Warp × Lane | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 的每 lane 独立 GPR namespace | `state/state.go` 使用私有 `[4][32]uint32` 等价布局；所有 read/view 返回值副本，State 层保证 x0 恒零。 |
| FPR f0..f31（每 lane） | Warp × Lane | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 的每 lane 独立 FPR namespace | F decode/read/writeback 与 `state` namespace 定向测试；Lane view 不复制出第二份长期数组。 |
| FFLAGS/FRM/FCSR | Warp | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 唯一持有八位 FCSR；`FROZEN（T2/02-atomic-effect-apply）`：本地事务先 sticky OR FFLAGS，后应用软件 FFLAGS/FRM/FCSR 写，故软件写按 RTL 覆盖重叠字段 | `VX_csr_data.sv` 的 per-warp fcsr/fcsr_n 顺序；snapshot 保留 raw FCSR，FloatInput 将 raw FRM 0..4 映射到 T1 enum。异步硬件来源/reset 仍见 U-CSR-01。 |
| trap/return CSR 与 mscratch：mstatus、mtvec、mepc、mcause、mtval、恢复 mask 等 | Warp | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 唯一持有 T1 已确认可写的六个 32-bit CSR 与 saved thread mask；`FROZEN（T2/02-atomic-effect-apply）`：T1 CSR/trap bundle 经 old-value/mask/scope/一致性预校验后与 PC/mask 一次提交 | RTL 虽物理分散在 scheduler/CSR data，功能模型没有第二份 CTA/counter/core state；异步 trap 与 reset/launch priority仍见 U-CSR-01。 |
| divergence/reconvergence stack、stack pointer | Warp | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 唯一持有三行 record 与 0..3 write pointer | `DV_STACK_SIZE=UP(NUM_THREADS-1)=3`、`DV_STACK_SIZEW=LOG2UP(3)=2`；初始化要求 `[0, writePointer)` 完整 live prefix，JOIN view只复制 decoded rs1 指向的一行。跨指令 under/overflow policy仍见 U-LOWER-01。 |
| barrier mask/count/event/phase；warp barrier wait relation | CTA/Core/barrier ID | `UNRESOLVED owner candidate`: Barrier coordinator owns barrier record；Warp lifecycle owns自身 block reason并只保存 barrier key | `VX_bar_unit.sv` 证明记录存在，但并发 CTA namespace/visibility 未闭合，见 U-BAR-01。 |
| local/shared memory bytes（冻结 RTL 名称 LMEM）及 allocation | CTA/cluster address scope | `UNRESOLVED owner candidate`: Memory/local space owns bytes，CTA state owns allocation descriptor | LMEM enabled；其与软件 shared memory 的等价性、跨 CTA scope/visibility 见 U-MEM-01。两者不得各存一份 bytes。 |
| 架构可读 counters | Core/Device | `PROVISIONAL`: Counter owner 构造显式 44-bit `CounterView` | T1 已冻结 MCYCLE/MINSTRET low/high read 和 instruction-originated MPM 零窗口；非周期计数策略与 checker observation 仍见 U-COUNT-01。 |
| warp、CTA、Device 完成状态 | 各 lifecycle scope | `PROVISIONAL`: Warp/CTA/Device 各自只拥有本层完成事实并单向聚合 | TMC mask=0 可退休 warp；CTA/host completion 协议见 U-CTA-01/U-ABI-01。上层聚合结果不能反写低层完成真值。 |

`FROZEN`：pipeline valid/ready、scoreboard busy bits、issue/dispatch queue、operand collector bank、cache tag/MSHR、arbiter priority、stall flags、UUID、流水寄存器和性能 backpressure 都不是功能模型的 canonical architecture state，除非后续证据证明 reference checker 可观察其中某项。

## 5. ISA、执行与 effect 契约

### 5.1 ISA 层职责

`FROZEN（T1/01-catalog-decode，decoder 边界）`：`isa` package 的 catalog/decode 已验证 ISA 是无长期状态的叶子。`Decode(word)` 不读取或保存 PC、GPR/FPR、CSR、memory、lane mask、divergence stack、barrier 或 lifecycle state；返回的 `Decoded` 只含寄存器 namespace/index、immediate、rounding/format、所需 PC/active mask、write-mask policy 及 memory/CSR/control/warp/barrier/ordering effect 边界。它不选择 Warp、不调度 Core/CTA、不结束 Kernel，也不绕过 owner mutation。T1 已冻结 integer/memory、RV32F、CSR/System 与 Vortex custom/SIMT 的单指令 evaluate/effect 接口；T2 已冻结 Lane/Warp owner 的 State/View/apply 及单指令连接子范围，上层 fetch、Warp/Barrier/CTA 协调仍为 `PROVISIONAL`，并须保持该已验证依赖方向。

`PROVISIONAL` 抽象边界（其中 decoder 与 integer/memory、RV32F、CSR/System、custom/SIMT evaluator/effect 的 T1 具体化已分别在第 5.1、5.3、5.4、5.5、5.6 节标为 `FROZEN`）：

- Decode 输入：冻结配置专用 decoder 只接收 32-bit instruction word；需要 PC 的操作由 decoded `RequiresPC` 声明，PC 在 evaluate 阶段作为显式 view 输入，decode 不读取整个 Device/Core。
- Decode 输出：与存储布局无关的 decoded operation，含源/目的 GPR/FPR、immediate、S-format/rm、active/write-mask 要求及 memory/CSR/control/lane/warp/barrier/ordering effect；非法或 disabled word 返回携带原 word 的确定性错误。
- Evaluate 输入：decoded operation、显式 operands、active lanes，以及该指令实际需要的 lane/warp/CTA/CSR/memory 最小只读 view 或 service response。
- Evaluate 输出：零个或多个结构化 result/effects；effect 至少能表达 masked register write、next-PC/branch/trap、memory read/write、CSR read-modify-write、lane mask/warp lifecycle、divergence stack、barrier arrive/wait/release 和完成通知。
- Owner apply：effect router 只负责路由和通用 scope/mask 检查，目标 state owner 做最终合法性校验并更新 canonical state。
- T0 只固定上述职责，不固定 Go interface/struct、concrete type、transaction、rollback、两阶段提交或 commit 算法；后续实现可在保持边界的前提下选择最小机制。

### 5.2 指令类别与架构 effects

下表中的指令/控制 effect 类别由 `FROZEN` RTL 路径约束；“交给谁应用”遵循第 4.2 节的 `PROVISIONAL` owner 候选。表中“可能”不表示每条指令都会写所有列出的状态。

| 指令/控制类别 | 最小读取 view | 显式 result/effect | effect/control owner |
| --- | --- | --- | --- |
| Instruction fetch / 取指 | Warp PC、instruction address space | transient instruction word 或 fetch fault；fetch 本身不偷偷推进 PC | Memory instruction source 返回结果；fault/PC effect 交 Warp lifecycle/state |
| RV32I ALU/LUI/AUIPC、M、Zicond | active Lane GPR、PC、immediate | masked GPR write、顺序 next-PC | Warp register state；Warp PC owner |
| F arithmetic/convert/compare/move | active Lane FPR/GPR、FRM | masked GPR/FPR write、FFLAGS accumulation、next-PC | Warp register state；CSR/FCSR owner；Warp PC owner |
| load | address operands、active mask、Memory space | memory request/fault、masked GPR/FPR write、next-PC | Memory validates read；register/PC owners apply results |
| store | address/data operands、active mask、Memory space | masked memory byte-write 或 fault、next-PC | Memory space owns bytes；Warp PC owner |
| branch/jump（branch/JAL/JALR） | GPR、PC、active mask | optional link GPR write、taken/target/next-PC | Warp register state 与 Warp PC owner；不替代 SPLIT/JOIN stack |
| trap/return（ECALL/EBREAK/URET/SRET/MRET） | PC、active mask、trap CSR view | trap CSR writes、PC redirect、mask save/restore 或 illegal | CSR owner 与 Warp PC/SIMT lifecycle owner；privilege细节见 U-CSR-01 |
| CSR/System | CSR context、GPR/immediate、warp/lane/CTA/device identity | old-value GPR result、CSR read-modify-write 或 illegal、next-PC | CSR owner validates/applies；GPR/PC owners receive effects |
| TMC | warp operand/current mask | active lane mask replacement、可能的 warp termination | Warp/SIMT lifecycle owner |
| PRED | per-lane predicate、current mask/fallback operand | predicated active mask、可能的 warp termination | Warp/SIMT lifecycle owner |
| WSPAWN | spawning warp operands/context | target warp activation、PC/mask；RTL 明确复制 `mscratch` | Core/Warp manager routes lifecycle effects；CSR owner handles mscratch；其他 context 见 U-WSPAWN-01 |
| SPLIT | per-lane predicate、PC、mask | divergence push、selected active mask、next-PC | Warp/SIMT divergence owner |
| JOIN | current mask、stack pointer/reconvergence view | divergence pop、reconverged mask/PC | Warp/SIMT divergence owner |
| BAR | barrier id/size/phase、CTA/core/warp identity | arrive/wait/event update、Warp block/release、可能的 memory-order effect | Barrier coordinator + Warp lifecycle owner；scope/visibility 见 U-BAR-01 |
| WSYNC | warp pending architectural work | ordering/wait result、Warp block/release；不产生 drain cycles | Warp executor/lifecycle owner；不保存 pipeline state |
| VOTE | active lanes、Lane GPR predicates | masked Lane GPR result、next-PC | Warp register/PC owners |
| SHFL | active lanes、Lane GPR values/index | masked Lane GPR result、next-PC | Warp register/PC owners；无持久 shuffle network state |
| WGATHER | Lane GPR tuple/source lane、instruction group | instruction-defined write mask 的 Lane GPR result、next-PC；RTL 将每个 4-lane group 的非 source lanes 置为写目标，即使它们不在输入 active mask | Warp register/PC owners；具体例外见第 9 节 E-LANE-01 |
| packed load（`vx_packlb_f`/`vx_packlh_f`） | active lanes、base/stride、Memory bounds/service | 每 lane 4 个 unsigned-byte 或 2 个 unsigned-halfword request、assembled FPR write、next-PC 或 typed fault | Memory owner 返回 element response；FPR/PC owners 原子 apply |
| Completion control | warp termination/fault、CTA membership、remaining CTA/device work | Warp done → CTA done → Device/kernel completion 或 fault | Warp、CTA、Device lifecycle owners 单向聚合；host protocol 见 U-ABI-01 |

共同规则为 `FROZEN`：x0 写入被抑制；普通 lane result 使用该指令传递的 active/write mask，WGATHER 使用 RTL 明确覆盖后的非 source-lane write mask；顺序 PC 在 C 关闭时按 4-byte instruction 前进；store、CSR、SIMT control 和 trap 等非寄存器 effects 也必须经过显式 owner，而不能被当成普通 writeback 遗失。

### 5.3 T1 整数、控制流与 memory effect 实证

`FROZEN（T1/02-integer-memory）`：`isa.EvaluateInteger` 已将第 5.1/5.2 节的无状态边界落实为固定四 lane 的 immutable `IntegerInput` 和仅包含请求/结果的 `IntegerEffects`。输入只携带本条指令已读取的 rs1/rs2 lane values、PC、active mask 及可选 immutable address bounds；输出只携带 masked register write、warp PC、memory request、ordering 或 typed fault。ISA package 不持有 register array、memory bytes、PC 或跨调用 transaction；`isa/integer_test.go` 使用 `support/memory` 证明 evaluate 前后 canonical memory 不变，只有测试中的外部 owner 消费 store request 后才修改 bytes。

`FROZEN` 整数与 PC 规则：

- RV32 ALU/M/Zicond 全部在 `uint32` 域 wrap；register shift amount 只取低 5 位；signed compare/shift/div/rem 明确使用 32-bit two's-complement。M 的除零与 `INT_MIN / -1`、高半有/无符号组合均形成确定结果。
- 普通指令产生 `PC+4`（包括 32-bit wrap）的顺序 control effect。AUIPC 使用当前 PC；JAL/JALR 对所有 active lanes 产生 link=`PC+4`，rd=x0 时不产生 register effect。
- 六类 conditional branch 和 JALR 的 warp-wide 决策/目标 operand 使用最高编号 active lane，与 `VX_alu_int.sv` 的 reverse `last_tid` 一致；branch target=`PC+imm`，JAL target=`PC+imm`，JALR target=`(rs1+imm)&~1`。C 关闭后 taken target 必须四字节对齐；misaligned taken target 产生 typed instruction-address fault 且不产生 PC/link write，not-taken branch 不检查未采用 target。
- 每个 register effect 显式携带 lane mask；inactive lane 不计算/写回，integer x0 在 ISA effect 边界即被抑制。

`FROZEN` integer memory/ordering 规则：

- 每个 active lane 的 effective address 是 `rs1 + sext(imm)` 的 32-bit wrap 结果。1/2/4-byte width 必须自然对齐；任一 active lane misaligned 时返回 load/store-address-misaligned effect，不发 memory request。inactive lane 不触发地址、对齐或 bounds fault。
- 对齐通过后产生逐 lane `MemoryRequest`，其中 byte address、aligned 32-bit word address、width、signedness、RTL-equivalent byte mask 和 aligned store data 均显式。可选 `AddressBounds` 能提前形成 bounds access fault；未提供时，memory owner 以 `MemoryResponse` 返回 access/bounds/service fault。
- memory issue outcome 只携带 requests，不提前携带 PC effect；`CompleteMemory` 要求 response lanes 完整覆盖 issue active mask。成功 completion 才产生 `PC+4`，load 同时根据 LB/LBU/LH/LHU/LW 做 32-bit sign/zero extension和 masked rd write，store 不产生 register write。一个 lane fault 会抑制该指令的 PC 与全部 partial writeback，最终 fault routing/trap priority 仍见 U-FAULT-01。
- FENCE 只产生 pred/succ ordering effect 和顺序 PC effect；不读写 memory bytes，不建 cache、flush timing、pipeline drain 或 scheduler state。

直接证据为 `hw/rtl/core/VX_alu_int.sv:72-133,225-361`、`hw/rtl/VX_gpu_pkg.sv:326-465`、`hw/rtl/core/VX_alu_muldiv.sv:34-337`、`hw/rtl/core/VX_lsu_agu.sv:16-53` 和 `hw/rtl/core/VX_lsu_slice.sv:145-250,325-407`。

### 5.4 T1 RV32F、SoftFloat 与 FCSR effect 实证

`FROZEN（T1/03-rv32f）`：`isa.EvaluateFloat` 接收固定四 lane 的 immutable `FloatInput`；rs1/rs2/rs3 是 decoded register namespace 已读出的 raw 32-bit values，此外仅携带 PC、active mask、可选 address bounds 和动态指令实际需要的 FRM view。输出复用 `InstructionEffects`，只含 masked GPR/FPR write、PC、memory request/typed fault 与 `FFlagsEffect`。ISA package 不保存 FPR/GPR array 或长期 FCSR；`FFlagsEffect.Accumulate` 明确要求 FCSR owner 用 OR 做 sticky accumulation。

`FROZEN` RV32F 数值与 FCSR 规则：

- FADD.S/FSUB.S/FMUL.S/FDIV.S/FSQRT.S、四类 fused multiply-add、FEQ/FLT/FLE 和所有 RV32 integer/float conversions 直接调用既有 `support/softfloat` CGO bridge。没有在 ISA 层复制浮点算法或引入第二个 FP dependency；bridge 将 tininess 固定为 after-rounding，并在每次调用前清零 SoftFloat exception state。
- strict decoder 的静态 rm=0..4 分别映射 RNE/RTZ/RDN/RUP/RMM；rm=7 才读取 `FloatInput.FRM`。动态 FRM 必须是这五种值，否则返回 evaluation input error；rm=5/6、D format、FLEN64 conversion 和非法 rs2/funct 组合在 evaluate 前已由 decoder 确定性拒绝。
- FMADD/FMSUB/FNMSUB/FNMADD 通过 RTL 同样的 product/addend sign transform 后只调用一次 SoftFloat FMA，保持 fused single-rounding。FSGNJ/FSGNJN/FSGNJX、FCLASS 和 FMV 是无 flags 的 raw-bit 操作；FCLASS 精确产生 RISC-V 十类 mask。
- FMIN/FMAX 的一侧 NaN 返回另一侧、双 NaN 返回 canonical qNaN `0x7fc00000`、任一 signaling NaN 置 NV，且 `FMIN(+0,-0)=-0`、`FMAX(+0,-0)=+0`。FEQ 是 quiet equality（仅 sNaN 置 NV），FLT/FLE 是 signaling compare（任一 NaN 置 NV）；numeric ordering 直接复用 SoftFloat compare。
- 每个 active lane 单独产生 raw result/exception bits，instruction outcome 对 active lanes 的 flags 按位 OR。flag-producing 指令即使本次 flags 为零也返回 non-nil sticky effect；FSGNJ/FCLASS/FMV 和 FLW/FSW 返回 nil，因而 owner 能区分“积累零”与“不得触碰 FFLAGS”。inactive lane 不计算、不写回且不贡献 flags。
- FLW/FSW 复用第 5.3 节的无 mutation、两阶段 memory request/response 契约；四字节自然对齐、bounds/service fault、成功后 PC+4 及 fault 时抑制 PC/partial FPR write 均与整数 LSU 一致。FPR `f0` 是普通可写目标；产生 GPR 的 compare/convert/move/class 仍在 effect 边界抑制 x0。

直接 RTL 证据为 `hw/rtl/core/VX_decode.sv:432-558` 的 op/fmt/rm 与 FCSR dependency decode，`hw/rtl/fpu/VX_fpu_unit.sv:92-99,227-261` 的 dynamic FRM 和 active-lane flags OR/FCSR write，`hw/rtl/fpu/VX_fma_unit_rtl.sv:108-128` 的 FMA sign 方程，以及 `hw/rtl/fpu/VX_fncp_unit.sv:61-321` 的 classify/min-max/compare/sign/move 与 NV 规则；实现证据为 `support/softfloat/bridge.c`、`support/softfloat/softfloat.go`、`isa/float.go` 和 `isa/effects.go`。

### 5.5 T1 CSR/System、trap-return 与 counter view 实证

`FROZEN（T1/04-csr-system）`：`isa/csr_catalog.go` 是冻结 CSR 地址合法性的唯一 manifest；`EvaluateSystem` 只消费一条 decoded instruction、raw rs1 lane values、PC/active mask 和 immutable `CSRView`。该 view 汇集 CSR owner、scheduler、CTA owner 与 counter owner 已读出的最小 snapshot，但不是 map/register file，也不跨调用保存状态。结果通过 masked GPR write、`CSRReadEffect`、`CSRWriteEffect`、`TrapEffect` 和 PC effect 返回；非法地址或写只读 CSR 在产生任何 partial effect 前返回 typed `CSRAccessError`。

`FROZEN` CSR 地址与 RMW 规则：

- 实际有可写 storage 的地址是 FFLAGS/FRM/FCSR（分别只实现低 5/3/8 bits）、每 warp `mstatus`/`mtvec`/`mscratch`/`mepc`/`mcause`/`mtval`（32-bit）。SATP 在 VM 关闭时以及 `medeleg`/`mideleg`/`mie`/`mnstatus`/`pmpcfg0`/`pmpaddr0` 是可读零、写地址合法但没有 storage；对应 effect 显式标记 `Ignored`，不假造 state。
- `mvendorid`/`marchid`/`mimpid` 为零，MISA=`0x40901120`（RV32 MXL 与冻结 F/I/M/U/X bits）。THREAD/WARP/CORE/ACTIVE、NUM_THREADS=4、NUM_WARPS=4、NUM_CORES=1、LMEM_BASE=`0xffff0000`、NUM_BARRIERS=8，以及 CTA id/rank/size/thread coordinates/block/grid/LMEM/cluster/entry 都是严格只读 identity/context view。未知地址、reserved MPM B01/B81 和未实现 standard aliases（例如 CYCLE C00）确定性拒绝。
- CSRRW 总是请求写；CSRRS/CSRRC 及 immediate variants 在 source value 为零时只读不写，和 `VX_csr_unit` 的 `csr_wr_enable = csr_write_enable || |csr_req_data` 一致。register form 的写 source 明确使用 rs1 lane 0；old-value GPR result 则逐 active lane 返回，THREAD_ID/MHARTID/CTA thread coordinates 可逐 lane 不同。rd=x0 只抑制 GPR effect，不抑制合法 CSR write。
- RMW 先从 snapshot 取得 old value，再按 write mask 形成 typed owner request。向只读 identity/config/counter CSR 的非零 set/clear 或任意 CSRRW 都非法；零 source 的 CSRRS/CSRRC 可合法读取。FCSR write 只替换实现 bits，不直接维护 FP flags 的长期副本。
- MCYCLE/MINSTRET 使用显式 `CounterView`；RTL `PERF_CTR_BITS=44`，所以 low read 返回 bits 31:0，high read 仅返回 bits 43:32。指令请求在 `VX_csr_unit` 强制 `mpm_class=BASE(0)`，因此 B03..B22 和 B83..BA2 虽为合法 MPM window，却确定性读零；ISA 不引入 cycle/performance accumulation policy。

`FROZEN` synchronous trap/return 规则：

- ECALL 与 EBREAK 要求正在运行的 thread mask，分别产生 mcause 11（machine environment call）与 3（breakpoint）；将 faulting PC 写 mepc、cause 写 mcause、零写 mtval，并把当前完整 thread mask 作为 scheduler restore-mask effect 保存。PC redirect 为 `mtvec & ~3`，不实现 vectored mode。
- URET/SRET/MRET 在冻结 RTL 中走同一个 `is_mret` 路径：PC redirect 为 `mepc & ~3`；saved mask 非零时请求恢复该 mask，为零时保留当前 running mask。它们不修改 mstatus，也不凭 encoding 名称增加 privilege validation/transition engine。
- trap CSR updates 是独立 `CSRWriteEffect`，mask save/restore 是 `TrapEffect`，PC 是 `ControlEffect`；最终原子 apply、reset 与 owner 冲突优先级仍由后续 execution/effect-router 集成解决，不授权 ISA 自行修改 scheduler。

直接 RTL 证据为 `hw/VX_types.vh:348-429` 的地址/cause 定义，`hw/rtl/core/VX_decode.sv:373-397` 的 CSR/System decode，`hw/rtl/core/VX_csr_unit.sv:43-187` 的 lane/context read 与 RMW 方程，`hw/rtl/core/VX_csr_data.sv:76-263` 的 write allowlist/read values/FCSR masks/counters，以及 `hw/rtl/core/VX_alu_int.sv:321-350`、`hw/rtl/core/VX_scheduler.sv:245-255,358-398,541-588` 的 trap/return/counter路径；实现证据为 `isa/csr_catalog.go`、`isa/system.go` 和 `isa/effects.go`。

### 5.6 T1 Vortex custom、cross-lane 与 SIMT effect 实证

`FROZEN（T1/05-vortex-custom）`：`isa.EvaluateCustom` 只消费 decoded operation 与 immutable `CustomInput`。输入包含本条指令已读取的四 lane raw operands、PC/active mask/warp id，以及 JOIN、barrier、WSYNC 或 packed load 实际需要的单行 divergence view、phase/pending predicate、mscratch 和可选 address bounds；它不是 Warp、stack、barrier coordinator、scheduler 或 memory。输出通过 `WarpMaskEffect`、`WarpSpawnEffect`、`DivergenceEffect`、`WarpDrainEffect`、`BarrierEffect`、packed requests、masked register write与 PC/fault effect 描述一条指令边界，ISA 不应用或保存它们。

`FROZEN` warp/SIMT control 规则：

- 与 `VX_wctl_unit` 一致，warp-wide rs1/rs2 使用最高编号 active lane；empty mask 时 RTL priority encoder 的 zero padding确定为 lane 0。TMC 用 rs1 低四位替换 active mask，零 mask 显式标记 inactive/termination；PRED 以每 lane rs1 bit 0（可由 encoding negate）筛选当前 mask，无 true lane 时使用选定 control lane 的 rs2 低四位 fallback。
- WSPAWN 将 `i < rs1[2:0] && i != current_wid` 的四个 frozen warp 形成目标 mask，目标 PC=rs2、初始 lane mask 仅 bit 0；effect 明确携带“只在 single-active-warp 条件下应用”和将 source `mscratch` 复制到每个 target 的边界，不创建 warp、不复制 GPR/FPR、也不调度。
- SPLIT 构造 then/else masks，只在二者均非空时 push `{original mask, PC+4}`；先执行 popcount 较小的一侧，平局选 then。rd 返回输入 view 的 current stack pointer。JOIN 只读取 rs1 指向的一行 `DivergenceRecordView`：首个 join 将该行标为 else-visited、选择 `original & ~current` 并 redirect 到保存的 PC；第二个 join 恢复 original mask并 pop；rs1 pointer 等于 write pointer 时不改变 mask/PC。stack under/overflow和跨指令 ownership仍不由 ISA 推断。
- BAR、BAR.ARRIVE、BAR.WAIT 在 LSU pending 时只返回 drain/wait，不泄漏 barrier/register/PC partial effect；drained 后才将 id、address-warp、size/count、sync/arrive/wait、global、phase 与 expect-transaction event 形成 typed request。BAR.ARRIVE 的 expect_tx 形式由 rs2[31] 选择，强制 event phase=1（登记/增加预期事件而不是完成递减），并以低五位编码 count、零编码 32；arrive 的 rd 返回显式 barrier phase view，sync/wait 的 release 明确交 coordinator。WSYNC 同样在 prior work pending 时只返回 wait/drain，drained 后才返回 release与 PC+4。两者都不保存 pending cycle、barrier mask/count/events/phase或释放队列。

`FROZEN` four-lane data/memory 规则：

- VOTE 仅统计 active lane 的 rs1 bit 0；ALL 对 empty mask 为 true、ANY 为 false、UNI 为 true，BALLOT 返回 true-lane mask，普通 write mask仍为 active mask。SHFL 从 rs2 的 bval/cval/mask fields 形成 UP/DOWN/BFLY/IDX target；target越过 subgroup boundary或不 active时逐 lane返回自身 rs1。
- WGATHER 对 encoded source 0..3 均已验证；nominal source inactive时使用最高 active lane，empty mask时使用 RTL zero-padding lane 0。以 encoded source 为旋转原点把 fallback/nominal source 的 rs1/rs2/rs3 分别送往 offset 1/2/3，并将 write mask固定为四 lane 中所有 non-source bits，即使这些 lane不在输入 active mask。
- `vx_packlb_f` 按 `base + element*stride` 发出四个 1-byte unsigned requests，`vx_packlh_f` 发出两个 2-byte unsigned requests；element 0 写 FPR最低 byte/halfword，依次向高位组装。issue 不推进 PC，完整且无 fault 的 response set 才原子地产生 FPR write与 PC+4；任一 alignment/bounds/service fault抑制全部 partial write/PC。该模型不建立 micro-op sequencer、LSU queue或 memory副本。

直接 RTL 证据为 `hw/rtl/core/VX_wctl_unit.sv:42-258`、`VX_split_join.sv:38-100`、`VX_ipdom_stack.sv:44-119`、`VX_scheduler.sv:202-241,325-349,405-445`、`VX_bar_unit.sv:48-149`、`VX_alu_int.sv:136-223,255-286`、`VX_uop_packld.sv:16-57`、`VX_lsu_agu.sv:16-53` 与 `VX_commit.sv:90-109`；实现和验证证据为 `isa/custom.go`、`isa/effects.go`、`isa/custom_test.go` 和 `scripts/verify.sh`。

### 5.7 T1 最终覆盖与无状态 API 审计

`FROZEN（T1/06-coverage-contract）`：最终 catalog 固定为 105 条 frozen-enabled instruction entry：RV32I 37 条、FENCE 1 条、RV32M 8 条、Zicond 2 条、System/CSR 11 条、RV32F 26 条、Vortex custom/SIMT 20 条。`isa/coverage_test.go` 维护一份不从 catalog 派生的 105 条 functional-vector registry；最终门禁对每个名字同时验证 catalog example 可达、functional word 可达且仍解码为同名指令、登记的 `EvaluateInteger`/`EvaluateFloat`/`EvaluateSystem`/`EvaluateCustom` 能产生非空 result/effect，并反向拒绝无 catalog entry 的孤立 vector。新增、删除或改名任一 catalog entry 而未同步 functional vector/evaluator 时测试确定性失败；各 evaluator 的逐指令数值、mask、fault 与边界结果仍由对应 `*_test.go` 的定向 vectors 验证。

`FROZEN` strict-illegal 门禁由 `isa/contract_test.go` 与 `isa/decode_test.go` 共同组成。类别门禁显式登记 reserved opcode、关闭的 C/RV64/D/A/vector/accelerator、reserved funct/format/rm/register field 及 RTL 宽松 default 容易泄漏的 fixed-field holes，并要求统一返回保留原 word 的 `IllegalInstructionError`；详细反例继续覆盖 width、FP conversion rs2、System reserved immediate、custom rd/rs2 和关闭单元 funct 编码。该门禁不把 RTL 的 `x`/default 当成合法性授权。

`FROZEN` 公开 API 审计解析所有非测试 `isa/*.go`，要求生产 package 只暴露 catalog/CSR catalog、严格 decode、四类单指令 evaluator 及 integer/float/packed-memory completion；不允许 `init` side effect、额外 orchestration operation 或 ISA 外依赖。package-level variables 只能存在于 `catalog.go`/`csr_catalog.go` 的不可导出 manifests；不得定义 canonical `State`/`Machine`/`Device`/`Core`/`Warp`/`Scheduler`/`Executor`/`Kernel`/`Cache`/`MMU` owner，也不得出现 fetch loop、`Warp.Step`、schedule/dispatch/launch/tick API。因此 ISA package 只消费调用方提供的 immutable views，不跨指令拥有 PC、GPR/FPR、CSR、memory、warp、barrier 或 CTA 状态。

最终离线证据为 `TestCatalogDecodeEvaluatorVectorCoverageGate`、`TestFinalIllegalEncodingClassGate`、`TestISAPublicSurfaceIsStatelessAndSingleInstruction` 与所有既有 functional tests，并统一由 `scripts/verify.sh` 执行 build/test/vet/gofmt/diff 门禁。外部 `Vortex_rtl` 仅经只读审计，最终检查时 worktree clean，HEAD 为 `85a88fe250b0da483cb33ed34126d675ecb93c1c`；交付从未向该路径写入。上述结论只闭合 T1 ISA 单指令层，canonical owner apply、Warp/Barrier/CTA 协调、memory scope、counter progression 与 checker/ABI 仍按第 10 节保持 `UNRESOLVED`。

### 5.8 T2 canonical State 与只读 ISA View 实证

`FROZEN（T2/01-canonical-state-views）`：`state.WarpState` 已成为一条 warp 的 lane/warp 范围长期真值 owner。初始化必须显式提供并校验冻结拓扑（4 lanes、4 warps、1 core、每 namespace 32 registers、IPDOM depth 3）、warp id、四个不重复 lane id、四字节对齐 PC、active mask 与 running/inactive lifecycle 一致性、saved thread mask、八位 FCSR、六个 T1 可写 trap/system CSR、每 lane GPR/FPR，以及完整 divergence live prefix。U-ABI-01 尚未决定的 startup 值没有 reset/launch 默认值；调用者必须明确给出。x0 非零初始化被拒绝，此后读取恒零且写入为 no-op。

`FROZEN（T2/01-canonical-state-views）`：`WarpSnapshot` 是 owner 字段、lane arrays 和 divergence records 的 detached value copy，字段私有，accessor 再返回数组/record 值。`IntegerInput`、`FloatInput`、`SystemInput`、`CustomInput` builder 按 `Decoded.Sources` 的 register namespace/index 读取 canonical snapshot；inactive lane 读取不改状态，写 mask 不在 read path 暗中与 active mask 求交。JOIN 只取得控制 lane rs1 指向的单行 `DivergenceRecordView`，不暴露 stack；FCSR raw bits 同时进入 System view，FRM 经显式 encoding 映射进入 Float view。

`FROZEN（T2/01-canonical-state-views）`：Core id/active-warps、CTA view、44-bit counters、memory bounds、barrier phase、pending prior work 与 pending LSU 均只存在于按调用提供的 `state.ReadContext`。builder 校验并复制所需值（包括对 `AddressBounds` 重新分配副本），`WarpState`/`WarpSnapshot` 从不保存 context 或其指针。因此后续 Core/CTA/Memory/Barrier owner 无需从 Warp 收回重复真值。实现与定向验证位于 `state/state.go`、`state/view.go`、`state/state_test.go`；该里程碑当时保留的 staged apply/future-owner routing 已由第 5.9 节闭合。

### 5.9 T2 原子 effect apply 与 future-owner 路由实证

`FROZEN（T2/02-atomic-effect-apply）`：`state.StageEffects` 在 canonical mutation 前复制完整 `WarpState`，并只在候选副本上处理 bundle。它预校验 register namespace/index/mask 与重复 destination、所有携带的 warp identity、Control 的 current-PC/next-PC/taken/target/decision-lane 关系、CSR catalog address/scope/warp/old-value/write-mask/ignored metadata、trap entry/return 的 PC/mask/精确 CSR write 组合（TrapEnter 的 Vector、Control NextPC/Target 必须共同等于 canonical `MTVec &^ 3`），以及 SPLIT/JOIN push/mark/pop 的 pointer、capacity、record、mask partition 和配套 Control/WarpMask。任何错误都丢弃候选；canonical GPR/FPR、PC、mask/lifecycle、CSR、saved mask 和 IPDOM records 保持逐位不变。

`FROZEN（T2/02-atomic-effect-apply）`：成功的本地 stage 一次替换 canonical owner，masked GPR/FPR write 不与 active mask 二次求交，因而普通 effect 只改其显式 Mask、WGATHER 的 non-source mask 仍可写 input inactive lanes；x0 effect 在 owner 再次成为 no-op。TMC/PRED 同步更新 mask/lifecycle，SPLIT/JOIN 同步更新 stack/mask/PC，trap entry 同步写 MEPC/MCAUSE/MTVAL、saved mask和PC，xRET按非零 saved mask恢复 lifecycle/mask。FFLAGS 先 sticky OR，随后同一 bundle 的软件 FFLAGS/FRM/FCSR 写覆盖其实现字段，直接对应 `VX_csr_data.sv` 的 `fcsr_n` 组合顺序。

`FROZEN（T2/02-atomic-effect-apply）`：MemoryRequest、PackedLoadRequest、Ordering、WarpSpawn、WarpDrain、Barrier、Fault、全部 CSRRead（包括 Core/CTA scope）及非本地 CSRWrite 都以 detached `ForwardedEffects` 保留，Warp owner 不标记为已消费。只要存在需未来 owner 成功的 effect，`ApplyEffects` 返回 `Pending EffectStage` 且不修改本地状态；普通 `Commit` 被拒绝，协调者只能在外部成功后显式调用 `CommitAfterExternal`。stage 保存完整 before image，期间任一 canonical 修改都会令提交确定性 stale-fail，阻止跨 owner partial commit。返回的 forwarded bundle 另作深拷贝，调用者不能反向篡改 stage。实现和验证证据为 `state/apply.go`、`state/apply_test.go` 及 `scripts/verify.sh`。

### 5.10 T2 ISA→State 单指令连接与集成实证

`FROZEN（T2/03-isa-state-integration-contract）`：`state.ExecuteSingle` 是一条由调用者提供 word 的 Decode → detached `WarpSnapshot`/最小 `ReadContext` View → 既有 T1 evaluator → `ApplyEffects` 连接边界。它只按 decoded category 分派 `EvaluateInteger`、`EvaluateFloat`、`EvaluateSystem` 或 `EvaluateCustom`，返回 decoded operation、detached effects 和 apply/forwarding 结果；不读取 instruction memory，不循环，不选择 warp，不保存 evaluator 状态，也不提供 fetch、`Warp.Step`、scheduler、dispatch、launch 或 tick API。连续 ADD→ADDI、branch→JAL、SPLIT→JOIN→JOIN 及 trap→xRET 调用证明跨调用事实只持续在 `WarpState`，两个独立 owner 用同一 instruction word 得到各自结果，`isa` 仍是无长期状态的叶子。

`FROZEN（T2/03-isa-state-integration-contract）`：`state.CompleteMemoryAndApply` 只把调用者已取得的 decoded memory operation、expected lane mask 和 future Memory owner responses交给 T1 completion helper，再走同一原子 apply；它不拥有 memory bytes、pending request 或服务循环。成功 load completion 将 masked GPR 与顺序 PC 一次提交；任一 lane service fault 只形成无损 forwarded fault，GPR/PC 均保持 completion 前状态。instruction issue 的 MemoryRequest、FENCE ordering、BAR/WarpDrain 和 WSPAWN 仍返回 pending stage，在未来 owner 明确成功前不执行或丢失。

`FROZEN（T2/03-isa-state-integration-contract）`：`state/integration_test.go` 的端到端路径覆盖 integer GPR+PC、RV32F FPR+sticky FFLAGS、taken branch/JAL、TMC/PRED mask、SPLIT/JOIN record 生命周期、CSR RMW、ECALL/xRET、memory register+PC completion、illegal decode/fault 无 partial mutation，以及 BAR/WSPAWN/ordering 保真转交。该范围将第 6 节步骤 4–8 的单 warp、单指令、已拥有 word 子链从设计方向提升为实现契约；instruction source、fault router、Memory/Core/CTA/Barrier owner 和 progress/completion 仍未实现。

### 5.11 T3 single-lane fetch/Step core 实证

`FROZEN（T3/01-fetch-step-core）`：`warp.Warp` 是 single-lane functional execution orchestration 边界。它只长期引用真实 `*state.WarpState` 与 `InstructionSource`，不复制或缓存 PC、GPR/FPR、CSR、lane mask、lifecycle 或 program bytes；外部 owner context 仍由调用者在每次 `Step(state.ReadContext)` 显式提供。`InstructionSource.Read` 接收 canonical snapshot PC 和一个长度恰为 4 的 destination，因 C 已关闭而按 little-endian 组成 raw word；source 不推进 PC。inactive lifecycle 在 fetch 前返回 finished；running state 必须恰有一个 active bit，多-lane state 确定性拒绝。

`FROZEN（T3/01-fetch-step-core）`：一次 `Step` 固定经过 canonical snapshot → fetch → `isa.Decode` → `WarpSnapshot.Evaluate` → `WarpState.StageEffects` → local `EffectStage.Commit`。新增的 `WarpSnapshot.Evaluate` 是 T2 `ExecuteSingle` 与 T3 共同使用的唯一 category dispatch，因此 executor 未复制 integer/M/Zicond、branch/JAL/JALR、RV32F、CSR/System、trap 或 custom 的 ISA 方程。成功的 local non-memory instruction 只由 T2 stage 一次提交，结果中的 next PC 再从 canonical owner snapshot 读取；ECALL/EBREAK/xRET 同样原子提交并返回 typed trap outcome。

`FROZEN（T3/01-fetch-step-core）`：`warp.Result` 区分 retired、trap、fault、deferred 与 finished，并保留 step PC、raw-valid/raw word、可用时的 decoded/effects、canonical next PC、typed `warp.Fault` 和底层 error。alignment/access fetch failure、illegal decode、view/evaluation error、effect validation/commit failure及架构 fault均停止且不提交本步 local stage。data Memory/Ordering、非 Warp-owned CSR write 及其他 future-owner prerequisite 返回 deferred；本里程碑全部 custom instruction 即使在单 lane 输入下可算出局部方程，也只 evaluate/validate 后 deferred，避免将 cross-lane、SIMT、Warp、Barrier、CTA/Core 行为伪装为 T3 single-lane 支持。对应实现与定向验证位于 `warp/warp.go`、`warp/warp_test.go`、`state/integration.go`；data-memory completion、连续 Run/budget 与 trace 仍留给后续 T3 里程碑。

### 5.12 T3 single-lane atomic memory Step 实证

`FROZEN（T3/02-atomic-memory-step）`：`warp.MemoryService` 是当前同步 functional byte owner 边界，提供 all-or-error `Read`/`Write`；`NewWithMemory` 显式配置该边界，原有 `New` 收到同时实现 MemoryService 的 source 时也识别同一实例，因此 fetch、load、store及自修改后重新 fetch均观察同一 canonical bytes。`warp.Warp` 与 `state.WarpState` 只保存 service 引用或 architectural state，不复制 instruction image/data bytes。`support/memory.Memory` 是该接口的基础 flat adapter，不冻结最终 global/LMEM scope、ordering、visibility或 self-modifying-code policy；这些仍由 U-MEM-01 管理。

`FROZEN（T3/02-atomic-memory-step）`：single active lane 的 LB/LBU/LH/LHU/LW、SB/SH/SW、FLW/FSW 先由 T1 evaluator产生 address/width/byte-mask/little-endian request；executor只按 typed request访问 byte owner，再通过 `WarpSnapshot.CompleteMemory` 分派既有 integer/RV32F completion helper。`vx_packlb_f`/`vx_packlh_f` 的 lane-local element request同样经 service读取并由 `WarpSnapshot.CompletePackedLoad` 调用 T1 assembler；其他 VOTE/SHFL/WGATHER、TMC/PRED、SPLIT/JOIN、WSPAWN、BAR/WSYNC custom effects仍 deferred。FENCE ordering 在配置同步 service时显式以 external-success完成，未配置 memory owner时保持 deferred，不静默丢弃。

`FROZEN（T3/02-atomic-memory-step）`：load/packed completion effects先在 T2 detached stage完整预校验后一次提交 register/FPR与PC。Store 先构造 T1 success completion并完成 T2 stage校验，再由 `EffectStage.CommitWithExternal` 在调用 memory前拒绝 stale stage；有效 stage只调用一次 all-or-error Write，Write成功后直接安装已验证的 canonical PC而没有可失败的第二阶段，Write失败则恢复 stage.before且按 MemoryService契约不改变 bytes。实现不使用可能失败或覆盖 intervening update 的补偿写。alignment/bounds pre-fault、service failure、completion/effect validation failure均不遗留本步 memory/state mutation。Fault继续作为 typed outcome保留，不猜测 U-FAULT-01 trap mapping。实现与验证位于 `warp/warp.go`、`warp/memory_test.go`、`state/apply.go`、`state/integration.go`；连续 Run/budget 与 trace仍留给后续里程碑。

### 5.13 T3 bounded Run、completion 与 trace 实证

`FROZEN（T3/03-run-trace-contract）`：`warp.Run(RunOptions)` 只按显式有限 `StepBudget` 重复调用 `Step`，不读取、缓存或自增影子 PC；每次 attempt 的 fetch仍从 `WarpState` canonical snapshot开始。零 budget不调用 Step；连续 retired progress达到上限后返回 typed budget-exceeded error；fault、trap、deferred或finished在该次 attempt后立即停止。`RunResult` 分别记录所有 Step calls 的 Attempts、仅 `OutcomeRetired` 的 Retired及可选 Last（零 budget为 nil）。自环 JAL与普通控制流loop均只由预算终止，不依赖外部 timeout。

`FROZEN（T3/03-run-trace-contract）`：`RunOptions.Context` 可按 zero-based attempt提供逐步 `ReadContext`，nil时使用零值；这不在 executor中保存 Core/CTA/Counter view。`TraceSink`/`TraceFunc` 同样按 Run调用注入且nil默认完全关闭。启用时每一 attempted Step产生 detached `TraceRecord`，包含 step index、WarpID、起始 PC、raw-valid/raw word、decoded instruction、T1 issued effects（包括memory requests）、最终completion effects、canonical next PC与Step outcome；decoded的register slices、effect slices及所有 pointer effects均深拷贝，因此 sink不能经record alias修改执行结果或Last。该接口可由现有 `support/logging`/slog应用层适配，但executor不拥有logger、文件或全局开关，也不产生pipeline/timing trace。

`FROZEN（T3 completion boundary）`：RunFinished唯一来自 Step观察到 canonical `WarpInactive`，不会fetch；T3不从PC范围、ECALL、trap return、预算、fetch/data fault或host image推断正常完成。Trap、fault、deferred和budget exhausted都是明确停止原因而非CTA/Kernel/host completion。完整hand-built stream与定向测试位于 `warp/run_test.go`，覆盖顺序依赖、taken/not-taken branch、JAL/JALR、M/Zicond、integer/FP memory链、CSR、illegal/fetch/data fault、loop/budget、inactive finish与trace observational isolation；T1/T2全量回归仍由 `scripts/verify.sh` 门禁。

## 6. 单条指令的功能执行流程

以下完整顺序仍是功能边界而不是 RTL cycle/pipeline 模型。single-lane执行的步骤 3–8及预算化步骤9子集已由第 5.11–5.13 节冻结；launch、runnable warp选择和CTA/Kernel/host completion仍是 `PROVISIONAL`：

1. **Launch/prepare**：Device/CTA manager 根据 launch context 建立 CTA，并请求 Warp owner 建立 entry PC、CTA key、LMEM allocation view 和初始 active lane mask。
2. **Select runnable warp**：Core/Warp manager 从 owner 提供的 runnable view 选择一个 Warp。调度策略在不改变架构结果时可替换，不复刻 RTL arbitration。
3. **Fetch**：instruction source 用该 Warp PC 从 instruction memory 取 32-bit word；C 已关闭。成功取指不自行改 PC，fault 产生显式 effect。
4. **Decode**：冻结专用 ISA decoder 仅用 word 产出 decoded operation 或 `IllegalInstructionError`；decoded operation 声明 evaluate 是否还需 PC/active mask/service view。
5. **Read view**：Warp executor 根据 decoded operation 向 owners 请求最小 immutable Lane/Warp、GPR/FPR、CTA、CSR、SIMT 或 Memory view，不把整个 Device 交给 ISA。
6. **Evaluate semantics**：ISA evaluator 计算单条指令的功能结果；Memory/CSR/Barrier 服务只经显式请求参与，不回调 manager 修改状态。
7. **Produce effect**：evaluator 返回 structured effects 或 fault/wait，不直接 mutation；无 effect 的非法路径也必须显式表示。
8. **Owner validate/update**：effect router 校验 target/scope、输入 active mask 与指令定义的 write mask 并路由，GPR/FPR、Warp PC/SIMT、CSR、Memory、Barrier、lifecycle 等 owner 各自最终验证和更新唯一 canonical state。
9. **Progress/completion**：Warp manager 重新派生 runnable view；Warp completion 单向聚合到 CTA、Device/kernel executor。Harness 只经公共结果接口读取最终状态和内存。

## 7. 后续公共接口清单

除第 5.1、5.3、5.4、5.5、5.6 节已冻结的 ISA decoder/evaluator/effect 具体接口、第 5.8 节的 Lane/Warp State/View、第 5.9 节的本地原子 apply/forwarding、第 5.10 节的 caller-supplied-word 单指令连接，以及第 5.11–5.13 节明确冻结的 single-lane `InstructionSource`/`MemoryService`/`Warp.Step`/bounded `Run`/trace子集外，下表其余上层集成用途仍是 `PROVISIONAL`。箭头方向均从调用者到服务，结果/effect 显式返回；任何接口都不授权全局查找或旁路 mutation。

| 公共边界（用途名） | 输入 | 输出 | 允许的依赖方向/约束 |
| --- | --- | --- | --- |
| Kernel launch/executor | image、entry、argument/data、launch dimensions、停止条件 | 初始化/进度/completion/fault、最终观察 handle | Harness → Device；只能经 Device/Memory/CTA manager 初始化 owners。 |
| Instruction source | PC/address space key | 32-bit instruction word 或 fetch fault | Warp executor → Memory instruction space；不推进 PC、不回调 scheduler。 |
| Memory spaces | global/local scope key、address/size、active lane requests、store data | load bytes/value、validated store effect 或 fault | evaluator/effect owner → Memory；global 与 LMEM bytes各有单一 owner，cache/timing透明。 |
| Lane/Warp views | warp/lane identity、decoded op 所需字段集合 | immutable active mask、operands、PC/SIMT/CTA-derived view | Warp executor → state owners；返回 view 而非可写引用。 |
| CSR context/service | CSR address/op、warp/lane/CTA/device identity、operand | old value、CSR/trap effect 或 illegal | evaluator → CSR service → CSR owner；不得直接调度 Core/CTA。 |
| Decoder | 32-bit word | decoded operation 或 `IllegalInstructionError` | Warp executor → ISA decoder；需要的 PC/active mask/service 由输出声明，ISA 不依赖 Device mutable state。 |
| Evaluator | decoded op、explicit operands、最小 views/service responses | structured results/effects 或 fault/wait | Warp executor → ISA evaluator；只计算一条指令。 |
| Effect envelope/router | target identities、input active mask、instruction-defined write mask、typed effect payload | routed owner result、fault 或验证错误 | evaluator → router → one target owner；router 不保存第二份 state。 |
| Warp executor/manager | runnable view、单步/运行请求、CTA assignment、owner results | selected warp、effects applied/progress、warp done/fault | Core → Warp manager → decoder/evaluator/services；低层不能反向选择 Core/CTA。 |
| CTA manager | launch descriptor、warp/LMEM capacity、warp done | CTA context、warp assignment、CTA done | Core/Device → CTA manager → Warp/Memory allocation ports；membership 单一 owner。 |
| Barrier coordinator | CTA/core/warp key、barrier id、arrive/wait/event/phase | validated barrier update、blocked/released warp keys、fault | Warp effect → Barrier → Warp lifecycle effect；无 Warp 可写引用。 |
| SIMT control owner | TMC/PRED/WSPAWN/SPLIT/JOIN/WSYNC effect、warp key | mask/PC/stack/lifecycle update result | effect router → Warp/SIMT owner；不调用 ISA 或 Harness。 |
| Snapshot/result | 明确观察 scope | deterministic registers/CSR/memory/fault/completion data | Harness → Device read-only observation；checker 协议闭合后再冻结字段。 |

## 8. 范围与非目标

### 8.1 架构功能范围

`FROZEN`：目标是在上述冻结配置下，接收 Kernel、入口和数据，执行 frozen ISA/custom SIMT 路径，维护跨指令架构状态，并最终产生可与 RTL/reference checker 对照的结果。必须保留对结果有影响的控制流、lane mask、register/CSR、memory、CTA/SIMT、barrier、trap/fault 和 completion 语义；当前缺失的 comparison/ABI 规则以 `UNRESOLVED` 管理。

### 8.2 明确非目标

`FROZEN`：正确性不要求复刻 RTL 的 cycle accuracy、pipeline stages、cache timing/替换、hazard/scoreboard、stall/backpressure、arbiter 优先级、吞吐、latency、带宽、bank conflict、MSHR、issue/dispatch/commit queue、performance scheduling 或任何其他微架构行为。也不要求性能等价、波形等价或相同 warp 交错顺序，除非后续证据证明某种交错会改变架构可见结果；此时应冻结所需的最小功能 ordering，而不是复制整条 pipeline。

`FROZEN（T3 final scope）`：当前实现 single-lane 32-bit fetch、同步 flat data-memory completion、原子 `Warp.Step`、bounded `Run`与可选debug trace。未实现最终 global/LMEM scope与ordering、多 active lanes、lane effect aggregation、真实 TMC/PRED/SPLIT/JOIN/cross-lane/WSPAWN/WSYNC/barrier、scheduler、CoreState/CTAState、Kernel/host completion或 cache/MMU/pipeline/cycle/performance model；deferred/finished outcome不能被解释为这些系统能力已经实现。

禁用扩展和可选加速器不是未来兼容性要求；T0 不为它们实现占位语义。

## 9. 关键 RTL 证据审计

本节只冻结允许输入中可直接复查的 RTL 事实。每行最后一列是由事实导出的 `PROVISIONAL` 功能模拟器 ownership/边界建议，不是 RTL 事实，也不承诺具体数据结构。路径均相对 `Vortex_rtl/`；未被这些证据唯一确定的语义进入第 10 节，而不以设计推导补空白。

| ID/主题 | `FROZEN` RTL 事实 | 具体证据 | 与事实分离的 `PROVISIONAL` 逻辑推导 |
| --- | --- | --- | --- |
| E-PC-01 PC/mask 与普通 branch | `VX_scheduler` 持有 `warp_pcs`、`thread_masks`、`active_warps` 和 `stalled_warps`；C 关闭时一次成功 schedule 写入 `PC + 4`，branch/trap/mret control 可另行重定向 PC 并解除该 warp stall。`VX_alu_int` 从 active mask 选 `last_tid`，形成一个 warp-wide `br_taken`/`br_dest`。 | `hw/rtl/core/VX_scheduler.sv`，module `VX_scheduler`，signals `warp_pcs`/`thread_masks`/`active_warps`/`stalled_warps`、`schedule_if_fire`、`branch_ctl_if`，以及 `VX_CFG_EXT_C_ENABLE` 路径；`hw/rtl/core/VX_alu_int.sv`，module `VX_alu_int`，signals `last_tid_r`、`br_taken`、`branch_ctl_if`。 | Warp 是 PC/mask/lifecycle 的唯一候选 owner；普通 branch 产生 warp PC effect，不隐式创建 divergence stack entry。 |
| E-SIMT-01 SPLIT/JOIN | `VX_wctl_unit` 用各 lane predicate 构造 `then_tmask`/`else_tmask` 和 `next_pc`；`VX_split_join` 仅在 divergent split 时驱动 `VX_ipdom_stack` push，JOIN pop 并返回 `join_tmask`/`join_pc`。scheduler 的 split 只在 `warp_ctl_if.split.is_dvg` 时换 mask，join 可同时换 PC/mask。 | `hw/rtl/core/VX_wctl_unit.sv`，module `VX_wctl_unit`，signals `then_tmask`/`else_tmask`、interface fields `split.next_pc`/`split.is_dvg`；`hw/rtl/core/VX_split_join.sv`，module `VX_split_join`，instance `VX_ipdom_stack`、signals `join_tmask`/`join_pc`；`hw/rtl/core/VX_scheduler.sv`，interfaces `warp_ctl_if.split` 与 join outputs。 | SPLIT/JOIN effect 交 Warp/SIMT owner 更新唯一 reconvergence state；它与普通 branch 的 PC effect 是不同控制边界。 |
| E-WSPAWN-01 | `VX_wctl_unit` 将小于 rs1 指定数量且不含当前 wid 的 warps 置入 spawn mask，目标 PC 来自 rs2。`VX_scheduler` 只在 `wspawn_valid && is_single_warp` 时激活目标 warp，令目标 `thread_masks[i][0] = 1`、写目标 PC，并显式复制 `mscratch_r`；此路径没有显示复制 GPR/FPR。 | `hw/rtl/core/VX_wctl_unit.sv`，module `VX_wctl_unit`，signal `wspawn_wmask`、interface fields `wspawn.wmask`/`wspawn.pc`；`hw/rtl/core/VX_scheduler.sv`，module `VX_scheduler`，signals `wspawn_valid`、`is_single_warp`、`thread_masks_n`、`warp_pcs_n`、`mscratch_r`。 | Core/Warp manager 路由目标 lifecycle/PC/mask effect，CSR owner 路由 mscratch effect；不得凭此猜测其他初始 context，见 U-WSPAWN-01。 |
| E-WSYNC-01 | `VX_wctl_unit` 在 WSYNC 遇到非空 `warp_pending_alm_empty` 条件时形成 `wsync_drain` 并压住 ready；drain 结束才发 `wsync_valid`。scheduler 收到该信号后解除 warp stall。 | `hw/rtl/core/VX_wctl_unit.sv`，module `VX_wctl_unit`，signals `warp_pending_alm_empty`、`wsync_drain`、`wsync_valid`、`execute_if.ready`; `hw/rtl/core/VX_scheduler.sv`，interface `warp_ctl_if.wsync_valid`。 | 功能模型只需要显式 architectural pending-work/order predicate 与 wait/release effect，不保存 pipeline drain 周期；精确可见性见 U-WSYNC-01。 |
| E-BAR-01 | `VX_wctl_unit` 解出 barrier id、size、event/sync/global/arrive/phase，并等待 `lsu_sched_drained` 后提交。`VX_bar_unit` 用 indexed stores 保存 `mask_r`/`count_r`/`events_r`/`phase_r` 状态并返回 unlock warp mask。冻结 `VX_CFG_NUM_CORES=1` 使 `USE_GBAR = (NUM_CORES > 1)` 为假，只走 core-local barrier 配置路径。 | `hw/rtl/core/VX_wctl_unit.sv`，module `VX_wctl_unit`，signals `bar_drain`/`lsu_sched_drained`/`wctl_bar_addr`、interface fields `bar.id`/`bar.size_m1`/`bar.is_event`/`bar.is_sync`/`bar.is_global`/`bar.is_arrive`/`bar.phase`；`hw/rtl/core/VX_bar_unit.sv`，module `VX_bar_unit`，localparam `USE_GBAR`、state `mask_r`/`count_r`/`events_r`/`phase_r`、instances `barrier_state_store`/`barrier_phase_store`；`hw/VX_config.vh` macro `VX_CFG_NUM_CORES`。 | Barrier coordinator 是 barrier record 候选 owner，Warp lifecycle 只保留 wait reason/key；物理 index 不足以冻结并发 CTA namespace，见 U-BAR-01。 |
| E-CSR-01 | `VX_csr_data` 的 `fcsr` 按 wid 保存并累积 FP flags；CTA CSR 数据来自 scheduler/dispatcher context。trap/mret 相关的 `mscratch_r`、`mstatus_r`、`mtvec_r`、`mepc_r`、`mcause_r`、`mtval_r` 及恢复 mask 物理存于 scheduler 并由 `sched_csr_if`/branch control 更新。`VX_csr_unit` 以 lane/wid/CTA context 生成 thread/hart/CTA identity 和 CSR RMW。 | `hw/rtl/core/VX_csr_data.sv`，module `VX_csr_data`，state `fcsr`、interfaces `fpu_csr_if`/`sched_csr_if`；`hw/rtl/core/VX_scheduler.sv`，上述 `*_r` state 与 `sched_csr_if`；`hw/rtl/core/VX_csr_unit.sv`，module `VX_csr_unit`，lane context 与 `sched_csr_if.cta_tid`/`cta_csrs`。 | 使用一个按明确 scope/key 寻址的 CSR owner，scheduler/warp 仅消费 view/effect；RTL 分散存储不授权重复 canonical state。ISA manifest/effect 已见第 5.5 节，owner apply/reset 集成仍见 U-CSR-01。 |
| E-CTA-01 CTA context | `VX_cta_dispatch` 的 `cta_ctx_ram`、`cta_warp_ram`、`cta_id_per_warp_r` 保存 CTA 描述、warp membership 与 readback；输出包含 CTA id/rank/size、block/grid、entry、param 和 LMEM address。每个 warp done 递减 remaining-warp state，最后一个产生 `cta_done` 并释放 slot。 | `hw/rtl/core/VX_cta_dispatch.sv`，module `VX_cta_dispatch`，state `cta_ctx_ram`/`cta_warp_ram`/`cta_id_per_warp_r`/`slot_valid_r`、signals `cta_rd_csrs`/`cta_rd_tid`/`warp_done`/`cta_done`。 | CTA manager 是 context/membership 候选 owner，Warp/Lane 只持 key 和只读派生 view；初始化与异常终止规则仍见 U-CTA-01/U-ABI-01。 |
| E-IFETCH-01 取指路由 | C 关闭时 scheduler PC 直接成为 I-cache request 地址；`VX_fetch` 在 response 上把 `icache_bus_if.rsp_data.data` 送入 `fetch_if.data.instr`，请求属性为 read。 | `hw/rtl/core/VX_fetch.sv`，module `VX_fetch`，interfaces `schedule_if`/`icache_bus_if`/`fetch_if`、signal `fetch_if.data.instr`，macro `VX_CFG_EXT_C_ENABLE`; `hw/VX_config.vh` macro `VX_CFG_ICACHE_ENABLE`。 | Instruction source 读取 Memory 中同一 canonical byte space，返回 word/fault；取指服务不能自行推进 Warp PC，cache 内容不是架构 owner。 |
| E-MEM-01 global/local data 路由 | `VX_lsu_slice` 用 `VX_MEM_LMEM_BASE_ADDR` 和 LMEM size 产生每 lane `is_addr_local`。`VX_lmem_switch` 按 lane memory attribute 分出 `local_mask`/`global_mask`，可把同一 warp 的 lane 子集分别送往 `local_out_if`/`global_out_if`。`VX_mem_unit` 将 local 路径接 `VX_local_mem`，global 路径接 D-cache/coalescing 路径，socket 再汇入外部 memory hierarchy。 | `hw/rtl/core/VX_lsu_slice.sv`，module `VX_lsu_slice`，`mem_req_attr_struct[].is_addr_local`、macro `VX_MEM_LMEM_BASE_ADDR`；`hw/rtl/mem/VX_lmem_switch.sv`，module `VX_lmem_switch`，`MEM_ATTR_LOCAL_OFFS`、`local_mask`/`global_mask`、ports `local_out_if`/`global_out_if`；`hw/rtl/core/VX_mem_unit.sv`，module `VX_mem_unit`，instances `VX_lmem_switch`/`VX_local_mem`；`hw/rtl/VX_socket.sv`；`hw/VX_config.vh` macros `VX_CFG_LMEM_ENABLE`/`VX_CFG_DCACHE_ENABLE`。 | Memory service 显式按地址与 lane 拆分 requests，global bytes 与 scope-keyed LMEM bytes各只保留一个可写真值；LMEM 的准确 scope、范围、fault 与 ordering 见 U-MEM-01。 |
| E-COMP-01 warp/CTA completion | scheduler 将 TMC 的 `tmc_valid && tmask == 0` 识别为 `cta_warp_done` 并发送 `warp_done`；dispatcher 对 remaining warps 计数，最后一个 warp 触发 `cta_done`。 | `hw/rtl/core/VX_scheduler.sv`，module `VX_scheduler`，signal `cta_warp_done`、interface fields `warp_ctl_if.tmc_valid`/`warp_ctl_if.tmc.tmask`，以及 instance `cta_dispatcher` 的直接端口连接 `.warp_done(cta_warp_done)`；`hw/rtl/core/VX_cta_dispatch.sv`，module `VX_cta_dispatch`，input `warp_done` 与 signal `cta_done`。 | Warp done 与 CTA done 分属各层唯一 lifecycle owner，并仅向上聚合；其他 termination/fault 路径不能从该单一路径猜出，见 U-CTA-01。 |
| E-KERNEL-01 kernel/device busy | `VX_kmu` 的 `running` 在 CTA launch 请求全部发出后即可撤销，因此它本身不是“全部 CTA 执行完成”；顶层 `Vortex` 的 `busy` 还聚合 `kmu_busy`、DCR request 和各 cluster busy。允许输入没有 host/checker 对该信号的完成判定协议。 | `hw/rtl/VX_kmu.sv`，module `VX_kmu`，signals `running`/`busy`/`raster_start_r` 与 CTA request accounting；`hw/rtl/Vortex.sv`，module `Vortex`，signals `kmu_busy`、`per_cluster_busy`、`dcr_bus_if.req_valid`、output `busy`。 | Device 可以聚合 CTA/Core progress，但不能把 KMU launch completion 当 kernel completion；公共 completion/host handshake 保持 U-ABI-01。 |
| E-LANE-01 WGATHER mask 例外 | `VX_alu_int` 对 WGATHER 覆盖 result header 的 `tmask` 为每个 4-lane group 的 `~wg_src_mask`；因此写目标是组内所有非 source lanes，并不受输入 active mask 的一般规则限制。 | `hw/rtl/core/VX_alu_int.sv`，module `VX_alu_int`，WGATHER 路径 signal `wg_src_mask` 与 field `alu_hdr_in.tmask`。 | WGATHER evaluator 必须产生 instruction-defined explicit write mask，register owner 按该 effect 应用；不能用统一 active-mask 过滤器静默删掉这些写。 |

## 10. `UNRESOLVED` 登记表

T0 不猜测以下事项。每项都记录当前已知 RTL 事实，避免把“未知”误写成“没有行为”；同时记录缺失输入、受影响边界和必须闭合的最晚阶段。

| ID | `UNRESOLVED` 问题 | 已知 RTL 事实 | 缺失证据 | 受影响边界 | 最晚闭合阶段 |
| --- | --- | --- | --- | --- | --- |
| U-FAULT-01 | typed integer instruction-address/load/store alignment/access fault 如何映射到 trap cause/CSR，多个 lane 或不同 fault 同时出现时优先级是什么？ | Integer evaluator 已确定 fault effect 边界：fault 时不返回 PC/memory/partial register apply；alignment 先于显式 bounds view；memory owner 可返回 bounds/service access fault。RTL default/`x` 仍不是 trap priority oracle。 | reference checker、完整 trap cause/priority oracle 被排除。 | Effect router、Memory/CSR service、Warp trap/PC owner。 | fault effect 与 Warp trap/CSR 模型验收前。 |
| U-CSR-01 | 已冻结的 CSR/trap effects 如何 reset，并与 T1 以外的异步硬件写来源仲裁？ | T1 已闭合严格地址/scope/RMW与 synchronous trap/xRET effects；T2/01 建立唯一 owner，T2/02 已闭合单个 T1 bundle 内 CSR/FFLAGS/trap 的原子 apply，T2/03 已闭合真实 State 上连续 CSR RMW→trap entry→xRET 的单指令连接子范围。软件 FCSR 写按 RTL 覆盖先前 sticky flags；RTL 同周期 async hardware trap 可覆盖 software CSR write。 | async RTU/fault router 的跨来源 transaction、reset/launch policy 尚未实现。 | CSR owner、Warp trap/lifecycle、future fault/RTU router。 | 异步 trap/fault 与 launch 集成验收前。 |
| U-LOWER-01 | compiler/runtime 如何生成并约束 SPLIT/JOIN、PRED/TMC、BAR、WSPAWN、WSYNC 序列，stack under/overflow 如何处理？ | T1 已闭合全部相关 encoding 与显式单指令 effects；T2/03 已闭合真实 State 上 TMC/PRED 和 SPLIT→JOIN mark→JOIN pop 的跨调用存续，并证明 BAR/WSPAWN 保真转交。 | `sw/`、compiler/runtime、Barrier/Core owner 与超出合法 effect sequence 的程序级 stack under/overflow policy 被排除。 | SIMT owner、Barrier、Warp lifecycle、runtime lowering。 | 首个 SIMT 程序验收前。 |
| U-WSPAWN-01 | WSPAWN 的 target 之外，初始 registers/CSR/CTA context 与软件使用协议的完整语义是什么？ | T1 effect 已冻结低编号非当前 target mask、lane0、PC、single-active-warp gate 和 mscratch copy；RTL/ISA均不复制 GPR/FPR。 | runtime lowering、目标 warp 预初始化与 CTA membership 协议缺失。 | Core/Warp manager、register/CSR state、CTA membership。 | WSPAWN/Warp lifecycle 集成验收前。 |
| U-WSYNC-01 | WSYNC 的 pending predicate 如何由 owner产生，并保证何种 memory visibility/order？ | T1 已冻结 consume显式 pending-work view并返回 wait/drain/release effect；RTL 在 warp pending ALM 非空时 drain，结束后 scheduler release。 | pipeline pending 集合到功能级 ordering 的最小映射、software convention 缺失。 | Warp executor/lifecycle、Memory ordering、Barrier integration。 | WSYNC 与多 warp memory/barrier 集成前。 |
| U-BAR-01 | 并发 CTA barrier namespace、ID 复用、event/phase、memory visibility 和异常退出释放是什么？ | T1 已冻结所有 barrier variant 的 id/size/phase/event/arrive/wait/LSU-drain request边界；`VX_bar_unit` 有 indexed mask/count/events/phase，冻结单 core不走 global path。 | 物理 index 不说明 concurrent CTA software namespace；coordinator apply、runtime/fault协议缺失。 | Barrier owner、CTA key、Warp wait reason、Memory ordering。 | Barrier/CTA 并发与 memory ordering 集成前。 |
| U-CTA-01 | CTA 分派、部分 warp coordinates、WSPAWN membership、正常/异常 warp 与 CTA termination 的完整规则是什么？ | dispatcher维护 CTA context/membership/remaining warps；TMC mask zero 是已见 warp-done 路径。 | runtime launch、fault/early-exit/partial-warp termination协议缺失。 | CTA manager、Warp lifecycle、Device completion。 | CTA/Warp lifecycle 阶段验收前。 |
| U-ABI-01 | Kernel image、startup PC/entry、arguments、初始 registers/CSR、host launch/completion ABI 是什么？ | KMU/dispatcher形成 launch/context请求，顶层暴露聚合 busy；KMU running仅代表 launch发送进度。T2 State 初始化因此要求 PC/mask/register/CSR/divergence 全部显式输入，不补 reset/launch 默认值。 | host/runtime/software 和 completion consumer 被排除。 | Loader、Kernel executor、Device lifecycle、公共 API。 | Kernel loader/runner 公共 API 冻结前。 |
| U-CHK-01 | checker 比较哪些 registers/CSRs/memory ranges，如何处理 FP、fault、console和未初始化状态？ | RTL 提供架构状态更新路径和顶层输出，但不定义 comparison contract。 | reference checker 被刻意排除。 | Snapshot/result、fault model、端到端验收。 | 端到端 differential harness 验收前。 |
| U-MEM-01 | global/LMEM 的最终地址范围与 scope、FENCE/BAR/WSYNC 的跨 warp 可见性及 self-modifying code 规则是什么？ | Integer LSU 已冻结自然对齐、typed request/response、可选 bounds view 和 FENCE ordering 边界；RTL 仍按 LMEM base/size 分 local/global lane masks并走不同 physical paths。 | cache/LSU wiring 不等于跨 scope 可见性 contract；软件/checker约定缺失。 | Instruction source、Memory spaces、Barrier/ordering owners。 | Memory space 与同步指令集成前。 |
| U-COUNT-01 | Counter owner 在非周期模型中如何推进显式 MCYCLE/MINSTRET view，checker 是否观察这些值？ | T1 已闭合 ISA read contract：44-bit Cycle/Instret explicit view 的 low/high halves；instruction MPM BASE windows 固定读零。 | State/execution 尚未提供推进 policy，checker visibility未知；cycle timing/performance仍是非目标。 | Counter owner、CSR service、snapshot。 | execution progress 与 checker contract 集成前。 |
| U-SCHED-01 | 多 warp 冲突 memory effects 需要何种最小确定性/ordering？ | RTL 可由 active/non-stalled warp调度并经 memory arbitration交错。 | software race policy和 checker observation未知。 | Runnable selection、Memory ordering、deterministic harness。 | 多 warp memory integration 前。 |

## 11. `RESOLVED` 审计记录

| 原 ID/闭合范围 | 决定 | RTL/实现证据 | 验证 | 闭合 Task |
| --- | --- | --- | --- | --- |
| U-ISA-01 / encoding 与 decoder | 冻结 decoder 采用显式 allowlist；D/FLEN64 泄漏和所有 disabled/reserved encoding 均为 illegal，不传播 RTL 的 `x`。fault effect/priority 分离为 U-FAULT-01。 | `VX_config.toml`；`hw/VX_config.vh`；`hw/rtl/core/VX_decode.sv:93-157,208-751`；`hw/rtl/VX_gpu_pkg.sv:249-533`；`isa/catalog.go`、`isa/decode.go`。 | `isa/decode_test.go` 对每个 entry 做正向可达验证，以字段约束 SAT 检查逐对不重叠，并覆盖 format/rm/width/reserved/disabled 反例；`scripts/verify.sh`。 | T1 milestone `01-catalog-decode` |
| U-FAULT-01 / integer effect boundary | alignment、explicit bounds 和 memory-service failures 已成为 typed、无 mutation 的 fault effects；fault outcome 不携带 PC 或 partial register write。trap cause/CSR/跨 fault 优先级仍保留在 U-FAULT-01。 | 上述 ALU/LSU RTL；`isa/effects.go`、`isa/integer.go`。 | `TestBranchMisalignmentOnlyWhenTaken`、`TestMemoryInactiveAlignmentBoundsAndAddressWrap`、`TestMemoryOwnerIntegrationAndNoISAMutation`、`TestMemoryCompletionSuppressesPartialWriteOnFault`。 | T1 milestone `02-integer-memory` |
| U-MEM-01 / integer request-response boundary | integer load/store 的 address/width/signedness/byte mask、自然对齐、bounds view、load extension 和 store owner boundary 已闭合；global/LMEM scope与跨 warp ordering仍留在 U-MEM-01。 | `VX_lsu_agu.sv`、`VX_lsu_slice.sv`；`isa.MemoryRequest`/`MemoryResponse`。 | 每条 LB/LBU/LH/LHU/LW/SB/SH/SW functional case，加上真实 `support/memory` owner integration test；`scripts/verify.sh`。 | T1 milestone `02-integer-memory` |
| U-FP-01 / RV32F 单指令与 FCSR effect | 冻结 S-format 的 raw-bit 数值、五种静态/dynamic rounding、NaN/min-max/compare/class、conversion saturation、active-lane flags OR、sticky FFLAGS 和 floating memory 边界；长期 FCSR 仍由外部 owner 唯一持有。端到端 checker observation 归 U-CHK-01，不再阻塞单指令语义。 | 上述 FPU RTL；`support/softfloat`；`isa/float.go`、`isa/effects.go`。 | `isa/float_test.go` 对每条 RV32F catalog 指令做 functional case，并覆盖 ±0/subnormal/∞/qNaN/sNaN、五类 exception、五种 static/dynamic rm、fused-vs-nonfused、conversion saturation、十类 FCLASS、memory owner/fault；`scripts/verify.sh`。 | T1 milestone `03-rv32f` |
| U-CSR-01 / ISA 地址、RMW 与 trap-return | CSR manifest、读写/ignored mask、lane/warp/CTA scope、old-value result、unknown/readonly reject、ECALL/EBREAK cause/CSR writes/mask save 和统一 xRET redirect/restore 已闭合；不构造 privilege engine 或 canonical state。 | 上述 CSR/System RTL；`isa/csr_catalog.go`、`isa/system.go`、`isa/effects.go`。 | `isa/system_test.go` 覆盖 manifest 全地址、六种 RMW、identity/CTA/FCSR/config、trap causes、三类 return 与非法无 partial effect；`scripts/verify.sh`。 | T1 milestone `04-csr-system` |
| U-COUNT-01 / ISA counter read | MCYCLE/MINSTRET 被定义为 owner 提供的显式 44-bit view，low/high read 精确截断；instruction-originated MPM BASE windows 固定零。计数推进与 checker 可观察性保留在 U-COUNT-01。 | `VX_gpu_pkg.sv:83`；`VX_csr_unit.sv:75-78`；`VX_csr_data.sv:21-27,236-263`；`isa.CounterView`。 | low/high 极值输入、MPM window 首尾地址与 unknown aliases 测试；`scripts/verify.sh`。 | T1 milestone `04-csr-system` |
| U-LOWER-01 / custom 单指令语义 | TMC/PRED、SPLIT/JOIN、所有 cross-lane op 与 packed load 的四 lane单指令方程和 typed effects已闭合；compiler/runtime序列和 stack capacity policy仍保留在 U-LOWER-01。 | 上述 wctl/IPDOM/ALU/pack-load RTL；`isa/custom.go`、`isa/effects.go`。 | 全 custom catalog functional case，active/partial/empty masks、split uniform/divergent/negate、四 source WGATHER、shuffle fallback、packed assembly/fault；`scripts/verify.sh`。 | T1 milestone `05-vortex-custom` |
| U-WSPAWN-01 / target 与 copy boundary | 冻结 target=`i<count && i!=wid`、PC、lane0 mask、single-active-warp apply条件及仅 mscratch copy；不创建/schedule warp且不猜 GPR/FPR/CTA context。 | `VX_wctl_unit.sv`、`VX_scheduler.sv`；`WarpSpawnEffect`。 | count 0/1/4/7、current-warp suppression、PC与 mscratch tests。 | T1 milestone `05-vortex-custom` |
| U-WSYNC-01 / 单指令 drain boundary | WSYNC消费显式 pending predicate并返回 typed wait/drain/release，不保存 pending pipeline/cycles；visibility policy继续保留。 | `VX_wctl_unit.sv`、`VX_scheduler.sv`；`WarpDrainEffect`。 | pending与drained两种 view tests。 | T1 milestone `05-vortex-custom` |
| U-BAR-01 / 单指令 request boundary | sync/async arrive/wait、expect-event、phase result与 LSU drain effect已闭合；expect_tx 强制 phase=1 并将零 count 解释为 32，barrier record、CTA namespace与release coordination继续保留。 | `VX_wctl_unit.sv`、`VX_bar_unit.sv`；`BarrierEffect`。 | sync/arrive/wait/expect_tx（含零值与偶数 count）及 phase/write-mask tests。 | T1 milestone `05-vortex-custom` |
| T1 ISA / 最终 coverage-contract | 105 条 frozen-enabled catalog entry、decode、四类 functional evaluator 与独立登记 vector 一一对应；公开 API 保持无长期状态和单指令边界，未来 canonical owners 与执行协调未被伪装闭合。 | `isa/catalog.go`、`isa/coverage_test.go`、`isa/contract_test.go` 及四类 evaluator/effect 实现。 | 三项最终 gate、全部逐指令 functional tests、`scripts/verify.sh`；外部 RTL worktree 只读且 clean。 | T1 milestone `06-coverage-contract` |
| T2 State/View / canonical ownership | Lane GPR/FPR、warp PC/mask/lifecycle、FCSR/trap CSR/saved mask 与三行 IPDOM stack 已由一个 `WarpState` 长期持有；四类 T1 input 来自 detached snapshot，非 T2 owner context 只按次复制。effect apply/route 另行闭合。 | `VX_gpu_pkg.sv:80-81`、`VX_ipdom_stack.sv`、`VX_scheduler.sv:53-64,245-255,311-398`、`VX_csr_data.sv:91-135`；`state/state.go`、`state/view.go`。 | `state/state_test.go` 覆盖初始化拒绝、namespace/x0/inactive lane、PC/mask/CSR/FCSR、divergence、snapshot/context alias isolation；`scripts/verify.sh`。 | T2 milestone `01-canonical-state-views` |
| T2 effect apply / 本地原子边界 | T1 Lane/Warp bundle 在 detached candidate 上完整预校验；本地状态一次提交，FFLAGS/软件 FCSR 按 RTL 顺序仲裁；future owner effects 保真转交并在外部成功前门控本地 commit，stale stage 不可提交。异步 trap/reset priority 仍留在 U-CSR-01。 | `VX_csr_data.sv:107-120`、`VX_scheduler.sv:245-255,358-398`、`VX_ipdom_stack.sv:39-95`；`state/apply.go`。 | `state/apply_test.go` 覆盖 GPR/FPR+PC、CSR/flags、TMC/PRED、SPLIT/JOIN、trap、x0/inactive/WGATHER、失败回滚、全类 forwarding/external gate/stale stage；`scripts/verify.sh`。 | T2 milestone `02-atomic-effect-apply` |
| U-CSR-01 / T2 synchronous State integration | canonical FCSR、T1 可写 warp CSR、saved mask 与 PC 已接入无状态 System/RV32F evaluator；CSR RMW、sticky FFLAGS、synchronous trap entry 与 xRET 可跨独立单指令调用持续并原子提交。异步 fault/RTU 仲裁及 reset/launch policy仍保留在 U-CSR-01。 | E-CSR-01；`state/state.go`、`state/view.go`、`state/apply.go`、`state/integration.go`。 | `TestExecuteSingleRV32FUpdatesFPRFlagsAndPC`、`TestExecuteSingleCSRRMWTrapEntryAndReturn`、fault completion rollback；`scripts/verify.sh`。 | T2 milestone `03-isa-state-integration-contract` |
| U-LOWER-01 / T2 local SIMT State integration | TMC/PRED active mask 与 SPLIT/JOIN 三行 IPDOM state 已接入单指令 View/Evaluate/Apply；合法 SPLIT→JOIN mark→JOIN pop 跨调用保持 record、pointer、mask 与 PC。BAR/WSPAWN 只转交未来 owner，不虚构 runtime lowering 或 lifecycle coordination。 | E-SIMT-01、E-WSPAWN-01、E-BAR-01；`state/integration.go`。 | `TestExecuteSingleTMCAndPredicateMasks`、`TestExecuteSingleSplitAndTwoJoinCallsPersistDivergence`、`TestExecuteSinglePreservesFutureOwnerEffectsWithoutLocalMutation`；`scripts/verify.sh`。 | T2 milestone `03-isa-state-integration-contract` |
| T2 ISA→State / 单指令连接边界 | caller-supplied word 经过 Decode/View/T1 Evaluate/atomic Apply；memory responses 经 T1 completion helper进入同一 apply。State 跨调用持久、ISA 无状态，illegal/fault 无 partial mutation，future owner effects 不执行或丢失；不建立 fetch loop、Warp.Step 或 scheduler。 | `state/integration.go`；既有 `isa` 单指令 API 与 T2 State/View/apply。 | `state/integration_test.go` 覆盖 ALU/FP/branch/jump/SIMT/CSR/trap/memory及 owner isolation/rollback/forwarding；`scripts/verify.sh`。 | T2 milestone `03-isa-state-integration-contract` |
| T3 single-lane fetch/Step core | canonical PC 经共享 InstructionSource 精确读取 4-byte little-endian word，复用 Decode、detached State view/T1 evaluator及 T2 stage/commit完成 non-memory single-lane instruction；typed outcome区分 retired/trap/fault/deferred/finished，非法 multi-lane 与 future-owner/custom effect均不产生本步 partial commit。 | E-IFETCH-01、E-PC-01；`warp/warp.go`、`state/integration.go`。 | `warp/warp_test.go` 覆盖 fetch、PC/dependency、branch/jump、M/Zicond/FP/CSR/trap、fault/rollback、finished与 deferred；`scripts/verify.sh`。 | T3 milestone `01-fetch-step-core` |
| T3 atomic memory Step | 同一 MemoryService提供 fetch/data canonical bytes；integer/FP/packed request经 T1 completion与T2 stage提交，store由 pre-stale-check→单次all-or-error Write→infallible state replacement协调 memory与PC，FENCE在同步 owner下显式完成。fault不猜测 trap mapping，flat adapter不冒充最终 memory architecture。 | E-IFETCH-01、E-MEM-01；`warp/warp.go`、`state/apply.go`、`state/integration.go`、`support/memory/memory.go`。 | `warp/memory_test.go` 覆盖宽度/符号、store→load→ALU、FLW→FP→FSW、packed、shared bytes、misalign/bounds/service fault、stale pre-write rejection、单次 Write及FENCE；`state/apply_test.go` 覆盖 coordinated success/failure/stale；`scripts/verify.sh`。 | T3 milestone `02-atomic-memory-step` |
| T3 bounded Run/trace contract | Run仅重复Step且不保存PC；显式budget确定性终止retired loop，其他Step outcome提前停止；finished仅来自canonical inactive。可选trace按step返回detached WarpID/PC/raw/decode/effect/nextPC/outcome，不改变执行。 | E-PC-01；`warp/run.go`及第5.11-5.12既有边界。 | `warp/run_test.go` 覆盖zero/exact/early budget、self-loop、全类别stream、fault/finished及trace完整性/隔离；全部T1/T2/T3 tests、vet、gofmt与`git diff --check`。 | T3 milestone `03-run-trace-contract` |

## 12. T0 最终覆盖与范围审计

### 12.1 八类交付要求覆盖

| T0 要求类别 | Contract 对应章节 | 审计结论 |
| --- | --- | --- |
| 1. 模拟器层级、模块与单向依赖 | 第 4.1 节 | Device → Core → CTA → Warp → Lane → ISA 主链及 Memory/CSR/Barrier 服务挂靠已定义，具体代码组织保持 `PROVISIONAL`。 |
| 2. 跨指令长期状态与唯一 owner | 第 4.2 节 | GPR/FPR、PC/mask、lifecycle、SIMT、context、CSR、barrier、memory、completion 均已登记；证据不足者是 `UNRESOLVED owner candidate`。 |
| 3. ISA decode/evaluate 边界 | 第 5.1、5.7、5.10–5.13 节 | ISA 只读最小 view、产生显式 effect，不持有长期状态或调度上层；最终自动门禁要求 105 条 catalog/decode/evaluator/vector 一一对应，T2/T3 连接证明真实 State 只经该边界调用 ISA。 |
| 4. 指令类别、effect 与 control owner | 第 5.2 节 | 标准指令、System、全部冻结 custom SIMT/lane 指令、fetch 与 completion 已映射。 |
| 5. 功能执行流程 | 第 6 节 | select runnable warp → fetch → decode → read view → evaluate → effect → owner update 已分责。 |
| 6. 后续公共接口职责 | 第 7 节 | decoder/evaluator/effect 已由T1冻结，T2已冻结State/View/apply，T3已冻结single-lane fetch/memory Step/bounded Run/trace；scheduler、Barrier/CTA/Kernel及最终memory scopes仍保持`PROVISIONAL`。 |
| 7. RTL 调查、证据与待决问题 | 第 9、10、11 节 | 可确认事实有 path/module/signal/macro 依据，设计推导分栏；未知项有事实、缺口、影响边界和 deadline；T0 不伪造 `RESOLVED`。 |
| 8. 功能范围与非目标 | 第 3、8 节 | 冻结配置/ISA 范围与 cycle/pipeline/cache timing/hazard/throughput/performance scheduling 等非目标明确分离。 |

### 12.2 治理与过度设计审计

- 状态标签及迁移门槛在第 2 节；`FROZEN` 事实、`PROVISIONAL` 设计、`UNRESOLVED` 缺口和未来 `RESOLVED` 记录没有互相冒充。
- 唯一 canonical owner 原则在第 4.2 节；control/effect 只能经第 5 节边界交给 owner，不允许反向依赖、全局后门或第二可写真值。
- 维护协议在第 1、13 节；后续任务开始前读取、结束后与代码同步更新。
- 第 5.1、8.2 节明确不预定 Go concrete types、transaction/rollback/commit 算法、调度策略或微架构行为，满足无过度设计要求。
- 原始 T0 基线交付仅修改本文件；后续 T1 已新增无长期状态的ISA decoder/evaluators，T2已新增canonical State/View、原子effect stage/future-owner forwarding及caller-supplied-word连接。T3新增了不持有第二份状态/bytes的single-lane fetch、同步memory Step、跨owner coordinated commit、bounded Run与detached trace；Warp scheduler、CoreState/CTAState、CTA/Barrier coordinator、真实custom/SIMT、最终global/LMEM architecture、Kernel/host completion及cache/MMU/pipeline/cycle/performance model仍未新增。外部`Vortex_rtl`始终作为只读输入，不属于交付内容。

## 13. 维护检查单

每个后续 Task 完成前必须确认：

- 是否读过本 Contract，且实现未违反任何 `FROZEN` 项；
- 是否发现新的 RTL 事实、`UNRESOLVED` 问题或已可闭合的条目；
- 是否改变 canonical state ownership、模块依赖、公共接口或 effect/control 边界；
- 是否把实现验证得到的设计从 `PROVISIONAL` 正确升级，或在失败时回退；
- 是否只模拟架构功能语义，没有把 pipeline/cache/hazard/timing 偶然带入正确性契约；
- 文档与代码是否在同一 Task 同步更新。
