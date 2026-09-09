# Task10 concurrent-effects

前两个里程碑已通过独立验收。本阶段接入四 Warp 并发功能交付；完整控制恢复、
pending 管理及用户入口观测仍属于第四里程碑。

## 已实现的 state 边界

`InstructionContext{WarpID, PC, Mask}` 是显式 Token 锁存上下文。
`WarpSnapshot.WithInstructionContext` 返回 detached view，保留该边沿 snapshot
中的寄存器、CSR、trap/divergence 数据，只替换评估使用的 PC/mask/lifecycle。
`NewLatchedOperandCapture` 在各银行读边沿从传入 snapshot 捕获实际操作数，
在 EvaluateAt 使用执行边沿 CSR/FRM 等上下文，同时保留 Token PC/mask。
普通 `NewOperandCapture` 的 canonical PC/mask 相等校验不变。

`EffectStream` 只为一个 Warp owner 的一次 residency 保存 control-order
frontier。`NewDelivery(order, context, effects)` 复用原 StageEffects 验证，
立即丢弃候选；交付时从 live owner 重新构造事务，沿用原 Commit stale 校验。
对控制关系的校验使用锁存 PC/mask，未被效果实际修改的字段保持 live 值。
没有跨周期保存一个稍后覆盖 owner 的 WarpState candidate。

软件 PC 规则：更高 instruction order 的控制效果成功交付后推进 frontier；
迟到的普通顺序 PC 被记为已交付但不再修改 canonical PC。其 WB/flags 仍按
各自可见事件交付。迟到的非顺序 branch/SIMT 控制报错，不能静默覆盖或忽略。
mask/lifecycle 只由对应 control effect 修改，不能由顺序完成恢复旧上下文。
这是 PROVISIONAL 软件可见性规则，不声称 RTL 有按序 retirement/ROB。

各 memory fragment 可为同一个 order 创建独立 delivery，必须由上层按
(epoch, warp, instruction, uop, lane mask) 校验身份、覆盖与去重。
旧 residency 的所有 delivery 必须取消后才可创建新的 stream；stream 本身
不负责服务取消或 epoch 校验。stream 的 `DeliverWarpSpawn` 复用原源/目标原子事务，要求 drained source PC/mask
与锁存值相等且 control frontier 未越过；成功后才推进 frontier。通用 forwarded
event 仍要求同步 all-or-error，不能以单 owner 回调代替跨 owner 事务。

## 已有定向测试

`emu/state/effect_stream_test.go` 验证：

- 同一寄存器的两个源位置分别捕获旧值与新值，而 AUIPC/顺序 PC 使用锁存值。
- canonical PC/mask 改变后继续读取旧 Token 上下文，且拒绝 foreign Warp、
  非对齐 PC、零/越界 mask。
- 较新 TMC 结束 Warp 后，较老 WB 仍写正确 lane，不能恢复旧 mask/lifecycle/PC，
  也不能覆盖已交付的其他寄存器/CSR。
- 较老 flags 累加保持 live FRM 与较新 PC/mask；重复 WB/控制和取消 delivery
  不再产生写入。
- 较老 branch 不会越过 control frontier；旧 StageEffects/NewEffectDelivery
  继续拒绝 stale PC；effects payload 在创建时复制。
- memory completion 晚于较新 PC 更新时仍能创建锁存上下文 delivery。

## 并发 effects 与 Runner

`effects.NewConcurrent([4]*WarpState, epoch, memory, [4]ExternalOwner)` 要求四个
显式 owner 与槽号一致。每 Warp 共用一个 EffectStream，每条宏指令按
`Identity{Epoch, Warp, ID}` 保存独立 Adapter receipt engine；packed uop 与 lane
fragment 在该 engine 内独立索引。没有共享 current 指针切换。

