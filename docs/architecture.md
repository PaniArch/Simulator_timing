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

`FROZEN`：输入包刻意排除了 `sw/`、`sim/`、`tests/`、reference checker、DPI C++ reference semantics、日志和 Git metadata。因此 Kernel/host ABI、软件生成序列、精确浮点 reference 行为及最终比较协议不能在 T0 由缺失内容推断。

### 3.2 冻结配置矩阵

下表由 `VX_config.toml`、`VX_types.toml`、`hw/VX_config.vh`/`hw/VX_types.vh` 和 decode 条件编译路径交叉核对，均为 `FROZEN`。

| 领域 | 冻结值/范围 | 直接含义 |
| --- | --- | --- |
| 字长 | `XLEN=32`，`FLEN=32`，memory address width 32 | 冻结执行能力只包含 RV32 数据通路和单精度 F；RV64/W 类及 D 执行能力不在范围内。D-format encoding 在通用 F decode 中的合法性另见 `UNRESOLVED`。 |
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

`FROZEN`：A 关闭时 `INST_AMO` case 被条件编译移除；C 关闭时没有 RVC decode 且普通 PC 前进量是 4 bytes；TCU/DXA/TEX/RASTER/OM/RTU cases 受各自关闭宏保护；VM 关闭时不发生地址翻译。D 必须区别处理：配置和 `FLEN=32` 明确关闭 D/FLEN64 执行能力，但 `INST_FL`/`INST_FS` 与 `INST_FMADD`/`INST_FCI` 的通用 F decode 只受 `VX_CFG_EXT_F_ENABLE` 保护；其中 fused/common arithmetic 仍把 `funct2[0]` 传播为 S/D format，只有 FCVT.S.D/FCVT.D.S 的 F2F case 受 `VX_CFG_FLEN_64` 保护。故通用 decode 没有完整拒绝 D-format encoding，其合法性/fault 归入下述 `UNRESOLVED`。

`UNRESOLVED`：`VX_decode.sv` 的 default 分支大量保留为未赋值/`x`，没有形成一份可直接移植的完整 illegal-instruction 判定表。尤其是上述通用 F decode 接收到 D-format encoding 后，在 D/FLEN64 能力关闭的冻结配置中应如何判定合法性并产生何种 fault，当前证据没有唯一答案。每个 funct/format 的合法组合、非法 encoding 的 fault 行为必须在 ISA 实现阶段逐项审计，最晚在 ISA decoder 验收前闭合。

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
| Lane/thread/warp/CTA identity 与 thread coordinates | Lane view over CTA/Warp | `PROVISIONAL`: CTA context + immutable core/warp/lane ids；Lane view 派生 | `VX_csr_unit.sv` 从 wid/lane/CTA tables形成 thread/hart/CTA CSR；不另设可写 Lane context mirror。 |
| warp PC、active lane mask | Warp | `PROVISIONAL`: Warp/SIMT state | `VX_scheduler.sv` 的 warp_pcs/thread_masks；fetch/evaluator 只读 view，PC effect 只回此 owner。 |
| warp active、blocked/wait reason、runnable、termination | Warp/Core scheduling scope | `PROVISIONAL`: Warp lifecycle owner；runnable 是由 lifecycle/wait reason 派生的 view | `active_warps`、stall/control/retire 路径证明这些事实存在；不得另存第二个可写 runnable truth。 |
| GPR x0..x31（每 lane） | Warp × Lane | `PROVISIONAL`: Warp register state | operand collector 的 banked namespace与 masked writeback；功能模型忽略 banking，x0 恒为 0。 |
| FPR f0..f31（每 lane） | Warp × Lane | `PROVISIONAL`: Warp register state | F decode/read/writeback 证明 FPR namespace；Lane view 不复制寄存器数组。 |
| FFLAGS/FRM/FCSR | Warp | `PROVISIONAL`: CSR state keyed by warp | `VX_csr_data.sv` 的 per-warp fcsr；FP flags 累积 effect 只交 CSR owner。 |
| trap/return CSR 与 mscratch：mstatus、mtvec、mepc、mcause、mtval、恢复 mask 等 | Warp/Core | `UNRESOLVED owner candidate`: 单一 CSR state keyed by warp，Warp control 仅通过 CSR effect/view 使用 | RTL 将部分状态放 scheduler、部分放 CSR unit；完整 CSR scope/reset/合法性见 U-CSR-01，闭合前不得双存。 |
| divergence/reconvergence stack、stack pointer | Warp | `PROVISIONAL`: Warp/SIMT state | `VX_split_join.sv`/`VX_ipdom_stack.sv` 保存 reconvergence PC/mask/pointer。 |
| barrier mask/count/event/phase；warp barrier wait relation | CTA/Core/barrier ID | `UNRESOLVED owner candidate`: Barrier coordinator owns barrier record；Warp lifecycle owns自身 block reason并只保存 barrier key | `VX_bar_unit.sv` 证明记录存在，但并发 CTA namespace/visibility 未闭合，见 U-BAR-01。 |
| local/shared memory bytes（冻结 RTL 名称 LMEM）及 allocation | CTA/cluster address scope | `UNRESOLVED owner candidate`: Memory/local space owns bytes，CTA state owns allocation descriptor | LMEM enabled；其与软件 shared memory 的等价性、跨 CTA scope/visibility 见 U-MEM-01。两者不得各存一份 bytes。 |
| 架构可读 counters | Core/Device | `UNRESOLVED owner candidate`: CSR/counter service | cycle/instret/MPM 路径存在；哪些可观察及非周期定义见 U-COUNT-01。 |
| warp、CTA、Device 完成状态 | 各 lifecycle scope | `PROVISIONAL`: Warp/CTA/Device 各自只拥有本层完成事实并单向聚合 | TMC mask=0 可退休 warp；CTA/host completion 协议见 U-CTA-01/U-ABI-01。上层聚合结果不能反写低层完成真值。 |

