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

`FROZEN`：模拟器语义分析的唯一 RTL reference 是仓库内只读目录 `Vortex_rtl` 中的配置、生成头文件和 RTL。具体基线为：

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
| barrier | 8 barrier IDs，`MAX_BAR_EVENTS=32` | T6功能owner以sealed CTA + RTL AddressWarp/ID隔离local records；global path、memory visibility与异常teardown仍见待决项。 |
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
| Runtime / Input adapter | 解析模块/符号和host argument、把image/parameter/input staging到device memory，并把硬件可见launch字段交给Device；导出最终可比较结果。 | Device公共入口；不传host struct或DCR transport，不直接改Core/Warp私有状态。 |
| Device / Kernel executor | 校验并持有runtime无关launch state，产生CTA并聚合下层完成；只引用调用方提供的global memory，不加载image、不重新staging参数、不复制结果。 | Cluster/Core/CTA、Memory和launch接口。 |
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
| global/device memory bytes | Caller-provided Device address space | `FROZEN（T7/kernel-execution-engine）`: 调用方提供的同一`device.BackingMemory`对象同时服务image/fetch、parameter/arguments、所有非LMEM global load/store与最终output；Device/Kernel只保存接口引用，不清空、复制或替换bytes | `KernelExecutor`把exact object同时传给四个Warp instruction source和四个`CTAMemory` global route；result/trace无memory snapshot，正常结束后caller直接读取原owner。 |
| Kernel launch描述与CTA generation progress | Device/launch | `FROZEN（T7/launch-contract）`: `device.LaunchState`保存KMU分别消费的startup PC、kernel entry、parameter address、grid/block dimensions、block size、warp step、LMEM size与cluster dimensions；`GridWalker`唯一持有origin/intra/emitted生成进度 | `VX_types.toml:dcr_kmu`与`VX_kmu.sv:41-173,202-313`；runtime DCR transport/host struct不进入公共契约。派生字段由一次完整validation计算，不成为caller第二真值。 |
| CTA context：CTA id/rank/size、block/grid dimensions/index、entry、param、LMEM allocation | CTA | `FROZEN（T6静态 + T7/reusable-cta-lifecycle动态）`: `core.CTAManager` 唯一持有 resident CTA metadata、rank/member records 与 allocation descriptor；`isa.CTAView` 只按选中 Warp 派生复制 | 静态manager保持attach前admission/seal；dynamic manager只接受`Core.AdmitCTA`协调的完整context，entry与parameter分别保留，失败不安装record。 |
| warp membership 与 warp→CTA link | CTA/Core | `FROZEN（T6静态 + T7动态）`: CTA manager 的 member records 与私有 warp→CTA key table由同一 admission/reclaim transaction维护；Core只持scheduler slot，Warp不复制membership | Dynamic admission从inactive且未participated slot选wid并与canonical WarpState launch一起提交；reclaim同步删除双向membership，失败不改变任一owner。 |
| Lane/thread/warp/CTA identity 与 thread coordinates | Lane view over CTA/Warp | `FROZEN（T2/01-canonical-state-views）`: Warp id 与四个 lane id 在 `WarpState` 初始化时校验；core/CTA identity 与 thread coordinates 只在 `ReadContext` 中按次传入并复制到 snapshot | `VX_csr_unit.sv` 从 wid/lane/CTA tables形成 thread/hart/CTA CSR；`emu/state/view.go` 不长期保存 CTA/Core context。 |
| warp PC、active lane mask | Warp | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 是唯一 owner | `VX_scheduler.sv` 的 warp_pcs/thread_masks；`state.WarpSnapshot` 只复制值，ISA input 不含 owner 引用。 |
| warp active、blocked/wait reason、runnable、termination | Warp/Core scheduling scope | `FROZEN（T2/01-canonical-state-views）` running/inactive lifecycle 与 lane mask 由 `WarpState` 一致持有；`FROZEN（T5/01-core-warp-manager）` Core 只持 inactive/runnable/blocked/finished、typed block reason 与 participated 调度事实，且每次观察/转换均校验其与 canonical lifecycle/mask 一致 | `active_warps`、`stalled_warps`、ready/control/retire 路径证明架构 active 与调度 eligibility 分层；Core slot 不复制 PC、register、mask、CSR 或 divergence。 |
| GPR x0..x31（每 lane） | Warp × Lane | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 的每 lane 独立 GPR namespace | `emu/state/state.go` 使用私有 `[4][32]uint32` 等价布局；所有 read/view 返回值副本，State 层保证 x0 恒零。 |
| FPR f0..f31（每 lane） | Warp × Lane | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 的每 lane 独立 FPR namespace | F decode/read/writeback 与 `state` namespace 定向测试；Lane view 不复制出第二份长期数组。 |
| FFLAGS/FRM/FCSR | Warp | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 唯一持有八位 FCSR；`FROZEN（T2/02-atomic-effect-apply）`：本地事务先 sticky OR FFLAGS，后应用软件 FFLAGS/FRM/FCSR 写，故软件写按 RTL 覆盖重叠字段 | `VX_csr_data.sv` 的 per-warp fcsr/fcsr_n 顺序；snapshot 保留 raw FCSR，FloatInput 将 raw FRM 0..4 映射到 T1 enum。异步硬件来源/reset 仍见 U-CSR-01。 |
| trap/return CSR 与 mscratch：mstatus、mtvec、mepc、mcause、mtval、恢复 mask 等 | Warp | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 唯一持有 T1 已确认可写的六个 32-bit CSR 与 saved thread mask；`FROZEN（T2/02-atomic-effect-apply）`：T1 CSR/trap bundle 经 old-value/mask/scope/一致性预校验后与 PC/mask 一次提交 | RTL 虽物理分散在 scheduler/CSR data，功能模型没有第二份 CTA/counter/core state；异步 trap 与 reset/launch priority仍见 U-CSR-01。 |
| divergence/reconvergence stack、stack pointer | Warp | `FROZEN（T2/01-canonical-state-views）`: `WarpState` 唯一持有三行 record 与 0..3 write pointer | `DV_STACK_SIZE=UP(NUM_THREADS-1)=3`、`DV_STACK_SIZEW=LOG2UP(3)=2`；初始化要求 `[0, writePointer)` 完整 live prefix，JOIN view只复制 decoded rs1 指向的一行。跨指令 under/overflow policy仍见 U-LOWER-01。 |
| barrier mask/count/event/phase；warp barrier wait relation | CTA + AddressWarp + barrier ID | `FROZEN（T6/cta-barrier-coordination 功能子范围）`: `BarrierCoordinator`唯一拥有arrival/wait masks、participant count、event count和phase；Core slot只保存匹配canonical waiter的stable key或尚未提交request的LSU-drain标记 | `VX_wctl_unit.sv`给出AddressWarp/3-bit ID和LSU drain边界，`VX_bar_unit.sv`给出mask/count/events/phase及unlock；冻结single-Core以CTA owner namespace隔离resident复用，memory visibility/异常退出仍见U-BAR-01。 |
| local/shared memory bytes（冻结 RTL 名称 LMEM）及 allocation | CTA/Core address scope | `FROZEN（T6 + T7/reusable-cta-lifecycle）`: `CTAManager` 的单一 16 KiB byte array是LMEM byte owner并同时持CTA-visible allocation descriptor与私有backing offset；`CTAMemory`以warp→CTA key翻译，不保存bytes或offset副本 | 64-byte aligned first-fit只占用resident extents；reclaim后extent可复用且不与仍resident CTA别名。残留byte不声明清零，也绝不并入调用方global backing。 |
| 架构可读 counters | Core/Device | `PROVISIONAL`: Counter owner 构造显式 44-bit `CounterView` | T1 已冻结 MCYCLE/MINSTRET low/high read 和 instruction-originated MPM 零窗口；非周期计数策略与 checker observation 仍见 U-COUNT-01。 |
| warp、CTA、Device 完成状态 | 各 lifecycle scope | `FROZEN（T7/kernel-execution-engine）`: Kernel complete同时要求walker exhausted、无pending cluster、无resident CTA、CTA/barrier records为空且四个Core/Warp slots inactive/unparticipated/zero-mask；blocked/deferred/trap/fault/budget均`Complete=false` | `VX_kmu.running`只覆盖请求生成而`Vortex.busy`还聚合cluster busy。当前MemoryService为同步all-or-error接口，fetch/load/store/batch在Step返回前已完成，故不存在另造的异步queue pending；host handshake仍见U-ABI-01。 |

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
| WSPAWN | spawning warp operands/context | target warp activation、PC/mask；RTL 明确复制 `mscratch` | `FROZEN（T5/02 子范围）`: Core协调source stage与inactive target owners一次提交PC/lane0/lifecycle/mscratch；其他 context 见 U-WSPAWN-01 |
| SPLIT | per-lane predicate、PC、mask | divergence push、selected active mask、next-PC | Warp/SIMT divergence owner |
| JOIN | current mask、stack pointer/reconvergence view | divergence pop、reconverged mask/PC | Warp/SIMT divergence owner |
| BAR | barrier id/size/phase、CTA/core/warp identity | arrive/wait/event update、Warp block/release、可能的 memory-order effect | Barrier coordinator + Warp lifecycle owner；scope/visibility 见 U-BAR-01 |
| WSYNC | warp pending architectural work | ordering/wait result、Warp block/release；不产生 drain cycles | `FROZEN（T5/02 子范围）`: Core消费显式pending provider/view并block/retry；不保存pipeline state、不补memory visibility |
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

直接 RTL 证据为 `hw/rtl/core/VX_wctl_unit.sv:42-258`、`VX_split_join.sv:38-100`、`VX_ipdom_stack.sv:44-119`、`VX_scheduler.sv:202-241,325-349,405-445`、`VX_bar_unit.sv:48-149`、`VX_alu_int.sv:136-223,255-286`、`VX_uop_packld.sv:16-57`、`VX_lsu_agu.sv:16-53` 与 `VX_commit.sv:90-109`；实现和验证证据为 `isa/custom.go`、`isa/effects.go`、`isa/custom_test.go` 和 `scripts/verify-emu.sh`。

### 5.7 T1 最终覆盖与无状态 API 审计

`FROZEN（T1/06-coverage-contract）`：最终 catalog 固定为 105 条 frozen-enabled instruction entry：RV32I 37 条、FENCE 1 条、RV32M 8 条、Zicond 2 条、System/CSR 11 条、RV32F 26 条、Vortex custom/SIMT 20 条。`isa/coverage_test.go` 维护一份不从 catalog 派生的 105 条 functional-vector registry；最终门禁对每个名字同时验证 catalog example 可达、functional word 可达且仍解码为同名指令、登记的 `EvaluateInteger`/`EvaluateFloat`/`EvaluateSystem`/`EvaluateCustom` 能产生非空 result/effect，并反向拒绝无 catalog entry 的孤立 vector。新增、删除或改名任一 catalog entry 而未同步 functional vector/evaluator 时测试确定性失败；各 evaluator 的逐指令数值、mask、fault 与边界结果仍由对应 `*_test.go` 的定向 vectors 验证。

`FROZEN` strict-illegal 门禁由 `isa/contract_test.go` 与 `isa/decode_test.go` 共同组成。类别门禁显式登记 reserved opcode、关闭的 C/RV64/D/A/vector/accelerator、reserved funct/format/rm/register field 及 RTL 宽松 default 容易泄漏的 fixed-field holes，并要求统一返回保留原 word 的 `IllegalInstructionError`；详细反例继续覆盖 width、FP conversion rs2、System reserved immediate、custom rd/rs2 和关闭单元 funct 编码。该门禁不把 RTL 的 `x`/default 当成合法性授权。

`FROZEN` 公开 API 审计解析所有非测试 `isa/*.go`，要求生产 package 只暴露 catalog/CSR catalog、严格 decode、四类单指令 evaluator 及 integer/float/packed-memory completion；不允许 `init` side effect、额外 orchestration operation 或 ISA 外依赖。package-level variables 只能存在于 `catalog.go`/`csr_catalog.go` 的不可导出 manifests；不得定义 canonical `State`/`Machine`/`Device`/`Core`/`Warp`/`Scheduler`/`Executor`/`Kernel`/`Cache`/`MMU` owner，也不得出现 fetch loop、`Warp.Step`、schedule/dispatch/launch/tick API。因此 ISA package 只消费调用方提供的 immutable views，不跨指令拥有 PC、GPR/FPR、CSR、memory、warp、barrier 或 CTA 状态。

最终离线证据为 `TestCatalogDecodeEvaluatorVectorCoverageGate`、`TestFinalIllegalEncodingClassGate`、`TestISAPublicSurfaceIsStatelessAndSingleInstruction` 与所有既有 functional tests，并统一由 `scripts/verify-emu.sh` 执行 build/test/vet/gofmt/diff 门禁。仓库内 `Vortex_rtl` snapshot 仅作只读审计，来源提交为 `85a88fe250b0da483cb33ed34126d675ecb93c1c`。上述结论只闭合 T1 ISA 单指令层，canonical owner apply、Warp/Barrier/CTA 协调、memory scope、counter progression 与 checker/ABI 仍按第 10 节保持 `UNRESOLVED`。

### 5.8 T2 canonical State 与只读 ISA View 实证

`FROZEN（T2/01-canonical-state-views）`：`state.WarpState` 已成为一条 warp 的 lane/warp 范围长期真值 owner。初始化必须显式提供并校验冻结拓扑（4 lanes、4 warps、1 core、每 namespace 32 registers、IPDOM depth 3）、warp id、四个不重复 lane id、四字节对齐 PC、active mask 与 running/inactive lifecycle 一致性、saved thread mask、八位 FCSR、六个 T1 可写 trap/system CSR、每 lane GPR/FPR，以及完整 divergence live prefix。U-ABI-01 尚未决定的 startup 值没有 reset/launch 默认值；调用者必须明确给出。x0 非零初始化被拒绝，此后读取恒零且写入为 no-op。

`FROZEN（T2/01-canonical-state-views）`：`WarpSnapshot` 是 owner 字段、lane arrays 和 divergence records 的 detached value copy，字段私有，accessor 再返回数组/record 值。`IntegerInput`、`FloatInput`、`SystemInput`、`CustomInput` builder 按 `Decoded.Sources` 的 register namespace/index 读取 canonical snapshot；inactive lane 读取不改状态，写 mask 不在 read path 暗中与 active mask 求交。JOIN 只取得控制 lane rs1 指向的单行 `DivergenceRecordView`，不暴露 stack；FCSR raw bits 同时进入 System view，FRM 经显式 encoding 映射进入 Float view。

`FROZEN（T2/01-canonical-state-views）`：Core id/active-warps、CTA view、44-bit counters、memory bounds、barrier phase、pending prior work 与 pending LSU 均只存在于按调用提供的 `state.ReadContext`。builder 校验并复制所需值（包括对 `AddressBounds` 重新分配副本），`WarpState`/`WarpSnapshot` 从不保存 context 或其指针。因此后续 Core/CTA/Memory/Barrier owner 无需从 Warp 收回重复真值。实现与定向验证位于 `emu/state/state.go`、`emu/state/view.go`、`emu/state/state_test.go`；该里程碑当时保留的 staged apply/future-owner routing 已由第 5.9 节闭合。

### 5.9 T2 原子 effect apply 与 future-owner 路由实证

`FROZEN（T2/02-atomic-effect-apply）`：`state.StageEffects` 在 canonical mutation 前复制完整 `WarpState`，并只在候选副本上处理 bundle。它预校验 register namespace/index/mask 与重复 destination、所有携带的 warp identity、Control 的 current-PC/next-PC/taken/target/decision-lane 关系、CSR catalog address/scope/warp/old-value/write-mask/ignored metadata、trap entry/return 的 PC/mask/精确 CSR write 组合（TrapEnter 的 Vector、Control NextPC/Target 必须共同等于 canonical `MTVec &^ 3`），以及 SPLIT/JOIN push/mark/pop 的 pointer、capacity、record、mask partition 和配套 Control/WarpMask。任何错误都丢弃候选；canonical GPR/FPR、PC、mask/lifecycle、CSR、saved mask 和 IPDOM records 保持逐位不变。

`FROZEN（T2/02-atomic-effect-apply）`：成功的本地 stage 一次替换 canonical owner，masked GPR/FPR write 不与 active mask 二次求交，因而普通 effect 只改其显式 Mask、WGATHER 的 non-source mask 仍可写 input inactive lanes；x0 effect 在 owner 再次成为 no-op。TMC/PRED 同步更新 mask/lifecycle，SPLIT/JOIN 同步更新 stack/mask/PC，trap entry 同步写 MEPC/MCAUSE/MTVAL、saved mask和PC，xRET按非零 saved mask恢复 lifecycle/mask。FFLAGS 先 sticky OR，随后同一 bundle 的软件 FFLAGS/FRM/FCSR 写覆盖其实现字段，直接对应 `VX_csr_data.sv` 的 `fcsr_n` 组合顺序。

`FROZEN（T2/02-atomic-effect-apply）`：MemoryRequest、PackedLoadRequest、Ordering、WarpSpawn、WarpDrain、Barrier、Fault、全部 CSRRead（包括 Core/CTA scope）及非本地 CSRWrite 都以 detached `ForwardedEffects` 保留，Warp owner 不标记为已消费。只要存在需未来 owner 成功的 effect，`ApplyEffects` 返回 `Pending EffectStage` 且不修改本地状态；普通 `Commit` 被拒绝，协调者只能在外部成功后显式调用 `CommitAfterExternal`。stage 保存完整 before image，期间任一 canonical 修改都会令提交确定性 stale-fail，阻止跨 owner partial commit。返回的 forwarded bundle 另作深拷贝，调用者不能反向篡改 stage。实现和验证证据为 `emu/state/apply.go`、`emu/state/apply_test.go` 及 `scripts/verify-emu.sh`。

### 5.10 T2 ISA→State 单指令连接与集成实证

`FROZEN（T2/03-isa-state-integration-contract）`：`state.ExecuteSingle` 是一条由调用者提供 word 的 Decode → detached `WarpSnapshot`/最小 `ReadContext` View → 既有 T1 evaluator → `ApplyEffects` 连接边界。它只按 decoded category 分派 `EvaluateInteger`、`EvaluateFloat`、`EvaluateSystem` 或 `EvaluateCustom`，返回 decoded operation、detached effects 和 apply/forwarding 结果；不读取 instruction memory，不循环，不选择 warp，不保存 evaluator 状态，也不提供 fetch、`Warp.Step`、scheduler、dispatch、launch 或 tick API。连续 ADD→ADDI、branch→JAL、SPLIT→JOIN→JOIN 及 trap→xRET 调用证明跨调用事实只持续在 `WarpState`，两个独立 owner 用同一 instruction word 得到各自结果，`isa` 仍是无长期状态的叶子。

`FROZEN（T2/03-isa-state-integration-contract）`：`state.CompleteMemoryAndApply` 只把调用者已取得的 decoded memory operation、expected lane mask 和 future Memory owner responses交给 T1 completion helper，再走同一原子 apply；它不拥有 memory bytes、pending request 或服务循环。成功 load completion 将 masked GPR 与顺序 PC 一次提交；任一 lane service fault 只形成无损 forwarded fault，GPR/PC 均保持 completion 前状态。instruction issue 的 MemoryRequest、FENCE ordering、BAR/WarpDrain 和 WSPAWN 仍返回 pending stage，在未来 owner 明确成功前不执行或丢失。

`FROZEN（T2/03-isa-state-integration-contract）`：`emu/state/integration_test.go` 的端到端路径覆盖 integer GPR+PC、RV32F FPR+sticky FFLAGS、taken branch/JAL、TMC/PRED mask、SPLIT/JOIN record 生命周期、CSR RMW、ECALL/xRET、memory register+PC completion、illegal decode/fault 无 partial mutation，以及 BAR/WSPAWN/ordering 保真转交。该范围将第 6 节步骤 4–8 的单 warp、单指令、已拥有 word 子链从设计方向提升为实现契约；instruction source、fault router、Memory/Core/CTA/Barrier owner 和 progress/completion 仍未实现。

### 5.11 T3 single-lane fetch/Step core 实证

