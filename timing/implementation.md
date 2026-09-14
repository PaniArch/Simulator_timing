# T9 周期组件实施记录

当前里程碑 `cycle-components` 已完成组件、连接与本地验证，可提交独立验收。本页记录可继续的实现状态，不替代 Harness 的冻结验收计划。

## 已读取与核对

实现前已完整读取 `docs/history/tasks/T9.md`、`docs/history/tasks/T8.md`、`emu/docs/architecture.md`，以及 timing 的 README、architecture、rules、integration 和完整 ir.yaml。核对了冻结包 README 的版本与排除范围。

当前实际采用的缓冲规则已直接核对 `VX_elastic_buffer.sv` 的 SIZE 分支、`VX_pipe_buffer.sv` 的 ready 链、`VX_stream_buffer.sv` 的 valid_in_r/valid_out_r 和两种数据 mux，以及 `VX_fifo_queue.sv` 的 pending/full、look-ahead RAM 与注册 head bypass。

第二轮进一步读取并核对 `VX_fetch` 的 C-disabled 请求/响应与 tag 写入、`VX_ibuffer`/`VX_uop_sequencer`/`VX_uop_packld`、`VX_scoreboard` 的 staging/registered readiness/FU pending、`VX_opc_unit` 的 bank 仲裁/partial passes/两级 RAM metadata、`VX_dispatcher`、`VX_alu_muldiv`/`VX_serial_div`/`VX_elastic_adapter`、ALU/SFU 实例与 PE switch、STD FPU 参数/serializer 的 global enable、CSR context counter 与无 ready 写使能、Commit P arbitration/ack-free WB/registered pending。冻结配置头的 IBUF、dispatch 和 STD PE ratio/latency 已交叉核对。LSU slice/内部 request queue、完整 FU 合流和 sideband 组合还须继续审计与实现。

## 已实现的基础

`PROVISIONAL`：`timing.Buffer` 从编译时嵌入的 YAML 按稳定 boundary ID 读取容量和输出编码；生产代码没有第二份数值默认表。未知 ID、非缓冲边界、不支持的参数明确拒绝。`timing/ir_test.go` 同时验证典型已审查值和修改 YAML 后实际参数跟随变化。

`PROVISIONAL`：`timing/model.Buffer` 独立持有容量、占用、在途 Token 与版本。公开内容和端口均为值副本，不携带功能 owner；Evaluate 只生成 proposal，CommitEdge 先验证全部 proposal 再安装，重复或 stale proposal 不产生部分更新。Flush 只清 transient state，外部请求取消和架构 rollback 不在该接口内。对应 `r-software-edge`。

`PROVISIONAL`：`timing/model.Clock` 使用现有 vendor Akita 的 timing.SerialEngine，一次事件推进一个公共边沿，支持有限预算、继续和停止。显式 period 只决定模拟时间刻度；不声称某个外部 RTL 构建频率。驱动自行保留 callback error，因为本版本 SerialEngine 丢弃 Handler 的 error；失败后不安排新事件。对应 `r-software-clock`。

已验证容量上限、满后背压和输出保持、同时接受/释放、直通与注册边界、提交顺序反转、stale/duplicate 全边沿拒绝、flush 和 snapshot 隔离。SIZE=2 的两种 OUT_REG 共穷举每种 65,536 个八边沿 valid/ready 历史，与独立的 RTL valid 寄存器方程 oracle 比较。链测试验证三个注册边界和一个直通边界恰需三个边沿差。

## 第二轮组件增量

`PROVISIONAL`：公共 Transition 已扩展为多个私有 owner mutation 的组合；混合缓冲、计数器、执行单元和复合组件也先整体校验，再统一提交。上层没有 next-state 引用或 owner 修改入口。

