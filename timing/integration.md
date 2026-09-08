# 功能衔接与实现候选

T9 增量状态：`timing/model` 已实现不携带架构 owner 的 value Token、前端/操作数/执行资源/Commit 组件和 Akita clock；DecodeToken 只复用严格 ISA Decode，现已通过 timing/effects 连接 evaluator 与分事件交付。下述功能衔接候选仍保持 PROVISIONAL，`u-visibility` 未闭合，完整进度见 [implementation.md](implementation.md)。

本页的实现设计均为 **PROVISIONAL**。RTL 事实继续维护于 [ir.yaml](ir.yaml)，功能结果与既有 owner 契约继续维护于 [emu/docs/architecture.md](../emu/docs/architecture.md)。这里不固定 Go 类型、接口签名或目录，不新增指令语义，也不将功能串行执行次序提升为周期事实。

## 职责和状态归属

| 职责 | 自有状态 | 交互边界 |
| --- | --- | --- |
| 周期推进 | 当前边沿、待消费事件、阶段快照 | 按 `r-software-edge` 候选先求组合稳定结果，再提交下一状态；不以组件调用次序制造旁路。组合环和同时事件仍须 `u-feedback` 验证。 |
| 流水组件 | valid/payload、队列占用、tag、bank 冲突、执行中间结果 | 经 YAML ports/channels 转移；ready/valid 接受、结果 ready、WB 是不同事件。不得拥有第二份可写寄存器或 memory 真值。 |
| 调度控制 | warp eligibility、scoreboard、FU credit、pending、仲裁状态 | 消费 `r-scoreboard-edge`、`r-scheduler-edge` 和 `r-completion-detail` 的反馈；scheduler 中 fetch PC 是推进状态，其与架构 PC 的映射须显式定义。 |
| 功能衔接 | detached operand/context、指令与 uop 身份、待交付 effects 及已交付标记 | 复用 evaluator，按具名可见事件交给唯一 owner；读快照不是长期可提交的全状态副本。 |
| 架构 owner | GPR/FPR、CSR、PC/mask/SIMT、CTA/barrier metadata 和唯一 bytes backing | 接受经过验证的效果；跨 owner 操作保留身份与成功确认，观察记录只能是 detached 副本。 |

建议保留 instruction identity、warp/CTA residency epoch、uop/lane/bytesel 和 memory tag 的关联，以区分分包、重试与 slot 复用；它们是候选关联信息，不是新增 ISA 字段。失效请求的取消和异常 teardown 仍由 `u-visibility` 关联的功能未决项约束。

## 基于实际实现的复用评估

| 位置与可定位符号 | 评估和理由 |
| --- | --- |
| `isa/decode.go:Decode`，`isa/integer.go`、`float.go`、`system.go`、`custom.go` 的 Evaluate，`isa/effects.go` | 可复用严格 decode、功能计算及 typed effects；不复制 opcode 方程或把 RTL 宽松 default 解释成合法指令。计算何时调用可适配，计算结果不定义硬件延迟。 |
| `emu/state/view.go` 与 `integration.go:WarpSnapshot.Evaluate/CompleteMemory/CompletePackedLoad` | 可借鉴 detached 最小 operand/context 构造及统一 evaluator dispatch；必须按实际 operand 读取事件捕获寄存器，并保存原指令 PC、mask 和响应覆盖，不在返回时读取另一条指令的最新 PC。 |
| `emu/state/apply.go:StageEffects/EffectStage.commit/CommitWithExternal` | 需适配。当前校验整个 before-image，成功后替换整个 WarpState，且同步 external callback 成功后本地提交不能失败。流水重叠时旧 stage 会 stale；删除 stale 检查或延后整体 replacement 都会覆盖别的已完成结果。保留效果验证规则，另行设计具名字段与事件的交付协议；本阶段不改现有 API。 |
| `emu/warp/warp.go:Step/completeMemory/completePackedLoad/writeStores` | 需重新组织执行驱动。Step 同步 fetch→decode→evaluate→memory completion→原子 apply，WriteBatch 是整批功能操作；调用一次 Step 再等待 N 周期已经提前暴露所有副作用。只复用底层语义与服务约束，拆分请求接受、响应累积和结果交付。 |
| `emu/core/core.go:Step/selectRunnable`，`run.go` | 需重新组织调度。当前每 Step 只选择一个 runnable warp 并执行完整 Warp.Step，round-robin 与 typed stop/trace 适用于功能推进。可借鉴隔离观察和错误分类，不能复用为 RTL fetch/issue 仲裁。 |
| `emu/core/cta.go`、`barrier.go` 与 `emu/state/spawn.go/launch.go` | 可复用 CTA namespace、membership、局部地址翻译、barrier key/phase 及最小初始化字段的约束；原子 admission、spawn、arrival/release 需要与流水反馈及 drain 事件衔接。不得同时让旧 manager 和新调度器分别持有可写 membership/phase。 |
| `emu/device/launch.go` 与 `kernel.go:KernelExecution.Run` | 可复用 launch 校验、walker 顺序、exact caller backing 和正常 completion 的语义条件；Run 的 reclaim/admit/Core.Step 循环、预算和 sequence 是功能推进，需重新组织成硬件控制交互，sequence 不是周期。 |
| `support/memory/memory.go:Read/Write/WriteBatch` | 可复用有界唯一 byte owner 和 overlap 拒绝契约；锁与同步原子 batch 不模拟 bank/cache/DRAM。时序队列只持请求/响应，不能复制第二份 canonical bytes，也不能把主机锁顺序作为跨 warp 可见顺序。 |
| `support/softfloat/softfloat.go:stateMu/F32*` | 可复用位级计算、rounding 和 flags；mutex 保护 SoftFloat 全局状态，主机计算耗时与锁等待不是 FPU 延迟。`r-fpu-std` 独立决定结果传输时刻。 |
| 离线 `github.com/sarchlab/akita/v5` 依赖 | 仅为未来推进基础设施候选；本 IR 未选择事件 API 或组件组织。不得由依赖存在推断零延迟/同周期处理约定。 |