`FROZEN（T3/01-fetch-step-core）`：`warp.Warp` 是 single-lane functional execution orchestration 边界。它只长期引用真实 `*state.WarpState` 与 `InstructionSource`，不复制或缓存 PC、GPR/FPR、CSR、lane mask、lifecycle 或 program bytes；外部 owner context 仍由调用者在每次 `Step(state.ReadContext)` 显式提供。`InstructionSource.Read` 接收 canonical snapshot PC 和一个长度恰为 4 的 destination，因 C 已关闭而按 little-endian 组成 raw word；source 不推进 PC。inactive lifecycle 在 fetch 前返回 finished；running state 必须恰有一个 active bit，多-lane state 确定性拒绝。

`FROZEN（T3/01-fetch-step-core）`：一次 `Step` 固定经过 canonical snapshot → fetch → `isa.Decode` → `WarpSnapshot.Evaluate` → `WarpState.StageEffects` → local `EffectStage.Commit`。新增的 `WarpSnapshot.Evaluate` 是 T2 `ExecuteSingle` 与 T3 共同使用的唯一 category dispatch，因此 executor 未复制 integer/M/Zicond、branch/JAL/JALR、RV32F、CSR/System、trap 或 custom 的 ISA 方程。成功的 local non-memory instruction 只由 T2 stage 一次提交，结果中的 next PC 再从 canonical owner snapshot 读取；ECALL/EBREAK/xRET 同样原子提交并返回 typed trap outcome。

`FROZEN（T3/01-fetch-step-core）`：`warp.Result` 区分 retired、trap、fault、deferred 与 finished，并保留 step PC、raw-valid/raw word、可用时的 decoded/effects、canonical next PC、typed `warp.Fault` 和底层 error。alignment/access fetch failure、illegal decode、view/evaluation error、effect validation/commit failure及架构 fault均停止且不提交本步 local stage。data Memory/Ordering、非 Warp-owned CSR write 及其他 future-owner prerequisite 返回 deferred；本里程碑全部 custom instruction 即使在单 lane 输入下可算出局部方程，也只 evaluate/validate 后 deferred，避免将 cross-lane、SIMT、Warp、Barrier、CTA/Core 行为伪装为 T3 single-lane 支持。对应实现与定向验证位于 `emu/warp/warp.go`、`emu/warp/warp_test.go`、`emu/state/integration.go`；data-memory completion、连续 Run/budget 与 trace 仍留给后续 T3 里程碑。

### 5.12 T3 single-lane atomic memory Step 实证

`FROZEN（T3/02-atomic-memory-step）`：`warp.MemoryService` 是当前同步 functional byte owner 边界，提供 all-or-error `Read`/`Write`；`NewWithMemory` 显式配置该边界，原有 `New` 收到同时实现 MemoryService 的 source 时也识别同一实例，因此 fetch、load、store及自修改后重新 fetch均观察同一 canonical bytes。`warp.Warp` 与 `state.WarpState` 只保存 service 引用或 architectural state，不复制 instruction image/data bytes。`support/memory.Memory` 是该接口的基础 flat adapter，不冻结最终 global/LMEM scope、ordering、visibility或 self-modifying-code policy；这些仍由 U-MEM-01 管理。

`FROZEN（T3/02-atomic-memory-step）`：single active lane 的 LB/LBU/LH/LHU/LW、SB/SH/SW、FLW/FSW 先由 T1 evaluator产生 address/width/byte-mask/little-endian request；executor只按 typed request访问 byte owner，再通过 `WarpSnapshot.CompleteMemory` 分派既有 integer/RV32F completion helper。`vx_packlb_f`/`vx_packlh_f` 的 lane-local element request同样经 service读取并由 `WarpSnapshot.CompletePackedLoad` 调用 T1 assembler；其他 VOTE/SHFL/WGATHER、TMC/PRED、SPLIT/JOIN、WSPAWN、BAR/WSYNC custom effects仍 deferred。FENCE ordering 在配置同步 service时显式以 external-success完成，未配置 memory owner时保持 deferred，不静默丢弃。

`FROZEN（T3/02-atomic-memory-step）`：load/packed completion effects先在 T2 detached stage完整预校验后一次提交 register/FPR与PC。Store 先构造 T1 success completion并完成 T2 stage校验，再由 `EffectStage.CommitWithExternal` 在调用 memory前拒绝 stale stage；有效 stage只调用一次 all-or-error Write，Write成功后直接安装已验证的 canonical PC而没有可失败的第二阶段，Write失败则恢复 stage.before且按 MemoryService契约不改变 bytes。实现不使用可能失败或覆盖 intervening update 的补偿写。alignment/bounds pre-fault、service failure、completion/effect validation failure均不遗留本步 memory/state mutation。Fault继续作为 typed outcome保留，不猜测 U-FAULT-01 trap mapping。实现与验证位于 `emu/warp/warp.go`、`emu/warp/memory_test.go`、`emu/state/apply.go`、`emu/state/integration.go`；连续 Run/budget 与 trace仍留给后续里程碑。

### 5.13 T3 bounded Run、completion 与 trace 实证

`FROZEN（T3/03-run-trace-contract）`：`warp.Run(RunOptions)` 只按显式有限 `StepBudget` 重复调用 `Step`，不读取、缓存或自增影子 PC；每次 attempt 的 fetch仍从 `WarpState` canonical snapshot开始。零 budget不调用 Step；连续 retired progress达到上限后返回 typed budget-exceeded error；fault、trap、deferred或finished在该次 attempt后立即停止。`RunResult` 分别记录所有 Step calls 的 Attempts、仅 `OutcomeRetired` 的 Retired及可选 Last（零 budget为 nil）。自环 JAL与普通控制流loop均只由预算终止，不依赖外部 timeout。

`FROZEN（T3/03-run-trace-contract）`：`RunOptions.Context` 可按 zero-based attempt提供逐步 `ReadContext`，nil时使用零值；这不在 executor中保存 Core/CTA/Counter view。`TraceSink`/`TraceFunc` 同样按 Run调用注入且nil默认完全关闭。启用时每一 attempted Step产生 detached `TraceRecord`，包含 step index、WarpID、起始 PC、raw-valid/raw word、decoded instruction、T1 issued effects（包括memory requests）、最终completion effects、canonical next PC与Step outcome；decoded的register slices、effect slices及所有 pointer effects均深拷贝，因此 sink不能经record alias修改执行结果或Last。该接口可由应用层 logger 适配，但executor不拥有logger、文件或全局开关，也不产生pipeline/timing trace。

`FROZEN（T3 completion boundary）`：RunFinished唯一来自 Step观察到 canonical `WarpInactive`，不会fetch；T3不从PC范围、ECALL、trap return、预算、fetch/data fault或host image推断正常完成。Trap、fault、deferred和budget exhausted都是明确停止原因而非CTA/Kernel/host completion。完整hand-built stream与定向测试位于 `emu/warp/run_test.go`，覆盖顺序依赖、taken/not-taken branch、JAL/JALR、M/Zicond、integer/FP memory链、CSR、illegal/fetch/data fault、loop/budget、inactive finish与trace observational isolation；T1/T2全量回归仍由 `scripts/verify-emu.sh` 门禁。

### 5.14 T4 普通四 lane Step 与原子 memory 实证

`FROZEN（T4/01-four-lane-execution）`：`warp.Warp.Step` 已解除 T3 的 exactly-one-active-lane 限制，接受 canonical running `WarpState` 的任意合法非空四位 active mask；inactive lifecycle 仍在 fetch 前返回 finished，running 的空/越界 mask 则在 fetch 前确定性返回 state fault。执行器没有新增 lane、PC、mask、register、FCSR 或 trap storage：每步仍严格经过 canonical `WarpSnapshot` → `Decode` → 同一个 `WarpSnapshot.Evaluate` → `WarpState.StageEffects`/commit。RV32I/M/Zicond、RV32F、CSR/System/trap 的 masked write因而只作用于 evaluator effect mask，inactive GPR/FPR及其 FFLAGS贡献保持不变；branch/JALR 的唯一 warp-wide control decision继续由最高编号 active lane产生，PC/寄存器/CSR/mask/trap effect在一条指令边界提交。

`FROZEN（T4/01-four-lane-execution）`：LB/LBU/LH/LHU/LW、SB/SH/SW、FLW/FSW及 packed load现在为每个 active lane服务 request；T1 completion helper强制 response集合完整匹配 issue mask（packed还匹配每个 element），任一 alignment、bounds或service fault只产生 fault bundle，不提交任何 lane writeback、PC或store byte。多 lane store先对全部 lane request做无 mutation preflight，再构造成功 completion并重新 stage；stale state在调用 memory owner前拒绝。单 lane store保持调用既有 `MemoryService.Write`；多地址 store要求可选 `AtomicMemoryService.WriteBatch`，`support/memory.Memory` 在同一 owner lock下先验证全部range及pairwise overlap再一次应用 batch。batch失败不调用补偿写且State/Memory均不变；不具备批量能力的旧service可继续执行单 lane store，但对多地址 store确定性返回service fault而不部分写入。

`UNRESOLVED（T4/01-four-lane-execution，U-MEM-01）`：同一条多 lane store的字节范围互相重叠时，冻结证据仍不足以确定lane可见优先级；当前 flat owner将其作为整条 batch service fault原子拒绝，不能把 Go slice/lane循环顺序伪装成architecture ordering。最终global/LMEM scope、跨warp ordering与该重叠优先级继续由 U-MEM-01 管理。定向证据位于 `emu/warp/warp_test.go`、`emu/warp/memory_test.go` 与 `support/memory/memory_test.go`，覆盖full/partial mask的不同per-lane integer/FP operands、inactive隔离、最高lane branch、多lane integer/FP/packed memory、response故障全指令回滚、batch失败和stale-stage pre-write拒绝；全量 T1–T3 regression继续由 `scripts/verify-emu.sh` 门禁。

### 5.15 T4 Warp-local SIMT/custom 连续执行实证

`FROZEN（T4/02-warp-simt-custom，owner/apply boundary）`：`CategoryCustom` 只描述 ISA 类别，不再被 `Warp.Step` 当作统一 future-owner 边界。custom word仍经同一个 `WarpSnapshot.Evaluate` 和 `WarpState.StageEffects`；stage没有 external prerequisite时，TMC/PRED、SPLIT/JOIN及VOTE/SHFL/WGATHER的 register、PC、mask、lifecycle和divergence effects作为一个 detached candidate原子提交。WSPAWN携带 `WarpSpawnEffect`，WSYNC携带 `WarpDrainEffect`，BAR/BAR.ARRIVE/BAR.WAIT携带 `WarpDrainEffect`/`BarrierEffect`，所以即使同 bundle已有顺序PC或BAR.ARRIVE register候选写，也必须整体 deferred且 canonical WarpState 不变；执行器不创建其它Warp、scheduler、barrier或CTA state。

`FROZEN（T4/02-warp-simt-custom，mask/divergence）`：TMC用最高 active lane的operand替换canonical mask；PRED只从输入active lanes形成predicate mask且空选择时用最高active lane的fallback mask。非零mask保持running，零mask在提交顺序PC的同时将lifecycle变为inactive，下一Step在fetch前finished。SPLIT从真实active predicate形成then/else partition，按冻结 minority-first/tie-then规则选择execute/deferred mask；只有两边均非空才push `{original mask, split PC+4, elseVisited=false}`，uniform split只返回当前stack pointer register结果而不伪造record。合法连续reconvergence为 SPLIT push → 第一次JOIN在同一join PC切换到`original &^ current` deferred path并mark else visited → 第二次JOIN顺序前进、恢复original mask并pop。pointer/record/capacity与同一步snapshot不一致时由effect validation返回typed fault，candidate register/PC/mask/stack均不提交；compiler/runtime在合法lowering之外的underflow/overflow策略仍保留于U-LOWER-01。

`FROZEN（T4/02-warp-simt-custom，cross-lane/memory/trace）`：VOTE.ALL/ANY/UNI/BALLOT把输入active predicates的warp-wide结果写回输入active mask；SHFL.UP/DOWN/BFLY/IDX按每lane control和group boundary读取当前Warp snapshot，目标lane inactive时保留该destination lane自身source值。WGATHER是普通inactive-write规则的显式例外：nominal source inactive时使用最高active source fallback，但write mask固定为四lane中除nominal source offset外的所有lanes，State applier不得再与输入active mask求交。`vx_packlb_f`/`vx_packlh_f`继续由同步Memory owner为每个active lane完成全部elements，任一element fault抑制整条指令FPR/PC提交。Run不缓存新的SIMT state；同一join PC可因canonical mask/record变化连续fetch两次，detached trace现有WarpMask/Divergence/RegisterWrite slices/pointers完整观察push/mark/pop且不能反向mutation。定向证据为`emu/warp/custom_test.go`和`emu/warp/memory_test.go`，并由`state`/`isa`既有单指令测试及`./scripts/verify-emu.sh`回归互证。

### 5.16 T4 完整 SIMT stream、trace 与 final scope 实证

`FROZEN（T4/03-simt-stream-contract，continuous Run）`：`emu/warp/simt_stream_test.go` 的 bounded program从`AllLanes` running state连续执行per-lane ADDI、四lane FADD.S、四地址SW、TMC、divergent SPLIT、路径体ADDI+VOTE.BALLOT+SHFL.DOWN+WGATHER、JOIN、同一路径体在deferred mask下第二次执行、JOIN pop、reconverged LW及零mask TMC。Run只按canonical PC重复Step：第一次JOIN将PC重定向回保存的split PC+4，第二次JOIN顺序前进；最终17条指令retired，随后一次finished观察，共18 attempts。逐lane GPR/FPR、四个memory words、PC=termination PC+4、mask=0、inactive lifecycle、divergence pointer=0及空record均有精确断言；路径体每lane计数最终只增加一次，证明另一分支inactive时没有普通写回。此hand-built合法序列闭合U-LOWER-01的单Warp连续功能子范围，不闭合缺失compiler/runtime lowering或任意非法程序的策略。

`FROZEN（T4/03-simt-stream-contract，Run stop contract）`：既有zero/exact budget、retired loop、fault/trap/finished语义保持不变。新增Run验证先退休一条普通指令后遇到WSPAWN、WSYNC、BAR/BAR.ARRIVE/BAR.WAIT中的任一种，均在第一次unresolved attempt返回deferred，Attempts/Retired为2/1且PC及该custom的候选local effects不提交。TMC/PRED mask=0仍是本阶段唯一实现的正常Warp termination入口；它本身retired并提交顺序PC，下一attempt由inactive lifecycle在fetch前返回finished。

`FROZEN（T4/03-simt-stream-contract，detached SIMT trace/failure audit）`：`Result`和`TraceRecord`在既有起止PC/effects之外显式携带起始/结束active mask、lifecycle与divergence pointer；这些均来自Step起始snapshot和返回边界canonical snapshot，不成为第二份状态。decoded slices、RegisterWrite/WarpMask slices及Divergence等pointer effects继续深拷贝；对trace中的mask/lifecycle/pointer、decoded和issued/completed effects进行恶意修改后，独立同程序Run的RunResult、最终WarpState和memory image仍逐位相同。failure证据同时覆盖multi-lane load/store/packed element service fault、illegal fetch word、divergence capacity/stale effect validation及trace mutation，所有失败均不遗留该指令的partial register/FPR/PC/mask/stack/memory mutation。

### 5.17 T5 Core/Warp manager、lifecycle 与 scheduler 实证

`FROZEN（T5/01-core-warp-manager，topology/ownership）`：`core.Core` 的内部固定数组严格包含 `FrozenWarpCount=4` 个 slot，以 architectural Warp ID 索引；`core.New` 要求四个既有 `warp.Warp` executor 恰好覆盖 ID 0..3，拒绝数量错误、nil、重复和缺失。每个 slot 只保留 executor 与其 `CanonicalState()` 指向的同一个 `WarpState` owner，以及 Core 自己拥有的 scheduling metadata；不保存 PC、GPR/FPR、active mask、CSR、divergence 或 instruction bytes。所有公开观察是 detached `SlotSnapshot`，所有调度/状态转换前后均重新读取 canonical snapshot 并验证 owner/executor identity、ID、running/inactive 与非零/零 mask 一致性。

`FROZEN（T5/01-core-warp-manager，lifecycle/completion）`：Core lifecycle 明确区分从未参与的 inactive、canonical running 且可选的 runnable、canonical running 但有非零 typed `BlockReason` 的 blocked，以及曾参与后 canonical inactive/zero-mask 的 finished。显式 `Block`/`Resume` 对非barrier reason只改变scheduler eligibility；T6起`BlockBarrier`保留给coordinator/drain owner，公共API不能创建或解除。`Activate` 只接纳调用者已经显式初始化为 running/nonzero mask 的 inactive owner，不补 PC/register/CSR/CTA ABI 默认值。`Warp.Step` 返回 deferred 或 fault 时该 warp 分别以派生 future-owner reason 或 fault reason阻塞，避免同一无进展 PC 被重复选择；TMC/PRED 零 mask经既有 canonical completion路径提交后，该 slot在同一个 Core step 返回边界成为 finished。Core completion只聚合本生命周期 `participated` slots，从未激活的空 slot不属于待完成集合；finished slot退出调度不会影响其他 runnable slot。

`FROZEN（T5/01-core-warp-manager，functional scheduling）`：`Core.Step` 每次最多选择一个 runnable slot并直接调用一次既有 `Warp.Step`。选择从一个纯调度 cursor 开始按 Warp ID round-robin，选择后 cursor移动到下一 ID；inactive、blocked、finished全部跳过，因此多个持续 runnable warp获得确定性的循环机会。Core按每次 canonical running snapshots派生 `ReadContext.ActiveWarps`（包含 blocked 但仍 active 的 warp）并固定唯一 `CoreID=0`，不缓存 CSR view。该 round-robin 是功能模型的确定性策略，不声明与 RTL priority encoder、pipeline stall、ibuffer/backpressure或任何 cycle timing相同；跨 Warp memory race/ordering继续保持 U-SCHED-01/U-MEM-01，不以 Go 调用顺序冻结硬件语义。定向证据在 `emu/core/core_test.go`，覆盖冻结拓扑/ID拒绝、四态与typed reason、round-robin公平性、不可运行跳过、PC/GPR/FPR/mask/CSR/divergence隔离、单 Warp完成和其他 Warp继续推进；全量门禁仍为 `scripts/verify-emu.sh`。

### 5.18 T5 WSPAWN/WSYNC 跨 Warp 协调实证

`FROZEN（T5/02-wspawn-wsync-coordination，WSPAWN validation/ownership）`：Core只消费既有 `Warp.Step` 保真返回的 deferred `WarpSpawnEffect`，再次校验 source ID、三位requested count导出的精确 `i<count && i!=source` 四位target mask、四字节对齐目标PC、`InitialLaneMask=lane0`、`CopyMScratch`、`RequiresSingleActiveWarp`与`ReleaseSourceAfterApply`。active-warp mask不只含source时，source保持原PC/State并以`BlockWarpSpawn`退出选择；其他active Warp仍可推进，gate变为single-active后source自动恢复runnable并重新执行同一WSPAWN。成功只允许从未参与的inactive targets；finished slot是否可复用缺少runtime协议，当前确定性拒绝而不把该guard升级成architecture reuse语义。

`FROZEN（T5/02-wspawn-wsync-coordination，atomic activation）`：Core在调用`Warp.Step`前取得完整source与target snapshots；deferred WSPAWN返回后先把完整source canonical snapshot与pre-issue source比较，拒绝fetch/evaluate期间的任意source mutation，再基于该同一快照重新stage。`state.StageWarpSpawn`接收source的既有`EffectStage`和pre-issue target snapshots，先验证所有source/target identity、inactive lifecycle、effect target coverage及staleness，再构造detached candidates。每个target candidate只替换PC、active mask/lifecycle与`TrapCSRs.MScratch`；GPR/FPR、FCSR、其他trap CSR、saved mask和divergence逐位保留，不复制source register/context也不补CTA/ABI默认值。`WarpSpawnStage.Commit`在任何写入前同时比较source与全部target canonical images，之后只执行不可失败的owner replacement；source顺序PC与全部target activation因而一次提交。target不可激活、source/target stale或任一预校验失败时，协调器不推进source也不激活任何target；外部失败注入本身造成的intervening mutation保持原样而不会被回滚覆盖。