- `Fetch` 在 req_buf 输入握手时分配每 Warp context，保留 iflush request buffer；外部请求尚未接受的响应、过期响应拒绝。同 Warp context 在响应被消费前不覆盖。`DecodeToken` 复用严格 ISA Decode，只补 timing 路由和源寄存器标识，不产生架构副作用；没有 Decode 寄存器。
- `Sequencer` 对普通指令直通，对 packed 指令先捕获首 uop，再在输出握手时推进；最后 uop 才 pop 上游 ibuffer。`Issue` 持有 staging、输出 skid、registered eligibility 和 FU credits；eligibility 由外部显式提供，尚未实现完整 scoreboard/hazard。credit 计数不增加物理 queue 容量。
- `Collector` 逐 bank 保留 fetched mask，最后一次 bank pass 才接受上游；RAM read observation 在 pipe_reg1 fire，两个 metadata 寄存器加独立输出 skid 组成已登记的 read latency。新增 `b-opc-read-first/second` 是 `b-opc-read` 的细分，不能重复计容量/周期。
- `Pipeline` 用整体 enable 模拟 RV32 MUL；`Divider` 在算术完成和输出停留期间保持 loaded，pop 当拍不能再 push。`NewSTDPath` 明确选择 STD 子路径并从测量中扣除已单独实例化的 serializer skid；未默认选择实际外部构建，header pool/backend merge 尚未连接。
- `WaitPool` 只持请求身份和 lane coverage，响应直通到下游；满资源不借用同拍释放槽，partial response 不提前释放。`CSR` 保留 context wait 和 result buffer，并暴露 request-valid window，尚不应用 CSR effects。`Commit` 固定 P 顺序汇合四类结果，WB 无 ready，pending release 晚一个注册边界。

新增测试覆盖两类整数执行延迟和吞吐/停顿、全部 STD 子路径测量边界、bank conflict/相同寄存器冲突/x0 与 f0、满 OPC、Issue 注册 eligibility/credit guard、packed burst、CSR 背压窗口、Commit priority/WB/pending、load 错序/partial/重复/epoch、Fetch 注册请求与直通响应，以及全部 ISA catalog 的路由可达性。`timing.Number` 强制按稳定 ID 读取整数并拒绝 null/缺字段/非整数，构造器还检查测量间的结构关系。

## 第三轮组合增量

进一步核对 `VX_rr_arbiter` MODEL=1、`VX_generic_arbiter`、`VX_stream_arb` 的 grant-ready 位置，ALU/SFU 的 PE 顺序、INT branch_reg、WCTL 的 BAR/WSYNC drain，STD FPU 的四子核顺序、tag_store/full 门控、allocator 注册 full 和 FFLAGS 注册反馈。

`PROVISIONAL`：`Merge` 从稳定 boundary ID 读取 P/R、输入数、STICKY/MODEL 与已有输出缓冲编码；缺项/未知条件拒绝，组合构造器检查端口数。R 的游标在汇合缓冲**输入接受**时前移，输出 pop 和满后停顿均不推进。没有在仲裁后另加一个周期。

`Frontend` 已连接 schedule、Fetch、直通 Decode、ibuffer、Sequencer、Issue、Collector、四类 Dispatch。Dispatch 各自持有四项物理队列，FU 接受队首时释放 credit。所有求值从旧注册输出倒推 ready，最终一次 CommitEdge；没有跨组件私有写入。

`ALU` 连接 INT result1、MUL pipeline3、DIV loaded、MULDIV P merge2、ALU R merge2；full-width lane dispatch/gather 验证为直通。branch/eop 在 INT result-fire 后一个边沿发出身份通知。`SFU` 连接 lane dispatch2、WCTL result2/CSR context+result2、R merge2、gather2；drain predicate 由调用者提供旧态判断，通知只携带 timing identity，未应用控制语义或 barrier 外部事件。

`STDFPU` 是**显式 STD** 构造器，不代表实际外部构建已选定。四条已有 serializer 路径经 R backend merge 合流；两个 header context 在 execute-fire 分配、backend response-fire 释放，满时不能借用同拍释放槽。header 与 datapath 中的 token 是别名，不重复计容量；身份抽象不模拟数字 tag 地址。FFLAGS 身份在返回完成后一拍通知，尚未计算或应用标志。

新增测试覆盖 P/R 的首选/轮转/稀疏请求/停顿，四 Dispatch 队列独立容量及同时接受/释放，ALU 各路径和分支通知周期，SFU drain/CSR 周期，STD 两项 header 满后背压/错序返回/FFLAGS 周期，以及非 eop header 的 tag 释放不改变原 eop。Frontend→ALU→Commit 测试跑普通整数、乘、除、分支；反向提交、前端重复只读求值保持同一完成周期。参数访问测试检查 YAML 修改传播及缺失/未知仲裁参数拒绝。

## 第四轮完整周期框架

