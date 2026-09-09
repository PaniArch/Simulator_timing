# Task10 multiwarp-scheduling 实施记录

本次交付对应第二个里程碑 `multiwarp-scheduling`，不代表整个 Task10
完成。此前 `timing-contract` 已通过独立验收。冻结 RTL 未修改，未使用
网络或外部 reference。RTL 事实与未决同时事件继续由
[multiwarp-contract.md](multiwarp-contract.md) 和 IR `cycle_contracts` 维护。

## 公共接口与连接

`model.NewScheduledCore("std", [4]WarpContext)` 启动自主选择取指的周期
Core。上下文只含 Active/Stalled、前端 PC/mask 和 epoch，由调用者在构造时
显式提供；不含 canonical WarpState、寄存器、CSR 或 memory owner。
`CoreInputs.Instruction` 在此模式必须 invalid；Fetch/Memory response 与 ready
仍为显式外部服务端口。Scheduler 产生全 Core 单调指令 ID，并将 warp、epoch、
PC/mask 经 b-schedule、Fetch context 锁存到 Token，不在执行时重新读取前端值。

`NewCore` 保留旧功能 runner 的显式 token admission；它也使用相同的四 Warp
资源与真实 Scoreboard。`NewFrontend` 是对应的显式 token 组件接口，
`NewScheduledFrontend` 则内置 Scheduler。Frontend.Evaluate 最后的 writeback
参数接实际 Commit 输出；Core 已统一完成此连接。旧 `Issue` 仅保留为 T9
孤立 credit/eligibility 诊断夹具，不再参与 Frontend/Core。

Frontend 持有四个独立深度 4 IBuffer、四个 Sequencer、Scoreboard 的四个
深度 1 staging；共享 Fetch、Scoreboard 输出 skid、Collector 和四类 Dispatch。
每条响应按锁存的 warp 进入对应 IBuffer，按该 FIFO 的旧 full 状态背压。
Scoreboard 输入 ready 驱动各自 Sequencer，只有最后 packed uop 被接收才 pop
宏指令。物理 pop 和 decode 接收同时送 Scheduler 的下一状态方程。

Evaluate 只观察旧态，所有子组件 proposal 由同一 CommitEdge 原子安装。
CoreReport 增加旧 Scheduler/Scoreboard 快照、实际 staging-output Issued、
Decoded、IBufferPop 事件；每 Warp 资源用 `/warp0` 至 `/warp3` 区分。
这里的 Issued 是 reserve/credit 消耗边界，不能混同后续 skid-output into OPC
的 pending increment 边界。

## 调度、依赖和资源规则

- Scheduler 根据旧 active、stall、注册 IBuffer full 选择最低 warp。count
  跨越内部 schedule 接收至物理 IBuffer pop，包含 Fetch 在途项；严格保留
  RTL 三位计数、`count==4` 和 all-full fallback，不能当作 FIFO occupancy。
  PC 在 b-schedule 输出被 Fetch 接收时推进，decode unlock 注册后才消费。
- Decode 复用 ISA catalog 的源/目的 namespace。integer x0 禁止 writeback，
  Scoreboard/Collector 不依赖它；`used_rs` 仍保留 RTL 对编码 x0 源的标记。
  f0 是真实 FPR。FFLAGS/FRM 分别按 bit0/bit1 派生 read/write dependencies，
  CSR write 看编码 rs1/zimm 是否为零，不读取运行时寄存器值。
- Scoreboard 先对 qualified eop WB 清位，再叠加 staging output reserve，最后
  为 incoming replacement 或原 staging 计算并注册资格。同周期 release
  不绕过 ready register；不同 Warp 的相同寄存器编号互不阻塞。
- Sticky R MODEL=1 仲裁只在共享 skid 接收时更新 previous winner/mask；若旧
  winner 仍请求则保留且不移动 mask。未接受时不更新仲裁状态，但组合请求
  集变化可以改变候选。已接受 payload 由 skid 在背压期间保持。
- FU credits 在 reserve 消耗，在 Dispatch 输出被 FU 接收时归还。
  下一资格读取旧 goingfull，不能借用本拍 credit return；序列锁读 next lock。
  `10/01` 获取/释放，普通及冻结 packed-load 的 `11` 保持锁。
- packed 各 uop 都写同一 FPR，后一个等待前一个最终 lane WB eop。
  部分 WB 可以更新字节但不释放依赖。原先要求所有 uop 请求先同时进入
  LSU 的程序测试已改为逐 uop 响应，保留逐 byte/lane 可见性与最终功能比较；
  独立 LSU 队列/标签容量测试不变。

