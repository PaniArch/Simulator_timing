# 周期 effects 适配（增量）

`Adapter` 当前将单活动指令的实际 `model.CoreReport` 接到原 `WarpState` owner。它复用 `isa.Decode`、`OperandCapture` 和已有 evaluator；没有调用 `Warp.Step`。普通与 packed load/store 已连接原 memory completion；执行与读取服务的 architectural fault 使用原 `warp.Fault` 停止接口，保留原 completion 的 FaultEffect 与底层 cause；不猜测 trap，也不声称支持故障恢复。

使用次序：创建 `effects.New(owner, epoch, externalOwner)`，在指令进入 core 前用带 word/PC/mask 的 Token 调用 `Begin`；每次共同边沿 `model.CommitEdge` 成功后，按严格递增 cycle 调用 `Observe`。观察会先校验整个 report 的身份，基于旧边沿 owner 快照读操作数和评估，再交付当前边沿的可见事件。原 `StageEffects` 仍在每次即时交付时执行完整校验。

- ALU/FPU 用实际执行接收；SFU 用 lane-dispatch 后 PE 接收，不能把 Dispatch queue 的接收当成 CSR 执行。
- CSR 仅支持 request window 未被结果背压阻塞的条件，受阻窗口明确拒绝，`u-csr-stall` 保留。
- WB 使用原 evaluator 的语义写掩码；WGATHER 可以写 inactive lane，因此不能简单和 request token mask 求交。当前非访存结果是 full-width 单包。
- Branch/trap、WCTL 与 FFLAGS 用对应注册通知；JOIN 在 WCTL 通知后再经过 `b-simt-feedback` 的一个边沿。
- 普通顺序 PC 在 pending release 更新，这是单活动驱动的模型策略，不宣称等于 RTL fetch PC 更新事件。

全部 WB、控制和需要的 flags 交付完且 pending release 到达后，`Finish` 释放 adapter 记录；调用者仍需等 core/service 排空再发下一条。遇到错误后必须 Reset 到更大 epoch，并同步 flush core、取消服务；不会回滚已经可见的 effects。epoch 不写入或替换 canonical WarpState。

外部控制 owner callback 沿用 `EffectStage` 的同步 all-or-error 契约。单 participant barrier 已通过原 BarrierCoordinator.Stage/Commit 回调验证；Core 调度 slot 管理由调用者负责，多目标 spawn 使用下述专用原子交付接口，不能用空 callback 冒充实际控制完成。端到端测试将已支持指令的最终状态与旧 `ExecuteSingle` 比较，同时检查各可见边沿前的状态未提前变化。

## 显式访存服务

先以 `BindMemory` 绑定原 `warp.MemoryService` 字节 owner。`MemoryAccepted` 后的未来周期，在当前 `Core.Evaluate` 之前调用 `Service(cycle, requestToken, laneMask)`；不能在接受边沿零延迟调用。服务时间与背压由调用者明确提供，不是冻结 cache 延迟。返回的 response 要保持到 core 接受，不能重复调用 Service 模拟保持。

load 在服务事件读取字节，复用 `CompleteMemory` 或 `CompletePackedLoadPart`。响应按 ID/epoch/uop/lane coverage 关联，只有对应 WB 才改寄存器。packed completion 共享原整包校验与组装代码，新增 `ByteMask` 只选择本 element 的字节；owner 在当前状态上合并，不恢复旧整寄存器值。原 `CompletePackedLoad` API 的全覆盖与故障语义不变。

store 的 LSU 完成可以早于字节服务；服务通过原 owner 的单次 Write 或 AtomicMemoryService.WriteBatch 提交全部 lane，成功只执行一次。保守单活动策略在 pending 与全部服务均完成后更新顺序 PC/允许 Finish。fence 接口要求外部 ordering owner，测试验证显式服务前不交付、服务时只交付一次；调用者通过 `ControlAllowed(context)` 驱动 SFU drain 门控。服务错误不回滚先前已经可见的片段。

测试以显式延迟服务比较旧 Warp.Step 参考结果：普通 load 分 lane 返回，store 检查早完成/晚字节可见；packed byte/half 逆序 uop 返回，分 lane WB 并逐边沿检查其他字节不被覆盖。

故障服务会收集本片段各 lane 的响应故障，经原 ordinary/packed completion 验证后返回 `warp.Fault{Kind: FaultArchitectural}`。该失败片段不发布 response、不写寄存器/bytes；adapter 停止并拒绝继续 Observe/Service/Finish。已经成功 WB 的其他片段不会回滚，因此这不是跨片段精确异常保证。原 Warp.Step 的整包事务行为没有变化。

## WSPAWN 与 drain

在 Begin 之前 `BindSpawn(activeMask, targets)` 绑定原 target owner 与 pre-issue Expected 快照；只接受 source 为唯一 active warp 的条件。调用者仍负责 CTA membership 和 slot residency，不会因绑定而创建 scheduler。注册 WCTL 反馈调用 `EffectDelivery.DeliverWarpSpawn`：当场构造 control stage，经原 `StageWarpSpawn` 同时校验并提交 source/targets，成功记录一次 control receipt；不调用普通 external callback 修改 source。过期 target、非 inactive target、source MScratch 不一致均由原事务拒绝。Finish/Reset 清除绑定。

每边沿用同一 ReadContext 调用 `ControlAllowed` 和 `Observe`。BAR 等待 PendingLSU，WSYNC 等待 PendingPriorWork，等待期间停在 SFU 执行接收前；错误放行 wait-only effects 会显式报错。owner 回调在 drain 完成后的注册通知才执行。跨指令测试使用同一 Akita clock/Core/Adapter，显式等待 store tail，连续执行整数、访存、浮点、CSR 与 branch，并逐条对照原功能 owner。

Task10 新增 `Concurrent`：四个显式 owner、按 epoch/warp/instruction/uop 路由、
统一 old-edge snapshot 与每 Warp EffectStream。单指令 Adapter 仍作为各在途
指令的 receipt 引擎复用。接口、并发测试和保留边界见
[并发效果实施记录](../concurrent-effects-progress.md)。冻结 catalog 的 packed
指令仅为 load；上文 load/store 泛指普通内存路径，不表示存在 packed store ISA。

T12 新增 `MemoryRequests` 和 `AcceptMemoryResult`，支持将真实 cache/LMEM 返回的对齐
word 与逐 lane store 应用事件交给 effects，不在完成时再次读取或写入 backing。
普通及 packed load 的寄存器写仍发生在匹配 WB；完整 Runner 接线尚未完成。
详见 [../memory-integration-progress.md](../memory-integration-progress.md)。

跨周期异步 barrier 回归：`TestConcurrentDelayedNonblockingBarrierFeedback`
覆盖年轻 ALU/branch 在先前 edge 完成，迟到 arrive 仍恰好交付且不回退 PC；
`TestConcurrentDelayedArrivalRejectsStaleIdentity` 覆盖重复、旧 epoch 和取消的 residency。
`emu/state/TestEffectStreamDelayedArrival` 覆盖独立 activation 截止线、回调失败与非法阻塞组。
`runner/TestMultiRunnerDelayedAsyncBarrier` 用真实 outstanding load 延迟 WCTL，
检查年轻 ALU/branch 先完成的周期证据。此 runner 测试使用外部事件探针；canonical
BarrierCoordinator 事务另由 `TestBarrierOwnerAtControlFeedback` 验证。