进一步核对 `VX_lsu_slice` 的 req_skip/no_rsp/fence_lock、`VX_lsu_scheduler` 的单 client 条件和 MEM_CHANNELS=NUM_LANES、`VX_mem_scheduler` 的 request queue/tag/partial response。源码附近 NUM_CLIENTS=2 的注释不能代替实际 `TCU_META_ENABLE` 条件；冻结配置为 1。LINE_SIZE=WORD_SIZE 禁用内部 coalescer，CORE_REQS=MEM_CHANNELS 禁用 batching；两个输出 buffer 编码均为 0，新增参数核对写入 `res-lsu-request`。

`LSU` 连接 dispatch、scheduler request/tag、load/store result、P merge 和 gather，严格保持共同接受门控。store 不使用 load tag，结果可早于外部请求接受；fence 在入队时锁定、最后响应进入 load result 时解锁，不能同拍借用解锁或满 tag 释放。已测试满 tag 下 store 可继续、未发出/重复响应拒绝、部分响应 eop、fence 停顿以及外部 store tail 保留。

`Core` 已将全部四类 FU 与 Frontend/Commit 连接。每个组件公开自己的 `Resources` 快照，不向上层暴露可写内部状态。`CoreReport` 记录旧边沿资源容量/占用/输出身份、credit、请求和接受、Dispatch、RF read、WB、pending、分支/WCTL/FFLAGS 及 CSR request window。寄存器字段是采样值，资源之间可能是同一 token 的别名，不能简单求和当成指令并发度。

`RunTokens` 和 `cmd/timing-token` 提供 Akita 驱动的独立诊断入口。必须显式选择 STD 后端、时间刻度和测试服务延迟；服务不拥有或修改 memory bytes。单活动 instruction 的全部 packed uop 通知与内外部资源排空后才发下一项；预算耗尽返回错误。测试覆盖四类 FU、全部主要路径和 packed burst。该入口是 token 流，不执行分支跳转或架构副作用，因此不代替后续功能执行器。

## 当前里程碑核对与后续工作

AC-001：前置全文读取及冻结 RTL 核对记录见上文。AC-002/003：Fetch、Decode、Issue、Collector、Dispatch、四 FU 与 Commit 已连接，各 owner 只经端口/proposal 交互。AC-004/005：公共边沿预检、注册边界和握手穷举、满资源/背压/同时事件、重复求值/反向提交及 stale 整边沿拒绝均有测试。AC-006：buffer、整数延迟、STD serializer、tag、bank、credit、仲裁与 scheduler 形状由稳定 YAML ID 读取；访问器拒绝未知项，结构关系和已审查数值由测试检查，新增规则同步 Markdown/YAML。

后续里程碑仍须实现功能 effect 分事件交付、canonical owner 衔接、真实程序推进和相应测试。`u-build/u-memory/u-feedback/u-visibility/u-csr-stall/u-mixed-split` 保留 UNRESOLVED。本里程碑完成不代表整个 Task9 完成。

## 本次验证

`go test ./timing ./timing/model`、`bash scripts/verify-timing.sh`、`bash scripts/verify-offline.sh` 和 `git diff --check` 均已退出 0。离线回归使用固定 Go/SoftFloat、空缓存和 vendor，没有下载依赖。Timing 脚本正常输出 Go 测试结果与 PASS 文本，因此不满足冻结验证描述中的 `expectedStdout: ""`；没有修改该验收条件或脚本来隐藏输出。以上为前三轮的验证记录。第四轮为满足冻结空 stdout 条件，将 timing 脚本的原有完整诊断送到 stderr；检查命令、退出码和验收条件均未改变。最终结果见交付回复。


第四轮最终验证：`bash scripts/verify-timing.sh` 退出 0，单独捕获 stdout 确认为 0 字节，原有测试与 PASS 日志完整保留在 stderr；`bash scripts/verify-offline.sh` 的全包 build/test/vet 通过；`git diff --check` 通过。全部 timing/model 测试约 26 秒，离线环境仅使用 vendor。独立 CLI 已用五项混合 token 验证 JSON 周期连续和五次 WB。当前里程碑准备完成，整体 Task9 的功能衔接与后续里程碑尚未完成。

## execution-effects：第五轮基础增量（里程碑未完成）

已重新核对 `emu/state/integration.go`、`view.go` 与 `apply.go` 的分派、CSR old-value 校验、控制/分歧/trap 一致性检查和 EffectStage stale gate。当前决定是不放宽或复用延迟 whole-state stage，而是每个可见事件从 live owner 创建即时 stage，再同步提交。