`FROZEN`：pipeline valid/ready、scoreboard busy bits、issue/dispatch queue、operand collector bank、cache tag/MSHR、arbiter priority、stall flags、UUID、流水寄存器和性能 backpressure 都不是功能模型的 canonical architecture state，除非后续证据证明 reference checker 可观察其中某项。

## 5. ISA、执行与 effect 契约

### 5.1 ISA 层职责

`PROVISIONAL` 职责边界：ISA **只**负责 decode 和单条指令的功能语义。它读取 instruction word、冻结配置及完成本条语义所需的最小 immutable state/view，产生操作描述、result/effect 或显式 illegal/fault。ISA 不持有或长期缓存 PC、GPR/FPR、CSR、memory、lane mask、divergence stack、barrier 或 lifecycle state；不选择 Warp、不调度 Core/CTA、不结束 Kernel，也不能绕过 owner 直接 mutation。这是待实现验证的模拟器边界，不是由 RTL 模块位置直接冻结出的方案。

`PROVISIONAL` 抽象边界：

- Decode 输入：32-bit instruction、当前 PC 和必要的 frozen ISA flags；decode 本身不读取整个 Device/Core。
- Decode 输出：与存储布局无关的 decoded operation，含源/目的寄存器、immediate/format、是否需要 memory/CSR/SIMT service，以及 illegal/unsupported 结果。
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
| Completion control | warp termination/fault、CTA membership、remaining CTA/device work | Warp done → CTA done → Device/kernel completion 或 fault | Warp、CTA、Device lifecycle owners 单向聚合；host protocol 见 U-ABI-01 |

共同规则为 `FROZEN`：x0 写入被抑制；普通 lane result 使用该指令传递的 active/write mask，WGATHER 使用 RTL 明确覆盖后的非 source-lane write mask；顺序 PC 在 C 关闭时按 4-byte instruction 前进；store、CSR、SIMT control 和 trap 等非寄存器 effects 也必须经过显式 owner，而不能被当成普通 writeback 遗失。

## 6. 单条指令的功能执行流程

以下顺序是 `PROVISIONAL` 的功能边界，不是 RTL cycle/pipeline 模型：

