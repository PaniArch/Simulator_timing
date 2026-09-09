# Task10：四 Warp 周期事件契约

本页保存已验收的 `timing-contract` 审计记录，不是整个 Task10 完成声明。后续 `multiwarp-scheduling` 的实现与验收映射见 [实施记录](task10-progress.md)。已完整读取 `Task/T10.md`、`Task/T9.md`、`emu/docs/architecture.md`、Task9 `timing/implementation.md` 和现有 `ir.yaml`，重新核对 Scheduler、Fetch、Decode、IBuffer/Uop、Issue/Scoreboard、OPC、Dispatcher、Commit，以及 branch/WCTL/SIMT/barrier/FCSR 和仲裁/计数 primitive。冻结输入未修改；没有使用外部 reference 或 RTLSIM。

结构化事实以 `ir.yaml` 的 `cycle_contracts` 为准；本页解释边界和实施后果。`FROZEN/rtl_fact` 表示配置条件下的源码事实，`PROVISIONAL/software_interface` 是软件接口选择，`UNRESOLVED/unresolved_pair` 保留尚未验证的同时事件。`rtl_anchors` 是 `source-id::逐字源码片段`，检查器验证片段确实存在于登记的冻结文件；原有 evidence 的模块/信号定位继续保留。文本锚点存在性和静态检查不等于 RTL 时序等价证明。

## 共同边沿与三种上下文

`E[k]` 从旧寄存器求稳定组合信号和握手，随后统一安装 next-state。寄存器在 E[k] 后输出新值，下游最早 E[k+1] 采样；不能把两者再计两拍。每个 contract 的 `consumer_offset` 只测量该条 `trigger` 到具名 `consumer`，不表示完整指令延迟。

`cc-frontend-context` 将三个事实分开：Scheduler 前端 PC/mask 用于下一次选择；Token 的 PC/mask 在内部 schedule_fire 锁存后不变；canonical WarpState PC/mask 继续由既有功能 owner 接受效果更新。C-disabled `schedule_if_fire` 同时写 fetch tag-store 和推进前端 PC，尚不表示 cache 总线已接受请求。返回数据使用保存的 wid context，不能重新读最新 WarpState 或 Scheduler mask。正常 stall→decode feedback 链防止同 Warp 未消费的取指 context 被新请求覆盖。

`cc-software-context-flush` 只规定后续接口方向：显式绑定四个既有 owners，按 epoch/warp/instruction/uop 关联锁存上下文、读事件和独立效果 receipt。每个可见事件从 live owner 创建即时事务；不得保存稍后替换整 Warp 的旧 candidate。顺序 PC 的并发完成策略必须避免较老慢指令晚完成时把 PC 回退；现有 `Adapter.Begin` 的 canonical PC/mask 相等校验和单 `current` 限制尚需后续扩展，本轮没有放宽旧 owner 的 stale 校验。

## 可复查证据与周期后果

以下路径均相对 `Vortex_rtl/hw/rtl/`；源码符号比行号更稳定，YAML 另附逐字锚点。