新增 `OperandCapture` 按源位置分别捕获 bank-read 值。重复引用同一寄存器的两个源可以来自不同读边沿；x0 自动完成、f0 仍需实际读取。PC/mask 和外部 context 为 detached copy。`EvaluateAt` 允许后续 adapter 在 CSR/FRM/控制的真实执行事件提供当时上下文，保留已读操作数。旧 `WarpSnapshot.Evaluate` 与新路径共用 evaluator 分派；Custom JOIN/barrier 的地址化 view 也从捕获的源值派生。

新增 `EffectDelivery` 保留复制的 effects 与 receipts，不保留延迟 WarpState replacement。WB 支持互不重叠的 lane 子集；控制/trap（包括 trap CSR）、CSR、FFLAGS、memory/ordering、fault 分组独立触发。每次只把当前组交给现有 `StageEffects`，需要外部 owner 的组沿用同步 all-or-error 回调；失败不记 receipt，成功后重复事件拒绝。Cancel 仅阻止未来交付，不回滚已可见效果，epoch 与 residency 仍由下一步 timing adapter 负责。

定向测试证明：bank-read 后源值变化不会覆盖已捕获值；构造/evaluate 不提前改状态；控制先于 WB 时后续 WB 不恢复旧 PC 或无关寄存器；partial lane 不重复交付；外部失败可重试但成功不会重复调用；旧 StageEffects stale 检查继续拒绝。尚未把这些 API 接到 CoreReport，因此不能据此宣称完成可见性或 execution-effects 里程碑。

下一步：建立按 ID/epoch/warp/uop 关联的 timing effect adapter，把 Read、执行接收、WB、控制、CSR/Flags 和 memory 服务事件连接起来；增加普通/packed memory completion、byte 覆盖、store bytes 与控制 owner 的恰好一次交付及端到端可见性测试。`u-visibility/u-csr-stall` 等保留未决，限定范围决定见 `r-software-effect-delivery-foundation`。

第五轮验证：新增 owner API 定向测试及既有 state/warp/ISA 测试通过；冻结 `verify-timing.sh`、`verify-offline.sh`（全包 build/test/vet）与 `git diff --check` 均退出 0。以上只验证本轮基础增量，execution-effects 尚未达到完成条件。

## execution-effects：第六轮周期绑定（里程碑未完成）

新增 `CoreReport.Executed` 和 `CSRRequest`，直接来自 ALU/FPU 接收、LSU slice 接收和 SFU PE 接收，而不是每类 Dispatch 的前置队列接收。新增 `timing/effects.Adapter`：Begin 绑定 canonical PC/mask、指令 word、warp、递增 ID 和 epoch；Observe 在任何效果之前校验 report 全部身份，按 actual Read 捕获源值，按 Execute 用旧边沿 CSR/控制 context 调用共享 evaluator。每事件新建的 EffectDelivery stage 没有跨周期保存旧整状态候选。

已绑定 WB、Branch/trap、WCTL、未受阻 CSR request、FFLAGS 和 pending release。顺序 PC 在 pending release 更新是保守单活动策略，非 RTL PC 时序声明。核对 `VX_alu_int.alu_hdr_in` 后，WB 使用 evaluator 的写掩码，保留 WGATHER 对 inactive non-source lanes 的语义，不与 token request mask 求交。JOIN 增加 `b-simt-feedback.OUT_REG=1` 的反馈边沿。重复/过期/跨指令 report 在效果前拒绝；中途错误停止 adapter，必须以更大 epoch Reset，并由调用者 flush core/cancel service。

定向端到端测试覆盖 ADD/MUL/DIV、branch、TMC、trap、CSR、浮点、WGATHER 和 JOIN，最终状态对照原 ExecuteSingle，并逐边沿检查 WB/控制/CSR/FFLAGS 可见性。另测外来反馈与合法 WB 同报时不会先写 WB、重复周期和旧 epoch 拒绝。

当前 adapter 明确拒绝 memory 和尚未绑定的 fault；完整外部控制 owner 仍依赖后续接线。CSR stalled request window 显式拒绝而非静默握手化。`u-visibility/u-csr-stall` 仅增加限定范围决定，未整体关闭，见 `r-software-nonmemory-visibility`。下一步仍是普通/packed memory 服务及 completion、store bytes、控制 owner 的完整衔接和混合端到端测试。

