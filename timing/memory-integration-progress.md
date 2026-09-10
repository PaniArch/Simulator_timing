# T12 里程碑 05：运行时接线进度

本里程碑尚未完成。已新增 effects 返回值接口及生产 memsys System 组合；MultiRunner 已有显式 MemorySystem 选项接入真实存储；Kernel 已默认使用真实存储；独立 Runner 默认路径仍使用旧固定延迟
Service。本文件不改变冻结验收目标。

## returned byte contract

`Adapter.MemoryRequests(token)`（以及 Concurrent 同名方法）在 execute 地址计算完成后返回
原功能 evaluator 的普通或 packed 请求副本。它不读取 memory、不标记请求接受，也不重新
计算 ISA 地址。调用方应使用 AlignedAddress、ByteMask/Width、StoreData 构造 SIMDRequest，
保留 packed Element/Uop 身份；不能把不同 uop 的相同 lane 混为一次完成。

`AcceptMemoryResult(cycle, token, MemoryResult)` 接受已由周期存储路径返回的对齐 4-byte word，
Mask 表示本次已完成的 lane。普通和 packed load 依据原请求 Address-AlignedAddress 及 Width
从真实 word 解包；签扩展、packed 寄存器元素更新和最终控制继续复用原功能 completion。
结果仍须经 model 的响应接受和对应 WB 才修改寄存器，不能在结果到达时直接写 owner。

store 结果是已应用事件：effects 不再次调用 Read、Write 或 WriteBatch，也不要求全 mask
同一次应用。每个 lane 只能确认一次；全 mask 覆盖后以无字节修改的 callback 退休原
MemoryEvent，最终控制仍等待原 pending 条件。发生错误时保留此前实际存储副作用，
沿原 architecturalFault 路径报告，不重试整组 store。

接口继续校验 instruction identity、uop、有效 lane、重复覆盖、accepted cycle 和 adapter
边沿先后。Concurrent 通过既有 lookup/取消作用域分派，失败后要求原 Reset 流程。
FENCE 不接受 data result；使用独立 ordering fragment 接口，见下文 inline flush 接线。

回归在绑定 memory 的 Read 被强制失败时，验证普通 load 和全 catalog packed load 的
返回字节完成；两批 store receipt 不产生任何 backing 再写。原 Service 的功能与控制回归
继续保留，以便迁移时检查断言；保留兼容方法不代表允许完整 Runner 最终继续走该路径。

## production component composition

`memsys.NewSystem(globalOwner, localResolver, config)` 组合 I-cache、D-cache、split、coalescer、
两类 adapter、LMEM 和共享 backend。初始化扫描仍需真实 Step，不预先快进。
`Responses()` 返回旧边沿 Fetch word 和 SIMD 响应快照，不推进状态或访问 owner；调用者据此
计算消费 ready，随后每周期一次 `Step`，根据实际 Accepted 保持或撤销请求。
`Stores` 是不可背压的实际应用事件；`Complete` 是全部 lane 交付/应用且子引用释放，不能当作
backing 可见性。完成时删除 global progress/released 辅助记录。

I/D memory ports 拼接到已有确定性后端，这是明确的软件外部存储边界，尚不声明 socket mux
的 RTL 周期等价。所有容量与带宽继续由已有 IR/后端配置决定。System 不新增请求队列。
I/D flush 分别透传控制请求、接受和响应，未将它们等同于 FENCE 硬件允许条件。
`Drained` 包括 backend 尾部，`HasResidency` 汇总各组件仍使用 CTA 身份的工作；dirty line
本身不占 CTA。Local resolver 仍须在服务时校验 generation 后返回原 CTA owner。

协议错误会永久锁住该 System 的 Step：错误发现前其它组件可能已前进或应用 store，不能
重试同一边沿。真正的访存故障仍作为 data/receipt/flush error 返回。取消时应保留 System
并继续推进已接受尾部，而不是重建后丢失 dirty 数据；已接受尾部的取消处理见下文；完整 reset 仍待接通。

`TestSystemFetchMixedDataAndFlush` 使用 1/30-cycle backend，验证共享 Fetch、mixed store/load、
响应背压、原 owner LMEM 数据、flush 才更新 backing、旧边沿响应视图及辅助记录回收。
测试尚不是实际 Kernel 执行接线证明。