| Contract | 冻结证据 | 实施约束 |
| --- | --- | --- |
| `cc-fetch-eligibility` | `core/VX_scheduler.sv:448–537`，ready/preferred/all-full、wid_select、out_buf；`libs/VX_priority_encoder.sv` 默认 REVERSE=0 | Fetch 选最低 eligible wid；四 Warp 共用一个输出。使用旧 active/stall/full；更新后的 unlock 不穿透当拍选择。 |
| `cc-ibuffer-accounting` | Scheduler `g_ibuf_cnt`；`core/VX_ibuffer.sv:37–95`；`core/VX_uop_sequencer.sv` input_if.ready | Scheduler 信用从内部 schedule_fire 到物理 FIFO pop，包含未到 decode 的工作；物理占用只从 decode fire 到 pop，不能合并。L1 all-full 是偏好例外，不是四项硬 admission 上限。 |
| `cc-decode-unlock` | `core/VX_decode.sv:307–397,561–610,877–902`；Scheduler decode unlock | 接受普通 decode 后一拍消费通知，再下一拍可重新选择。branch/jump/trap、TMC/PRED/SPLIT/JOIN、WSPAWN、BAR wait/sync、WSYNC 保持 stall；非零 rd 的 BAR.ARRIVE 不等待控制 unlock。 |
| `cc-reserve-release` | `core/VX_scoreboard.sv:106–215`，writeback_fire、staging_fire、ibuffer_fire、in_use_mask | Reserve 在 staging **输出**进入共享 skid 时；不是输入进入 staging，也不是 OPC 读完。WB release 后叠加 reserve，再计算后继 readiness，最后注册。 |
| `cc-special-dependencies` | Scoreboard ibf/stg_opd_mask、xregs_mask；Decode rd_xregs/wr_xregs；`VX_gpu_pkg.sv` NUM_XREGS | 每 Warp 分离 GPR/FPR namespace，used 源和 wb 目的都检查；特殊依赖是 read/write masks 的并集。f0 不能按 x0 处理；仅按 enabled decode 派生 FFLAGS/FRM busy。 |
| `cc-fu-credit-lock` | Scoreboard fu_pending、fu_locked_n、operands_ready_n；`core/VX_dispatcher.sv:43–60`；`libs/VX_pending_size.sv` | 共享每 FU credit 从 skid 输入接受到 dispatch 输出接受；旧 goingfull 与 next lock 分别进入注册资格。guard 不新增队列，不代表只有三项容量。 |
| `cc-issue-arbitration` | Scoreboard out_arb；`libs/VX_generic_arbiter.sv` g_round_robin；`libs/VX_rr_arbiter.sv` g_model1 | 四请求 R、sticky=1、MODEL=1、LUT_OPT=0。只有有效 grant 被共享 skid 接受才更新状态；保留上次成功 winner，直到它不再 eligible。 |
| `cc-pending` | `core/VX_issue_slice.sv:99–101`；`core/VX_issue.sv:99–106`；`core/VX_commit.sv:64–87`；Scheduler per_warp_ctr | Pending 的增加来自 Scoreboard **输出**被 OPC 接受后注册通知，减少来自 WB eop 后注册通知。它不涵盖所有 frontend work，也不是外部 store 可见性。 |
| `cc-packed-release` | `core/VX_uop_packld.sv:40–58`；IBuffer 的两个 1；OPC simd_iter；`core/VX_lsu_slice.sv:258–322,411–416`；Commit byteen/eop | 每个 element uop 继承相同 rd/wb 和 lock=unlock=1。Scoreboard 全寄存器 WAW 使后一个 uop 等前一个 eop 释放。 |
| `cc-commit-visibility` | `core/VX_commit.sv:39–137`；`libs/VX_stream_arb.sv`；package EX indices | P 合流 ALU→LSU→SFU→FPU，无 ROB；注册 WB 无 ready。wb=false 仍可完成并释放特殊依赖；pending 另晚一拍。 |
| `cc-control-visibility` | `core/VX_alu_int.sv:331–350`；`core/VX_wctl_unit.sv:181–252`；Scheduler split_join.OUT_REG、wspawn_valid/is_single_warp；`core/VX_bar_unit.sv` unlock_valid_r；`fpu/VX_fpu_unit.sv:248–261` | Branch 来自 INT result fire，WCTL 来自 execute fire，各自独立于 Commit。JOIN/barrier unlock 另经注册；spawn 的 pending latch 和 single-active register 不能省略。 |

IBuffer 的 L1 all-full fallback 允许调度信用超过物理深度：例如四个 size 都是满值且其中 Warp 可运行时，仍可能接受 schedule 预留；该 Warp 随后因 stall/返回背压停住。源码 `ibuf_full_n` 是 `size_n == IBUF_SIZE`，不是 `>=`；不得以饱和 counter 或普通 `!full` admission 悄悄替换。物理 FIFO 满时仍拒绝新 decode，即使当拍 pop。更广泛可达性验证仍属于 `u-feedback`。

## 同拍 WB、替换 staging 和注册资格

`cc-reserve-release` 的方程可以直接按每 Warp 的位集合实现：

```
released = old_busy & ~qualified_WB_release
next_busy = released | accepted_staging_output_reserve
candidate = ibuffer_fire ? incoming_uop : old_staging_uop
next_ready = no_dependencies(candidate, next_busy)
             && !old_fu_goingfull[candidate.FU]
             && !(next_fu_locked[candidate.FU] && candidate.fu_lock)
current_requests[w] = old_staging_valid[w] && old_ready[w]
```