第六轮验证：`go test ./timing/effects -count=1` 通过（约 33 秒）；冻结 timing、offline 全包 build/test/vet、diff 检查均退出 0。没有修改功能 API 的旧 stale 检查或下载依赖。该结果仅覆盖新增非访存周期绑定，execution-effects 仍需 memory/packed 与完整外部 owner 集成。

## execution-effects：第七轮访存绑定（里程碑未完成）

新增 `timing/effects/memory.go` 与 `packed.go`，绑定原 MemoryService/AtomicMemoryService，CoreReport 携带实际响应身份。请求接受、未来周期 Service、响应保持与 WB 分离；服务调用必须严格晚于请求接受与已观察边沿。普通 load 复用 CompleteMemory，store 通过原 owner 单 Write/原子 WriteBatch 在服务时提交一次，LSU 早完成不提前写 bytes。顺序 PC 保守等待 pending 和全部服务。

packed 按 uop 分别捕获操作数、保存请求和响应片段。新增无状态 CompletePackedLoadPart，与完整 CompletePackedLoad 共用覆盖检查、故障处理和组装；逐片段结果新增 ByteMask，owner 即时合并到当前寄存器值，不恢复旧字节。原完整 API 不变，公共 API 契约仅增加此明确操作。最终控制复用完整 completion，等待全部片段 WB/pending。

新增测试：普通 lw/lb 的分 lane 响应、store 的早 pending/晚原子字节可见；packed byte/half 逆序 uop 与分 lane 返回，对照原 Warp.Step 最终状态并逐边沿检查 lane/字节覆盖；partial completion 缺失/外来 element 拒绝；部分写保留 owner 新字节且旧 stage 仍 stale。模型条件同步 r-software-memory-visibility 和 effects/README。

下一步：完整外部控制/ordering/fault owner 接线及混合路径、fence、服务错误与恰好一次交付测试。当前服务错误仍是明确终止条件，不支持对已可见片段回滚，不能据本轮成功路径测试宣告 execution-effects 完成。

第七轮验证：冻结 verify-timing.sh、verify-offline.sh（空缓存、vendor-only 全包 build/test/vet）与 git diff --check 均退出 0；effects 全包约 49 秒。当前里程碑仍未完成，以上验证不代表 fault/外部控制 owner 已接通。

## execution-effects：第八轮 ordering 与故障停止边界（里程碑未完成）

核对原 emu/warp Fault/architecturalFault：故障是 typed stop，不会自动写 trap。新增共享转换 helper 保留此接口；执行期 FaultEffects 和 ordinary/packed 读取响应故障均返回原 warp.Fault。服务先收集当前覆盖集合再调用原 completion；失败不发布响应、不产生当前片段写入，adapter 停止且必须 reset/flush。已经可见的其他片段保留，不宣称跨片段精确异常。

新增 fence 定向测试，外部 ordering callback 在显式服务时恰好交付一次，此前 owner 不变。load/store 服务失败注入测试验证原 cause、架构故障元数据、无响应/状态/bytes mutation、不能重试或 Finish。新增实际周期 barrier 测试：单 participant bar.arrive 的反馈通过原 CTA/BarrierCoordinator Stage/Commit，注册通知之前 barrier/PC 不变，整个路径只调用一次 callback。没有引入 barrier 影子状态，也没有调用 Warp.Step 作为周期实现。

后续仍需完整单活动驱动的 drain、Core slot/多目标 spawn 接线，以及跨指令混合路径测试与里程碑审计。本文成功的限定范围不能替代完整运行入口的 owner 协调。

后续具体接口审计：WSPAWN 应复用 emu/state.StageWarpSpawn 的 source+targets 原子事务（emu/core.coordinateWarpSpawn 同时校验 CTA membership、唯一活动 source、未使用 inactive targets 和 MScratch）；不能以普通外部 callback 中修改 source 再让外层 EffectStage 替换 source 的方式衔接。需要专门的即时交付接口，保持两侧 stale 检查及一次 receipt。此项尚未实现。

第八轮验证：冻结 verify-timing.sh、verify-offline.sh（全包 build/test/vet；effects 约 61 秒）与 git diff --check 全部退出 0。未下载依赖、未修改既有功能执行 API 或 stale 检查。

## execution-effects：第九轮原子 spawn 与连续执行验证