## MultiRunner opt-in integration

`MultiOptions.MemorySystem` 显式提供后端配置、token 到 residency 的 Bind 和服务时校验的
LocalOwner。该增量路径允许 FetchCycles/MemoryCycles 为零，不调用旧 Service，也不调用
MemoryDelay。独立默认 MultiRunner 和单 Warp Runner 路径仍未迁移，不能宣布 AC-022 完成。

每个周期先把旧 System 返回移入已有 Runner response 槽，以真实 bytes 调用 effects；
已入槽值在 core 背压时保持，不重复完成。上边沿 store 应用事件在本边沿 Observe 前交付，
避免在 effects 已关闭的边沿补写。随后以 ready=false Evaluate 得到稳定请求，System.Step
确定实际接受，再以这些接受值重新 Evaluate 同一旧 Core 状态并 Commit 一次。
这是软件求组合握手的两次求值，不额外推进周期。逐请求 Transaction 独立递增；payload
记录保留完整 token/uop 和 residency。local/global 分类读取 System 的 IR aperture。

BAR/WSYNC 仍使用原硬件 pending 和 LSU scheduler drained。FENCE 已通过 inline flush word 接线，
T12 模式 Flush 重置仍在任何修改前明确拒绝，防止原恢复实现丢失已接受存储或 dirty 数据。
Cancel 已保留已接受请求的传输尾部并屏蔽其架构结果；部分提交 SIMD 的取消也保留完整传输，见下文。
这些剩余限制不代表本任务最终允许的行为；错误恢复和完整 flush 控制仍须落实。

`TestMultiRunnerRealMemory` 执行四 Warp 普通、packed 及 store 后 load 程序，在后端延迟
1/30 下比较完整 Warp 状态与功能执行参考。global load 读到 dirty store 的值而 backing
仍旧，证明完成没有从 backing 重读。完整请求路径已经包含 I-cache 和 LMEM 组件，但
实际 LMEM 程序和同步已通过下述 Kernel 回归，取消/重启增量回归见下文。

## 尚需实现

1. 完成必要 socket/DCR 控制边界。
2. 迁移独立默认 MultiRunner/单 Warp 路径，消除旧 due 服务依赖；Kernel 已迁移。
3. 保留已接通 BAR/WSYNC/FENCE 条件，完成完整 flush/error reset。
4. 补齐完整错误恢复，继续保留已经应用的 bytes，并将剩余身份携带到传输尾部。
5. 迁移剩余依赖固定延迟的 Runner 测试，保留功能与控制断言，运行冻结全量验证。

前次组件验证：`verify-timing.sh`、`go test ./timing/memsys -count=1` 和 `go vet ./timing/memsys`
通过。当前 Runner 增量的 `verify-offline.sh` 已通过全仓 build/test/vet；Runner 包 231.525s。
`verify-timing.sh`、新程序及取消重启定向测试、`git diff --check` 也通过。
早一轮验证曾因测试使用不允许的新 epoch 重启失败；改为原接口要求的同 epoch 后，全仓重跑通过。

mixed 路径可能先应用子集，再释放整个 LSU 输入。运行时用实际 MemoryAccepted 记录许可，
早到 store 事件暂存，读响应保留在 System 返回缓冲，直到父请求接受后的边沿才交给 effects。
许可在 Complete 后、对应 store 事件消费后释放。这不回滚或重复已经应用的 bytes。


## cancellation increment

MultiRunner.Cancel 对已接受请求登记按完整 transport identity 的取消状态，保留 System，
不丢弃 dirty line、MSHR 或 LMEM 中已排队的请求。旧 load/fetch 响应继续握手排空，但不调用
已取消的 effects；store 应用事件也不重放。后台写回错误仍报告。重启请求使用新
Transaction，与旧返回分离。被取消的 held fetch 若随后接受，不得将其 ready 误传给新
Core fetch；它只完成自己的缓存握手。