`FROZEN（T5/02-wspawn-wsync-coordination，WSYNC/BAR boundary）`：`PendingWorkProvider`是Core可注入的显式只读owner接口；未注入时继续消费调用者每次`Core.Step`提供的`ReadContext.PendingPriorWork` view。WSYNC pending=true时既有evaluator不产生Control，Core保持整个WarpState和该WSYNC PC不变并记录`BlockPendingWork`；predicate清除时Core自动恢复slot、重新fetch/evaluate同一WSYNC、验证唯一`DrainPriorInstructions{Wait=false,ReleaseAfterDrain=true}`后通过原EffectStage提交顺序PC，结果成为retired/runnable。初次view已经false时同一个Core step直接完成。实现不保存pending周期、pipeline/ALM内容或drain timing，也不把provider清除、同步Go调用或scheduler顺序声明为跨Warp memory visibility。BAR/BAR.ARRIVE/BAR.WAIT（包括仅有LSU-drain effect的pending形态）统一保持canonical State不变并以`BlockBarrier`等待未来真实coordinator；T5不建立临时barrier record/release规则。定向证据为`emu/core/core_test.go`与`emu/state/spawn_test.go`的count/targets、single-active retry、mscratch-only copy/target isolation、target activation rejection、source/target issue期间mutation与commit期stale failure injection、WSYNC immediate/pending/resume以及三种BAR deferred测试；完整回归由`scripts/verify-emu.sh`门禁。

### 5.19 T5 Core Run、trace 与最终 Multi-Warp contract

`FROZEN（T5/03-core-run-trace-contract，bounded Run/outcomes）`：`Core.Run(RunOptions)`只在显式`StepBudget`内重复现有`Core.Step`；Attempts计数实际选择并调用的Warp steps，Retired只计完整`OutcomeRetired`，预算是功能指令attempt上限而不是cycle、issue或drain时间。Run开始和每次retired后从Core lifecycle owner聚合completion：只有至少一个`participated` slot且所有这些slots均finished才返回`RunComplete`，从未激活的inactive slots不参与；任一runnable或blocked相关slot都使completion为false。达到有限上限返回`RunBudgetExceeded`；当前无runnable但相关工作未完成返回`RunBlocked`；本次选择产生deferred、trap或fault分别原样停止为`RunDeferred`、`RunTrap`或`RunFault`。deferred当次不会被吞掉来继续其他Warp；调用者可在owner view改变后再次Run，scheduler cursor与canonical owners均持续存在。provider/协调错误同样确定性返回fault，若错误发生在实际选择之后则保留该次detached Last/trace。

`FROZEN（T5/03-core-run-trace-contract，detached trace）`：每次实际scheduler selection产生一条`core.TraceRecord`，包含当前Run内zero-based CoreStep、selected Warp ID、Core slot lifecycle/block reason前后值和完整detached `warp.Result`。`warp.DetachResult`深拷贝Decoded source/destination slices、issued/completed effect slices及Control/Ordering/FFlags/Trap/WarpSpawn/Divergence pointers，并复制Fault的architectural slice；Core的RunResult.Last与sink record还分别detach，二者不互相alias。sink修改selected ID、lifecycle、decoded名称/register slices、next PC、memory request或control effect不能改变Last、scheduler cursor、任何WarpState或Memory bytes。nil trace不产生logging/global side effect；该记录是功能attempt trace，不是RTL pipeline/cycle trace。

`FROZEN（T5/03-core-run-trace-contract，end-to-end evidence）`：`emu/core/run_test.go`的hand-built单Core程序从唯一active source wid3执行WSPAWN，原子激活wid0..2到共同entry；round-robin前缀确定为`3,0,1,2,3`，source随后zero-mask提前finished，其余targets继续。每个target先TMC恢复四lane，再独立执行divergent SPLIT→两条path body→JOIN mark/pop、reconverged ADDI、各自四个不重叠地址store、WSYNC和zero-mask termination。wid1首次WSYNC由provider置pending使第一次Run明确deferred，清除后第二次Run重试同一PC；wid0/wid2继续按确定scheduler顺序完成，最终四个participated slots全部finished而空divergence stack、per-warp/lane GPR/FPR/CSR/mask/PC和memory结果互不串扰。两次独立场景产生逐字段相同trace；恶意trace sink场景与baseline的Run outcomes/attempts、四份canonical snapshots和完整memory image相同。

`RESOLVED（T5/03，U-SCHED-01 非冲突功能调度子范围）`：T5为可复现功能测试冻结round-robin选择和一次只调用一个`Warp.Step`的串行执行次序；这只回答“功能模型如何确定性推进多个无竞态Warp”，不回答硬件对冲突跨Warp memory effects的可见顺序。端到端store使用每Warp/每lane不重叠地址，故不能从测试结果推导Vortex arbitration、lane conflict priority、atomicity或memory ordering。冲突行为仍完整保留在U-SCHED-01/U-MEM-01，后续只能由新增RTL/runtime/checker证据闭合。

### 5.20 T6 CTA context、membership 与 LMEM 子范围实证

`FROZEN（T6/cta-context-lmem，canonical CTA owner）`：`core.CTAManager`是resident CTA id、rank-ordered members、warp→CTA stable key、由rank/lane/block dimensions确定性派生的thread coordinates、block/grid metadata、entry、cluster size、LMEM allocation与逐成员finished observation的唯一可写owner。`CTAConfig`完整预校验resident ID、非空且不重复的wid 0..3、非零dimensions、block id范围、thread product与member count、aligned entry、cluster size和LMEM capacity；只有全部成功后才一次安装CTA record、reverse keys与allocation，失败不留下partial membership/context。公开`CTASnapshot`复制member slice，`isa.CTAView`为按次value copy。Core只附着一个不可替换的manager引用；附着提交前要求member route精确引用同一owner，seal后静态manager禁止新增membership。动态manager对全部四route做同样证明并只经第5.24节Core协议改变residency。有owner时`Core.Step`按selected wid派生并覆盖调用者`ReadContext.CTA`，因此caller不能以任意CTA id/rank/coordinates影响CSR，也不能让CSR allocation与实际LMEM owner分叉。

`FROZEN（T6/cta-context-lmem，WSPAWN/completion observation）`：CTA-aware WSPAWN在任何State stage commit前要求`source ∪ targets`精确等于同一canonical CTA membership；missing、cross-CTA或额外member均确定性fault，source PC、target lifecycle/PC/mscratch与CTA state均不改变。Core外部`Activate`在attached owner下同样要求membership。canonical Warp变为zero-mask finished后Core把该事实单向通知CTA member record；这只是后续Barrier-aware completion的输入，不定义fault/early-exit release，也不提前报告CTA completion。

`FROZEN（T6/cta-context-lmem，address routing/atomicity）`：冻结CTA-visible LMEM窗口严格为`[FrozenLocalMemBase=0xffff0000, +FrozenLocalMemSize=2^14)`；每个CTA的公开allocation从同一虚拟base开始并以64-byte粒度计入resident capacity，manager私有`backingOffset`把该窗口按warp→CTA membership翻译到唯一16 KiB byte array中的互斥区域。backing offset不进入CSR/snapshot/router，因此同CTA Warp通过各自`CTAMemory{manager,wid}`观察同一bytes，而不同CTA对完全相同的数值地址观察独立bytes。CTA请求仍受自身size约束。完全位于窗口外的range只交global `MemoryService`，窗口内只交CTA owner，跨窗口range在调用任一owner前拒绝。local multi-address batch先校验全部range与overlap；global batch交既有`AtomicMemoryService`；mixed batch持有local owner lock并完成全部local预校验，随后以all-or-error global batch作为external success，成功后只剩不可失败local copy，失败则两边均不改变。`warp.NewWithServices`只分离global instruction source与data router，并校验router bound wid等于canonical WarpState id；`Warp.DataMemoryService`只向Core暴露原service identity用于附着校验，不产生bytes副本。真实Warp SW→另一同CTA Warp LW、不同CTA同数值地址isolation、global/mixed route、invalid lane、跨窗口、不同manager构造和batch failure测试证明CSR/LMEM owner不能分叉，失败不推进PC/register或修改LMEM/global bytes。跨Warp竞态顺序、BAR/FENCE/WSYNC visibility与重叠lane priority继续留在U-MEM-01。

### 5.21 T6 CTA barrier coordination 子范围实证

`FROZEN（T6/cta-barrier-coordination，key/canonical owner）`：`core.BarrierCoordinator`绑定并seal同一个`CTAManager`，以`BarrierKey{CTAID, AddressWarp, ID}`寻址；`AddressWarp`和request warp都必须属于该CTA，ID严格为0..7。CTA字段是functional resident namespace，RTL物理字段仍是`AddressWarp || barrier ID`，两者不形成第二份可写truth。每条record唯一持有arrival/wait masks、当前participant count、0..32 events、phase及participants-complete标记；public snapshot和每Step phase table都是detached copy。冻结`NUM_CORES=1/USE_GBAR=0`不提供global unit，故global请求确定性拒绝而不是永久等待；local count须在1..CTA size且同phase稳定，重复arrival、cross-CTA AddressWarp、count变化、event overflow/underflow和stale stage均在mutation前拒绝。

`FROZEN（T6/cta-barrier-coordination，request/phase/release）`：Core为selected CTA构造完整4×8 canonical phase view并覆盖caller scalar，`state.CustomInput`在读取本条rs1后只选择对应AddressWarp/ID，因此BAR.ARRIVE rd不能被外部`ReadContext.BarrierPhase`冒充。LSU pending形态只记录`barrierDrain` scheduler fact，保持PC/register/coordinator不变；predicate清除后重新fetch同一PC。drained形态必须恰有一个`DrainLSU{Wait=false}`和一个匹配decoded kind/source的Barrier effect。BAR sync执行arrive+wait，非末participant提交PC一次后阻塞；BAR.ARRIVE只arrival并返回提交前phase，不阻塞；BAR.WAIT的request phase不同于canonical phase时立即retire，相同时提交PC一次并登记wait。最后participant且events为零时清空本phase arrival/wait、翻转phase并只释放同key waiters；events非零时保持blocked，`CompleteBarrierEvent`每次递减一个，最后event仅在participants complete时执行同一release。

`FROZEN（T6/cta-barrier-coordination，atomic scheduler boundary）`：Barrier request先完成Warp issue snapshot stale检查、T2 local candidate restage、coordinator optimistic stage及全部release-slot/key预校验；issue前phase view同时携带该key的detached exact record image，若fetch/evaluate期间canonical record改变则request在任何本地提交前stale-fail。`EffectStage.CommitForwardedWithExternal`再次在调用coordinator前检查Warp owner staleness，coordinator stage也在写入前比较exact record image。任一预提交拒绝都保持WarpState、barrier record以及调用前Core lifecycle/block reason/stable-key metadata不变，不把无fault effect的协调错误单独提交为`BlockFault`。全部检查成功后先完成all-or-error coordinator update，再执行不可失败Warp replacement，最后只做已预校验的Core lifecycle updates；成功边界之后没有可返回的validation failure。被接受并阻塞的指令已推进PC，release只把slot恢复runnable，绝不重新arrival。round-robin自然跳过`BlockBarrier`；跨CTA/跨ID release mask不能通过matching-key检查。该功能release边界不声明cache flush、跨Warp竞态顺序、global barrier网络或fault/early-exit teardown，分别继续保留在U-MEM-01/U-BAR-01。

`FROZEN（T6/cta-barrier-coordination，lifecycle API ownership）`：公共`Core.Block`确定性拒绝`BlockBarrier`，公共`Core.Resume`确定性拒绝任何barrier-blocked slot；两种拒绝均不改变slot、stable key/drain metadata、WarpState或coordinator record。已登记waiter只能由匹配key的canonical release恢复；尚未提交request的LSU drainer只能由`refreshFunctionalWaits`观察本次`PendingLSU=false`后重新执行原barrier PC。非barrier的`BlockExplicit`等T5 typed reason仍可经公共`Resume`恢复。该边界防止scheduler runnable truth与canonical waiter mask分叉。

### 5.22 T6 CTA completion、detached trace 与端到端实证

`FROZEN（T6/cta-completion-e2e，canonical completion）`：`CTAManager`只从自身sealed resident record复制明确成员与逐成员finished observation，再与Core提供的只读slot lifecycle/block reason以及同一manager绑定的`BarrierCoordinator` pending view聚合。CTA complete必须同时满足：成员集合非空、每个明确成员均已被owner观察为正常finished且对应slot为`WarpFinished`、没有成员blocked、该CTA不存在arrival、waiter、participant、event或participants-complete pending record。phase-only历史record不阻止下一phase后的正常完成。未参与Warp、其他CTA和旧Core聚合不能加入或反写结果；`Core.Complete`在附着CTA owner后只聚合全部resident CTA completion。全员暂时在barrier等待时`Core.Run`返回`RunBlocked`而非complete，matching release后再次Run从已提交barrier的下一PC继续并最终返回`RunComplete`。

`FROZEN（T6/cta-completion-e2e，observation/alias boundary）`：`Core.StepResult`和`TraceRecord`增加selected CTA id、barrier request/key、before/after canonical record、blocked/released mask、phase及当步CTA completion；`RunResult`增加按CTA id排序的completion snapshots。所有barrier/completion pointers与member slices在Step→trace、trace→sink和Run返回时逐层detach；调用者修改CTA id、record、release mask、member或嵌套`warp.Result`不能改变CTA、barrier、Warp、scheduler、LMEM/global bytes或另一份结果。trace仍是functional attempt observation，不是cycle/pipeline事件流。

`FROZEN（T6/cta-completion-e2e，program evidence）`：`emu/core/cta_e2e_test.go`执行真实四字节指令流。一个六thread CTA由两个Warp组成，第二Warp仅lanes 0..1参与；各成员先用CTA thread-x CSR派生不同LMEM位置并写值，经同一barrier id同步后读取对方bytes，再复用同一barrier完成第二phase并TMC终止。测试精确观察14次retired attempt、两次block/release、phase `false→true→false`、partial-lane coordinates、共享load结果、所有成员恢复/完成以及detached trace mutation isolation。另一个四Warp场景同时运行两个CTA：二者使用相同barrier id和完全相同的数值LMEM虚拟地址，但经CTA membership翻译得到独立key/phase/bytes/completion，20次attempt后均正常完成。`emu/core/completion_test.go`另覆盖pending event阻止完成、全waiter blocked→event release→继续完成、resident CTA独立聚合及返回snapshot mutation isolation。

`RESOLVED（T6/cta-completion-e2e，U-CTA-01 正常功能完成子范围）`：在显式resident CTA API与冻结single-Core条件下，正常TMC zero-mask成员完成、pending local barrier门控、blocked/resume和CTA completion已由RTL completion路径、canonical owner实现及端到端测试共同闭合。其当时未定义的grid launch、动态正常调度/回收及Kernel functional completion现由第5.23–5.26节闭合；fault/early-exit teardown、host handshake与multi-Core仍保留在U-CTA-01/U-BAR-01/U-ABI-01。

### 5.23 T7 Kernel launch contract 与 KMU grid walk 实证

`FROZEN（T7/launch-contract，runtime boundary）`：`device.LaunchState`是runtime无关的hardware-visible launch value。它显式且分别保存`StartupPC`、`KernelEntryPC`、`ParameterAddress`、三维grid/block dimensions、独立`BlockSize`、三维`WarpStep`、原始`LocalMemorySize`和三维cluster dimensions；不接收ELF/vxbin、module symbol、host argument struct、buffer allocation或DCR write sequence。Runtime/input adapter负责解析与staging，并把已经位于canonical device memory中的parameter地址交给该值；第5.25节Kernel executor只消费这个launch与调用方memory interface，不清空global bytes、不复制program/data/result。`VX_kmu.sv:111-143,299-312`证明这些DCR字段分别存储和广播，`VX_cta_dispatch.sv:385-483`证明startup PC送`cta_PC`而entry/param进入CTA CSR context。因此二者均独立要求RV32四字节取指对齐，每个`device.CTA`仍以`StartupPC`作为首次Warp PC并另存`KernelEntryPC`，generation不得以entry替换startup。

`FROZEN（T7/launch-contract，pre-execution validation）`：`ValidateLaunch`/`NewLaunchState`先在detached value上完成全部校验，`NewGridWalker`只在成功后创建生成进度。冻结field widths为block dimension 5 bit、warp step 4 bit、cluster dimension 3 bit；block dimensions与`BlockSize`/`WarpStep`分别保留，不能从dimension product反推或覆盖KMU独立字段。`BlockSize`必须为1..16并单独派生`WarpsPerCTA=ceil(BlockSize/4)`；LMEM按`VX_CFG_MEM_BLOCK_SIZE=64`向上对齐且不超过16 KiB。cluster product必须非零且不超过4个CTA slots，并同时满足`cluster_size * warps_per_CTA <= 4`及`cluster_size * aligned_LMEM <= 16 KiB`，派生`ResidentCTACapacity`取warp与LMEM上限最小值。所有block/cluster/grid乘法使用checked widening；CTA总数超过冻结32-bit counter即拒绝。非空grid逐轴必须被cluster dimension整除，因为`VX_kmu.sv:149-173,243-290`以equality而非greater-or-equal完成origin wrap，不整除会越界且不能结束。任何失败统一返回`ErrInvalidLaunch`且没有walker/execution owner；派生字段若由caller以非零冲突值重报也拒绝，防止第二真值。

`FROZEN（T7/launch-contract，two-level X→Y→Z walk）`：`GridWalker`唯一保存`origin[3]`、`intra[3]`与emitted count。非空grid先让intra X最快、再Y、再Z填满一个cluster，然后让cluster origin同样按X→Y→Z推进；输出`BlockID=origin+intra`，并携带cluster origin/offset/rank/first标记和全部独立launch字段。该顺序逐项对应`VX_kmu.sv:59-80,149-165,202-312`。整除与checked count前置保证每个BlockID恰好一次且加法不wrap；任一grid dimension为零时`TotalCTAs=0`，`Next`从第一次起永久返回exhausted，对应RTL `grid_nonempty`防止zero bound无限walk。动态CTA生命周期已由第5.24节闭合；完整Kernel Run/completion仍不得由walker exhausted提前推断。

实现证据为`emu/device/launch.go`。`emu/device/launch_test.go`定向覆盖startup/entry/parameter与block fields不混用、64-byte LMEM派生、cluster origin/intra精确顺序、BlockID唯一性、三个empty-grid轴的重复exhaustion、field widths/alignment、warp/LMEM co-residency、non-divisible walk、CTA count overflow、冲突派生字段、最大合法边界与validation幂等；完整门禁仍由`scripts/verify-emu.sh`执行。

### 5.24 T7 reusable CTA lifecycle 与 dispatch-window 实证

`FROZEN（T7/reusable-cta-lifecycle，owner/atomicity）`：`NewDynamicCTAManager`允许以空resident set附着，但四个Warp executor必须预先通过`CTAMemory`绑定同一manager与各自wid；附着后只有`Core.AdmitCTA`/`Core.ReclaimCTA`可改变residency，旧`NewCTAManager`的attach前`Admit`与sealed静态用法保持兼容。Admission先选择最低编号的inactive、未participated Warp slots和空CTA slot，再用64-byte aligned first-fit检查唯一LMEM backing中的空extent；manager epoch stage、全部CTA context/membership派生、`state.StageWarpLaunch`和scheduler slot recheck均成功后才提交。任一资源、validation或stale failure都不留下CTA/reverse membership、WarpState、scheduler、barrier或LMEM allocation的partial state。

`FROZEN（T7/reusable-cta-lifecycle，context/coordinates）`：`device.CTA.CoreConfig`把walker产生的BlockID、block/grid dimensions、独立BlockSize/WarpStep、startup、entry、parameter、LMEM和cluster信息原样交给Core，resident ID与WarpIDs只由Core分配。`BlockSize`单独决定`ceil(size/4)`个Warp及末Warp低lane mask；坐标不从BlockSize或rank线性反推，而按`VX_cta_dispatch.sv:187-190,418-438,556-641`从每CTA零base开始：每lane沿X加一并向Y/Z进位，每个后续Warp的base分别增加三维WarpStep，Y wrap可独立于X carry发生。派生membership保存active mask与每lane三维坐标，完整entry/parameter等context经现有`isa.CTAView`/CSR路径读取。