## 计算、就绪和可见是三件事

计算只产生 detached effects；执行资源达到输出条件时结果才 ready；下游接受或指定 sideband 被 owner 消费后，相关架构字段才可见。一次功能 bundle 可对应多个硬件事件，不能给整个 bundle 指定统一退休时刻。

GPR/FPR 经 `r-commit` 的 WB 和 bytesel/mask 更新；scoreboard 的释放依 `r-scoreboard-edge`，不得由 Evaluate 完成触发。packed load 的完整功能组装可用于验证，但局部 uop WB 需要保存未覆盖字节并防止重复写；整 bundle Apply 不能直接表达这个过程，见 `u-visibility`。

PC/mask/SIMT 由单一控制 owner 接受 branch 或 WCTL/JOIN 的具名反馈。功能 evaluator 的顺序 PC 是语义结果，scheduler 的 fetch PC 是另一种时序用途；必须以明确关联管理，不并行运行旧 Warp.Step 的 PC 更新。`r-simt-barrier-detail` 的控制路径不能等待统一 WB 后才全部生效。

CSR 的 operand/context 读取、软件 RMW、FPU flags 以及 trap 来源须分别对齐 `r-csr-detail`。FFLAGS 不能先由浮点计算修改 owner，再在 sideband 重复 OR；软件覆盖与硬件更新按已确认源级顺序处理，未验证并发仍为 `u-feedback`/`u-csr-stall`，功能 `U-CSR-01` 未闭合。

memory 请求计算地址和 store bytes 不修改 backing；在已定义的服务可见事件只执行一次 owner 操作。load 响应按 tag/lane 收齐后调用 completion 语义，再等待结果/WB；store 的 LSU no-response completion 并不证明外部可见。FENCE、BAR、WSYNC 的 drain 也不能替代可见性协议。混合 local/global 的重复接受疑点保留 `u-mixed-split`，不能通过偷偷增加软件 sent mask 声称符合 RTL。

WSPAWN、CTA admission/reclaim、barrier arrival/release 和 Kernel completion 交给唯一对应控制 owner；时序层保留 pending 身份与确认，避免因 stall 每拍重复到达 barrier 或重建 CTA。异常取消、early-exit、host completion 不从正常功能完成逻辑外推。

## 未决事项与闭合边界

以下仅索引 YAML `unknowns` 的权威问题、证据缺口、影响节点和最晚阶段；不复制其完整登记。新增 `functional_refs` 连接原契约问题，表示相关而非已闭合。

| 稳定 ID | 后续工作方向 |
| --- | --- |
| `u-build` | 获得具体构建定义和被选后端，才能选择运行专属时序。 |
| `u-execution` | 补所选后端及争用路径；变量背压不转成固定延迟。 |
| `u-memory` | 明确服务/可见性边界，保留功能竞态、fault 与 ordering 缺口。 |
| `u-feedback` | 验证跨组件同时事件可达性，不能以软件阶段顺序代替证明。 |
| `u-visibility` | 验证分事件 effect 交付、唯一 owner、packed WB 和 residency epoch；不复用全状态旧事务。 |
| `u-csr-stall` | 定向验证 ready 非限定 CSR 写使能的可见行为。 |
| `u-mixed-split` | 证明或验证混合 lane 非对称 ready 下的子集接受次数。 |