1. **Launch/prepare**：Device/CTA manager 根据 launch context 建立 CTA，并请求 Warp owner 建立 entry PC、CTA key、LMEM allocation view 和初始 active lane mask。
2. **Select runnable warp**：Core/Warp manager 从 owner 提供的 runnable view 选择一个 Warp。调度策略在不改变架构结果时可替换，不复刻 RTL arbitration。
3. **Fetch**：instruction source 用该 Warp PC 从 instruction memory 取 32-bit word；C 已关闭。成功取指不自行改 PC，fault 产生显式 effect。
4. **Decode**：ISA decoder 仅用 word/PC/frozen flags 产出 decoded operation 或 illegal/unsupported。
5. **Read view**：Warp executor 根据 decoded operation 向 owners 请求最小 immutable Lane/Warp、GPR/FPR、CTA、CSR、SIMT 或 Memory view，不把整个 Device 交给 ISA。
6. **Evaluate semantics**：ISA evaluator 计算单条指令的功能结果；Memory/CSR/Barrier 服务只经显式请求参与，不回调 manager 修改状态。
7. **Produce effect**：evaluator 返回 structured effects 或 fault/wait，不直接 mutation；无 effect 的非法路径也必须显式表示。
8. **Owner validate/update**：effect router 校验 target/scope、输入 active mask 与指令定义的 write mask 并路由，GPR/FPR、Warp PC/SIMT、CSR、Memory、Barrier、lifecycle 等 owner 各自最终验证和更新唯一 canonical state。
9. **Progress/completion**：Warp manager 重新派生 runnable view；Warp completion 单向聚合到 CTA、Device/kernel executor。Harness 只经公共结果接口读取最终状态和内存。

## 7. 后续公共接口清单

所有接口均为 `PROVISIONAL` 的用途契约，不固定 Go 名称、interface/struct、字段、具体类型或调用风格。箭头方向均从调用者到服务，结果/effect 显式返回；任何接口都不授权全局查找或旁路 mutation。