## 可复查的周期证据

`model/multiwarp_test.go` 使用外部显式响应延迟驱动实际连接，断言具体周期：

| 场景 | 事件 |
| --- | --- |
| 四 Warp，Fetch response 延迟 2 | 首轮 schedule E0/E1/E2/E3；首轮 Issue E7/E8/E9/E10；下一轮 schedule E7/E8/E9/E10 |
| Warp0 load 服务延迟 40 | WB E58，RAW dependent Issue E59；其他三个 Warp 已在 E30/E31/E32 完成各自三条指令；最大同时驻留 10 个不同 ID |
| 同 Warp 无依赖流水 | Warp1 第二条 Issue E15，早于第一条 WB E16；无需逐指令排空 |
| packed-byte 服务延迟 12 | Issue E8/E32/E56/E80，WB E31/E55/E79/E103；另一 Warp E30 已完成 |
| ALU Dispatch 阻塞至 E120 | Warp0 先在 E7/E14/E21 Issue，IBuffer 填满后停取；释放后 E123 恢复。Warp1 LSU 在 E8/E15/…/E85 持续 Issue |
| Fetch 总线阻塞至 E10 | 两个可选 Warp 的 request 分别 E10/E11 接收；warp/epoch/ID/PC/mask 保持，inactive 与预置 stalled Warp 不被选中 |

四 Warp 完整 trace 在重复 Evaluate 并逆序安装全部 owner proposals 后逐字段
相同。新增 Scoreboard/Scheduler 单测另覆盖满队列不能借同拍 pop、四 Warp
竞争、RAW/WAW、GPR/FPR/x0/f0、特殊状态读写、同拍 release/reserve、注册
credit 恢复、10/01 序列锁、packed 部分/最终 WB、无效反馈原子失败和 Flush。
同 rd release/reserve 的重叠测试是显式种入的方程夹具，不增加 RTL 可达性声明。

## 验收映射

| 条目 | 实现与测试 |
| --- | --- |
| AC-006 | frontend.go 四套缓冲与 sequencer、Scoreboard 四 staging；Core/Frontend common-edge；完整并发 trace 逆序 proposal 测试 |
| AC-007 | scheduler.go 的上下文/资格/计数；Scheduler 单测和 held Fetch/inactive Warp 连通测试 |
| AC-008 | decode.go、scoreboard.go；dependencies_test.go、scoreboard_test.go；packed Core/效果/runner 回归 |
| AC-009 | merge.go sticky 状态；Scoreboard next-state/credit/lock；Collector/Dispatch 连通及资源背压测试 |
| AC-010 | scheduler_test.go、scoreboard_test.go、multiwarp_test.go、merge_test.go 的具名周期与选择断言 |

## 后续里程碑边界

现有功能 Adapter/runner 仍是单活动策略；此里程碑不放宽 canonical owner
stale 校验。多 instruction receipt、锁存上下文的并发功能交付，以及 branch/
SIMT/WSYNC/BAR/WSPAWN 的 identity-aware 恢复、pending counters、选择性清理
和用户侧 trace 在后续已完成，见最终控制验收映射。当前 scheduled Core 对 wstall 指令停止后续取指，
不凭 timing 层伪造分支结果或恢复 PC；有限组件测试以末尾 ecall 保持停止。

`Idle(serviceIdle)` 检查资源、Scoreboard、前端在途和当前可调度工作；它表示
流水排空，不代表所有 canonical Warp 已 finished。Flush 停止 autonomous
fetch，清瞬态资源与计数并保留单调 ID；外部请求取消仍由调用者负责，不做
架构回滚。未实现的控制输入和效果边界不宣称已经达到 RTLSIM 等价。

## 最终验证

本里程碑已完成，可独立验收（不代表整个 Task10 完成）：

- `bash scripts/verify-timing.sh`：退出 0；成功诊断均在 stderr，stdout 为空。
- `bash scripts/verify-offline.sh`：退出 0；空缓存、vendor-only、禁用网络环境下
  全仓 go list/build/test/vet 全部通过。
- `git diff --check`：通过。
- `git diff --exit-code -- Vortex_rtl`：通过，冻结输入未修改。

全仓门禁包含新增四 Warp 完整流水、资源背压、packed WAW、注册资格与旧功能
结果回归。没有把测试服务延迟解释为 Cache/DRAM 时间或 RTL 精度等价结论。

第三、四里程碑已补齐功能交付、控制恢复和观测；本文件保留第二里程碑的历史
验收记录，当前接口见 control-observability-progress.md，状态层实现见
[concurrent-effects-progress.md](concurrent-effects-progress.md)。