`audit-functional` 与 `audit-timing-rules-partial` 只保留原子范围的 RESOLVED 审计。未来适配验收应包含：重叠指令不覆盖彼此状态、partial WB 只改指定字节、响应错序不串 tag、背压不重复 store/control、旧 residency 的事件不能污染复用 slot。这些是未来验证要求，本次不声称已实现或动态证明。

T9 第三轮新增 `ALUTransition.Branch`、`SFUTransition.Control`、`FPUTransition.Flags` 注册身份通知，以及 `FrontendTransition.Read` 的实际 bank-read 边沿观察。它们均不含第二份架构状态，不计算分支目标、CSR 值或浮点标志。后续 effect adapter 必须在对应事件使用唯一 owner；SFU 的旧态 drain eligibility 仍由调用者提供，CSR 背压窗口与 barrier 外部事件没有被这些通知自动解决。

execution-effects 基础选择（PROVISIONAL，`r-software-effect-delivery-foundation`）：`OperandCapture` 保存逐源 bank-read 值，`EvaluateAt` 将捕获源值与明确执行时点的 CSR/控制上下文结合。`EffectDelivery` 按显式可见事件创建即时 `StageEffects`；不延迟提交旧 WarpState，也不改旧 stale gate。WB lane 子集与其他效果组分别记 receipt，外部 callback 仍必须 all-or-error。该基础随后已绑定 cycle 事件、memory completion 与 epoch；`u-visibility` 在测试范围外继续保留。

第六轮 `r-software-nonmemory-visibility` 已将 owner API 绑定真实 Read/Execute/WB/Control/Flags/Pending 报告，并以 epoch/ID 预检拒绝旧事件。CSR 是明确的未受阻条件；JOIN 独立经过 SIMT 输出寄存器；WGATHER 写掩码沿用 evaluator。memory/fault、Barrier owner 与原子 spawn 交付已实现，完整多 Warp scheduler 不在 Task9 范围，接口范围及错误后的 reset 契约见 [effects/README.md](effects/README.md)。

### Task9 程序运行器增量

`timing/runner` 复用 Core/effects 和原 state/memory owner，以单活动指令排空策略从 canonical PC 自动取指；取指与数据服务均采用显式正延迟，不代表 cache 时间。Akita Run 支持预算续跑，Flush 清除在途服务并增加 epoch，不回滚已可见效果。条件见 `r-software-program-runner`。

本地入口：在仓库根目录 `source env/env.sh` 后运行 `go run ./cmd/timing-run`；`-trace` 输出逐周期 JSON，`-program file.bin` 从 0x100 加载 raw little-endian RV32 镜像。默认四 lane、x1=64+8*lane，内建示例验证依赖 ADDI/MUL/DIV、store/load、分支跳过指令与 TMC 结束。显式 STD、1ps 模型时基、fetch=3/memory=19 cycles；可用对应 flags 修改服务延迟。命令行预算耗尽返回非零；库保留进度可续跑。多目标 spawn residency 仍需 effects.BindSpawn，当前程序入口明确拒绝，不实现多 Warp scheduler。

观测包含旧边沿 CoreReport、提交后的 ResourcesAfter/Residents、Services 及 Events。按 ID/epoch/warp/uop/mask 对应资源生成 enter/stay/advance/leave，读、执行、WB/反馈另有事件；Flush 取消事件保留旧 epoch。位置与 Remaining 是本地资源状态，不推定外部 cache 时间。

### Task9 对象驻留观测与验证

每个资源通过 `Residents()` 返回 detached value，含 Token、位置、局部 Remaining 与 readiness 原因；runner 不访问组件队列。CoreReport.Resources 是旧边沿，ResourcesAfter 是本次统一提交后状态。Events 的 enter/leave 对应本周期提交的转移，stay/advance 表示资源仍持有该对象；tag/context alias 保持独立资源身份，不能累加为指令数量。FIFO 中的 queue-order、输出 awaiting-transfer、执行 execution-latency、tag response-coverage 与外部 backpressure/control-drain 明确区分；这些原因不宣称解析全部 RTL 仲裁信号。Services 列出原 byte owner 服务队列及显式 due cycle。

程序 trace 测试验证驻留记录闭合、除法 33 周期占用、packed 请求队列填满四项后恢复且每 uop 只 WB 一次；增加 memory 服务延迟或请求背压会延长完整运行，功能结果不变。重复运行记录确定；快照修改不会改变组件 owner。基础注册边界与满队列同时接收/释放继续由 timing/model 门禁覆盖。