`Observe` 先验证所有事件身份与类型，将同一边沿事件按指令分组，再统一采样
四个 owner 和外部指针上下文，最后交付。读与执行总是使用 old-edge snapshot，
不因另一个 engine 提前 WB 而得到旁路值。排序仅保证诊断确定性；同 Warp
branch/control 或 flags/CSR 同时事件在任何 mutation 前显式报 UNRESOLVED。
未知身份、重复边沿及非法服务使整个 adapter 停止，不能重试失败边沿。
`Reap` 仅依赖该指令的 pending、WB 与服务尾部完成，不等待 Core Idle。
`Reset` 取消所有未交付 receipt 并增加 epoch；调用者须同时清除 Core 与服务。

`runner.NewMulti(owners, memory, MultiOptions)` 连接 NewScheduledCore，前端每次
Decode 接受都建立在途上下文，不检查整 Core Idle。四 Warp 独立推进，Run
预算结束可续跑。Fetch/Memory 按完整 Token 身份排队，响应在接受前保持；
服务只执行一次。FetchCycles/MemoryCycles 必须为正，MemoryDelay 可指定各
请求的外部延迟；Ready 提供请求背压。这是显式外部服务策略，不模拟 Cache。
MultiRecord 提供每边沿报告、驻留资源、服务 due、在途数量和独立完成身份。

## 验收映射及测试

- AC-011：NewMulti 显式四 owner，Decode admission 与独立 Reap；仅最终结束
  检查 Core Idle，普通指令准入不依赖它。
- AC-012/013：Concurrent 身份路由与统一 old-edge snapshot，所有单指令、
  memory、packed delivery 使用相应 stream。`TestConcurrentOldEdgeSnapshotAndIndividualReap`
  验证同边沿旧指令 WB、新指令读不能互相污染，并能独立释放。
- AC-014：上述 state 测试与 `TestConcurrentPartialLoadAfterNewerTermination`
  验证 TMC 后迟到的分片 load 不恢复旧 PC/mask；各 lane 与无关效果保留。
- AC-015：沿用原 Service/receipt 对 uop、mask、响应覆盖、WB 和重复服务的
  检查；Concurrent 先匹配完整身份。实际集成覆盖普通 load/store、packed load、
  乱序响应和背压，定向测试覆盖分片 load；既有 packed 分片测试继续通过。
  冻结 ISA catalog 只有 byte/halfword packed **load**，没有 packed store
  指令（isa/catalog.go 的 Memory.Packed 定义）；不发明新 ISA 方程。
- AC-016：`TestMultiRunnerConcurrentFunctionalResults` 运行四个真实程序，
  比较所有 owner snapshot 与整个 RAM 的功能执行器结果。断言同 Warp 多条
  指令驻留、每个 Warp 在长 load WB 前实际执行，非 packed Warp 至少执行两条、不同指令同边沿
  读/WB、较新 TMC 早于长 load 完成。包含整数乘法、FADD/FMUL、AUIPC、普通
  load/store 和 packed byte load，重复 trace 必须相等。第三次运行改变请求
  延迟与周期背压，明确验证 Warp3 的响应先于早发的 Warp0 请求返回。

## 当前控制边界

旧 Runner/CLI 和单指令 Adapter 保持诊断兼容。MultiRunner 已接入 branch、
TMC、SPLIT/JOIN、WSYNC、Barrier Release 与显式 WSPAWN owner 绑定，以及
定向取消/Restart/Flush 和 trace；最终验收映射与限制见
[控制与观测](control-observability-progress.md)。外部 coordinator、完整 CTA/
Kernel orchestration 与 RTLSIM 对齐不在 Task10 范围内。

## 验证结果

`bash scripts/verify-offline.sh` 通过（空缓存、vendor、网络禁用，完整 build/test/vet）；
`bash scripts/verify-timing.sh` 通过且 stdout 为空；`git diff --check` 通过。
`git diff --exit-code -- Vortex_rtl` 确认冻结 RTL 未修改。第三里程碑可独立验收，
这是第三里程碑历史验证；当前第四里程碑结果见最终控制实施记录。