旧 miss 未返回时允许同 epoch Restart，新 token 可前进。TestRealMemoryCancelLoadAndRestart
在实际 global load 接受后取消并立即重启，检查无取消范围内的 WB、最后状态与功能参考一致。
取消发生在 SIMD 父输入仍部分提交时，保留完整 held payload 并继续提交剩余子集；
接口没有 abort 信号，因此已展示的完整请求在此软件边界不可撤销。其后所有结果只排空，
旧请求最终接受不得传给重启后的 Core 请求。已应用 bytes 不回滚，也不重复应用。
完整 Flush/error reset 尚未实现，保留明确拒绝。独立 MultiRunner/单 Warp 默认路径仍待迁移。


## ordering completion interface

`AcceptOrderingResult(cycle, token, completionError)` 已为 Adapter/Concurrent 提供独立 FENCE
完成入口。成功结果复用原请求接受、覆盖及返回/WB 生命周期，仅退休已完成的功能
MemoryEvent，不再次调用 external ordering owner。它不产生 flush，也不允许调用方
以任意周期代替真正的排序返回。数据接口继续拒绝 FENCE。

定向回归保留旧 Service FENCE 测试，并新增 completed-ordering 模式：在禁用 backing Read
时完成指令，完整 owner 状态与功能参考一致，旧 ordering callback 调用次数为零。

本轮再次核对 VX_cache_init：flush mask 出现时阻塞请求；WAIT1 等待 bank-select 在途；
WAIT2 等全部 bank flush_end；DONE 才释放当前带 flush 属性的 core 请求，直到各端口接受。
因此 scan done、该原请求接受和最终响应是不同事件。当前公开 Cache.Flush 的可见性 ticket
不能直接冒充这条 RTL 原请求的响应。VX_lsu_slice 的 fence_lock 在 EOP mem_req_fire 设置，
在 fence response EOP fire 清除；FENCE 路由仍需携带真实 attr/地址及 mask 通过各级接口。
这一传输在下面的 inline flush 增量中接通；本里程碑仍未完成。

本轮已实现 flush-marked word 的原位传输：扩展 SIMD/合并/word 属性，cache 外层扫描后
释放同一请求及 tag，再以其真实返回完成 effects。不要把现有独立 Visibility 控制 ticket
直接映射为指令返回，也不要仅以 `System.Drained()` 作为 FENCE 准入条件。

排序完成增量验证：effects 全包测试通过（121.236s），新增错误用例后的定向测试通过
（31.339s），effects vet、verify-timing.sh 和 git diff --check 通过。错误结果不泄漏
owner 副作用，后续重试及 Finish 被拒绝。完整离线验证的最近通过记录仍为前一轮 Runner
接线版本；本轮仅修改 effects 接口及定向测试，尚未重新运行全仓离线脚本。


## inline flush integration

SIMDRequest.Flush 保留到 coalescer 两个 word 及 local adapter word。cache inline 控制检测
cached flush mask，等待 bank 扫描完成，再捕获并释放当时持有的 flush 端口。每端口独立接受，
仍有 flush mask 时普通 cached 请求继续锁住，全部释放后恢复普通准入。原 word 的地址、
tag、身份及数据响应保留，不产生独立 FlushReply 或 Visibility ticket。当前实现复用
已建模的 bank 准入和 bank empty/MSHR 状态，不额外添加经验等待周期。

VX_cache_wrap 的 NC bypass 位于 VX_cache/VX_cache_init 外，所以 NC 请求不触发 bank 扫描，
也不被 inline cached flush 锁住；专项测试覆盖这个结构边界。公开独立 Cache.Flush 的
软件可见性语义保持独立，不能与 inline 控制合并。

FENCE 地址已通过进一步静态核对确定：VX_decode INST_FENCE 不设置 used_rs，offset/pack
为零；VX_opc_unit src_valid 受 used_rs 门控，opd_buffer 在每次 pipe_fire_st2 清零，因此
无源读取的下一次输出保留零 operand。VX_lsu_slice 的默认 byte-enable 分支产生全 word。
Runner 构造地址零、全字节、原 mask 的只读 Flush SIMD 请求，而不是从 backing 服务 FENCE。
返回可拆成多 mask；AcceptOrderingFragment 复用原响应/WB 路径，最后覆盖才退休 ordering
MemoryEvent。不会每个 fragment 再次调用 ordering owner。