| 公共边界（用途名） | 输入 | 输出 | 允许的依赖方向/约束 |
| --- | --- | --- | --- |
| Kernel launch/executor | image、entry、argument/data、launch dimensions、停止条件 | 初始化/进度/completion/fault、最终观察 handle | Harness → Device；只能经 Device/Memory/CTA manager 初始化 owners。 |
| Instruction source | PC/address space key | 32-bit instruction word 或 fetch fault | Warp executor → Memory instruction space；不推进 PC、不回调 scheduler。 |
| Memory spaces | global/local scope key、address/size、active lane requests、store data | load bytes/value、validated store effect 或 fault | evaluator/effect owner → Memory；global 与 LMEM bytes各有单一 owner，cache/timing透明。 |
| Lane/Warp views | warp/lane identity、decoded op 所需字段集合 | immutable active mask、operands、PC/SIMT/CTA-derived view | Warp executor → state owners；返回 view 而非可写引用。 |
| CSR context/service | CSR address/op、warp/lane/CTA/device identity、operand | old value、CSR/trap effect 或 illegal | evaluator → CSR service → CSR owner；不得直接调度 Core/CTA。 |
| Decoder | word、PC、frozen flags | decoded operation 或 illegal/unsupported | Warp executor → ISA decoder；ISA 不依赖 Device mutable state。 |
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
| E-CSR-01 | `VX_csr_data` 的 `fcsr` 按 wid 保存并累积 FP flags；CTA CSR 数据来自 scheduler/dispatcher context。trap/mret 相关的 `mscratch_r`、`mstatus_r`、`mtvec_r`、`mepc_r`、`mcause_r`、`mtval_r` 及恢复 mask 物理存于 scheduler 并由 `sched_csr_if`/branch control 更新。`VX_csr_unit` 以 lane/wid/CTA context 生成 thread/hart/CTA identity 和 CSR RMW。 | `hw/rtl/core/VX_csr_data.sv`，module `VX_csr_data`，state `fcsr`、interfaces `fpu_csr_if`/`sched_csr_if`；`hw/rtl/core/VX_scheduler.sv`，上述 `*_r` state 与 `sched_csr_if`；`hw/rtl/core/VX_csr_unit.sv`，module `VX_csr_unit`，lane context 与 `sched_csr_if.cta_tid`/`cta_csrs`。 | 使用一个按明确 scope/key 寻址的 CSR owner，scheduler/warp 仅消费 view/effect；RTL 分散存储不授权重复 canonical state，完整 scope 见 U-CSR-01。 |
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
| U-ISA-01 | 每个 funct/format 的完整合法 encoding、illegal/misaligned fault 与优先级是什么？D/FLEN64 关闭时通用 F decode 接收的 D format 如何处理？ | `VX_decode` 的 FL/FS/FMADD/FCI 通用 F 路径受 EXT_F 保护，部分路径传播 format；F2F D conversion 受 FLEN64 保护。 | decode default/`x` 不是软件规范，也无完整 fault oracle。 | Decoder、fault effect、Warp trap/PC owner。 | ISA decoder 与 fault 模型验收前。 |
| U-FP-01 | F32 NaN、rounding、flags、conversion 边界及 checker 逐位规则是什么？ | F32 datapath和 per-warp FCSR/flag accumulation 存在。 | DPI C++ reference semantics 与 checker 被排除。 | F evaluator、FCSR effects、differential comparison。 | F extension 实现及 RTL differential test 前。 |
| U-CSR-01 | 完整 CSR 合法性、读写掩码、reset、trap/return 与逐项 canonical scope 是什么？ | CSR 数据物理分布在 `VX_csr_data`、`VX_csr_unit` 和 scheduler，部分 machine/trap/CTA行为可见。 | 地址定义和局部 RTL 行为不是完整软件契约；特权/fault oracle 缺失。 | CSR owner、Warp trap/lifecycle、snapshot。 | CSR/System 阶段验收前。 |
| U-LOWER-01 | compiler/runtime 如何生成并约束 SPLIT/JOIN、PRED/TMC、BAR、WSPAWN、WSYNC 序列，stack under/overflow 如何处理？ | decode、lane predicate、IPDOM stack、barrier和 warp-control路径存在。 | `sw/`、compiler/runtime 与 tests 被排除。 | Decoder/evaluator、SIMT owner、Barrier、Warp lifecycle。 | 首个 SIMT 程序验收前；各指令实现不得晚于其单元验收。 |
| U-WSPAWN-01 | WSPAWN 的目标选择、初始 registers/CSR/CTA context 与软件使用协议的完整语义是什么？ | RTL 激活小于 rs1 的非当前 warps，目标 lane 0 active、PC=rs2，明确复制 mscratch；未见该路径复制 GPR/FPR。 | runtime lowering、目标 warp 预初始化与 CTA membership 协议缺失。 | Core/Warp manager、register/CSR state、CTA membership。 | WSPAWN/Warp lifecycle 集成验收前。 |
| U-WSYNC-01 | WSYNC 等待哪些架构工作，并保证何种 memory visibility/order？ | RTL 在 warp pending ALM 非空时 drain，结束后 scheduler release。 | pipeline pending 集合到功能级 ordering 的最小映射、software convention 缺失。 | Warp executor/lifecycle、Memory ordering、Barrier integration。 | WSYNC 与多 warp memory/barrier 集成前。 |
| U-BAR-01 | 并发 CTA barrier namespace、ID 复用、event/phase、memory visibility 和异常退出释放是什么？ | `VX_bar_unit` 有 indexed mask/count/events/phase；冻结单 core 不走 global-barrier path。 | 物理 index 不说明 concurrent CTA software namespace；runtime/fault协议缺失。 | Barrier owner、CTA key、Warp wait reason、Memory ordering。 | Barrier/CTA 并发与 memory ordering 集成前。 |
| U-CTA-01 | CTA 分派、部分 warp coordinates、WSPAWN membership、正常/异常 warp 与 CTA termination 的完整规则是什么？ | dispatcher维护 CTA context/membership/remaining warps；TMC mask zero 是已见 warp-done 路径。 | runtime launch、fault/early-exit/partial-warp termination协议缺失。 | CTA manager、Warp lifecycle、Device completion。 | CTA/Warp lifecycle 阶段验收前。 |
| U-ABI-01 | Kernel image、startup PC/entry、arguments、初始 registers/CSR、host launch/completion ABI 是什么？ | KMU/dispatcher形成 launch/context请求，顶层暴露聚合 busy；KMU running仅代表 launch发送进度。 | host/runtime/software 和 completion consumer 被排除。 | Loader、Kernel executor、Device lifecycle、公共 API。 | Kernel loader/runner 公共 API 冻结前。 |
| U-CHK-01 | checker 比较哪些 registers/CSRs/memory ranges，如何处理 FP、fault、console和未初始化状态？ | RTL 提供架构状态更新路径和顶层输出，但不定义 comparison contract。 | reference checker 被刻意排除。 | Snapshot/result、fault model、端到端验收。 | 端到端 differential harness 验收前。 |
| U-MEM-01 | global/LMEM 范围、alignment/fault、FENCE/BAR/WSYNC 可见性及 self-modifying code 规则是什么？ | LSU 按 LMEM base/size分 local/global lane masks，local与 global走不同 physical paths。 | cache/LSU wiring 不等于功能 memory contract；软件/checker约定缺失。 | Instruction source、Memory spaces、LSU/fault/order effects。 | Memory/LSU 与同步指令集成前。 |
| U-COUNT-01 | MCYCLE/MINSTRET/MPM 哪些可观察，非周期模拟器如何确定性定义？ | RTL CSR/counter读取路径存在。 | checker visibility未知；cycle/performance是明确非目标。 | CSR/counter service、snapshot。 | CSR 对照测试前；若不可观察则明确排除。 |
| U-SCHED-01 | 多 warp 冲突 memory effects 需要何种最小确定性/ordering？ | RTL 可由 active/non-stalled warp调度并经 memory arbitration交错。 | software race policy和 checker observation未知。 | Runnable selection、Memory ordering、deterministic harness。 | 多 warp memory integration 前。 |