新增 `EffectDelivery.DeliverWarpSpawn`：只在注册控制事件当场创建包含完整 control 分组的 source stage，调用原 StageWarpSpawn/Commit 一次性提交 source 与全部 targets；没有跨边沿保存 source candidate，没有在普通 external callback 中修改 source。Adapter.BindSpawn 保留 pre-issue target Expected 快照与原 owner 引用、要求唯一 active source；Finish/Reset 清除绑定。调用者继续负责 CTA membership/residency。新增 owner 与真实流水测试验证失败不部分激活、target stale 拒绝、成功不覆盖 source 后来无关寄存器、重复交付拒绝、注册反馈前 source/target 都不变。

新增 ControlAllowed(ReadContext)，以旧边沿 drain context 门控现有 SFU 接收。WSYNC/BAR 等待期间不执行；错误放行 Wait effects 明确拒绝。定向测试在 45 个边沿前保持 prior-work pending，验证有占用但无执行/控制，然后一次性交付 drain 回调。混合测试使用同一 Akita clock/Core/Adapter 与原 state/memory owner，连续 ADD/MUL/store/load/FDIV/CSR/branch，显式服务延迟 17 周期与请求背压，每条含服务尾部排空后与原 Warp.Step 状态/字节比较。测试中的 Step 仅为参考，生产实现未调用。

当前 execution-effects 验收证据：

| 验收项 | 实现及验证 |
| --- | --- |
| AC-007 | timing/model 执行资源与全部路由；容量、延迟、间隔、保持/背压测试；effects 真实执行/WB 绑定 |
| AC-008 | LSU request/response + Adapter.Service；显式非零未来服务与响应保持、部分覆盖、store tail/fence 测试 |
| AC-009 | Decode/OperandCapture/EvaluateAt、原 ordinary/packed completion；mixed 对照原功能 owner |
| AC-010 | 独立 Read、Executed、WB、Branch/WCTL、CSR、FFLAGS、Memory、Pending；spawn 专用原子 control 交付 |
| AC-011 | 原 WarpState/Memory/Barrier owner；即时 StageEffects，旧 stale 检查保留；source/target 原子事务及 byte 合并测试 |
| AC-012 | partial lane/byte、逆序 packed、长等待、store 原子一次、barrier/spawn/drain 注册可见性与故障无当前片段副作用 |
| AC-013 | r-software-memory-visibility / nonmemory-visibility，明确 STD backend、服务时间和错误停止范围；u-memory/u-visibility 等仍保留外部未决 |

完整程序入口、自动取指/单 Warp 控制与观测属于下一里程碑。多 Warp scheduler/slot 选择、跨片段精确异常、外部 backend/cache 时间不在本轮已验证范围内，不宣称由本实现解决。

第九轮最终验证：verify-timing.sh、verify-offline.sh（空缓存 vendor-only 全包 build/test/vet，effects 约 74 秒）与 git diff --check 全部退出 0。execution-effects 当前里程碑实现与定向证据已齐备，可独立验收；整个 Task9 尚需下一里程碑的完整程序驱动与观测交付。

## single-warp-runner：第十轮程序入口基础（里程碑未完成）

新增 timing/runner：从原 memory owner 在显式 fetch 服务时读取 word，以 canonical PC/mask 连续创建新 ID；沿原 Decode/effects/Core 处理，等待所有 uop pending、内部占用及外部 store tail 全部排空才发下一条。服务延迟与 Akita period 显式正值，Ready callback 控制请求背压，Run 预算耗尽保留进度。Flush 清除 core 与服务队列并 Reset 到新 epoch；错误边沿在 Akita 中不前进，因此 fault 后 Flush 先消费该边沿，保证新的 Observe 周期严格递增。

新增 cmd/timing-run，本地内建确定性四 lane 程序或 raw RV32 image（加载地址 0x100）；默认 x1=64+8*lane。内建程序包含依赖 ADDI/MUL/DIV、store/load、branch 与 TMC0，205 cycles/7 instructions 正常结束。程序 tests 对照原 Warp.Step 全寄存器/PC/mask/bytes，验证同输入记录一致、背压延迟并恢复、预算续跑、取消 store tail 不写 bytes、执行故障 flush 后可恢复。生产运行器没有调用 Step。