`FROZEN（T7/reusable-cta-lifecycle，canonical Warp launch）`：每次dispatch均由`state.StageWarpLaunch`在既有四个canonical `WarpState` owners上原子更新PC、active mask/lifecycle和`mscratch`，不创建第二份register/CSR state；GPR/FPR及RTL未指定的其他状态保留。Core的per-kernel initialized mask对应`warp_init_mask_r`：某wid在本Kernel首次使用时PC取独立`StartupPC`，回收后再次派发时取该owner完成PC减20字节的per-CTA dispatch window；每次派发无条件以`ParameterAddress`重写canonical `mscratch`。`KernelEntryPC`只留在CTA context供既有CSR/启动代码读取，绝不替换两种初始PC规则。

`FROZEN（T7/reusable-cta-lifecycle，normal reclaim/LMEM）`：Reclaim先从T6 owners确认所有明确成员均normally finished、没有blocked Warp或pending barrier，再要求已配置的functional pending-work provider对每个member均为false；未完成CTA不可覆盖。成功边界把finished scheduler slots恢复inactive/unparticipated，删除warp→CTA与CTA→warp记录和该CTA的空phase barrier history，并让CTA/Warp/LMEM slot重新可选；per-kernel initialized位及canonical registers保留。First-fit复用的LMEM extent不会与仍resident CTA别名，同一虚拟地址继续CTA隔离；LMEM bytes仍只属于manager的16 KiB local owner，不并入external global memory，回收残留初值因RTL/ABI未冻结而不保证清零。fault/early-exit teardown仍留在U-CTA-01。

实现证据为`emu/state/launch.go`、`emu/core/cta.go`、`emu/core/core.go`与`emu/device/launch.go`。`emu/state/launch_test.go`覆盖首次/复用PC、partial mask、每次mscratch、register保留及multi-owner stale原子失败；`emu/core/dynamic_cta_test.go`覆盖partial Warp、三维WarpStep carry、context、并发resident、乱序完成、Warp/LMEM不足无副作用、pending/提前回收拒绝、slot/dispatch-window复用及resident/reused LMEM隔离；静态T1–T6回归由完整门禁继续执行。

### 5.25 T7 Kernel execution engine、canonical backing 与 completion 实证

`FROZEN（T7/kernel-execution-engine，public boundary）`：`device.NewKernelExecutor`接收完整`LaunchState`和调用方唯一的`BackingMemory{Read,Write,WriteBatch}`；constructor再次执行完整launch validation并只保存normalized value与同一interface引用。便捷的`KernelExecutor.Run(KernelRunOptions)`每次创建新的walker、dynamic CTA/barrier/Core owners和四个inactive canonical WarpState；需要解除functional wait或分段预算时，caller先用`NewExecution`创建一个`KernelExecution`，随后多次`Run`均在同一walker/Core/CTA/Warp/Barrier/LMEM owners上继续，`CompleteBarrierEvent`也只路由到该session的canonical coordinator。每个调用的预算只计本次实际`Core.Step` instruction attempts，trace sequence则在同一session跨调用单调；它不解析image/symbol/host args，也不清空、snapshot或替换外部memory。冷启动owner当前以显式零值构造只是避免读取未初始化Go状态，不提升为软件/RTL ABI保证，正常程序仍只能依赖其明确初始化内容。

`FROZEN（T7/kernel-execution-engine，one memory/one execution path）`：exact backing object同时作为每个Warp的`InstructionSource`，并作为每个`CTAMemory`的global owner；后者只在固定LMEM窗口分流到CTA-local 16 KiB owner，窗口外fetch/argument/global/output始终回到原对象。循环只组合`GridWalker.Next → Core.AdmitCTACluster → Core.Step → Core.ReclaimCTA`，实际fetch/decode/ISA/effect/register/CSR/barrier/memory仍由T1–T6既有路径完成，没有Kernel专用decoder、Warp/Lane state或store旁路。同步`Read/Write/WriteBatch`必须在调用返回时完成或失败，所以一个Step返回即是该次functional memory effect的提交/故障原子边界，不虚构cache/DRAM/pipeline queue。

`FROZEN（T7/kernel-execution-engine，cluster/backpressure loop）`：walker按已冻结顺序一次取出完整`ClusterSize`组，`Core.AdmitCTACluster`在同一epoch transaction内为组内全部CTA预选互异Warp/CTA slots和非重叠LMEM extents，并用一个multi-Warp State launch stage提交；第二个CTA validation或任一资源失败不会留下组内第一个CTA。`ErrCTAResourcesUnavailable`只表示暂时backpressure：Kernel保留整个pending cluster并继续Step现有resident；正常reclaim释放资源后重试整组。空Core仍无法接纳已通过launch co-residency校验的cluster属于owner inconsistency/fault，不能无限等待。该循环允许多个cluster并发resident及乱序CTA completion，但每个cluster的generation/admission事件保持连续成组。

`FROZEN（T7/kernel-execution-engine，stop/completion）`：每次retired Step后先从CTA owner按resident slot id排序回收全部normally complete CTA，再继续admission。只有walker remaining=0、pending cluster为空、generation→resident map为空、`CTACompletions`为空，并且四个scheduler/architectural slots均inactive、unparticipated、unblocked、zero-mask且无barrier metadata时，`KernelComplete`才令`Complete=true`；generation exhausted、暂时无slot或KMU式running结束都不足够。`KernelBudgetExceeded`、`KernelBlocked`、`KernelDeferred`、`KernelTrap`和`KernelFault`为独立typed outcomes且一律`Complete=false`，不执行未授权异常CTA teardown。

`FROZEN（T7/kernel-execution-engine，result/trace）`：结果只包含typed outcome、attempt/retired及generated/admitted/completed计数、detached CTA events、last Kernel/Core record和error，不包含memory bytes。`KernelTraceRecord`用单调sequence交错记录generated/admitted/completed CTA事件与现有detached`core.TraceRecord`（其中继续含Warp result/effects、barrier和CTA completion）；event resident member slice、Core decoded/effects/pointers以及result/trace/sink之间均独立复制，恶意sink不能反写result、owners或memory。正常完成的output只由caller从原`BackingMemory`读取。

实现证据为`emu/device/kernel.go`、`emu/core/core.go`的cluster batch admission以及既有`warp/core/cta`路径。`emu/device/kernel_test.go`以六CTA/三cluster程序证明两cluster填满Core、第三cluster backpressure后继续、Warp/CTA slot复用、30个既有Core attempts、完整事件顺序与sink mutation隔离；另以真实CSR-mscratch→argument load→global store→TMC程序证明exact backing同时承担fetch/parameter/global output且image/argument不被清空，并覆盖empty grid、budget、blocked、deferred、trap、fault全部typed stop。`emu/core/dynamic_cta_test.go`另验证cluster第二CTA非法时全组零mutation。最终真实程序闭环及session续跑证据见第5.26节。

### 5.26 T7 Kernel 真实指令流、session continuation 与最终 E2E 实证

`FROZEN（T7/kernel-e2e-contract，startup/entry/parameter）`：最终程序的`StartupPC=0x100`与`KernelEntryPC=0x200`不同。首次Warp先执行startup-only marker，再进入恰好五条指令的dispatch window：从CTA CSR `cta_entry`读取entry、从canonical `mscratch`读取parameter address，并以JALR调用kernel body；body返回后TMC完成Warp。回收后同一wid从完成PC减20重新进入该window，而不重复startup marker。把launch startup直接替换为entry会跳过entry/parameter dispatch、缺失marker/return context并得到明确非完成或不同output，故测试不能以entry冒充initial Warp PC。

`FROZEN（T7/kernel-e2e-contract，memory/CTA/Warp path）`：同一caller backing保存program、parameter中的output base与input scalar以及最终output。三个BlockID、每CTA六thread需要两个Warp，超过同时resident的两CTA容量；正常completion/reclaim后第三CTA继续派发。kernel body只经既有CSR/ISA/LMEM/Barrier路径读取thread/block/LMEM context，active thread先写CTA-local LMEM、两个成员执行local barrier、再从LMEM读回并写唯一global word。末Warp mask为`0b0011`，WarpStep和dimension carry产生rank-1坐标，inactive lanes不产生memory request；因此caller原memory中恰有18个精确output writes，LMEM高地址从未到达global backing，三个BlockID generation/admission各恰好一次。

`FROZEN（T7/kernel-e2e-contract，wait/failure/isolation）`：当grid已经exhausted且两个CTA成员已完成barrier arrival，但两个显式events仍pending时，`KernelExecution.Run`必须返回`KernelBlocked/Complete=false`并保留同一resident CTA、Warp PC、barrier record与LMEM。caller对该session的canonical key完成两个events后再次Run，从原PC继续TMC并最终完成；budget stop同样可在同一owners上继续。invalid launch在创建execution owner或访问memory前失败；illegal instruction fault不写外部memory，之后fresh便捷Run不继承故障session。Kernel trace sink可恶意修改CTA member/coordinate slice及nested memory effect而不能改变result、owners、output或后续执行；empty grid及全部typed stops仍由第5.25节测试覆盖。

实现与最终验收证据为`emu/device/kernel_e2e_contract_test.go`、`emu/device/kernel.go`及不变的T1–T6执行链。该闭环只冻结runtime-independent functional contract；host runtime/CP、`vx_*` API、ELF/vxbin解析、module/symbol lookup、buffer staging/checker handshake、cache/DRAM/MMU及任何pipeline/cycle/timing/performance行为仍明确不在目标内。

## 6. 单条指令的功能执行流程

以下完整顺序仍是功能边界而不是 RTL cycle/pipeline 模型。四 lane普通及Warp-local SIMT执行的步骤 3–8与预算化步骤9子集已由第 5.11–5.16 节冻结；T5已冻结步骤2和有限Core Run；T6已冻结CTA/context/barrier/LMEM；T7第5.23–5.26节已冻结步骤1、cluster admission/backpressure、完整既有执行链、正常reclaim、可续跑functional wait与Kernel functional completion。异常CTA teardown及host completion handshake仍是 `UNRESOLVED`：

1. **Launch/prepare**：Device完整校验`LaunchState`并让`GridWalker`按KMU cluster/intra顺序产生完整cluster；Kernel层通过`Core.AdmitCTACluster`原子选择Warp/CTA/LMEM slots，按first-use或reuse规则设置PC、mask与mscratch，同时传递entry/param等独立CTA context；资源不足保留pending cluster而非部分接纳。
2. **Select runnable warp**：Core/Warp manager 从 canonical lifecycle/mask 与自身 typed block reason派生 runnable view，以确定性 round-robin 选择一个 Warp；不复刻 RTL arbitration/timing。
3. **Fetch**：instruction source 用该 Warp PC 从 instruction memory 取 32-bit word；C 已关闭。成功取指不自行改 PC，fault 产生显式 effect。
4. **Decode**：冻结专用 ISA decoder 仅用 word 产出 decoded operation 或 `IllegalInstructionError`；decoded operation 声明 evaluate 是否还需 PC/active mask/service view。
5. **Read view**：Warp executor 根据 decoded operation 向 owners 请求最小 immutable Lane/Warp、GPR/FPR、CTA、CSR、SIMT 或 Memory view，不把整个 Device 交给 ISA。
6. **Evaluate semantics**：ISA evaluator 计算单条指令的功能结果；Memory/CSR/Barrier 服务只经显式请求参与，不回调 manager 修改状态。
7. **Produce effect**：evaluator 返回 structured effects 或 fault/wait，不直接 mutation；无 effect 的非法路径也必须显式表示。
8. **Owner validate/update**：effect router 校验 target/scope、输入 active mask 与指令定义的 write mask 并路由，GPR/FPR、Warp PC/SIMT、CSR、Memory、Barrier、lifecycle 等 owner 各自最终验证和更新唯一 canonical state。
9. **Progress/completion**：Warp manager重新派生runnable view；canonical zero-mask completion向CTA owner单向通知。Dynamic CTA在明确成员均normally finished、无blocked/barrier/pending functional work后回收。Kernel再同时观察walker exhausted、pending cluster/resident/CTA records为空及全部Core/Warp slots inactive；同步memory调用已在Step内完成。仅此时返回`KernelComplete/Complete=true`，output仍从同一global backing读取。

## 7. 后续公共接口清单

除第 5.1–5.26 节明确冻结的ISA→State→Warp→Core→CTA/Barrier/Memory→Kernel functional边界外，下表其余host/runtime集成用途仍是 `PROVISIONAL`。箭头方向均从调用者到服务，结果/effect显式返回；任何接口都不授权全局查找或旁路mutation。

| 公共边界（用途名） | 输入 | 输出 | 允许的依赖方向/约束 |
| --- | --- | --- | --- |
| Kernel launch/grid generation | runtime已准备的`LaunchState`（startup/entry/param/grid/block/block-size/warp-step/LMEM/cluster） | normalized派生资源或`ErrInvalidLaunch`；逐CTA KMU-order detached record | `FROZEN（T7/launch-contract）`: Runtime adapter → `device.ValidateLaunch`/`NewGridWalker`；不接收image/host args/DCR transport，不创建Memory副本；walker只拥有generation progress，不能报告Kernel completion。 |
| Kernel executor/session | normalized launch、caller `BackingMemory`、finite StepBudget、optional per-Step context/trace sink及显式barrier event | typed complete/budget/blocked/deferred/trap/fault、detached CTA/Core events与last record；memory无snapshot | `FROZEN（T7/kernel-e2e-contract）`: caller → `NewKernelExecutor`；便捷`Run`每次fresh，`NewExecution`创建可续跑session，后续Run/event使用同一GridWalker/Core/CTA/Warp/Barrier/LMEM owners。exact backing始终供fetch/global访问并由caller直接观察output。 |
| Instruction source | PC/address space key | 32-bit instruction word 或 fetch fault | Warp executor → Memory instruction space；不推进 PC、不回调 scheduler。 |
| Memory spaces | global/local scope key、address/size、active lane requests、store data | load bytes/value、validated store effect 或 fault | evaluator/effect owner → Memory；global 与 LMEM bytes各有单一 owner，cache/timing透明。 |
| Lane/Warp views | warp/lane identity、decoded op 所需字段集合 | immutable active mask、operands、PC/SIMT/CTA-derived view | Warp executor → state owners；返回 view 而非可写引用。 |
| CSR context/service | CSR address/op、warp/lane/CTA/device identity、operand | old value、CSR/trap effect 或 illegal | evaluator → CSR service → CSR owner；不得直接调度 Core/CTA。 |
| Decoder | 32-bit word | decoded operation 或 `IllegalInstructionError` | Warp executor → ISA decoder；需要的 PC/active mask/service 由输出声明，ISA 不依赖 Device mutable state。 |
| Evaluator | decoded op、explicit operands、最小 views/service responses | structured results/effects 或 fault/wait | Warp executor → ISA evaluator；只计算一条指令。 |
| Effect envelope/router | target identities、input active mask、instruction-defined write mask、typed effect payload | routed owner result、fault 或验证错误 | evaluator → router → one target owner；router 不保存第二份 state。 |
| Warp executor/manager | 四个既有 executor/owner refs、typed lifecycle/block、launch targets、单步 context、WSPAWN stage、WSYNC pending view、stable barrier key | dynamic admission/reclaim、round-robin selected warp、既有 Step result、slot lifecycle、atomic spawn/sync/barrier commit、Core/CTA completion | `FROZEN（T7/reusable-cta-lifecycle）`: Core协调CTA manager、canonical WarpState及scheduler；首次/复用PC、mask和mscratch只经State stage更新，正常reclaim检查lower owners并释放slot。 |
| CTA manager | 静态完整config，或dynamic Core提供的launch context与已选warp；LMEM capacity、finished observation、scheduler/barrier facts | detached context/membership/allocation/completion views、CTA-keyed LMEM route | `FROZEN（T6静态 + T7动态）`: 静态attach后seal；dynamic attach验证全部route且只经Core admission/reclaim。membership/metadata/allocation/LMEM bytes各只有一份真值，失败无partial owner state；normal Kernel completion由Device聚合，fault teardown仍待后续。 |
| Barrier coordinator | CTA + AddressWarp + 3-bit ID、request warp、arrive/wait/event/phase/count | canonical arrival/wait masks、participant/events/phase update、matching blocked/released warp keys、fault | `FROZEN（T6/cta-barrier-coordination）`: Warp effect → optimistic Barrier stage → coordinated WarpState commit → Core lifecycle；coordinator无Warp可写引用，Core不复制record。 |
| SIMT control owner | TMC/PRED/WSPAWN/SPLIT/JOIN/WSYNC effect、warp key | mask/PC/stack/lifecycle update result | effect router → Warp/SIMT owner；`FROZEN（T5/02）`跨owner WSPAWN及WSYNC wait由Core路由，仍不调用 ISA 或 Harness。 |
| Snapshot/result | 明确观察 scope | deterministic registers/CSR/memory/fault、CTA/barrier/completion data | `FROZEN（T6 CTA子范围）`: Harness → Core/CTA/barrier read-only observation；nested records/member slices均detach，不提供owner引用。Device/checker协议仍待闭合。 |
| Core Run/trace | finite step budget、逐step context、optional sink | complete/budget/blocked/deferred/trap/fault、attempt/retired/Last、CTA id、barrier transition、CTA completions及detached scheduler records | `FROZEN（T6 + T7 composition）`: Core.Run仍只说明当前resident；Kernel直接复用`Core.Step`并以`TraceRecordFromStep`组合同一detached observation，再由walker/pending/resident/lower-owner条件判定Kernel completion。 |

## 8. 范围与非目标

### 8.1 架构功能范围

`FROZEN`：目标是在上述冻结配置下，接收 Kernel、入口和数据，执行 frozen ISA/custom SIMT 路径，维护跨指令架构状态，并最终产生可与 RTL/reference checker 对照的结果。必须保留对结果有影响的控制流、lane mask、register/CSR、memory、CTA/SIMT、barrier、trap/fault 和 completion 语义；当前缺失的 comparison/ABI 规则以 `UNRESOLVED` 管理。

### 8.2 明确非目标

`FROZEN`：正确性不要求复刻 RTL 的 cycle accuracy、pipeline stages、cache timing/替换、hazard/scoreboard、stall/backpressure、arbiter 优先级、吞吐、latency、带宽、bank conflict、MSHR、issue/dispatch/commit queue、performance scheduling 或任何其他微架构行为。也不要求性能等价、波形等价或相同 warp 交错顺序，除非后续证据证明某种交错会改变架构可见结果；此时应冻结所需的最小功能 ordering，而不是复制整条 pipeline。

`FROZEN（T4 final scope）`：当前实现一个canonical四lane Warp的active-mask 32-bit fetch、普通 integer/FP/branch/jump/CSR/System/trap、同步 flat multi-lane data-memory、TMC/PRED、SPLIT/JOIN、VOTE/SHFL/WGATHER/packed load原子Step、跨分支连续bounded Run及可审计detached SIMT trace。WSPAWN/WSYNC/BAR仍保持 Core/多 Warp future-owner deferred边界；scheduler、CoreState/CTAState、Barrier coordinator、CTA/Kernel/host completion、最终global/LMEM scope与ordering以及cache/MMU/pipeline/cycle/timing/performance model均未实现。deferred/finished outcome和hand-built单Warp stream不能被解释为这些系统能力已经实现。

`FROZEN（T5/01 scope）`：当前还实现冻结单 Core 的四个既有 Warp slots、canonical lifecycle一致性校验、typed blocked/runnable/finished scheduler metadata、确定性round-robin单步选择、单 Warp zero-mask completion退出与相关-slot Core completion聚合。WSPAWN目标初始化/原子协调、WSYNC pending-work provider与恢复、BAR/CTA/Kernel/host completion、周期级scheduler、跨 Warp memory ordering及最终global/LMEM architecture仍未实现；本里程碑的 Core step顺序不得用于补定这些范围。

`FROZEN（T5/02 scope）`：当前进一步实现WSPAWN single-active gate、unused inactive target的PC/lane0/lifecycle/mscratch-only原子激活与source PC协调提交，以及WSYNC显式pending provider/view的block/retry/immediate completion。未冻结的target完整runtime/CTA/register初始化不补默认值，finished slot reuse保持拒绝且语义未决；BAR coordinator、CTA/Kernel/host completion、WSYNC/BAR跨Warp memory visibility、周期级scheduler及最终global/LMEM architecture仍未实现。