回归包含 I/D inline 原请求返回、dirty writeback、正常端口阻塞及 NC 并行、无 Visibility
请求，以及四 Warp store/FENCE/load 程序的完整架构状态和原身份返回。更完整的 socket
控制、独立默认 Runner 接线和错误 reset 仍待完成。


inline flush 增量验证：verify-timing.sh 通过；verify-offline.sh 全仓 build/test/vet 通过
（Runner 241.661s）；memsys 全包及 inline 定向测试、四 Warp FENCE 程序、git diff --check
均通过。本里程碑尚未完成，下一步迁移默认/Kernel 存储路径并闭合恢复和资源回收。

## Kernel default integration

NewKernel 始终建立真实 MemorySystem；Options.MemoryConfig 为 nil 时采用 IR 的外部后端默认，
非 nil 时复制其配置。旧 FetchCycles/MemoryCycles 不参与 Kernel 的请求或返回。
Bind 使用当前物理 CTA 槽和递增 generation；LocalOwner 在实际服务时检查槽、成员和 generation，
返回原 ResidencyMemory 路由，不复制 LMEM。WarpQuiescent 包含真实 Fetch/data/store receipt 尾部，
因此旧 owner 不会在尾部交付前重用。dirty cache line 不作为 CTA 存活条件。

Ready callback 在此路径限制新请求的展示；已经展示的请求继续保持，直到实际接受。
它不冻结存储时钟或撤销已部分接受的 SIMD。Services 追踪真实传输，Due 为零，表示没有预定完成周期；
记录从请求展示开始，身份检查不能只在整组 MemoryAccepted 后登记。

KernelStatus 分别提供 Complete（执行及 CTA 回收）、MemoryDrained 和 BackingVisible。
执行完成不隐含写回。MakeVisible(budget) 在 Complete 后显式发起独立 D-cache flush，
使用软件可见性 ticket 等待实际写回，保持跨预算的请求状态；其内存边沿继续推进 Runner 时钟，
不再执行 ISA/Core 边沿。它不是指令 FENCE，也不是自动执行尾部。
调用者读取最终 backing 输出前须完成此操作；提前调用返回错误，重复成功调用幂等。

Kernel 原启动、LMEM barrier exchange、外部 barrier generation、spawn 和 CTA 重入测试均迁移至
真实路径，保留功能、同步及身份断言。增加执行完成时 backing 仍旧、分预算可见性恢复，以及
巨大旧延迟不影响默认 Kernel 的测试。Kernel 全组回归通过；本轮 verify-offline.sh 全仓 build/test/vet 通过（Runner 278.164s），
verify-timing.sh 和 git diff --check 通过。

仍需迁移独立 MultiRunner/单 Warp 默认路径、完成完整 flush/error reset。

本轮部分提交取消回归：TestRealMemoryCancelPartiallyAppliedMixedStore 执行真实 mixed store，
在 local 子集已经应用、global 背压且整个 SIMD 未接受时取消并立即重启。
检查 owner 不因取消改变、held payload 身份保留、无旧架构 WB、无旧 Core 接受、
每个 local 传输身份恰好一次 WriteBatch，最终取消记录和请求记录全部回收。
该恢复前提先于独立默认入口迁移完成；完整 reset 仍未实现。

本轮验证：取消定向回归通过（17.158s），verify-timing.sh 通过；verify-offline.sh
全仓 build/test/vet 通过（Runner 287.762s），git diff --check 通过。
下一步继续默认 NewMulti 的真实存储配置及测试迁移，再统一单 Warp 入口；不能把现有
独立 fixed-delay 路径保留为完整执行路径。完整 flush/error reset 仍是未完成验收项。

## Default MultiRunner migration

NewMulti 已默认建立真实 System，旧 FetchCycles/MemoryCycles/MemoryDelay 不再决定执行。
MemorySystem 为 nil 时使用 IR 默认后端或 MemoryConfig，固定 Warp 身份及原 DataMemory
路由。路由须保持 runner 生命周期内绑定；动态 CTA 继续显式提供 MemorySystem callbacks。
缺少显式原子 local route 时返回错误，不借用 global backing。