若 A 在 E[k] WB，B 已在 staging 等待 A，B 只能在 E[k+1] 最早离开；E[k] 不能用 next_ready 发射 B。若一个已就绪的旧 staging 在 E[k] 离开，同时新的 C 进入同 Warp staging，则 E[k] 计算的是 C 对包含新 reserve 的 next_busy 的依赖。只依据 WB 后 busy 而漏掉旧 staging 新 reserve，会放过 RAW/WAW。WB 对未 reserve 的 rd/wr_xregs 在 RTL 还有 runtime assertions，软件应做身份校验而非静默清位。

credit 的门控与依赖 release 不同：goingfull 来自旧注册计数标志，因此一次从门限向下的 dispatch release，先更新 goingfull，再在后续边沿更新 registered readiness。不能把同拍 credit 回收直接当作新发射资格。fu_locked_n 则在当拍 issue 方程中更新并进入 next_ready；普通 uop 的 `11` 不改变锁，`10/01` 才分别 acquire/release。当前冻结 pack-load 并不产生这种跨 uop 锁。

Sticky R 也不是“停顿时锁定当前组合 winner”。reset 后先选最低请求；成功接受 w2 后，w2 仍 eligible 就继续选 w2；w2 消失时从保存的 RR mask 选择。没有接受就不改 prev_grant/mask；eligible 请求变化仍可能改变组合 winner。它没有持续请求的有界公平保证。Task9 的 `timing.Arbiter` 原先只支持非 sticky Merge。Task10 已同步扩展 accessor 与 Merge 的 previous-winner 状态和握手测试；不能仅让解析器接受 sticky=1 后丢弃这个参数。

## Packed 与 pending 粒度

`cc-packed-release` 明确三种不同结束：最后 element 进入 staging 才 pop 一条 IBuffer instruction；每个 element 的最后 lane response 最终形成该 uop 的 WB eop 并清 rd busy；整条功能 packed instruction 完成要求所有 element/lane 的独立 receipts 和服务结束。部分 WB 的 byteen 可以先写寄存器，但不清 busy，也不减少 pending。不能等宏指令最后 element 才清 busy，否则相同 rd 的后续 uop 永远等不到发射；也不能把第一片 WB 当 eop。

Task9 token-only packed 测试允许多个相同 rd 的 uop 进入 LSU request queue，因为旧 Issue 尚未实现完整 hazard。这些是组件容量/效果覆盖测试，不能作为 Task10 并发 packed 吞吐证据。Task10 保留低层容量测试，真实 Scoreboard 路径已增加逐 uop WAW/release 验证；packed 程序/效果测试按这个边界供给响应。`cc-pending` 中的硬件 instret/pending 按 uop 记账，不能与程序 runner 的宏指令 completed 数混用。

WSYNC 在执行前已经进入 pending，所以使用 almost-empty 而非 empty；old pending 从 2 降到 1 的提交当拍仍阻塞 WCTL。BAR 独立使用 LSU scheduler drained。软件正常完成还应联合前端、在途 token、等待上下文和外部服务 tail；四 Warp owner 均 inactive 或 pending 为零中的任何单一条件都不足以报告整个模型 drain。

## 控制、同时事件与恢复边界

`cc-control-collisions` 保留 `u-feedback`；下面列出的源码次序是 **FROZEN 局部字段赋值事实**，可达性和跨 owner 组合仍是 **UNRESOLVED**，不是一个通用软件事件队列优先级。