当前 trace 包含周期、epoch、粗粒度等待 phase 和 CoreReport 的对象身份/占用/边界通知。尚需更完整的阶段进入/驻留原因/离开观测和对应断言、更多延迟/容量敏感端到端验证、最终文档与完整验收核对。程序 runner 暂时明确拒绝需要多目标 residency 的 WSPAWN；原 effects.BindSpawn 仍可使用，不新增多 Warp scheduler。

下一步观测接口位置：timing/model/observe.go 的 ResourceState 目前只有 ID/Capacity/Occupancy/Output；队列深处和尚未到输出的执行对象不可见。需要由各组件提供 detached resident 快照（不能由 runner 直接访问内部队列），再从连续快照形成按 ID/epoch/uop 关联的进入/保持/离开记录，区分等待执行成熟、等待服务与下游阻塞，并补齐断言。现有粗 phase 不能冒充这些完整诊断。

第十轮最终验证：verify-timing.sh、verify-all.sh（含冻结 RTL manifest、既有功能门禁及空缓存全包 build/test/vet）与 git diff --check 全部退出 0。最后的 fault-flush 修复后已重跑完整 verify-all；runner 测试约 14 秒。命令行内建示例已运行成功。single-warp-runner 里程碑仍因完整阶段观测与其验收测试未完成而保持进行中。

## single-warp-runner：第十一轮对象观测与最终验收

每个 storage resource 新增 Residents() detached 快照，覆盖 Buffer 全队列、全 Pipeline stages、STD pipeline+serializer、Divider 的剩余周期、WaitPool contexts、Sequencer、CSR/merge/commit。快照数与 Occupancy 一致，不暴露内部 slices。runner 从提交后的连续资源快照生成按 resource/ID/epoch/warp/uop/mask 关联的 enter/stay/advance/leave；跨资源 context alias 不合并。读/执行/WB/反馈、显式 memory service 完成与 flush cancel 独立记录；Services 给出队列的 due time。原 CoreReport 仍为旧边沿快照，ResourcesAfter 为提交后，两者不会混称。

新增测试验证驻留记录闭合、无重复进入/缺失离开，Divider 在资源中持有 33 周期，packed 程序在请求背压期间填满四项 request buffer、解除后四个 uop 各 WB 一次。增加 memory 服务延迟延长程序且最终语义不变，determinism 覆盖完整扩展记录。组件快照脱离 owner 的测试通过。内建 cmd/timing-run 示例入口现已直接纳入 Go 自动测试。

| 验收项 | 证据 |
| --- | --- |
| AC-014 | cmd/timing-run 默认示例和 raw image；runner 自动从 canonical PC 取指，TMC0 停止；单指令排空策略明确 |
| AC-015 | TestProgramAndDeterminism 对照原 Warp.Step 的依赖算术、MUL/DIV、branch、load/store 最终完整 snapshot/bytes；effects mixed 覆盖 FPU/CSR |
| AC-016 | Residents、ResourcesAfter、StageEvent、Services、Subject；trace 闭合和重复运行完全相同测试 |
| AC-017 | packed request capacity/backpressure 恢复、33 周期 divider 驻留、服务延迟敏感测试；既有原子边沿/注册边界组件与全路径测试 |
| AC-018 | Pending/Completed/Flush；取消 store tail 无 bytes 污染，旧 epoch effects 拒绝，fault flush 周期推进与恢复；正常 completion 等待所有服务尾部 |
| AC-019 | README/architecture/rules/integration/ir.yaml/emu architecture 均同步运行、映射、观测、模型条件与未知项 |
| AC-020 | 最终 verify-timing、verify-all（含冻结 RTL manifest 与功能/离线全包 build/test/vet）、diff 检查 |

范围限定：显式 STD 后端和正服务延迟为模型输入，不冒充外部 build/cache 时间；程序 runner 不调度新 spawn targets，明确拒绝此类 residency 请求，底层 effects.BindSpawn 原子交付仍可由拥有 residency 的调用者使用。未实现完整多 Warp scheduler/scoreboard/cache 或跨片段精确异常，保留原 unknowns。以上不构成 RTLSIM 周期对齐证明。

第十一轮最终验证：verify-timing.sh、verify-all.sh 与 git diff --check 全部退出 0。verify-all 包含冻结 RTL manifest 校验、cmd/timing-run 内建示例测试、功能门禁和空缓存 vendor-only 全包 build/test/vet；runner 测试约 22 秒。当前 single-warp-runner 里程碑已具备独立验收条件，Task9 基础周期模型交付完成；精度与后续调度范围仍按上述条件限定。