新增 MultiRunner.MakeVisible，执行后显式写回并按预算恢复。默认路径的普通/packed、
dirty store 后 load、不同外部延迟、最终输出写回及四 CTA LMEM 路由定向回归通过。
timing-multi CLI 已改用 backend-cycles；实际运行 387 cycles 完成，retired=[4 4 4 3]。

尚需迁移按旧固定周期触发控制或取消的 MultiRunner 测试、完整 flush/error reset 和
单 Warp 入口。没有删除其功能/控制断言；本轮全包回归结果将记录于此。

本轮首次默认切换全包回归：go test ./timing/runner -count=1 失败（324.126s）。
该运行在 visibility.go 及 CTA 输出测试迁移之前启动；其中 CTA backing=0 已由最新
TestMultiCTAMemoryRoutesThroughTiming 的显式 MakeVisible 迁移修复并定向通过。
仍需处理的测试与证据：

- TestMultiRunnerCancelYoungerKeepsOldLoadAndOtherWarps：取消后 b-schedule 出现范围内 token ID5，需核对冷取指时取消与 scheduler feedback，不能仅延长测试预算。
- TestMultiRunnerRecoveryAndFlush：selective-restart 报 restart retains frontend work；epoch-flush 仍被明确拒绝。必须接通真实恢复，不能绕回旧服务。
- TestMultiRunnerWSYNCDrainsOwnWarp：返回周期与控制周期均改变，需保留实际自身 drain 断言，取消 callback latency 模拟。
- TestMultiRunnerArchitecturalCountersUseOldHardwareEvents、TestMultiRunnerExplicitSpawnOwners/bound-activates：原预算不足/未完成，按实际事件等待并保留计数及寄存器断言。
- TestMultiRunnerConcurrentFunctionalResults：15-cycle overlap 检查早于真实冷取指返回；需事件驱动开始观察并保留重叠与功能断言。
- TestBarrierExternalReleaseAndDrain：200-cycle 时尚未到达全部 barrier，需按 ticket/阻塞状态等待。
- TestBarrierReleaseDoesNotWaitForExternalStoreTail：直接 backing 检查仍旧，需分别检查 cache 应用尾部与显式输出可见性。
- TestMultiRunnerSpawnWaitAndOutstandingServiceTails/other-warps-active：旧外部服务尾部断言需迁移到真实资源事件。

最新定向 TestMultiRunnerRealMemory/TestMultiCTAMemoryRoutesThroughTiming 通过（22.454s），
取消及 Kernel 默认定向通过（39.200s），Runner/CLI vet、verify-timing.sh、git diff --check 通过。
本轮未宣称完整 verify-offline 通过；待上述失败修复后重跑冻结全量验证。

## Cold-fetch cancellation and healthy reset repair

确认旧问题：Cancel 设置普通 Stalled 后，较老且被保留的 decode unlock 会将其清除，
导致新 b-schedule 请求出现。新增独立 Parked 软件门控，只约束自主取指，保留 RTL
stall/control 的更新；Restart 明确释放门控，CTA dispatch 和完整 Scheduler flush 清理它。
WarpObservation 单独暴露 Parked，并将其计入 Runnable。该位不被声明为 RTL 寄存器。

取消/重启测试改为等待旧 load 的真实 MemoryAccepted，保留旧 load 返回及其它 Warp 前进断言。
冷取指尚未离开 frontend 时 Restart 的拒绝仍正确，不通过删除 guard 绕过。
模型旧 decode 解锁回归、取消/重启回归、model 全包均通过。

健康状态的 MultiRunner.Flush 已重建新 epoch Core/effects，同时 tombstone 旧传输身份，
保留 cache、dirty 数据和 partially-held SIMD；调用本身不推进时钟、不改变 canonical owner。
恢复测试及 mixed store 部分写入后的 epoch Flush 回归通过，local 身份只写一次且尾部回收。
失败边沿 Flush 继续拒绝，仍须闭合 Core/System 已推进边沿的对齐后才能恢复。