| 同时事件 | 已确认方程或边界 | 未闭合部分/后续处理 |
| --- | --- | --- |
| 同 Warp WB release + staging reserve | 后 reserve 覆盖相同 bit；资格注册 | 后续测试替换 staging、RAW/WAW 和无效 WB 身份；不允许 Go 顺序改变方程。 |
| 同 FU issue + dispatch release | counter 净增零；readiness 读旧 goingfull | 后续测试门限前后一拍及不同 Warp 共享 credit。 |
| 同 Warp issue/commit 注册通知 | pending 净增零 | 验证 uop 身份、partial eop、WSYNC 自身 pending；不能只按宏指令计数。 |
| 解锁 + internal schedule_fire | 最后的 schedule assignment 设 stall | 对原本 stalled Warp，旧 eligibility 已阻止同拍重新选它；其他反馈组合的完整可达性仍未证明。 |
| branch/JOIN redirect + schedule_if_fire 同 wid | C-disabled 最后按锁存 payload.PC+4 写 PC | 正常 wstall 链通常隔离二者；不得据局部顺序授权投机错误路径或绕过身份检查。 |
| 多种控制修改同 wid | CTA→decode unlock→pending spawn→TMC→SPLIT→JOIN→bar unlock→WSYNC→branch→schedule stall→schedule_if PC advance，仅覆盖各自所写字段；reset 优先 | 非同字段更新可合并；重叠 owner effects 须先验证来源/可达性，否则明确拒绝，不能任意选一个。冻结 RTU/C 分支不参与。 |
| 新 WSPAWN 到达 + 旧 pending spawn 激活 | sequential 中先置 pending 后清 pending；single-active 是旧注册 flag | 两个 pending spawn 的合法来源及外部 residency API 冲突保持未决，不扩展 CTA orchestration。 |
| CSR write + synchronous trap | scheduler 硬件 trap CSR 写在软件 trap CSR 写之后；redirect 读取旧 mtvec/mepc | 跨指令 old-value 与 owner batching 需要验证；不能用最新 owner CSR 代替旧边沿输入。 |
| FFLAGS + 软件 FCSR write | CSR data 先 OR flags 后软件字段覆盖；FRM 读旧 fcsr | 特殊依赖使哪些组合可达仍需验证；`u-csr-stall` 的 request-valid 无 ready 写使能疑点保留。 |
| redirect/取消 + 服务到期/WB | 既有功能 owner 不提供已可见效果回滚；RTL 没有一般 branch squash 接口 | `cc-software-context-flush` 是软件恢复选择。取消 younger 身份及未交付服务，不清掉 older/其他 Warp 的工作；对已可见副作用不得宣称回滚。 |

正常 branch/SIMT 指令在 decode 不解锁，所以无须凭空添加错误路径取指或固定 flush penalty。显式软件恢复仍需支持选择性清理与 epoch/身份隔离，但必须与 `r-flush` 的 DCR cache flush 区分。多 owner 同时事件在真正交付前应整体校验，不能通过逐个 Go callback 的先后擅定 RTL 优先级。

## 本里程碑验证与后续实现

`timing/check/cycle_contract.go` 强制完整 contract 集合、事实/接口/未决分类、具名边沿与非负整数周期、引用、RTL 逐字锚点、冻结四 Warp/单 Issue 形状、pending/credit/hazard 资源和 fetch/issue/commit 仲裁 profile。变异测试从真实合法 IR 出发，证明删除事件、改错 reserve/release/packed 边界、抹掉 unresolved、丢失 sticky 或伪造 RTL 证据都会被拒绝。它不执行 RTL，也不验证自然语言规则的全部语义。

首里程碑完成时，后续工作包括实现四 Warp Scheduler/Scoreboard 和 sticky 仲裁，把本契约绑定公共 Evaluate/CommitEdge（现已实现，见实施记录）；扩展锁存上下文与多 instruction effect receipts；加入注册资格、控制恢复、packed、长延迟切换和真实程序观测测试。首个 timing-contract 里程碑保持所有运行时实现和 canonical ownership 不变，不宣称已完成 Multi-Warp Core、cache/DRAM 或 RTLSIM 精度对齐。

最终本地验证：`bash scripts/verify-timing.sh` 退出 0，单独捕获 stdout 确认为 0 字节，诊断保留在 stderr；`go test ./timing` 通过，包含新增仲裁元数据读取与拒绝 sticky 降级的测试；`git diff --check` 通过；`git diff --exit-code -- Vortex_rtl` 通过。工具链与依赖使用服务器固定环境及 vendor，没有下载。

| 本里程碑验收项 | 交付证据 |
| --- | --- |
| AC-001 | 本页读取/审计记录及逐模块源码定位；完整既有功能契约与 Task9 边界保持 |
| AC-002 | `cycle_contracts` 的 eligibility/context/IBuffer/pending/reserve/FU/arbitration/control 条目及 `rtl_anchors` |
| AC-003 | `cc-reserve-release`、`cc-packed-release`、本页边沿方程和 packed 三种结束边界 |
| AC-004 | classification/status、`cc-control-collisions/u-feedback`、逐字段碰撞表及 PROVISIONAL context/flush 选择 |
| AC-005 | `timing/check/cycle_contract.go`、真实 IR 变异测试、`timing/arbiter_test.go` 及上述通过的门禁；冻结 RTL 未改 |