`FROZEN（T5 final scope）`：当前完整实现冻结单Core/四Warp功能模型：既有canonical WarpState/Warp.Step复用、四态Core lifecycle、deterministic round-robin、WSPAWN/WSYNC协调、有限Core Run、相关Warp completion与detached Multi-Warp trace。仍未实现CTA state/membership与调度、真实BAR coordinator/release、Kernel grid/CTA launch、Kernel/host completion、cache/MMU、scoreboard、pipeline/issue/commit timing或cycle-level scheduler；global/LMEM最终scope及冲突跨Warp memory ordering/atomicity仍为U-MEM-01/U-SCHED-01。本T5 Core completion只说明调用者显式提供/激活的slots全部zero-mask finished，不得冒充CTA、Kernel或device completion。

`FROZEN（T6/cta-context-lmem scope）`：当前在T5之上实现显式resident CTA canonical owner、membership/rank/thread coordinates和CTA CSR context、CTA-aware WSPAWN/Activate、固定高地址窗口及隔离allocation的LMEM byte owner/routing，并记录member finished observation。尚未实现真实BAR coordinator、blocked/resume release、Barrier-aware CTA completion、Kernel launch/grid scheduling/ABI、异常CTA teardown、跨Warp竞态memory ordering或cache/timing模型；这些后续范围不得由本子里程碑的allocation隔离或串行functional tests推断。

`FROZEN（T6/cta-barrier-coordination scope）`：当前进一步实现CTA-scoped 8-ID local barrier coordinator、BAR/BAR.ARRIVE/BAR.WAIT的LSU drain、participant/arrival/wait、最多32 events、phase reuse、scheduler block/resume及WarpState/coordinator原子提交。冻结single-Core的global bit确定性unsupported；event completion只由显式`CompleteBarrierEvent`输入，不猜异步memory pipeline。尚未闭合Barrier对global/LMEM竞态访问的visibility/order、global multi-Core网络、fault/early-exit waiter teardown、Barrier-aware CTA completion和Kernel/host completion。

`FROZEN（T6 final scope）`：T6闭合冻结single-Core、显式sealed resident CTA条件下的canonical context/membership、CTA/thread CSR、隔离LMEM、local barrier coordinator、block/release、正常CTA completion、detached trace/result及多Warp共享LMEM端到端执行。其当时保留的launch/grid walk、dynamic lifecycle和Kernel functional runner已由第5.23–5.26节闭合；未冻结任意startup寄存器初值ABI、host handshake、multi-Core/global barrier、fault/early-exit CTA teardown、跨Warp竞态memory visibility/order及微架构模型仍为后续范围。

`FROZEN（T7/launch-contract scope）`：当前新增runtime-independent `LaunchState`、冻结资源/overflow预校验和KMU两级三维grid walker。Runtime仍负责module/symbol/host args与memory staging；startup PC是首次Warp取指地址，kernel entry/parameter是独立CTA context；global backing memory仍归调用方。

`FROZEN（T7/reusable-cta-lifecycle scope）`：当前进一步实现同一single-Core Kernel内的dynamic CTA admission、正常完成回收、partial-Warp mask、WarpStep坐标、每派发mscratch参数、首次startup PC与复用dispatch-window PC，以及CTA隔离LMEM extent复用。其当时保留的自动walker→admission与Kernel completion/result现由第5.25节闭合。

`FROZEN（T7/kernel-execution-engine scope）`：当前实现runtime-independent、便捷`KernelExecutor.Run`每次fresh transient的single-Core Kernel functional executor，成组cluster backpressure循环、完整T1–T6 execution path、同一caller backing fetch/arguments/global output、严格lower-owner completion和detached typed result/trace；显式可续跑session由下一段最终范围冻结。仍不定义host command/completion handshake、任意GPR/FPR/CSR/LMEM初值ABI、fault/early-exit CTA teardown、multi-Core/global barrier、race ordering或cycle/cache/MMU/pipeline/performance；这些不得由normal Kernel completion推导。

`FROZEN（T7 final/kernel-e2e-contract scope）`：当前最终闭合真实startup→CTA entry dispatch、canonical mscratch parameter、caller backing image/input/output、超resident grid的dynamic reuse、六thread partial-Warp坐标与mask、CTA-local LMEM+Barrier及同owner event/budget续跑。便捷Run仍fresh，显式KernelExecution才保留一次launch的owners。Host runtime与CP、`vx_*`函数、ELF/vxbin/module/symbol、host argument/buffer staging、host/checker completion协议、任意未初始化ABI、fault teardown、multi-Core/global barrier、冲突memory ordering、cache/DRAM/MMU及pipeline/cycle/timing/performance仍非目标。

禁用扩展和可选加速器不是未来兼容性要求；T0 不为它们实现占位语义。

## 9. 关键 RTL 证据审计

本节只冻结允许输入中可直接复查的 RTL 事实。每行最后一列是由事实导出的 `PROVISIONAL` 功能模拟器 ownership/边界建议，不是 RTL 事实，也不承诺具体数据结构。路径均相对 `Vortex_rtl/`；未被这些证据唯一确定的语义进入第 10 节，而不以设计推导补空白。

| ID/主题 | `FROZEN` RTL 事实 | 具体证据 | 与事实分离的 `PROVISIONAL` 逻辑推导 |
| --- | --- | --- | --- |
| E-PC-01 PC/mask 与普通 branch | `VX_scheduler` 持有 `warp_pcs`、`thread_masks`、`active_warps` 和 `stalled_warps`；C 关闭时一次成功 schedule 写入 `PC + 4`，branch/trap/mret control 可另行重定向 PC 并解除该 warp stall。`VX_alu_int` 从 active mask 选 `last_tid`，形成一个 warp-wide `br_taken`/`br_dest`。 | `hw/rtl/core/VX_scheduler.sv`，module `VX_scheduler`，signals `warp_pcs`/`thread_masks`/`active_warps`/`stalled_warps`、`schedule_if_fire`、`branch_ctl_if`，以及 `VX_CFG_EXT_C_ENABLE` 路径；`hw/rtl/core/VX_alu_int.sv`，module `VX_alu_int`，signals `last_tid_r`、`br_taken`、`branch_ctl_if`。 | Warp 是 PC/mask/lifecycle 的唯一候选 owner；普通 branch 产生 warp PC effect，不隐式创建 divergence stack entry。 |
| E-SIMT-01 SPLIT/JOIN | `VX_wctl_unit` 用各 lane predicate 构造 `then_tmask`/`else_tmask` 和 `next_pc`；`VX_split_join` 仅在 divergent split 时驱动 `VX_ipdom_stack` push，JOIN pop 并返回 `join_tmask`/`join_pc`。scheduler 的 split 只在 `warp_ctl_if.split.is_dvg` 时换 mask，join 可同时换 PC/mask。 | `hw/rtl/core/VX_wctl_unit.sv`，module `VX_wctl_unit`，signals `then_tmask`/`else_tmask`、interface fields `split.next_pc`/`split.is_dvg`；`hw/rtl/core/VX_split_join.sv`，module `VX_split_join`，instance `VX_ipdom_stack`、signals `join_tmask`/`join_pc`；`hw/rtl/core/VX_scheduler.sv`，interfaces `warp_ctl_if.split` 与 join outputs。 | SPLIT/JOIN effect 交 Warp/SIMT owner 更新唯一 reconvergence state；它与普通 branch 的 PC effect 是不同控制边界。 |
| E-WSPAWN-01 | `VX_wctl_unit` 将小于 rs1 指定数量且不含当前 wid 的 warps 置入 spawn mask，目标 PC 来自 rs2。`VX_scheduler` 只在 `wspawn_valid && is_single_warp` 时激活目标 warp，令目标 `thread_masks[i][0] = 1`、写目标 PC，并显式复制 `mscratch_r`；此路径没有显示复制 GPR/FPR。 | `hw/rtl/core/VX_wctl_unit.sv`，module `VX_wctl_unit`，signal `wspawn_wmask`、interface fields `wspawn.wmask`/`wspawn.pc`；`hw/rtl/core/VX_scheduler.sv`，module `VX_scheduler`，signals `wspawn_valid`、`is_single_warp`、`thread_masks_n`、`warp_pcs_n`、`mscratch_r`。 | Core/Warp manager 路由目标 lifecycle/PC/mask effect，CSR owner 路由 mscratch effect；不得凭此猜测其他初始 context，见 U-WSPAWN-01。 |
| E-WSYNC-01 | `VX_wctl_unit` 在 WSYNC 遇到非空 `warp_pending_alm_empty` 条件时形成 `wsync_drain` 并压住 ready；drain 结束才发 `wsync_valid`。scheduler 收到该信号后解除 warp stall。 | `hw/rtl/core/VX_wctl_unit.sv`，module `VX_wctl_unit`，signals `warp_pending_alm_empty`、`wsync_drain`、`wsync_valid`、`execute_if.ready`; `hw/rtl/core/VX_scheduler.sv`，interface `warp_ctl_if.wsync_valid`。 | 功能模型只需要显式 architectural pending-work/order predicate 与 wait/release effect，不保存 pipeline drain 周期；精确可见性见 U-WSYNC-01。 |
| E-BAR-01 | `VX_wctl_unit`以`{rs1[AddressWarp],rs1[BAR_ID_SHIFT +: 3]}`形成物理地址，解出size/event/sync/global/arrive/phase，并等待`lsu_sched_drained`后提交。`VX_bar_unit`用indexed stores保存`mask_r`/`count_r`/`events_r`/`phase_r`，arrival count达标且events为零或最后event完成时返回unlock mask并翻phase；wait phase不同时立即unlock。冻结`VX_CFG_NUM_CORES=1`使`USE_GBAR=(NUM_CORES>1)`为假。 | `hw/rtl/core/VX_wctl_unit.sv`，module `VX_wctl_unit`，signals `bar_drain`/`lsu_sched_drained`/`wctl_bar_addr`、interface fields `bar.id`/`bar.size_m1`/`bar.is_event`/`bar.is_sync`/`bar.is_global`/`bar.is_arrive`/`bar.phase`；`hw/rtl/core/VX_bar_unit.sv`，module `VX_bar_unit`，localparam `USE_GBAR`、state `mask_r`/`count_r`/`events_r`/`phase_r`、instances `barrier_state_store`/`barrier_phase_store`；`hw/VX_config.vh` macros `VX_CFG_NUM_CORES`/`VX_CFG_MAX_BAR_EVENTS`。 | T6 functional owner把物理字段嵌入sealed CTA namespace，Warp lifecycle只存stable wait key；memory visibility和异常退出仍见U-BAR-01/U-MEM-01。 |
| E-CSR-01 | `VX_csr_data` 的 `fcsr` 按 wid 保存并累积 FP flags；CTA CSR 数据来自 scheduler/dispatcher context。trap/mret 相关的 `mscratch_r`、`mstatus_r`、`mtvec_r`、`mepc_r`、`mcause_r`、`mtval_r` 及恢复 mask 物理存于 scheduler 并由 `sched_csr_if`/branch control 更新。`VX_csr_unit` 以 lane/wid/CTA context 生成 thread/hart/CTA identity 和 CSR RMW。 | `hw/rtl/core/VX_csr_data.sv`，module `VX_csr_data`，state `fcsr`、interfaces `fpu_csr_if`/`sched_csr_if`；`hw/rtl/core/VX_scheduler.sv`，上述 `*_r` state 与 `sched_csr_if`；`hw/rtl/core/VX_csr_unit.sv`，module `VX_csr_unit`，lane context 与 `sched_csr_if.cta_tid`/`cta_csrs`。 | 使用一个按明确 scope/key 寻址的 CSR owner，scheduler/warp 仅消费 view/effect；RTL 分散存储不授权重复 canonical state。ISA manifest/effect 已见第 5.5 节，owner apply/reset 集成仍见 U-CSR-01。 |
| E-CTA-01 CTA context | `VX_cta_dispatch` 的 `cta_ctx_ram`、`cta_warp_ram`、`cta_id_per_warp_r` 保存 CTA 描述、warp membership 与 readback；输出包含 CTA id/rank/size、block/grid、entry、param 和 LMEM address。每个 warp done 递减 remaining-warp state，最后一个产生 `cta_done` 并释放 slot。 | `hw/rtl/core/VX_cta_dispatch.sv`，module `VX_cta_dispatch`，state `cta_ctx_ram`/`cta_warp_ram`/`cta_id_per_warp_r`/`slot_valid_r`、signals `cta_rd_csrs`/`cta_rd_tid`/`warp_done`/`cta_done`。 | CTA manager 是 context/membership 候选 owner，Warp/Lane 只持 key 和只读派生 view；初始化与异常终止规则仍见 U-CTA-01/U-ABI-01。 |
| E-IFETCH-01 取指路由 | C 关闭时 scheduler PC 直接成为 I-cache request 地址；`VX_fetch` 在 response 上把 `icache_bus_if.rsp_data.data` 送入 `fetch_if.data.instr`，请求属性为 read。 | `hw/rtl/core/VX_fetch.sv`，module `VX_fetch`，interfaces `schedule_if`/`icache_bus_if`/`fetch_if`、signal `fetch_if.data.instr`，macro `VX_CFG_EXT_C_ENABLE`; `hw/VX_config.vh` macro `VX_CFG_ICACHE_ENABLE`。 | Instruction source 读取 Memory 中同一 canonical byte space，返回 word/fault；取指服务不能自行推进 Warp PC，cache 内容不是架构 owner。 |
| E-MEM-01 global/local data 路由 | `VX_lsu_slice` 用 `VX_MEM_LMEM_BASE_ADDR` 和 LMEM size 产生每 lane `is_addr_local`。`VX_lmem_switch` 按 lane memory attribute 分出 `local_mask`/`global_mask`，可把同一 warp 的 lane 子集分别送往 `local_out_if`/`global_out_if`。`VX_mem_unit` 将 local 路径接 `VX_local_mem`，global 路径接 D-cache/coalescing 路径，socket 再汇入外部 memory hierarchy。 | `hw/rtl/core/VX_lsu_slice.sv`，module `VX_lsu_slice`，`mem_req_attr_struct[].is_addr_local`、macro `VX_MEM_LMEM_BASE_ADDR`；`hw/rtl/mem/VX_lmem_switch.sv`，module `VX_lmem_switch`，`MEM_ATTR_LOCAL_OFFS`、`local_mask`/`global_mask`、ports `local_out_if`/`global_out_if`；`hw/rtl/core/VX_mem_unit.sv`，module `VX_mem_unit`，instances `VX_lmem_switch`/`VX_local_mem`；`hw/rtl/VX_socket.sv`；`hw/VX_config.vh` macros `VX_CFG_LMEM_ENABLE`/`VX_CFG_DCACHE_ENABLE`。 | Memory service 显式按地址与 lane 拆分 requests，global bytes 与 scope-keyed LMEM bytes各只保留一个可写真值；LMEM 的准确 scope、范围、fault 与 ordering 见 U-MEM-01。 |
| E-COMP-01 warp/CTA completion | scheduler 将 TMC 的 `tmc_valid && tmask == 0` 识别为 `cta_warp_done` 并发送 `warp_done`；dispatcher 对 remaining warps 计数，最后一个 warp 触发 `cta_done`。 | `hw/rtl/core/VX_scheduler.sv`，module `VX_scheduler`，signal `cta_warp_done`、interface fields `warp_ctl_if.tmc_valid`/`warp_ctl_if.tmc.tmask`，以及 instance `cta_dispatcher` 的直接端口连接 `.warp_done(cta_warp_done)`；`hw/rtl/core/VX_cta_dispatch.sv`，module `VX_cta_dispatch`，input `warp_done` 与 signal `cta_done`。 | T6正常路径由Warp/Core/CTA owner单向聚合，并额外要求无blocked成员及pending local barrier，避免功能模型提前完成；其他termination/fault路径仍不能从该单一路径猜出，见U-CTA-01。 |
| E-KERNEL-01 kernel/device busy | `VX_kmu` 的 `running` 在 CTA launch 请求全部发出后即可撤销，因此它本身不是“全部 CTA 执行完成”；顶层 `Vortex` 的 `busy` 还聚合 `kmu_busy`、DCR request 和各 cluster busy。允许输入没有 host/checker 对该信号的完成判定协议。 | `hw/rtl/VX_kmu.sv`，module `VX_kmu`，signals `running`/`busy`/`raster_start_r` 与 CTA request accounting；`hw/rtl/Vortex.sv`，module `Vortex`，signals `kmu_busy`、`per_cluster_busy`、`dcr_bus_if.req_valid`、output `busy`。 | T7 functional complete聚合walker/pending/resident/Core/Warp/Barrier owners而不使用generation alone；对host如何消费result仍保持U-ABI-01。 |
| E-LAUNCH-01 KMU字段与grid walk | `VX_kmu`分别存储startup PC、kernel entry、parameter、grid/block dimensions、block size、warp step、LMEM和cluster dimensions；output分别广播PC/entry/param及64-byte aligned LMEM。walk先递增cluster内X/Y/Z offset，完整cluster后再递增X/Y/Z origin；zero grid不会启动。origin wrap使用`== grid_dim`。 | `hw/rtl/VX_kmu.sv:41-103,107-173,202-313`；`hw/VX_config.vh`的NUM_WARPS=4、NUM_THREADS=4、LMEM_LOG_SIZE=14、MEM_BLOCK_SIZE=64；`hw/rtl/core/VX_cta_dispatch.sv:158-190,220-303,385-483`。 | T7 public launch分别保留字段、预校验整除/容量/overflow，walker复现两级顺序；entry绝不替换startup。runtime transport、dynamic residency及completion不属于walker。 |
| E-CTA-REUSE-01 dynamic dispatch/reuse | Dispatcher以`~(active_warps|dispatched_warps)`优先选择空wid；block size单独产生warp count/partial mask，WarpStep三维推进warp base并为各lane沿X carry展开坐标。每kernel context的`warp_init_mask_r`决定首次`cta_PC`或复用`warp_pc-20`，每次`cta_fire`都写该CTA parameter到wid的`mscratch`；最后member done释放CTA slot。 | `hw/rtl/core/VX_cta_dispatch.sv:140-190,347-371,385-470,540-661`；`hw/rtl/core/VX_scheduler.sv:170-190,338-354`。 | T7 dynamic Core只选择空scheduler/CTA/LMEM slots；State owner stage复现PC/mask/mscratch，CTA owner保留entry/context并在normal lower-owner completion后回收。异常teardown仍未决。 |
| E-LANE-01 WGATHER mask 例外 | `VX_alu_int` 对 WGATHER 覆盖 result header 的 `tmask` 为每个 4-lane group 的 `~wg_src_mask`；因此写目标是组内所有非 source lanes，并不受输入 active mask 的一般规则限制。 | `hw/rtl/core/VX_alu_int.sv`，module `VX_alu_int`，WGATHER 路径 signal `wg_src_mask` 与 field `alu_hdr_in.tmask`。 | WGATHER evaluator 必须产生 instruction-defined explicit write mask，register owner 按该 effect 应用；不能用统一 active-mask 过滤器静默删掉这些写。 |

## 10. `UNRESOLVED` 登记表

T0 不猜测以下事项。每项都记录当前已知 RTL 事实，避免把“未知”误写成“没有行为”；同时记录缺失输入、受影响边界和必须闭合的最晚阶段。