迁移计数器及 bound-spawn 的完成预算，保留寄存器和硬件事件断言；barrier release 的首边沿
改用实际 releaseCycle，最终 backing 检查前显式 MakeVisible。WSYNC 不再用旧延迟 callback
制造 Warp0 最慢；真实 backend/bank 顺序下 Warp3 最后返回，检查其它 Warp 的 WSYNC
在 Warp3 load 尚未完成时已经执行，并保持各自 release 后下一边沿执行的断言。
上述定向回归均通过；最新完整离线结果待记录。

本轮完整 verify-offline.sh 在 go test 阶段失败，仅剩两个 Runner 场景：
TestMultiRunnerConcurrentFunctionalResults 的固定 15-cycle 重叠断言，以及
TestMultiRunnerSpawnWaitAndOutstandingServiceTails/other-warps-active 的旧服务尾部顺序
（execute=189, commit=194, activation=321, lastOldStop=319, storeFinished=296）。
Runner 352.204s；其它包测试通过。脚本因此未执行最终全仓 vet；单独 model/runner vet 已通过。
verify-timing.sh 和 git diff --check 通过。此前取消、重启、计数、barrier、bound-spawn、
WSYNC 的全包失败均已消除。下一步用真实缓存/LMEM 资源差异重建剩余重叠和重排场景，
保留功能及身份断言；仍不得恢复旧 MemoryDelay 服务。

## Real overlap and spawn regression migration

两个剩余失败场景均已迁移并定向通过。multiSetupMemory 可为 packed Warp 提供原 CTA
LMEM owner；普通 load 仍走 global cache miss。功能参考与周期测试使用相同地址和路由，
保留完整架构/内存比较，显式 MakeVisible 后读取 backing。重叠起点等待实际 InFlight，
不再断言冷取指 15 cycles 内已进入执行。仍断言 packed 先返回、同 Warp 多指令重叠、
跨级同时事件、所有 Warp 在旧 load 等待期间执行、late WB，以及相同配置两次 trace 完全一致。

spawn 的 target-tails 子例保留 global 目标 load 尾部；other-warps-active 子例为带真实
load 依赖的目标 Warp 提供原 CTA LMEM 路由，使它们在父 Warp global store miss 尾部前
停止。保留 spawn 提交/激活/其它 Warp 停止/旧效果完成的所有顺序断言与最终寄存器断言。
这两个测试都不再提供 MemoryDelay callback，没有人为调度各 token 完成周期。

定向结果：ConcurrentFunctionalResults 通过（17.026s），SpawnWaitAndOutstandingServiceTails
通过（15.168s）。接下来运行冻结全量验证；失败边沿恢复与单 Warp 入口仍未完成。

本轮冻结验证已恢复全通过：verify-offline.sh 全仓 build/test/vet PASS（Runner 367.068s），
verify-timing.sh PASS，git diff --check PASS。两个旧服务场景的失败已经消除。
剩余验收工作是失败边沿的 Core/System 恢复对齐，以及单 Warp 默认入口/CLI 迁移与相关回归；
不能将本轮全包通过视为完整 T12 或里程碑 05 完成。

## Failed-edge alignment recovery

System.NextCycle 只读查询下一未提交边沿；协议 fault 返回原错误，不声称存在安全的共同边沿。
MultiRunner.Flush 在修改 effects/Core 前校验 System 与 Clock 至多相差一个边沿。
消费旧返回导致的错误发生在 Step 前，保持 Clock 当前周期；Step 成功后的 decode/effects
错误则只补记该已发生边沿，不再次 Step 或读写 owner。随后沿现有新 epoch 恢复，保留旧
transport、cache 和 dirty bytes。协议 fault 继续显式拒绝，不能清空 cache 冒充恢复。

TestMemoryFaultResetAlignsCommittedEdges 分别注入 refill read fault 与 WSYNC external
完成错误，验证两个边沿方向、reset 前后 canonical owner 不变、真实返回数据和恰好两次
refill read（失败一次及恢复成功一次）。load 后加入真实依赖以阻止 TMC 在故障 load 返回前
停止 Warp；恢复不承诺重放已经放弃的指令，既有退休副作用仍不回滚。

放弃预算中的显式 MakeVisible 时保留缓存控制状态，运行时排空其旧回复但不报告可见性成功。
该场景也有回归，避免控制回复背压导致恢复后的 Completed 永远不能达到。
当前剩余主工作为单 Warp 默认入口/CLI 迁移；本轮完整验证结果待记录。