## 11. `RESOLVED` 审计记录

T0 无 `RESOLVED` 项。后续闭合时在此追加原 ID、决定、RTL/测试证据、实现位置、验证结果和 Task 标识；不得删除原问题来掩盖历史。

## 12. T0 最终覆盖与范围审计

### 12.1 八类交付要求覆盖

| T0 要求类别 | Contract 对应章节 | 审计结论 |
| --- | --- | --- |
| 1. 模拟器层级、模块与单向依赖 | 第 4.1 节 | Device → Core → CTA → Warp → Lane → ISA 主链及 Memory/CSR/Barrier 服务挂靠已定义，具体代码组织保持 `PROVISIONAL`。 |
| 2. 跨指令长期状态与唯一 owner | 第 4.2 节 | GPR/FPR、PC/mask、lifecycle、SIMT、context、CSR、barrier、memory、completion 均已登记；证据不足者是 `UNRESOLVED owner candidate`。 |
| 3. ISA decode/evaluate 边界 | 第 5.1 节 | ISA 只读最小 view、产生显式 effect，不持有长期状态或调度上层。 |
| 4. 指令类别、effect 与 control owner | 第 5.2 节 | 标准指令、System、全部冻结 custom SIMT/lane 指令、fetch 与 completion 已映射。 |
| 5. 功能执行流程 | 第 6 节 | select runnable warp → fetch → decode → read view → evaluate → effect → owner update 已分责。 |
| 6. 后续公共接口职责 | 第 7 节 | instruction/memory/view/CSR/decoder/evaluator/effect/warp/barrier/kernel 等边界保持 `PROVISIONAL`。 |
| 7. RTL 调查、证据与待决问题 | 第 9、10、11 节 | 可确认事实有 path/module/signal/macro 依据，设计推导分栏；未知项有事实、缺口、影响边界和 deadline；T0 不伪造 `RESOLVED`。 |
| 8. 功能范围与非目标 | 第 3、8 节 | 冻结配置/ISA 范围与 cycle/pipeline/cache timing/hazard/throughput/performance scheduling 等非目标明确分离。 |

### 12.2 治理与过度设计审计

- 状态标签及迁移门槛在第 2 节；`FROZEN` 事实、`PROVISIONAL` 设计、`UNRESOLVED` 缺口和未来 `RESOLVED` 记录没有互相冒充。
- 唯一 canonical owner 原则在第 4.2 节；control/effect 只能经第 5 节边界交给 owner，不允许反向依赖、全局后门或第二可写真值。
- 维护协议在第 1、13 节；后续任务开始前读取、结束后与代码同步更新。
- 第 5.1、8.2 节明确不预定 Go concrete types、transaction/rollback/commit 算法、调度策略或微架构行为，满足无过度设计要求。
- T0 交付改动仅限本文件 `docs/architecture.md`，没有新增或修改 ISA、State、Warp、SIMT、scheduler、CTA、Kernel execution 等功能代码；外部 `Vortex_rtl` 始终作为只读输入，不属于交付内容。

## 13. 维护检查单

每个后续 Task 完成前必须确认：

- 是否读过本 Contract，且实现未违反任何 `FROZEN` 项；
- 是否发现新的 RTL 事实、`UNRESOLVED` 问题或已可闭合的条目；
- 是否改变 canonical state ownership、模块依赖、公共接口或 effect/control 边界；
- 是否把实现验证得到的设计从 `PROVISIONAL` 正确升级，或在失败时回退；
- 是否只模拟架构功能语义，没有把 pipeline/cache/hazard/timing 偶然带入正确性契约；
- 文档与代码是否在同一 Task 同步更新。