| ID | `UNRESOLVED` 问题 | 已知 RTL 事实 | 缺失证据 | 受影响边界 | 最晚闭合阶段 |
| --- | --- | --- | --- | --- | --- |
| U-FAULT-01 | typed integer instruction-address/load/store alignment/access fault 如何映射到 trap cause/CSR，多个 lane 或不同 fault 同时出现时优先级是什么？ | Integer evaluator 已确定 fault effect 边界：fault 时不返回 PC/memory/partial register apply；alignment 先于显式 bounds view；memory owner 可返回 bounds/service access fault。RTL default/`x` 仍不是 trap priority oracle。 | reference checker、完整 trap cause/priority oracle 被排除。 | Effect router、Memory/CSR service、Warp trap/PC owner。 | fault effect 与 Warp trap/CSR 模型验收前。 |
| U-CSR-01 | 已冻结的 CSR/trap effects 如何 reset，并与 T1 以外的异步硬件写来源仲裁？ | T1 已闭合严格地址/scope/RMW与 synchronous trap/xRET effects；T2/01 建立唯一 owner，T2/02 已闭合单个 T1 bundle 内 CSR/FFLAGS/trap 的原子 apply，T2/03 已闭合真实 State 上连续 CSR RMW→trap entry→xRET 的单指令连接子范围。软件 FCSR 写按 RTL 覆盖先前 sticky flags；RTL 同周期 async hardware trap 可覆盖 software CSR write。 | async RTU/fault router 的跨来源 transaction、reset/launch policy 尚未实现。 | CSR owner、Warp trap/lifecycle、future fault/RTU router。 | 异步 trap/fault 与 launch 集成验收前。 |
| U-LOWER-01 | compiler/runtime 如何生成并约束 SPLIT/JOIN、PRED/TMC、BAR、WSPAWN、WSYNC 序列，合法lowering以外的stack under/overflow程序策略是什么？ | T1已闭合单指令effects，T2已闭合canonical state transition；T4已闭合hand-built单Warp stream中的TMC termination与合法SPLIT→JOIN mark→JOIN pop连续执行，并对capacity/stale transition确定性fault。 | `sw/`、compiler/runtime、Barrier/Core owner 与任意非法程序的语言/runtime处置策略被排除；T4的effect rejection不等同于软件ABI。 | SIMT owner、Barrier、Warp lifecycle、runtime lowering。 | compiler/runtime与Core/Barrier集成前。 |
| U-WSPAWN-01 | WSPAWN 的 target 之外，初始 registers/CSR/CTA context、finished slot reuse与软件使用协议的完整语义是什么？ | T1 effect 已冻结低编号非当前 target mask、lane0、PC、single-active-warp gate 和 mscratch copy；T5/02已对unused inactive targets原子应用该最小集合，RTL/实现均不复制 GPR/FPR。 | runtime lowering、目标 warp 预初始化、finished reuse与 CTA membership 协议缺失；当前拒绝finished target而不猜reuse。 | Core/Warp manager、register/CSR state、CTA membership。 | CTA/runtime lifecycle 集成前。 |
| U-WSYNC-01 | WSYNC pending provider最终如何组合异步owners，并保证何种 memory visibility/order？ | T1 已冻结显式pending-work view与wait/drain/release effect；T5/02已接入可注入provider/逐Step view并闭合block/retry/PC提交；T6 barrier block/release不改变该predicate或宣称flush。 | pipeline pending集合到异步memory/barrier owner组合及功能级ordering的最小映射、software convention缺失。 | Warp executor/lifecycle、Memory ordering、Barrier integration。 | memory visibility集成前。 |
| U-BAR-01 | global barrier、memory visibility及fault/early-exit waiter teardown是什么？ | T6已冻结sealed CTA namespace下的AddressWarp+8 ID、unique participants、0..32 events、phase reuse、local block/release、stale/atomic failure，并以pending record门控正常CTA completion；single-Core global请求确定性unsupported。 | RTL/runtime未给出异常Warp如何取消arrival/event或释放同CTA waiters，也无跨Core global路径与software/checker visibility协议。 | Barrier owner、CTA completion/fault、Memory ordering。 | fault lifecycle、multi-Core barrier或memory ordering集成前。 |
| U-CTA-01 | fault/early-exit时异常Warp/CTA teardown的完整规则是什么？ | T7已闭合normal walker→cluster admission/backpressure→existing execution→reclaim→strict Kernel completion及typed non-complete stop。 | fault/trap/permanent-blocked如何取消barrier、撤销pending effect并终止/回收CTA仍无RTL/runtime协议；当前Run停止且`Complete=false`，不擅自teardown。 | CTA manager、Warp lifecycle、Barrier、Device completion。 | fault lifecycle验收前。 |
| U-ABI-01 | 初始GPR/FPR/CSR/LMEM残留值、host completion handshake和checker观察协议是什么？ | T7已冻结runtime边界和functional Run：runtime负责staging；executor引用同一caller memory，返回detached typed result且output留在原memory；KMU running不代表complete。 | host/runtime/software如何消费result仍被排除；RTL未唯一规定任意冷启动register值、recycled LMEM清零或host handshake。 | Device lifecycle、Runtime adapter、公共result API。 | Runtime Integration验收前。 |
| U-CHK-01 | checker 比较哪些 registers/CSRs/memory ranges，如何处理 FP、fault、console和未初始化状态？ | RTL 提供架构状态更新路径和顶层输出，但不定义 comparison contract。 | reference checker 被刻意排除。 | Snapshot/result、fault model、端到端验收。 | 端到端 differential harness 验收前。 |
| U-MEM-01 | FENCE/BAR/WSYNC的跨warp可见性、竞态同址/重叠lane store优先级及self-modifying code规则是什么？ | T6已闭合固定LMEM虚拟窗口、warp→CTA-scoped backing翻译、同CTA共享/跨CTA同数值地址隔离、global分流及local/global/mixed atomic batch失败边界；RTL local/global physical paths与16KiB/64-byte配置一致。 | cache/LSU wiring不等于竞态ordering contract；软件/checker约定缺失。当前重叠batch原子拒绝而不猜lane顺序。 | Instruction source、Memory spaces、Barrier/ordering owners。 | Barrier visibility与冲突Memory ordering集成前。 |
| U-COUNT-01 | Counter owner 在非周期模型中如何推进显式 MCYCLE/MINSTRET view，checker 是否观察这些值？ | T1 已闭合 ISA read contract：44-bit Cycle/Instret explicit view 的 low/high halves；instruction MPM BASE windows 固定读零。 | State/execution 尚未提供推进 policy，checker visibility未知；cycle timing/performance仍是非目标。 | Counter owner、CSR service、snapshot。 | execution progress 与 checker contract 集成前。 |
| U-SCHED-01 | 多 warp 冲突 memory effects 需要何种最小确定性/ordering？ | RTL 可由 active/non-stalled warp调度并经 memory arbitration交错；T5/03只闭合无竞态functional test的deterministic round-robin/serial Step次序。 | software race policy和 checker observation未知；功能调用顺序不是hardware ordering证据。 | Runnable selection、Memory ordering、deterministic harness。 | 冲突多warp memory integration 前。 |

## 11. `RESOLVED` 审计记录

| 原 ID/闭合范围 | 决定 | RTL/实现证据 | 验证 | 闭合 Task |
| --- | --- | --- | --- | --- |
| U-ISA-01 / encoding 与 decoder | 冻结 decoder 采用显式 allowlist；D/FLEN64 泄漏和所有 disabled/reserved encoding 均为 illegal，不传播 RTL 的 `x`。fault effect/priority 分离为 U-FAULT-01。 | `VX_config.toml`；`hw/VX_config.vh`；`hw/rtl/core/VX_decode.sv:93-157,208-751`；`hw/rtl/VX_gpu_pkg.sv:249-533`；`isa/catalog.go`、`isa/decode.go`。 | `isa/decode_test.go` 对每个 entry 做正向可达验证，以字段约束 SAT 检查逐对不重叠，并覆盖 format/rm/width/reserved/disabled 反例；`scripts/verify-emu.sh`。 | T1 milestone `01-catalog-decode` |
| U-FAULT-01 / integer effect boundary | alignment、explicit bounds 和 memory-service failures 已成为 typed、无 mutation 的 fault effects；fault outcome 不携带 PC 或 partial register write。trap cause/CSR/跨 fault 优先级仍保留在 U-FAULT-01。 | 上述 ALU/LSU RTL；`isa/effects.go`、`isa/integer.go`。 | `TestBranchMisalignmentOnlyWhenTaken`、`TestMemoryInactiveAlignmentBoundsAndAddressWrap`、`TestMemoryOwnerIntegrationAndNoISAMutation`、`TestMemoryCompletionSuppressesPartialWriteOnFault`。 | T1 milestone `02-integer-memory` |
| U-MEM-01 / integer request-response boundary | integer load/store 的 address/width/signedness/byte mask、自然对齐、bounds view、load extension 和 store owner boundary 已闭合；global/LMEM scope与跨 warp ordering仍留在 U-MEM-01。 | `VX_lsu_agu.sv`、`VX_lsu_slice.sv`；`isa.MemoryRequest`/`MemoryResponse`。 | 每条 LB/LBU/LH/LHU/LW/SB/SH/SW functional case，加上真实 `support/memory` owner integration test；`scripts/verify-emu.sh`。 | T1 milestone `02-integer-memory` |
| U-FP-01 / RV32F 单指令与 FCSR effect | 冻结 S-format 的 raw-bit 数值、五种静态/dynamic rounding、NaN/min-max/compare/class、conversion saturation、active-lane flags OR、sticky FFLAGS 和 floating memory 边界；长期 FCSR 仍由外部 owner 唯一持有。端到端 checker observation 归 U-CHK-01，不再阻塞单指令语义。 | 上述 FPU RTL；`support/softfloat`；`isa/float.go`、`isa/effects.go`。 | `isa/float_test.go` 对每条 RV32F catalog 指令做 functional case，并覆盖 ±0/subnormal/∞/qNaN/sNaN、五类 exception、五种 static/dynamic rm、fused-vs-nonfused、conversion saturation、十类 FCLASS、memory owner/fault；`scripts/verify-emu.sh`。 | T1 milestone `03-rv32f` |
| U-CSR-01 / ISA 地址、RMW 与 trap-return | CSR manifest、读写/ignored mask、lane/warp/CTA scope、old-value result、unknown/readonly reject、ECALL/EBREAK cause/CSR writes/mask save 和统一 xRET redirect/restore 已闭合；不构造 privilege engine 或 canonical state。 | 上述 CSR/System RTL；`isa/csr_catalog.go`、`isa/system.go`、`isa/effects.go`。 | `isa/system_test.go` 覆盖 manifest 全地址、六种 RMW、identity/CTA/FCSR/config、trap causes、三类 return 与非法无 partial effect；`scripts/verify-emu.sh`。 | T1 milestone `04-csr-system` |
| U-COUNT-01 / ISA counter read | MCYCLE/MINSTRET 被定义为 owner 提供的显式 44-bit view，low/high read 精确截断；instruction-originated MPM BASE windows 固定零。计数推进与 checker 可观察性保留在 U-COUNT-01。 | `VX_gpu_pkg.sv:83`；`VX_csr_unit.sv:75-78`；`VX_csr_data.sv:21-27,236-263`；`isa.CounterView`。 | low/high 极值输入、MPM window 首尾地址与 unknown aliases 测试；`scripts/verify-emu.sh`。 | T1 milestone `04-csr-system` |
| U-LOWER-01 / custom 单指令语义 | TMC/PRED、SPLIT/JOIN、所有 cross-lane op 与 packed load 的四 lane单指令方程和 typed effects已闭合；compiler/runtime序列和 stack capacity policy仍保留在 U-LOWER-01。 | 上述 wctl/IPDOM/ALU/pack-load RTL；`isa/custom.go`、`isa/effects.go`。 | 全 custom catalog functional case，active/partial/empty masks、split uniform/divergent/negate、四 source WGATHER、shuffle fallback、packed assembly/fault；`scripts/verify-emu.sh`。 | T1 milestone `05-vortex-custom` |
| U-WSPAWN-01 / target 与 copy boundary | 冻结 target=`i<count && i!=wid`、PC、lane0 mask、single-active-warp apply条件及仅 mscratch copy；不创建/schedule warp且不猜 GPR/FPR/CTA context。 | `VX_wctl_unit.sv`、`VX_scheduler.sv`；`WarpSpawnEffect`。 | count 0/1/4/7、current-warp suppression、PC与 mscratch tests。 | T1 milestone `05-vortex-custom` |
| U-WSYNC-01 / 单指令 drain boundary | WSYNC消费显式 pending predicate并返回 typed wait/drain/release，不保存 pending pipeline/cycles；visibility policy继续保留。 | `VX_wctl_unit.sv`、`VX_scheduler.sv`；`WarpDrainEffect`。 | pending与drained两种 view tests。 | T1 milestone `05-vortex-custom` |
| U-BAR-01 / 单指令 request boundary | sync/async arrive/wait、expect-event、phase result与 LSU drain effect已闭合；expect_tx 强制 phase=1 并将零 count 解释为 32，barrier record、CTA namespace与release coordination继续保留。 | `VX_wctl_unit.sv`、`VX_bar_unit.sv`；`BarrierEffect`。 | sync/arrive/wait/expect_tx（含零值与偶数 count）及 phase/write-mask tests。 | T1 milestone `05-vortex-custom` |
| T1 ISA / 最终 coverage-contract | 105 条 frozen-enabled catalog entry、decode、四类 functional evaluator 与独立登记 vector 一一对应；公开 API 保持无长期状态和单指令边界，未来 canonical owners 与执行协调未被伪装闭合。 | `isa/catalog.go`、`isa/coverage_test.go`、`isa/contract_test.go` 及四类 evaluator/effect 实现。 | 三项最终 gate、全部逐指令 functional tests、`scripts/verify-emu.sh`；仓库内 RTL snapshot 只读。 | T1 milestone `06-coverage-contract` |
| T2 State/View / canonical ownership | Lane GPR/FPR、warp PC/mask/lifecycle、FCSR/trap CSR/saved mask 与三行 IPDOM stack 已由一个 `WarpState` 长期持有；四类 T1 input 来自 detached snapshot，非 T2 owner context 只按次复制。effect apply/route 另行闭合。 | `VX_gpu_pkg.sv:80-81`、`VX_ipdom_stack.sv`、`VX_scheduler.sv:53-64,245-255,311-398`、`VX_csr_data.sv:91-135`；`emu/state/state.go`、`emu/state/view.go`。 | `emu/state/state_test.go` 覆盖初始化拒绝、namespace/x0/inactive lane、PC/mask/CSR/FCSR、divergence、snapshot/context alias isolation；`scripts/verify-emu.sh`。 | T2 milestone `01-canonical-state-views` |
| T2 effect apply / 本地原子边界 | T1 Lane/Warp bundle 在 detached candidate 上完整预校验；本地状态一次提交，FFLAGS/软件 FCSR 按 RTL 顺序仲裁；future owner effects 保真转交并在外部成功前门控本地 commit，stale stage 不可提交。异步 trap/reset priority 仍留在 U-CSR-01。 | `VX_csr_data.sv:107-120`、`VX_scheduler.sv:245-255,358-398`、`VX_ipdom_stack.sv:39-95`；`emu/state/apply.go`。 | `emu/state/apply_test.go` 覆盖 GPR/FPR+PC、CSR/flags、TMC/PRED、SPLIT/JOIN、trap、x0/inactive/WGATHER、失败回滚、全类 forwarding/external gate/stale stage；`scripts/verify-emu.sh`。 | T2 milestone `02-atomic-effect-apply` |
| U-CSR-01 / T2 synchronous State integration | canonical FCSR、T1 可写 warp CSR、saved mask 与 PC 已接入无状态 System/RV32F evaluator；CSR RMW、sticky FFLAGS、synchronous trap entry 与 xRET 可跨独立单指令调用持续并原子提交。异步 fault/RTU 仲裁及 reset/launch policy仍保留在 U-CSR-01。 | E-CSR-01；`emu/state/state.go`、`emu/state/view.go`、`emu/state/apply.go`、`emu/state/integration.go`。 | `TestExecuteSingleRV32FUpdatesFPRFlagsAndPC`、`TestExecuteSingleCSRRMWTrapEntryAndReturn`、fault completion rollback；`scripts/verify-emu.sh`。 | T2 milestone `03-isa-state-integration-contract` |
| U-LOWER-01 / T2 local SIMT State integration | TMC/PRED active mask 与 SPLIT/JOIN 三行 IPDOM state 已接入单指令 View/Evaluate/Apply；合法 SPLIT→JOIN mark→JOIN pop 跨调用保持 record、pointer、mask 与 PC。BAR/WSPAWN 只转交未来 owner，不虚构 runtime lowering 或 lifecycle coordination。 | E-SIMT-01、E-WSPAWN-01、E-BAR-01；`emu/state/integration.go`。 | `TestExecuteSingleTMCAndPredicateMasks`、`TestExecuteSingleSplitAndTwoJoinCallsPersistDivergence`、`TestExecuteSinglePreservesFutureOwnerEffectsWithoutLocalMutation`；`scripts/verify-emu.sh`。 | T2 milestone `03-isa-state-integration-contract` |
| T2 ISA→State / 单指令连接边界 | caller-supplied word 经过 Decode/View/T1 Evaluate/atomic Apply；memory responses 经 T1 completion helper进入同一 apply。State 跨调用持久、ISA 无状态，illegal/fault 无 partial mutation，future owner effects 不执行或丢失；不建立 fetch loop、Warp.Step 或 scheduler。 | `emu/state/integration.go`；既有 `isa` 单指令 API 与 T2 State/View/apply。 | `emu/state/integration_test.go` 覆盖 ALU/FP/branch/jump/SIMT/CSR/trap/memory及 owner isolation/rollback/forwarding；`scripts/verify-emu.sh`。 | T2 milestone `03-isa-state-integration-contract` |
| T3 single-lane fetch/Step core | canonical PC 经共享 InstructionSource 精确读取 4-byte little-endian word，复用 Decode、detached State view/T1 evaluator及 T2 stage/commit完成 non-memory single-lane instruction；typed outcome区分 retired/trap/fault/deferred/finished，非法 multi-lane 与 future-owner/custom effect均不产生本步 partial commit。 | E-IFETCH-01、E-PC-01；`emu/warp/warp.go`、`emu/state/integration.go`。 | `emu/warp/warp_test.go` 覆盖 fetch、PC/dependency、branch/jump、M/Zicond/FP/CSR/trap、fault/rollback、finished与 deferred；`scripts/verify-emu.sh`。 | T3 milestone `01-fetch-step-core` |
| T3 atomic memory Step | 同一 MemoryService提供 fetch/data canonical bytes；integer/FP/packed request经 T1 completion与T2 stage提交，store由 pre-stale-check→单次all-or-error Write→infallible state replacement协调 memory与PC，FENCE在同步 owner下显式完成。fault不猜测 trap mapping，flat adapter不冒充最终 memory architecture。 | E-IFETCH-01、E-MEM-01；`emu/warp/warp.go`、`emu/state/apply.go`、`emu/state/integration.go`、`support/memory/memory.go`。 | `emu/warp/memory_test.go` 覆盖宽度/符号、store→load→ALU、FLW→FP→FSW、packed、shared bytes、misalign/bounds/service fault、stale pre-write rejection、单次 Write及FENCE；`emu/state/apply_test.go` 覆盖 coordinated success/failure/stale；`scripts/verify-emu.sh`。 | T3 milestone `02-atomic-memory-step` |
| T3 bounded Run/trace contract | Run仅重复Step且不保存PC；显式budget确定性终止retired loop，其他Step outcome提前停止；finished仅来自canonical inactive。可选trace按step返回detached WarpID/PC/raw/decode/effect/nextPC/outcome，不改变执行。 | E-PC-01；`emu/warp/run.go`及第5.11-5.12既有边界。 | `emu/warp/run_test.go` 覆盖zero/exact/early budget、self-loop、全类别stream、fault/finished及trace完整性/隔离；全部T1/T2/T3 tests、vet、gofmt与`git diff --check`。 | T3 milestone `03-run-trace-contract` |
| T4 ordinary four-lane Step / atomic memory | 任意合法非空active mask复用T1 evaluator和T2 stage/commit完成普通integer/FP/control/CSR/trap；最高active lane决定warp-wide branch。multi-lane integer/FP/packed memory强制完整response coverage，store经可选atomic batch owner与State协调一次提交；fault/stale不遗留partial state或bytes，重叠store优先级仍属U-MEM-01并原子拒绝。 | E-PC-01、E-MEM-01；`emu/warp/warp.go`、`support/memory/memory.go`、既有`state`/`isa`边界。 | `emu/warp/warp_test.go`覆盖full/partial mask、inactive integer/FP/FFLAGS、branch decision；`emu/warp/memory_test.go`覆盖multi-lane integer/FLW/FSW/packed、fault/batch rollback/stale；`support/memory/memory_test.go`覆盖batch prevalidation；`scripts/verify-emu.sh`。 | T4 milestone `01-four-lane-execution` |
| T4 Warp-local SIMT/custom Step | CategoryCustom不再统一deferred；TMC/PRED、SPLIT/JOIN、VOTE/SHFL/WGATHER以现有T1 effects和T2 Warp/Lane owner原子提交，合法split→join mark→join pop可在Run中连续reconverge。WGATHER保留non-source write mask例外；WSPAWN/WSYNC/BAR仍因forwarded prerequisite整体deferred。capacity/stale transition无partial mutation。 | E-SIMT-01、E-WSPAWN-01、E-BAR-01；`emu/warp/warp.go`、`emu/state/view.go`、`emu/state/apply.go`、`isa/custom.go`。 | `emu/warp/custom_test.go`覆盖mask/lifecycle、uniform/divergent stack、全部vote/shuffle、inactive fallback、WGATHER、future-owner isolation、capacity/stale及Run trace；`emu/warp/memory_test.go`覆盖multi-lane packed success/element fault；`scripts/verify-emu.sh`。 | T4 milestone `02-warp-simt-custom` |
| U-LOWER-01 / T4合法单Warp连续stream子范围 | hand-built四lane stream可把普通integer/FP/memory、mask control、SPLIT/JOIN两条路径、cross-lane和零masktermination连续组合；路径各执行一次并恢复空stack。compiler/runtime生成规则、跨Warp custom和非法程序策略仍留在U-LOWER-01。 | E-SIMT-01、E-LANE-01、E-COMP-01；`warp.Warp.Step/Run`只消费既有ISA effects和canonical State。 | `emu/warp/simt_stream_test.go`精确验证18 attempts/17 retired/finished、逐lane GPR/FPR/memory、PC/mask/lifecycle/stack、SIMT trace与mutation isolation；future-owner Run定向停止；`scripts/verify-emu.sh`。 | T4 milestone `03-simt-stream-contract` |
| T4 final trace/failure contract | 每个attempt的detached observation包含起止PC/mask/lifecycle/divergence pointer、decode、issued/completed effects和outcome；sink mutation不能改变Run或owners。memory/illegal/stack validation failure均无本指令partial mutation。 | `emu/warp/run.go`、`emu/warp/warp.go`、T2 detached stage与T4 atomic memory owner。 | `emu/warp/simt_stream_test.go`、`emu/warp/memory_test.go`、`emu/warp/custom_test.go`、既有`emu/warp/run_test.go`；完整离线门禁。 | T4 milestone `03-simt-stream-contract` |
| T5 Core/Warp manager | 固定四 slot 严格覆盖 wid 0..3并引用既有 Warp/WarpState；Core metadata区分inactive/runnable/blocked/finished及typed reason且持续校验canonical running/mask。每个Core.Step以functional round-robin只推进一个runnable Warp，跳过其他状态；zero-mask completion立即退出且不影响其他Warp，Core只聚合participated slots。 | E-PC-01、E-COMP-01；`emu/core/core.go`、`warp.Warp.CanonicalState`与既有`Warp.Step`。round-robin不冒充RTL arbitration/timing，跨Warp ordering仍属U-SCHED-01/U-MEM-01。 | `emu/core/core_test.go`覆盖topology/ID、四态/block-resume、fairness/skip、全canonical state隔离、completion继续执行、deferred/fault不自旋；`scripts/verify-emu.sh`。 | T5 milestone `01-core-warp-manager` |
| T5 WSPAWN/WSYNC coordination | Core保真消费typed effects；WSPAWN校验count/targets/gate/PC/lane0/mscratch并以source+all-target stage一次提交，只改RTL确认字段；WSYNC由显式pending provider/view保持PC、block并在clear后重试提交。BAR仍BlockBarrier，无临时coordinator。 | E-WSPAWN-01、E-WSYNC-01、E-BAR-01；`emu/core/core.go`、`emu/state/spawn.go`及既有`Warp.Step`/`EffectStage`。runtime初始化、finished reuse、visibility仍属U-WSPAWN-01/U-WSYNC-01/U-MEM-01。 | `emu/core/core_test.go`覆盖count/targets、single gate retry、state保留、target mutation failure、WSYNC immediate/pending/resume与BAR；`emu/state/spawn_test.go`覆盖target validation及source/target stale原子失败；`scripts/verify-emu.sh`。 | T5 milestone `02-wspawn-wsync-coordination` |
| U-SCHED-01 / T5 deterministic non-conflicting Run | 有限Core.Run反复round-robin选择并串行调用一个Warp.Step；明确区分complete/budget/blocked/deferred/trap/fault，相关slot completion与detached trace已闭合。该决定只用于无冲突功能执行，冲突memory ordering仍留在U-SCHED-01/U-MEM-01。 | E-PC-01、E-COMP-01；`emu/core/run.go`、`emu/core/core.go`、`warp.DetachResult`及T5/01–02 owner边界。 | `emu/core/run_test.go`覆盖全部outcomes、blocked非complete、trace选择/lifecycle/effect mutation isolation以及WSPAWN→round-robin→SPLIT/JOIN→stores→WSYNC resume→all-finished端到端；完整门禁。 | T5 milestone `03-core-run-trace-contract` |
| U-CTA-01 / T6 canonical context与membership子范围 | 显式resident CTA admission一次建立唯一metadata/member/rank/thread-coordinate/allocation真值；Core attach要求每个member的data route绑定同wid与exact manager，之后按selected wid覆盖CTA view；WSPAWN source∪targets必须精确等于同CTA membership；member finished仅作为后续completion输入。launch/partial-warp/fault teardown仍未闭合。 | `VX_cta_dispatch.sv` context/warp tables、thread index progression、reverse lookup、remaining-warp；`emu/core/cta.go`、`emu/core/core.go`、`warp.DataMemoryService`。 | `emu/core/cta_test.go`覆盖admission失败原子性、detached view、CTA/thread/LMEM CSR、caller override隔离、missing/different-manager route拒绝、cross-CTA WSPAWN rollback；完整门禁。 | T6 milestone `cta-context-lmem` |
| U-MEM-01 / T6 CTA LMEM scope子范围 | 固定`0xffff0000..0xffff3fff`是每个CTA共同的虚拟窗口；唯一16KiB owner以warp membership翻译至64-byte对齐互斥backing allocation。同CTA共享、不同CTA同数值地址隔离，窗口外global分流，mixed batch以external-success→infallible-local协调，跨窗口/overlap/越界整批拒绝。竞态ordering与BAR/FENCE/WSYNC visibility仍未闭合。 | `VX_config.vh` LMEM_LOG_SIZE/MEM_BLOCK_SIZE；`VX_lsu_slice.sv` local range；`VX_cta_dispatch.sv` allocation/lmem_addr；`emu/core/cta.go`、`warp.NewWithServices`。 | `emu/core/cta_test.go`覆盖direct与真实Warp SW/LW共享、同数值地址CTA隔离、global/mixed route、multi-lane invalid rollback、batch和窗口边界；既有T1–T5 memory tests；完整门禁。 | T6 milestone `cta-context-lmem` |
| U-BAR-01 / T6 CTA local barrier子范围 | sealed CTA + AddressWarp + 3-bit ID唯一寻址canonical arrival/wait masks、participant count、0..32 events及phase；sync/arrive/wait在LSU drain后原子接入Warp PC/register和scheduler，matching key release且不重复arrival；公共Block/Resume不能创建或解除barrier lifecycle。single-Core global、memory visibility和fault teardown仍未闭合。 | E-BAR-01；`emu/core/barrier.go`、`emu/core/core.go`、`state.BarrierPhaseView`、`EffectStage.CommitForwardedWithExternal`。 | `emu/core/barrier_test.go`覆盖sync/arrive/wait、canonical phase、先wait后arrive、立即通过、多waiters、expect/completion、8 IDs、跨CTA、overflow/underflow、duplicate/count/stale/global failure、LSU retry及waiter/drainer Resume绕过拒绝；`emu/state/apply_test.go`覆盖external callback前stale gate；完整门禁。 | T6 milestone `cta-barrier-coordination` |
| U-CTA-01 / T6正常CTA completion与端到端子范围 | CTA manager聚合自身明确成员的finished observation、Core slot lifecycle/block和同owner Barrier pending view；全成员正常finished且无blocked/pending barrier才完成。Run在全waiter场景报告blocked，release后从下一PC继续；不同CTA相同barrier ID与完全相同数值LMEM地址保持隔离。Kernel launch、异常teardown与Kernel/device completion仍未闭合。 | E-COMP-01、E-CTA-01、E-BAR-01；`emu/core/cta.go`、`emu/core/barrier.go`、`emu/core/core.go`、`emu/core/run.go`。 | `emu/core/completion_test.go`覆盖pending event、全waiter release、resident CTA独立聚合与snapshot隔离；`emu/core/cta_e2e_test.go`覆盖六thread partial lanes、CTA CSR→LMEM write→双phase BAR→cross-warp read→termination、双CTA同ID/同虚拟地址隔离及trace mutation；完整门禁。 | T6 milestone `cta-completion-e2e` |
| U-ABI-01/U-CTA-01 / T7 hardware-visible launch与CTA generation子范围 | Runtime与executor的launch职责已分离；startup PC、kernel entry、parameter及KMU其余字段保持独立，资源/乘法/count/traversability在generation state前一次校验；empty grid无CTA，非空grid严格按cluster intra后origin的X→Y→Z顺序生成。动态normal residency随后由下一行闭合；任意startup state、异常teardown和host completion仍保留。 | E-LAUNCH-01、E-KERNEL-01；`emu/device/launch.go`。 | `emu/device/launch_test.go`覆盖字段隔离、精确order/唯一性、empty grid、alignment/field width、LMEM/warp/cluster capacity、non-divisible grid、overflow、边界与派生一致性；`scripts/verify-emu.sh`。 | T7 milestone `launch-contract` |
| U-CTA-01 / T7 dynamic normal CTA lifecycle子范围 | Dynamic manager以Core协调的epoch stage原子选择空Warp/CTA/LMEM slots；BlockSize与WarpStep分别产生member mask/coordinates；State owner按per-kernel first-use或PC-20 reuse规则更新PC/mask/mscratch。只有normally complete且无blocked/barrier/pending work的CTA可回收，membership、scheduler及LMEM extent同步释放；fault teardown仍保留。 | E-CTA-REUSE-01；`emu/state/launch.go`、`emu/core/cta.go`、`emu/core/core.go`、`emu/device/launch.go`。 | `emu/state/launch_test.go`与`emu/core/dynamic_cta_test.go`覆盖首次/复用、partial mask、WarpStep各轴carry、mscratch、并发/乱序、资源/early/pending拒绝原子性、LMEM隔离与extent复用；完整T1–T6门禁。 | T7 milestone `reusable-cta-lifecycle` |
| U-CTA-01/U-ABI-01 / T7 normal Kernel functional execution子范围 | 便捷Run以fresh owners和完整cluster transaction连接walker→Core/CTA→existing Warp/ISA/effect/memory→normal reclaim；exact caller backing同时供fetch/arguments/global output。Complete严格聚合generation、pending/resident及lower owners；五类非正常stop均typed且非完成，result/trace detach且无memory副本。显式session continuation由下一行闭合；host handshake与fault teardown仍保留。 | E-KERNEL-01、E-CTA-REUSE-01及T1–T6执行证据；`emu/device/kernel.go`、`emu/core/core.go`、`emu/core/run.go`。 | `emu/device/kernel_test.go`覆盖多cluster backpressure/reuse、真实external memory程序、事件/Core trace隔离、empty complete及budget/blocked/deferred/trap/fault；cluster batch rollback；完整门禁。 | T7 milestone `kernel-execution-engine` |
| U-ABI-01/U-CTA-01 / T7 final Kernel E2E子范围 | 非entry startup执行marker并经CTA entry CSR/mscratch dispatch window调用body；三个六thread CTA在两CTA容量上回收复用，LMEM+local barrier后18个active threads只向exact caller backing写一次。显式session在grid exhausted/pending barrier events及budget stop后保留同一owners并可继续；invalid/fault/trace mutation不污染memory或fresh后续执行。host runtime/checker、未初始化ABI与异常teardown仍保留。 | E-LAUNCH-01、E-CTA-REUSE-01、E-KERNEL-01及T1–T6 CSR/LMEM/Barrier/ISA路径；`emu/device/kernel.go`。 | `emu/device/kernel_e2e_contract_test.go`覆盖startup≠entry差异、parameter/input/output、超容量grid/BlockID唯一性、partial mask/WarpStep/CSR、LMEM/barrier/inactive lane、same-session release、empty/budget/invalid/fault及nested trace隔离；完整离线门禁。 | T7 milestone `kernel-e2e-contract` |