本轮验证全部通过：verify-offline.sh 全仓 build/test/vet PASS（Runner 390.252s），
verify-timing.sh PASS，恢复定向回归及协议错误拒绝测试 PASS，git diff --check PASS。
普通运行错误的失败边沿恢复已接通；无法保证共同边沿的组件协议错误仍明确终止并保留状态。
下一步统一单 Warp Runner/CLI 到真实存储默认入口，并迁移其回归；里程碑 05 尚未完成。


## Single-Warp production entry (Worker 23)

Runner.New now wraps the scheduled MultiRunner with the original owner's Warp ID
active and explicit inactive owners in the other slots. Fetch, ordinary/packed LSU,
FENCE, cancellation via epoch Flush and failed-edge recovery share System. Removed
the old single-instruction runner and unreachable MultiRunner fixed-delay service
branches; FetchCycles/MemoryCycles remain ignored source-compatibility fields.
Options.DataMemory supplies the explicit local owner route. timing-run now exposes
backend-cycles, and Runner.MakeVisible is the same resumable writeback API.

Flush snapshots canonical PC/mask immediately: redirects must precede Flush. The
single-Warp store-tail test checks retained pending transport, discarded old results,
unchanged backing before visibility, and all accepted bytes after MakeVisible.
Program tests retain full functional snapshots, full backing comparison after
visibility, timing variation and deterministic traces; one replay deliberately sets
both deprecated delays to 99999 and must have an identical trace.

Trace residency now distinguishes token fragments by epoch/Warp/uop/mask. Packed
writeback checks disjoint nonempty lane masks and full coverage instead of assuming
one writeback for a four-lane return. Existing capacity, WAW, prior-final-WB and
33-cycle divider assertions remain. Targeted regressions and CLI pass; the final
full offline validation is running. This is not a whole-T12 completion statement.

Worker 23 final validation: verify-offline.sh PASS (all build/test/vet, Runner
428.868s), verify-timing.sh PASS, final Runner/CLI vet PASS, git diff --check PASS.
The added Warp-3 single-entry barrier test also passed independently: inactive
companions never fetch, the exact coordinator token releases BAR, stale/duplicate
wakes fail, and the original owner retires the complete program. Final-source
single-Warp determinism (including ignored deprecated delays) passed independently.

Milestone 05 is ready for independent validation. All production Runner, MultiRunner
and Kernel entries now use System; no fixed-delay service invocation remains in
these execution paths. Diagnostic timing-token remains separate. Milestone 06 and
whole-task T12 final delivery are outside this milestone completion claim.


## AC-023 repair: external D/I cache-control chain

Independent review found that the previous milestone completion omitted the
production I-cache flush entry. FlushCaches now exists on Runner/MultiRunner/Kernel,
with an explicit completed-execution admission boundary and resumable memory-only
budgets. D completion is latched before I request; combined completion waits I too.
Run/epoch reset/MakeVisible reject interleaving with a paused flush. Control IDs are
shared and monotonic across D-only visibility and joint flush, including epoch reset.
Ordinary failure recovery drains abandoned I as well as D control replies.

The integration regression stores a new instruction through D-cache, verifies
D-only MakeVisible plus epoch Flush still executes the cached old instruction,
then verifies the new instruction after combined flush. It checks old-edge D-done
before I acceptance, final completion after both phases, unchanged architecture,
rearming, budgets and two backend latencies. Kernel entry and error-recovery
regressions also cover compatibility. Final frozen validation is pending.

The injected D-writeback failure case also passes: no I request is sent, combined
completion stays false, and failed execution rejects a silent retry. The Kernel
budget/port-exclusion regression and existing failed-edge recovery pass. Final
Runner vet and diff check pass; the full offline run remains in progress.

Repair validation complete: verify-offline.sh PASS (full build/test/vet; Runner
466.253s), verify-timing.sh PASS (latest IR), final flush regression including
writeback failure and residency exclusion PASS (42.284s), Kernel/recovery targeted
regressions PASS. The full script's final vet includes the final residency guards.
AC-023's missing production I/D flush chain is now ready for independent review;
this repair does not claim completion of the later T12 milestone.
