# Task10 control-observability 实施与验收映射

当前交付提供四 Warp 单 Core 并发执行、控制恢复、显式外部控制边界与 trace。
调用者提供四个原功能 owner；不新增 CTA admission/reclaim、Kernel 生命周期、
Cache、DRAM 或其他 Core。冻结 RTL 未修改。软件恢复不宣称 RTL 有通用 squash。

## 控制与恢复

`SchedulerFeedback` 携带执行时锁存的指令身份和 PC/mask 结果。旧边沿反馈与
功能效果在同一 CommitEdge 生效，下一周期才可参与取指。branch/TMC/SPLIT
使用原 sideband 边沿；JOIN 在 SFU control 后一拍输出；WSPAWN 同样先注册
pending，再用注册 SingleActive 门控激活。重复、旧 epoch、非法上下文或未确认
的同 Warp 多反馈/前端冲突在 mutation 前拒绝，不靠 Go 顺序补定优先级。

正常 wstall 防止同 Warp 错路取指。Runner 在外部服务之前调用自动重定向清理，
若有显式注入的年轻工作，按 `(After, Through]` 有界 Warp/epoch/ID 取消资源、
Scoreboard 预留/credit/lock、effects 与排队/已准备响应；保留其他 Warp 与有效
较老工作。指令 ID 上界外的新请求不受旧 tombstone 影响。取消不能撤销此前
已可见的寄存器、CSR、控制或 store，故失败边沿停止，不重试/回滚整个 owner。

`Cancel` 暂停 Warp，`Restart` 使用显式前端上下文并跳过取消上界；有残留前端
或控制工作时拒绝恢复，较老普通执行可继续。`Flush` 清除全部未交付工作，增加
epoch，从 live canonical owner 重建前端；不保证重放放弃的旧指令。两者都是
周期之间的显式软件恢复，保留原严格 stale 校验。

## pending 与外部控制 owner

pending 按完整宏指令身份对资源、effects 和服务去重；包括部分 uop、分片 WB、
服务尾部和 external wait。Warp inactive/暂时无 runnable 均不意味着完成。
`DrainBefore` 与 caller 的 PendingLSU/PendingPriorWork 做 OR，使用旧边沿值。
共享 SFU 有真实队首阻塞，WSYNC 后续请求不能绕过等待 LSU 的控制头。

Barrier 的原效果由 `Options.External` 同步 all-or-error 交付。bar/bar.wait
保留完整等待身份，即使流水已排空也不完成；原 coordinator 决定何时调用
`Release(token)`，队列在下一 Core 边沿产生 wake，之后下一周期才可取指。
接口不负责成员计数、phase 或 CTA 协调。重复、失效 epoch、未知身份的 release
拒绝；bar.arrive 保持原不等待行为。取消/Flush 清除对应等待和排队 release。

WSPAWN 由 `MultiOptions.Spawn` 显式提供原 owner、active mask 与 pre-issue target
snapshot。未绑定保持阻塞；绑定检查只有 source active、目标属于显式四 owner，
且没有旧在途工作。并发源先 drain 较老指令，以复用原严格 StageWarpSpawn
源 PC/mask 合约；这是软件适配限制，不声称 RTL 额外增加 drain。注册激活边沿
复用源/所有目标的原子事务，目标 lane0、PC、mscratch 由原 state API 初始化。
目标有 stale image 则整个事务失败，不部分初始化。没有创建/回收 CTA 或 Warp。

## 观测

`MultiRecord.Warps` 是旧边沿 active/stalled/runnable、前端 PC/mask/epoch、pending
和 LSU receipt 数及 stall reason。`CoreReport.IssueCandidates` 显示 staging token、
注册 eligibility 与当前寄存器/特殊状态/credit/sequence 输入；后者不能当作
未注册旁路。IssueSelected 是仲裁选择，Issued.Valid 才表示接受。Offered 与
InstructionAccepted 表示 fetch 选择及握手。Resources/Events 提供 Collector、
Dispatch、执行、WB、release、wakeup、服务背压和取消身份。返回数据是 detached
值，记录顺序确定。Restart 是上下文事件，Token.ID=0，不代表指令退休。

## AC-017 至 AC-023 证据

| 验收项 | 实现与验证 |
|---|---|
| AC-017 | effects.Feedback/SchedulerFeedback、自动 cleanupRedirects、有界 Cancel/Restart；TestMultiRunnerSIMTAndBranchRecovery 检查 branch/TMC/SPLIT/JOIN 周期、mask 与错路无 Decode；取消测试保留旧 load 与其他 Warp。 |
| AC-018 | 完整身份/tombstone/epoch；TestCancelRangeAndResources 拒绝迟到响应，TestConcurrentCancellationTombstonesUnbegunFetch 拒绝未 Begin 的旧 fetch；SIMT 周期背压、恢复/Flush 在途 Fetch、重复 Release 测试。 |
| AC-019 | DrainBefore、独立 Reap、external-wait 纳入 pending；取消暂停和四 Warp Barrier 排空都不误报 Completed；预算续跑测试。 |
| AC-020 | TestBarrierExternalReleaseAndDrain、TestMultiRunnerWSYNCDrainsOwnWarp、TestMultiRunnerExplicitSpawnOwners：原 owner 边界、阻塞/释放、spawn 两级注册与目标 lane0 结果。原 state/effects stale-spawn/原子性回归继续保留。 |
| AC-021 | MultiRecord.Warps/Events、IssueCandidates/IssueSelected、Offered/Issued/Resources；混合程序断言 runnable/注册选择、inactive pending、重复 trace 相等，恢复测试修改返回事件不影响状态。 |
| AC-022 | 混合 ALU/FPU/LSU 与特殊状态、同 Warp 实际驻留、长 load 前其他 Warp 执行、乱序服务、背压；SIMT/WSYNC/Barrier/spawn、取消/恢复/Flush、全 snapshot/RAM 功能结果对比。组件测试覆盖满队列、credit、RAW/WAW 和 packed sequence。 |
| AC-023 | timing/README.md 的 timing-multi 示例和 verify-all/verify-timing；architecture 与 IR 来源同步；旧 Runner/CLI 明确保留为单活动诊断兼容接口。 |

`TestRedirectCleanupCancelsYoungServiceIdentity` 是显式故障注入的接口级测试：
正常 wstall 不会产生该年轻请求，测试防御性清理在 byte service 前发生；不得
将此 fixture 声称为冻结 RTL 的可达分支预测路径。

## 保留事项

同 Warp 部分反馈组合、CSR request 背压、跨 Warp memory ordering、外部效果故障
仍为 UNRESOLVED。STD 后端和服务正延迟均显式选择，不推导 Cache/DRAM 时间。
本阶段只保留 Barrier/spawn 的显式 owner 协作边界，不负责完整 coordinator/
CTA/Kernel 运行。IR 静态检查与功能/周期测试不是 RTLSIM trace 等价证明。

## 最终门禁结果

`bash scripts/verify-all.sh` 通过：冻结 RTL MANIFEST 完整性、功能回归、空缓存/
vendor/禁网的全仓 build/test/vet 均成功。`bash scripts/verify-timing.sh` 通过，
stdout 为空；`git diff --check` 通过；`git diff --exit-code -- Vortex_rtl` 通过。
第四里程碑现可独立验收。上述 UNRESOLVED 和任务范围外事项不因门禁通过而消失。