## 12. T0 最终覆盖与范围审计

### 12.1 八类交付要求覆盖

| T0 要求类别 | Contract 对应章节 | 审计结论 |
| --- | --- | --- |
| 1. 模拟器层级、模块与单向依赖 | 第 4.1 节 | Device → Core → CTA → Warp → Lane → ISA 主链及 Memory/CSR/Barrier 服务挂靠已定义，具体代码组织保持 `PROVISIONAL`。 |
| 2. 跨指令长期状态与唯一 owner | 第 4.2 节 | GPR/FPR、PC/mask、lifecycle、SIMT、context、CSR、barrier、memory、completion 均已登记；证据不足者是 `UNRESOLVED owner candidate`。 |
| 3. ISA decode/evaluate 边界 | 第 5.1、5.7、5.10–5.26 节 | ISA 只读最小 view、产生显式 effect，不持有长期状态或调度上层；最终自动门禁要求 105 条 catalog/decode/evaluator/vector 一一对应，T2–T7 连接证明真实 State/CTA/barrier phase view 只经该边界调用 ISA。 |
| 4. 指令类别、effect 与 control owner | 第 5.2 节 | 标准指令、System、全部冻结 custom SIMT/lane 指令、fetch 与 completion 已映射。 |
| 5. 功能执行流程 | 第 6 节 | launch/cluster admission → select/fetch/decode/evaluate/effect/owner update → CTA reclaim → strict Kernel completion 已分责并实现。 |
| 6. 后续公共接口职责 | 第 7 节 | T1–T6既有边界继续冻结；T7已冻结LaunchState、KMU walker、dynamic lifecycle及caller-memory Kernel functional Run/result/trace。host handshake、global barrier、fault teardown及冲突memory ordering仍保持`PROVISIONAL`/`UNRESOLVED`。 |
| 7. RTL 调查、证据与待决问题 | 第 9、10、11 节 | 可确认事实有 path/module/signal/macro 依据，设计推导分栏；未知项有事实、缺口、影响边界和 deadline；T0 不伪造 `RESOLVED`。 |
| 8. 功能范围与非目标 | 第 3、8 节 | 冻结配置/ISA 范围与 cycle/pipeline/cache timing/hazard/throughput/performance scheduling 等非目标明确分离。 |

### 12.2 治理与过度设计审计

- 状态标签及迁移门槛在第 2 节；`FROZEN` 事实、`PROVISIONAL` 设计、`UNRESOLVED` 缺口和未来 `RESOLVED` 记录没有互相冒充。
- 唯一 canonical owner 原则在第 4.2 节；control/effect 只能经第 5 节边界交给 owner，不允许反向依赖、全局后门或第二可写真值。
- 维护协议在第 1、13 节；后续任务开始前读取、结束后与代码同步更新。
- 第 5.1、8.2 节明确不预定 Go concrete types、transaction/rollback/commit 算法、调度策略或微架构行为，满足无过度设计要求。
- 原始 T0 基线交付仅修改本文件；T1–T5依次新增无状态ISA、canonical Warp State/apply、fetch/memory Run、四lane SIMT与Multi-Warp Core。T6新增canonical CTA/LMEM/barrier/completion；T7当前新增runtime-independent LaunchState、KMU walker、dynamic cluster admission/reclaim、Warp/LMEM reuse、同一caller backing上的完整Kernel functional Run/result/trace、可续跑session及真实startup/entry E2E。仍未冻结任意未初始化ABI、host runtime/CP/`vx_*`/vxbin与command/completion handshake、multi-Core/global barrier、fault teardown、冲突memory ordering及cache/DRAM/MMU/scoreboard/pipeline/cycle/timing/performance model。仓库内`Vortex_rtl`始终作为只读证据输入。

## 13. 维护检查单

每个后续 Task 完成前必须确认：

- 是否读过本 Contract，且实现未违反任何 `FROZEN` 项；
- 是否发现新的 RTL 事实、`UNRESOLVED` 问题或已可闭合的条目；
- 是否改变 canonical state ownership、模块依赖、公共接口或 effect/control 边界；
- CTA completion是否仍只聚合明确成员的正常finished状态，并同时拒绝blocked成员或任何pending barrier record；
- Kernel completion是否仍同时要求walker、pending cluster、resident CTA、CTA/barrier records与全部Core/Warp slots无工作，而非只观察generation exhausted；
- Device是否仍把exact caller backing用于fetch/global访问且result/trace不含memory副本；
- 公共lifecycle API是否仍拒绝创建/解除`BlockBarrier`，waiter release与LSU drain retry是否只走各自canonical owner路径；
- 新增trace/result字段及其嵌套slice/pointer是否完整detach，恶意观察者能否反写owner；
- 是否把实现验证得到的设计从 `PROVISIONAL` 正确升级，或在失败时回退；
- 是否只模拟架构功能语义，没有把 pipeline/cache/hazard/timing 偶然带入正确性契约；
- 文档与代码是否在同一 Task 同步更新。

## 14. T8 静态 Timing IR 衔接（timing-baseline）

`PROVISIONAL（T8/timing-baseline）`：新增独立的 [Timing IR 入口](../../timing/README.md)、[结构解释](../../timing/architecture.md) 与 [结构化基线](../../timing/ir.yaml)。本阶段已按冻结配置与实际 RTL 实例登记流水节点、资源、端口、缓冲和控制/访存反馈；这些属于独立 timing 证据，不改变本文的功能正确性范围、canonical state owner 或已有 `RESOLVED` 审计。

`PROVISIONAL`：后续可复用无状态 ISA decode/evaluate/completion 和最小 detached view 原则；现有 `Warp.Step`、同步 MemoryService、全 before-image `EffectStage` 原子替换及 Core round-robin 不能直接解释为重叠流水的执行/可见顺序。这是 T8 的衔接基线；Task9 周期组件与 effect 可见性实现见文末，真实多 Warp Scheduler 仍不在当前范围。

`UNRESOLVED`：Timing IR 的 `u-build/u-execution/u-memory/u-feedback/u-visibility` 分别跟踪外部构建轴、执行路径详细时序、memory 服务及内部队列、完整同周期反馈表和功能 effect 适配。它们不替代第 10 节的 fault、ordering、ABI 或 checker 问题；结构化事实和时序参数只维护于 Timing IR，不在本功能契约重复维护。

`PROVISIONAL（T8/timing-rules）`：静态 Timing IR 现补充 [周期规则说明](../../timing/rules.md) 与具备条件、单位、起止事件的 YAML 定量条目。底层队列、串行/流水执行、局部反馈与 cache bypass 参数的源级求值不改变功能原子 effect 契约；仍未实现周期推进。此次发现 CSR 背压写使能与混合 local/global 子集握手的源码疑点，分别登记于 Timing IR 的 `u-csr-stall` 和 `u-mixed-split`，不把未验证行为补入本功能语义。

`PROVISIONAL（T8/timing-integration-validation）`：[功能衔接设计](../../timing/integration.md) 按实际 isa、state、warp、core、device 与 support 实现记录复用和适配边界。功能计算、时序结果就绪、架构可见事件分开，唯一 owner 不变；同步 Warp.Step、memory completion 和全 before-image 原子 effect stage 不能直接充当重叠流水。本文仍是功能语义、owner 与既有未决/RESOLVED 审计的权威来源；timing YAML 的 functional_refs 只索引相关问题，不宣称闭合它们。

T8 维护检查：结构参数、周期规则与来源更新 timing/ir.yaml；软件职责候选更新 timing/integration.md；若未来改变功能 owner/effect 契约，必须同步本文，不能只改时序文档。`bash scripts/verify-timing.sh` 使用已有离线 YAML 依赖检查结构及无效样例，`bash scripts/verify-all.sh` 保留 RTL 完整性及功能回归职责。T8 没有改写冻结 RTL 或现有功能语义实现，也没有实现周期执行器、Scheduler、cache 模型或 RTLSIM 对齐。

## 15. T9 周期组件增量（cycle-components，未完成）

`PROVISIONAL`：`timing/model` 开始实现独立的 transient 缓冲状态与 Akita 公共边沿驱动，使用 `timing/ir.yaml` 的稳定 boundary ID 读取参数。Token 只含值身份，没有 canonical state 或 memory 引用；Evaluate/CommitEdge/Flush 不调用 ISA、Warp.Step 或 State apply，不改变既有唯一 owner 和原子功能契约。第一轮先完成缓冲级容量/背压/注册边界验证；后续已补齐完整组件、功能连接与效果可见性，见 [实施记录](../../timing/implementation.md)。Timing 未决项继续保留；新增 transient flush 不意味着架构 rollback 或外部请求取消已获得语义定义。

`PROVISIONAL（第二轮组件增量）`：新增 Fetch、直通 DecodeToken、packed Sequencer、单 Warp Issue、Collector、整数/显式 STD 执行资源、WaitPool、CSR context 与 Commit 组件。DecodeToken 调用既有严格 ISA Decode，仅生成 timing route/source IDs；其余组件推进不计算或交付功能效果。Collector 的 read observation、CSR request window、WB 和 registered pending 保持不同边界，后续已补齐完整资源图与 functional adapter。没有改变任何 canonical owner、ISA 功能方程或既有 EffectStage 契约。

T9 execution-effects 基础 API：`OperandCapture` 保存 detached 逐源读事件，和原 `WarpSnapshot.Evaluate` 共用 evaluator 分派；`EffectDelivery` 只保留 effects/receipt，每个明确事件从 live owner 创建并同步提交新的 `StageEffects`。旧原子 API 和 stale 检查保留。外部 owner 成功仍遵守既有 all-or-error 契约。周期事件绑定、epoch 校验及 memory/packed completion 尚待后续适配，详见 timing/implementation.md。

Task9 memory 增量：`RegisterWriteEffect.ByteMask` 为可选的 lane 内字节选择，0 保持既有全字写入语义。非零 mask 只能使用低四位，由 StageEffects 在当前 owner 值上合并；旧事务 stale 检查不变。无状态 `CompletePackedLoadPart` 与 `CompletePackedLoad` 共享校验/组装代码，前者只接受指定 element 的 lane 覆盖并不产生 PC effect，后者原完整语义保持不变。

Task9 WSPAWN 控制交付使用 `EffectDelivery.DeliverWarpSpawn`，在当前可见事件构造 source control stage 并立即调用原 `StageWarpSpawn/Commit`。source 与 targets 在同一事务提交，沿用旧 target Expected、source MScratch/lifecycle 和双侧 stale 校验；只有成功才记 control receipt。此接口不创建 Core slot 或 CTA owner，也不跨周期保存可替换整状态的候选。

### Task9 程序运行器增量

`timing/runner` 复用 Core/effects 和原 state/memory owner，以单活动指令排空策略从 canonical PC 自动取指；取指与数据服务均采用显式正延迟，不代表 cache 时间。Akita Run 支持预算续跑，Flush 清除在途服务并增加 epoch，不回滚已可见效果。条件见 `r-software-program-runner`。

本地入口：在仓库根目录 `source env/env.sh` 后运行 `go run ./cmd/timing-run`；`-trace` 输出逐周期 JSON，`-program file.bin` 从 0x100 加载 raw little-endian RV32 镜像。默认四 lane、x1=64+8*lane，内建示例验证依赖 ADDI/MUL/DIV、store/load、分支跳过指令与 TMC 结束。显式 STD、1ps 模型时基、fetch=3/memory=19 cycles；可用对应 flags 修改服务延迟。命令行预算耗尽返回非零；库保留进度可续跑。多目标 spawn residency 仍需 effects.BindSpawn，当前程序入口明确拒绝，不实现多 Warp scheduler。

观测包含旧边沿 CoreReport、提交后的 ResourcesAfter/Residents、Services 及 Events。按 ID/epoch/warp/uop/mask 对应资源生成 enter/stay/advance/leave，读、执行、WB/反馈另有事件；Flush 取消事件保留旧 epoch。位置与 Remaining 是本地资源状态，不推定外部 cache 时间。

### Task9 对象驻留观测与验证

每个资源通过 `Residents()` 返回 detached value，含 Token、位置、局部 Remaining 与 readiness 原因；runner 不访问组件队列。CoreReport.Resources 是旧边沿，ResourcesAfter 是本次统一提交后状态。Events 的 enter/leave 对应本周期提交的转移，stay/advance 表示资源仍持有该对象；tag/context alias 保持独立资源身份，不能累加为指令数量。FIFO 中的 queue-order、输出 awaiting-transfer、执行 execution-latency、tag response-coverage 与外部 backpressure/control-drain 明确区分；这些原因不宣称解析全部 RTL 仲裁信号。Services 列出原 byte owner 服务队列及显式 due cycle。

程序 trace 测试验证驻留记录闭合、除法 33 周期占用、packed 请求队列填满四项后恢复且每 uop 只 WB 一次；增加 memory 服务延迟或请求背压会延长完整运行，功能结果不变。重复运行记录确定；快照修改不会改变组件 owner。基础注册边界与满队列同时接收/释放继续由 timing/model 门禁覆盖。

## 16. T10 四 Warp 并发周期 Core（最终实现）

`PROVISIONAL（Task10 软件接口）`：本节增量记录 Task10 当前实现。第 1–15 节
保留原功能契约、T1–T9 实施证据与 UNRESOLVED/RESOLVED 审计；其中“未完成”、
“尚待适配”等阶段描述是当时的实施记录，不表示本节所述 Task10 功能仍未实现。
旧 `runner.New` / `cmd/timing-run` 保留单活动诊断策略；当前四 Warp 接口为
`runner.NewMulti` / `cmd/timing-multi`，普通指令准入不等待整 Core Idle。

### 16.1 唯一 owner 与锁存指令上下文

`WarpState`、寄存器、CSR、divergence/trap 数据与 memory bytes 仍由既有功能
owner 持有；timing 不新增架构状态写源。`NewMulti` 接受四个显式 Warp owner，
不调用原 Core.Step 的逐条完成调度，也不创建 CTA 或 Kernel owner。

Scheduler 的前端 PC/mask 是瞬态取指上下文；Token 锁存 epoch/warp/instruction/
uop/PC/mask，背压期间保持稳定；canonical WarpState 只在定义的效果事件更新。
`InstructionContext` 与 `NewLatchedOperandCapture` 显式使用 Token PC/mask，
逐银行读取旧边沿实际操作数，执行时复用原 ISA evaluator 和 state API。
`NewOperandCapture` 的旧 canonical PC/mask 相等校验继续保留。

四套 IBuffer/Sequencer 与共享 Scoreboard、Collector/Dispatch、FU、Commit
通过统一 Evaluate/CommitEdge 更新。Scoreboard 按真实解码依赖、寄存器类型、
零寄存器和特殊状态维护资格；WB release、staging reserve 与下一周期注册
eligibility 分开。packed 按每个 uop 的最终 lane WB 释放，部分 WB 不提前释放；
宏指令完成另等待全部元素/服务 receipt。因此 T9 历史 packed 队列演示不能被
解释为当前 Scoreboard 允许同目的寄存器的 packed uop 无 WAW 等待。

### 16.2 并发效果的交付规则

`effects.Concurrent` 按完整身份索引独立在途 receipt，每 Warp 共用一个
`EffectStream`。同边沿先校验全部事件身份、采样各 owner 的旧边沿 snapshot，
再进行读/执行和可见效果交付；不因 Go map 遍历顺序产生隐式 WB 旁路。

每次交付从 live owner 构造并同步提交事务，保持原 StageEffects stale 检查；
不保存可在未来覆盖 owner 的整状态候选。控制顺序 frontier 仅是软件可见性
规则：较新控制成功后，迟到的普通顺序 PC receipt 不再回退 canonical PC；其
WB/flags 仍独立交付。mask/lifecycle 只由实际控制效果改变，旧顺序完成不能
恢复旧 mask。越过 frontier 的非顺序控制报错，不静默选择优先级，也不宣称
RTL 存在 ROB 或统一 retirement 顺序。

非阻塞 `BAR.arrive`（含 expect_tx）例外仅针对外部事件：当前 activation 内
迟于年轻 ALU/branch 的合法 arrive 丢弃旧顺序 PC 效果，仍将 barrier 与已满足的
LSU drain 一起交给外部 owner，成功后记录一次 ControlEvent receipt。
`EffectStream.RecordActivation` 独立维护 admission order 截止线，即使 PC frontier
已到达相同 order，也禁止旧 activation 的 arrive。epoch/residency/cancellation
仍由 timing 身份路由校验；阻塞 BAR、未完成 drain 和真正过期跳转仍拒绝。
这不改变 arrive 的非阻塞 decode 或现有 LSU admission gate，不添加 Core drain。

Fetch/Memory 服务按身份匹配，使用显式正延迟和响应背压；分片与 packed load
保留覆盖、去重和单次交付。冻结 ISA 只有 packed load，无 packed store 指令。
失败边沿停止，不重试部分成功的效果；外部 owner 仍须遵守同步 all-or-error
契约，不能回滚此前已可见的寄存器、CSR 或 store。

### 16.3 控制恢复、pending 与外部协作

`SchedulerFeedback` 来自执行时锁存的效果，不从交付后的最新 WarpState 猜测
分支结果。branch/TMC/SPLIT 与原 sideband 同边沿更新，下一周期参与取指；JOIN
额外保留一拍注册；WSPAWN 先注册 pending，再使用注册 SingleActive 门控。
同 Warp 未确认的同时反馈显式拒绝，旧 epoch、重复反馈不产生架构效果。

pending 汇总在途资源、部分 uop、效果 receipt、服务尾部和外部等待，按完整
宏指令身份去重。WSYNC/BAR 的内部 drain 与调用者外部谓词做 OR；共享 SFU
队首阻塞保留。inactive、blocked 或暂时无 runnable Warp 都不等于整体完成。

Barrier 效果通过原外部 owner 交付，`Release(token)` 只排队明确的注册唤醒，
成员计数/phase 由原 coordinator 决定。WSPAWN 通过 `MultiOptions.Spawn` 显式
绑定原 owner 与 pre-issue target snapshot，复用 `StageWarpSpawn/Commit` 的
源/目标原子事务。未绑定保持阻塞；并发适配要求源较老工作先 drain，以满足
原严格 source PC/mask 契约，这是软件接口限制，不是新增 RTL drain 事实。
目标旧在途工作或 stale image 被拒绝，不进行部分激活或 CTA admission/reclaim。

正常 wstall 阻止错误路径继续取指。防御性重定向清理在 byte service 之前按
Warp/epoch/有界指令年龄取消年轻资源、依赖预留和服务，保留较老及其他 Warp
工作。`Cancel` 暂停 Warp，`Restart` 接受显式前端上下文并避开取消身份；残留
同 Warp 前端/控制工作时拒绝恢复。`Flush` 取消全部未交付工作、增加 epoch，
从 live canonical owner 重建前端，不撤销已可见效果或保证重放放弃的指令。
这些是软件恢复边界，不等价于冻结 RTL 有通用 branch squash 输入。

### 16.4 观测、验收与范围

`MultiRecord.Warps` 给出旧边沿 active/stalled/runnable、PC/mask/epoch、pending
及 stall reason。IssueCandidates 区分注册资格和当前依赖/credit/lock 输入，
IssueSelected/Issued 与 Offered/InstructionAccepted 分别记录 issue/fetch
选择和握手。Resources/Events 关联完整身份的驻留、读、执行、WB、release、
wakeup、取消与恢复，返回值与内部可变状态隔离；资源别名不能累加为指令数。

可复现入口（仓库根目录）：

```bash
source env/env.sh
go run ./cmd/timing-multi -cycles 1000 -fetch-cycles 2 -memory-cycles 60
go run ./cmd/timing-multi -trace -cycles 1000 -fetch-cycles 2 -memory-cycles 60
bash scripts/verify-timing.sh
bash scripts/verify-all.sh
```

[周期契约](../../timing/multiwarp-contract.md)、
[并发效果实施](../../timing/concurrent-effects-progress.md) 与
[最终验收映射](../../timing/control-observability-progress.md) 给出可复查 RTL
证据、具体周期测试和完整 owner/RAM 功能对比。Timing IR 的 `functional` 来源
继续引用本文第 2/4/5/10/11 节，本文不被 Timing IR 替代。

`UNRESOLVED`：原第 10/11 节问题与闭合证据不因 Task10 测试通过而改写；同 Warp
部分同时反馈、CSR 背压、跨 Warp memory ordering 和外部效果故障继续保留证据
边界。Task10 不新增 CTA/Kernel orchestration、Cache、DRAM timing、多 Core，
也不宣称 RTLSIM trace 精度等价。所有冻结 RTL 输入保持不变。

### 存储响应片段身份修复（2026-09-16）

`PROVISIONAL（软件校验元数据）`：timing/memsys 的 global load 响应在
coalescer 展开后仍携带 BatchID（slot/generation），split 对相同父 identity
下的不同 batch 允许零缓冲 priority pack 重选。相同片段及独立 producer 的
背压稳定性仍分别校验；父请求完成仍要求全部 lane 交付及子请求引用释放。
该元数据不改变 canonical owner、RTL 缓冲/仲裁、外部服务延迟或功能效果接口。
接口与定向回归见 [memsys 说明](../../timing/memsys/README.md)；原规模 runtime
benchmark 和 RTL 周期等价不由组件回归宣称通过。

### Runtime 终态审计发布（protocol-and-audit）

`integration/vortexruntime` 在启用 JSONL 审计时，先完成 launch-finish/cache-flush
记录的 Write/Close，再以设备锁原子发布 detached RunSummary、错误和 idle。
独立 audit mutex 仅串行化记录 I/O，不持有设备状态锁或改变下层模拟周期。
打开、写入、短写、关闭失败均进入 native error channel；原模拟器错误及 origin 不被
审计错误覆盖。未启用审计时无日志 I/O。此规则不等于断电持久化，也不把 timing
execution complete 升级为 backing visible；后者仍要求原生 CP 真实 CACHE_FLUSH。
固定单 Core/四 Warp/四 lane、外部服务 100 cycles、memory Batch provenance 与
EffectStream activation 截止线均保持不变。详见 runtime 集成文档和 audit_test.go。

### D-cache port 0 transport ownership（dcache-port-buffer，第 1 步）

`timing/memsys/System` 通过 `b-dflush` 的两槽注册输出连接 adapter port 0 与
D-cache；port 1 直通。普通请求和独立 flush 共用容量，原位 flush 保留原 word
身份。adapter 接受、Cache 接受、store 应用、响应交付仍是独立事件。新增缓冲
只持传输值和 flush 回复尾部，不拥有 canonical bytes，也不改变功能 effect。
`Drained`/`HasResidency` 包含缓冲；取消不删除已暴露请求，终态协议错误不允许重放。
具体边沿及第 1 步验证范围见 [memsys 契约](../../timing/memsys/README.md)。
本记录不表示后续生命周期组合和里程碑完整验收已经完成。

### D-cache port buffer lifecycle closure（dcache-port-buffer，第 2 步）

`FROZEN（局部 RTL transport 边界）`：`b-dflush` 的 port 0 两槽注册输出、满时不
复用同拍释放 credit、port 1 直通已由生产 System 逐边沿 witness 与冻结 RTL 接线
互证；普通访存和原位/独立 flush 均使用该边界。`timing/check/dflush_contract.go`
将 RTL 实例和编码链、IR 值与生产握手串联到 timing 门禁，不能以空置实例替代接线。

`PROVISIONAL（软件 identity 尾部）`：runner 的 `drained` 还要求下一边沿待消费的
completion receipt、token/acceptance/cancellation maps 和 store events 为空；
`warpPending` 包含最终 completion 身份，防止取消 load/FENCE 的返回刚排空就结束
执行或回收相关 residency。原有 `receive` 消费顺序不变，没有新增 canonical owner、
硬件延迟、全 Core drain、跨 launch 复用或 Warp 分配策略。
只读 `DCachePort0Occupancy` 用于诊断及边沿测试，不可替代完整 `Drained`。

验收映射、命令结果和外部实验限制见 runtime trace 分析文档的里程碑收尾记录。

### runtime 设备存储续用

`PROVISIONAL（已实现软件所有权接口）`：timing Kernel.NextLaunch 将完整排空的
memsys 与连续时钟转交新 launch；backing owner 不变，CTA/LMEM 架构 owner 重建，
launch 身份递增并重新绑定 LMEM resolver。旧 Kernel 禁止继续推进。native runtime
仍要求成功 D/I flush 后才能重启，不以执行完成替代 backing visibility。
冻结 RTL 的 reset/init 与 flush 状态区分、验证和未闭合的首次 host 控制时间线见
[设备生命周期交接](../../docs/runtime/device-lifecycle-handoff.md)。

### Timing 增量 Warp residency

`PROVISIONAL（已实现软件所有权接口）`：`core.ResidencyMemory` 增加 Reserve、
BindWarp、DetachWarp，将 CTA metadata/固定 stride LMEM 的生命周期与物理 Warp
绑定分开。功能 Core 的整 CTA API 保持；timing Kernel 在逐边沿 coordinator 中
使用增量接口，调用解绑前验证该 Warp 的完整流水/效果/传输排空并保留 barrier
地址保护。CTAView 的 Size 取完整 block 大小，Rank 不依赖当前成员 slice 下标。

`FROZEN（局部 RTL 控制依据）`：VX_cta_dispatch 的 IDLE/DISPATCH、注册 warp_fire_r、
单 Warp 选择和两级退休/共享 RAM 写端口定义生产 Kernel 的分配控制状态；软件的
完整尾部检查仍可能推迟安全回收。物理 Warp 与 CTA slot generation 独立，TLS
仍保留于 canonical WarpState；没有新建架构寄存器或 memory bytes 的写源。
实现、验证范围和下一 Worker 的事件接口见
[增量 Warp 驻留交接](../../docs/runtime/incremental-warp-handoff.md)。


## Runtime 身份与硬件计数（device-and-warp-lifecycle，第 3 Worker）

`RESOLVED`：Kernel 的边沿记录以 DeviceID/LaunchID、显式 WarpBinding 和
Token.ID/Epoch/Uop 关联逻辑 CTA/rank、物理 slot/generation；存储 fragment trace
连接 SIMD parent、coalescer Batch、adapter wire transaction 和 Cache 接受，
保留 port-0 注册缓冲相位。绑定来自真实 dispatch 与唯一 residency owner。

`RESOLVED`：原生 MPM 按 VX_dcr_data tag 编码导出已有 scheduler 44 位 Cycle/Instret。
NextLaunch 转移计数与 busy 寄存器；显式 flush 在真实空闲边沿推进 busy 尾部。
Instret 是 committed Warp/EOP 通知，不是宏收据或 lane 计数。runtime 保存终态
硬件快照并在审计之后发布，未知统计明确 unavailable，不以有效零值代替。

字段、RTL 依据、复验命令及未验证的外部边界见
[身份与计数交接](../../docs/runtime/identity-performance-handoff.md)。
本条不关闭首次 host DCR 时间线、外部 native benchmark 或 RTLSIM 等价问题，
也不替代 closure Worker 的全仓与里程碑验收。

### 生命周期跨层收尾（device-and-warp-lifecycle）

`RESOLVED（计数接线）`：`VX_scheduler.busy` 包含注册 busy_buf 与组合
cta_dispatcher_busy；Kernel 将边沿前 DISPATCH 或真实 CTA admission 经
CoreInputs.DispatchBusy 传给唯一 accounting owner，按 OR 每拍计数一次。
分配 busy 不增加额外注册尾拍；44 位累计、EOP Instret 和宏退休的单位保持独立。

组合回归覆盖提前 Warp 复用、真实存储/取消尾部、跨 launch fragment 身份、
barrier/LMEM 交换、TLS、flush 后不同输入，以及终态审计阻塞期间的 MPM 发布。
没有增加 canonical owner、通用故障自动恢复或放宽排空门槛。逐项验收、最终
命令结果和外部实验限制见 [生命周期收尾](../../docs/runtime/lifecycle-integration-closure.md)。
