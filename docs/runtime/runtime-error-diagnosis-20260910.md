# Runtime 扩展测试错误诊断（2026-09-10）

对象：`.cache/runtime-supported/20260910T123839Z-87khjjzf` 全部 56 项测试。
本文统一累计监控状态和错误诊断，不代表主体问题已经修复，也不是整轮验收。
主体 `timing/`、`emu/`、`isa/`、`Vortex_rtl/` 与 T12 HEAD 无差异。
诊断字段和组件复现仅加入 `/tmp/timing-diagnosis.3od1OJ` 临时副本。

最终监控结果（2026-09-11 10:26 核对）：56/56 均已报告，42 PASS（functional 28、
timing 14）、5 timing 内部错误、9 timing TIMEOUT，正式作业队列已空。
最后新增 softmax 内部错误及 sort/stencil3d 超时的诊断也已结束，全部 14 项正式失败
均有分析与证据边界。监控分析完成，不代表整轮测试通过；总表及处理优先级见 §6。
临时源码副本现已不存在；后续诊断复用此前保存在工作区诊断目录的 progress-lib，
不修改正式快照。全部结论及后续诊断结果仍统一在本文。

## 1. async_barrier：PC 可见顺序与异步外部事件被混用

原始参数 `-n32 -t4`，job 12702531，exit 255，1668 cycles；错误为
`stale nonsequential control delivery`。functional 对照通过。
缩小至 `-n4 -t4` 的单 CTA 仍在 1644 cycles 复现，因此不是大规模 CTA 回收问题。

诊断快照：

| 字段 | 值 |
| --- | --- |
| 到达的旧事件 | order 262，Warp 1，Epoch 1，PC 0x80000124 |
| 事件内容 | BAR.arrive，barrier ID 1，4 participants，DrainLSU=true |
| WarpStall | false |
| 已可见控制 frontier | order 286，PC 0x80000140，普通 `addi s9,s9,-1` |
| 当前软件 PC | 0x80000144 |
| BAR 的 PC 效果 | PCSequential，0x80000124 → 0x80000128 |

`emu/state/delivery.go` 在普通指令 ControlEvent 交付时推进 controlOrder；
`emu/state/effect_stream.go:stageStreamControl` 对较老普通 PC 回执允许丢弃其 PC 效果，
但只要回执含 Barriers 就拒绝。结果是：年轻普通指令先完成后，合法的旧 barrier
外部事件无法交付。错误名称中的 nonsequential 并不说明本次 PC 是跳转。

冻结 RTL `VX_decode.sv` 的 asynchronous arrive/wait 分支明确
`is_wstall = is_rd_zero`（arrive 不阻塞 Warp），`VX_wctl_unit.sv` 又要求 BAR 等待
`lsu_sched_drained`。模型 `effects/control.go` 实现了对应门控。因此非阻塞 arrive
与较年轻指令重叠有硬件依据，不能用强制 Warp stall 或全 Core drain 掩盖。

修复方向：分别管理 PC 前沿与外部 barrier 事件的一次性交付，保留 token/epoch/
residency、重复事件、真正过期跳转和控制事件的保护。较老合法 arrive 不回退 PC，
但不能仅因 PC frontier 已推进就取消其外部语义。

已有 `TestConcurrentNonblockingBarrierAndBranchFeedback` 覆盖的是同一 edge
同时交付 BAR/branch，按指令序排序即可通过；本次是跨 edge，单边排序无法解决。
应新增旧 BAR 延迟、年轻 ALU/branch 先完成、重复回执及 activation 变更的组合测试。

### 本轮修复（asynchronous-barrier-delivery）

`EffectStream` 已分离 PC frontier 与 activation admission 截止线：迟到的当前
activation 非阻塞 arrive 保留 barrier/drain 外部事件，只移除旧顺序 PC 写入。
外部事务成功才记录 receipt；重复、旧 activation/epoch/取消 residency、阻塞
BAR 和过期 branch 继续拒绝。未改 decode stall、LSU gate、RTL 或外部服务契约。
跨 edge state/effects 与实际 runner load→arrive→ALU→branch 回归见
`emu/state/async_barrier_test.go`、`timing/effects/concurrent_test.go`、
`timing/runner/async_barrier_test.go`。这些是离线机制回归，未重跑原规模
async_barrier benchmark 或 RTLSIM，不将本文历史失败记录改写为已验收通过。

本 Worker 在固定离线环境验证通过：`go test ./emu/state -count=1`、
`go test ./timing/effects -count=1`、`go test ./timing/memsys -count=1`；runner
选定回归 `TestMultiRunnerDelayedAsyncBarrier`、`TestMultiRunnerExplicitSpawnOwners`、
`TestMultiRunnerSpawnWaitAndOutstandingServiceTails`、`TestMultiRunnerRealMemory`、
`TestRealMemoryCancelLoadAndRestart`、`TestRealMemoryFenceReturnsOriginalRequest`、
`TestCTADispatchWhileOtherWarpsRun`；`go vet ./emu/state ./timing/effects ./timing/runner`
与 `git diff --check`。完整离线门禁及 runtime 竞态验收由 closure Worker 执行。

## 2. conv3：已确认响应稳定性检查的身份粒度错误

原始参数 `-n32 -l`，job 12702539，exit 255，97.496 秒；错误为
`lost stalled path lanes`。functional 对照通过；诊断 timing `-n8 -l` 通过。

代码证据：

1. GlobalAdapter 的零输出缓冲 priority pack 允许未握手时重新选择不同 batch。
2. Coalescer 按 BatchID（Slot、Generation）验证稳定性，允许切换不同 BatchID。
3. Coalescer.Preview 将片段还原为父 SIMD Identity/Tag 和 lane mask，不携带 BatchID。
4. SIMDSplit.validate 却把“父 Identity 相同”当成“同一个返回片段”，要求旧 mask
   为新 mask 子集；不同 batch 属于同一个父请求时会误触发 `lost stalled path lanes`。

冻结 `VX_mem_unit.sv` 的 D-cache adapter 明确 ARBITER=P、RSP_OUT_BUF=0；
`VX_stream_pack.sv` 按当时有效端口选 tag，`VX_generic_arbiter.sv` 默认 STICKY=0。
稳定性应该施加于底层 producer/相同片段，而不是任意重新仲裁后的同一父请求。

临时组件测试 `TestDiagnosticSameParentFragmentReselection` 已复现：先用 lane 0
填满 split 返回缓冲，再呈现 lane 3（mask 8）并背压，随后同一父请求改选 lane 1
（mask 2），在 cycle 4、global path 触发同一错误。

随后完整 `-n32 -l` 诊断复现也在 cycle 20513 退出 255，确认这就是 benchmark
实际触发路径，而非仅理论上的检查器问题：

| 字段 | 背压时旧选择 | 新选择 |
| --- | --- | --- |
| BatchID | Slot 0, Generation 529 | Slot 1, Generation 472 |
| Batch channel mask | 2（port 1） | 1（port 0） |
| 下层 transaction/tag | 1693 | 1694 |
| 父 SIMD transaction/tag | 2654 | 2654 |
| 父身份 | Kernel 1, CTA 3, Warp 3, WarpGeneration 16, Token 6800, Epoch 1 | 相同 |
| 展开 lane mask | 4（lane 2） | 2（lane 1） |

出错时两个 globalWords 端口均 valid、未交付，旧 port 1 的数据仍为
`[150 49 64 63 221 151 196 62]`，与旧 batch 相同；不存在该端口丢掉旧响应的证据。
新有效 port 0 按优先级获选，不同 batch 的原始标识在 coalescer 展开后丢失，
split 因 `4 & ~2 != 0` 错报 lane 丢失。错误路径为 global，不是 LMEM 数据损坏。

首次逐周期格式化大量诊断字符串的版本在 180 秒诊断限额超时，不用于模型结论。
改为仅出错时格式化后取得上述完整快照；两版均未改变调度/协议逻辑。

修复方向：保留片段 provenance，或在正确的 producer/片段层验证稳定性，同时保留
lane 未发送、重复完成、数据变动、旧 generation 和 CTA residency 检查。
不应删除全部检查、随意新增缓冲或改为全局串行返回。
回归应覆盖同父不同 batch 背压重选，以及真数据不稳定仍被拒绝。

2026-09-16 存储响应修复：`Coalescer.Preview/Step` 现通过 `SIMDResponse.Batch`
保留批次 slot/generation，split 以父 identity + Batch 判断是否同一片段。
原 adapter 逐 producer 稳定性检查、coalescer generation/发送检查、split 逐 lane
完成账本均保留；不增加缓冲或串行化返回，也不修改外部 100-cycle 服务契约。
离线入口为 `source env/env.sh` 后 `go test ./timing/memsys`；新增
`TestSameParentFragmentReselection` 通过真实组件握手重现本节共享机制，并验证被
优先级隐藏的端口仍受稳定性保护、最终所有 lane 恰好交付一次且释放全部引用。
反向输入另见 `TestSplitFragmentStabilityAndLedger`、
`TestCoalescerRejectsUnsentFragment` 和既有 stale-generation 回归。
这些是 conv3/dogfood/fence/softmax/stencil3d 的共享机制回归，不是原规模
benchmark 或 RTLSIM 重跑结果；本次存储 Worker 不关闭 barrier 或审计问题。

## 3. 日志收尾存在竞态，0 cycles 不是执行证据

原始 conv3 result.json 有 launch=1、finish=0、execution_cycles=0。
`integration/vortexruntime/device.go:run` 先清 busy、解锁，再写 launch-finish。
host 可以在此窗口观察失败、退出进程，使异步审计来不及落盘。
这是源码中确定存在的竞态；与本次缺失 finish 一致，但原日志无法证明具体调度顺序。
`scripts/vortex-supported.py` 对 finish 列表求和，空列表得到 0，因此实际周期未知。

应先确保终态审计完成，再暴露执行结束；汇总对缺失终态的周期使用 unknown/null。
审计函数还会静默忽略打开/写入失败，需要在修复日志可靠性时一起处理。
此问题影响诊断完整性，不是上述两种模拟器内部异常的起因。

## 4. 外部 memory 边界与结论范围

runtime 的 `runTiming` 只把 backend config.Latency 设为 100；backend 在真正
接受请求时记录 AcceptedCycle，并设置 due = cycle + Latency。它不是指令总访存
固定延迟，也不是 Cache 命中延迟；backpressure 还可能推迟响应交付。
未发现本次异常由 100 设置位置错误引起。延迟和真实 cache 行为会暴露更多重叠，
但调小延迟使测试通过不能作为修复。

当前证据定位的是周期效果交付与存储响应协议边界，不足以宣称整个 cache 设计有误，
也不能由 functional 通过推断 timing 已正确。完整 56 项作业仍应独立继续记录。

## 5. 持续监控汇总

2026-09-10 21:15（北京时间）快照：已报告 13/56 项，9 PASS（timing 2、functional 7），
3 SIMULATOR_INTERNAL，1 TIMEOUT；43 项未报告。未报告不算通过。
活动链尾：index 10 / dotproduct / job 12702874 和 index 16 / fence / job 12702878，
查询均为 PENDING / MaxJobsPerAccount，未取消或重排其它用户作业。
周期模式通过项为 demo（7436 execution cycles）和 dotproduct2（79051 cycles）。
后续状态、同类错误增补和新根因均更新本文件，不创建新的报告文档。

21:25 更新：已报告 14/56 项，9 PASS、4 SIMULATOR_INTERNAL、1 TIMEOUT。
fence timing 新增失败，链已提交 io_addr functional / job 12702906；dotproduct timing
仍在运行。此前队列快照仅代表当时状态。

21:30 更新：15/56 已报告，10 PASS、4 SIMULATOR_INTERNAL、1 TIMEOUT。
dotproduct timing / job 12702874 通过（104514 execution cycles、410 flush cycles、
670.749 秒），后继 dotproduct2 functional / job 12702936 已提交。
diverge 独立诊断开始执行，小规模 `-n1 -d8` host 验证 PASSED，原规模正在采集进度。

21:33 更新：17/56 已报告，12 PASS（timing 3、functional 9）、4 内部错误、1 超时。
新通过 io_addr functional、dotproduct2 functional。诊断 job 12702898 已完成，详见下节。

21:43 更新：18/56 已报告，13 PASS（timing 4、functional 9）、4 内部错误、1 超时。
dropout timing / job 12702950 通过（47010 execution cycles、716 flush cycles、
192.684 秒），后继 fence functional / job 12702979 已提交。jacobi timing 仍在运行。

21:54 更新：19/56 已报告，14 PASS（timing 4、functional 10）、4 内部错误、1 超时。
fence functional / job 12702979 通过（0.5 秒），后继 io_addr timing / job 12703016
已提交；jacobi timing 尚未产生终态。

21:58 更新：21/56 已报告，15 PASS（timing 5、functional 10）、4 内部错误、2 超时。
io_addr timing / job 12703016 通过（27259 execution cycles、717 flush cycles、
161.317 秒）；jacobi timing 1500.130 秒超时，正在独立诊断，不自动归类为死锁。

22:04 更新：23/56 已报告，17 PASS（timing 5、functional 12）、4 内部错误、2 超时。
jacobi functional 通过（job 12703034，5.584 秒），madmax functional 通过
（job 12703032，37.706 秒）。后继 mstress timing / 12703045、madmax timing /
12703043 已提交；jacobi 诊断正在执行。

22:15 更新：24/56 已报告，18 PASS（timing 6、functional 12）、4 内部错误、2 超时。
mstress timing / job 12703045 通过（31702 execution cycles、740 flush cycles、
318.531 秒），后继 multikernel functional / job 12703089 已提交。madmax timing 仍在运行。

22:22 更新：25/56 已报告，19 PASS（timing 6、functional 13）、4 内部错误、2 超时。
multikernel functional / job 12703089 通过（3 launch / 3 finish，2.284 秒），
后继 occupancy timing / job 12703105 已提交；madmax timing 仍在运行。

22:33 更新：26/56 已报告，19 PASS、4 内部错误、3 超时。madmax timing / job
12703043 在 1500.351 秒超时，后继 mstress functional / job 12703139 已提交。
occupancy timing 继续运行。madmax 独立进度诊断 job 12703162 已提交，详见第 5.5 节。

22:37 更新：27/56 已报告，20 PASS（timing 6、functional 14）、4 内部错误、3 超时。
mstress functional / job 12703139 通过（0.651 秒），后继 multikernel timing /
job 12703175 已提交。occupancy timing 尚未结束，madmax 诊断待运行。

22:54 更新：28/56 已报告，20 PASS、4 内部错误、4 超时。occupancy timing /
job 12703105 在 1500.103 秒超时，后继 packld functional / job 12703384 已提交。
独立 occupancy 诊断 job 12703389 已提交，详见第 5.6 节；multikernel timing 继续运行。

23:05 更新：29/56 已报告，21 PASS（timing 6、functional 15）、4 内部错误、4 超时。
packld functional / job 12703384 通过（0.322 秒），后继 pathfinder timing / job
12703478 已提交。occupancy 诊断已运行；multikernel timing 前两次 launch 已完成并
写回，第三次仍在执行，不能据此把整项记为通过。

23:10 更新：30/56 已报告，21 PASS、4 内部错误、5 超时。multikernel timing /
job 12703175 在 1500.314 秒达到整项超时，3 launch / 2 finish；后继 occupancy
functional / job 12703492 已提交。multikernel 小规模诊断 job 12703497 已提交。

23:17 更新：31/56 已报告，22 PASS（timing 6、functional 16）、4 内部错误、5 超时。
occupancy functional / job 12703492 通过（6.605 秒），后继 packld timing / job
12703513 已提交。pathfinder timing 正在运行；multikernel 诊断待运行。

23:22 更新：32/56 已报告，23 PASS（timing 7、functional 16）、4 内部错误、5 超时。
pathfinder timing / job 12703478 通过（31 launch / 31 finish，56017 execution cycles、
12679 flush cycles、492.606 秒），后继 raycast functional / job 12703550 已提交。
31 次 launch 成功是 runtime 多次执行/写回链可工作的证据，但不替代 multikernel
第三入口的 TLS 与特定工作量验证。

23:28 更新：34/56 已报告，25 PASS（timing 8、functional 17）、4 内部错误、5 超时。
packld timing / job 12703513 通过（3893 execution cycles、496 flush cycles、23.254 秒）；
raycast functional / job 12703550 通过（39.251 秒）。未新增错误类型。

23:42 更新：35/56 已报告，26 PASS（timing 8、functional 18）、4 内部错误、5 超时。
pathfinder functional 通过，relu timing 正在执行；multikernel 原规模诊断已进入
第三入口，10000→20000→30000 cycles 间四个 Warp 退休数持续增加，尚未完成。

23:44 更新：36/56 已报告，27 PASS（timing 9、functional 18）、4 内部错误、5 超时。
relu timing / job 12703605 通过（31918 execution cycles、716 flush cycles、198.952 秒），
后继 sgemm functional / job 12703747 已提交；multikernel 第三入口诊断已推进到
60000 cycles，退休数持续增加。

23:55 更新：37/56 已报告，28 PASS（timing 9、functional 19）、4 内部错误、5 超时。
sgemm functional / job 12703747 通过（1.576 秒），后继 sgemm2 timing / job
12703774 已提交。raycast timing 仍在运行。multikernel 原规模诊断完整通过见第 5.7 节，
不计入本轮正式 PASS 数量。

2026-09-11 00:12 更新：38/56 已报告，28 PASS、4 内部错误、6 超时。raycast timing /
job 12703658 在 1500.330 秒超时，后继 relu functional / job 12703873 已提交。
raycast 独立诊断 job 12703901 已提交；sgemm2 timing 继续运行。

2026-09-11 00:27 更新：39/56 已报告，29 PASS（timing 9、functional 20）、4 内部错误、
6 超时。relu functional / job 12703873 通过（0.699 秒），后继 sgemm timing /
job 12704021 已提交。raycast 诊断已开始；sgemm2 timing 接近时限。

2026-09-11 00:28 更新：40/56 已报告，29 PASS、4 内部错误、7 超时。sgemm2 timing /
job 12703774 在 1500.391 秒超时，后继 sgemmx functional / job 12704024 已提交。
sgemm2 独立诊断 job 12704054 已提交，raycast 诊断继续执行。

2026-09-11 00:38 更新：41/56 已报告，30 PASS（timing 9、functional 21）、4 内部错误、
7 超时。sgemmx functional / job 12704024 通过（1.019 秒），后继 sgemv timing /
job 12704087 已提交。sgemm timing 继续运行，sgemm2 独立诊断已启动。

### 5.1 dogfood：确认与 conv3 同根因，非乘法计算错误

原始 `-n64 -s0 -e21 -c` 在 Test0 iadd 通过后，Test1 imul 失败。
第一 launch 7750 cycles、flush 664；第二 launch 在 3252 cycles 报
`lost stalled path lanes`。剩余子测试没有执行，不能把 dogfood 当成全 22 项已测试。
functional 对照通过。

临时诊断只运行 `-n64 -s1 -e1 -c`，无需先运行 iadd，即在相同 cycle 3252 复现。
这排除了“必须先执行上一个 kernel 才触发”的解释。
父请求 transaction/tag 256、Token 803、CTA 0、Warp 0、WarpGeneration 1：

| 字段 | 旧选择 | 新选择 |
| --- | --- | --- |
| BatchID | Slot 2, Generation 78 | Slot 3, Generation 61 |
| 下层 transaction/tag | 487 | 488 |
| 父 lane mask | 4 | 2 |
| 旧端口当前是否仍 valid | 是，数据仍为 [146 2 0 0 147 2 0 0] | 新 port 0 优先获选 |

与第 2 节同样是零缓冲 pack 重选不同片段、split 用父身份误判稳定性。
错误发生在存储响应协议检查，不能根据子测试名 imul 就归因于乘法实现。
证据保存于 `.cache/runtime-diagnosis/20260910/dogfood-imul.stderr.log`。
修复/回归合并到第 2 节问题，不另列为一种根因。

### 5.2 diverge：原始超时，暂未证明死锁

原始 `-n64 -d8`，job 12702553，1500.228 秒后 timeout 返回 124。
只有 launch-start，无完成事件；原始日志缺少中间进度，因此实际停止在哪个周期未知。
functional 对照通过但耗时 104.816 秒，本身明显比多数 functional benchmark 重。

源码不是只测试 8 层分支：host 将 n 乘以硬件 total_threads，本轮 1024 points、
64 CTA、每 CTA 16 lanes。kernel 每个 lane 先执行 num_points=1024 次 hacker/分支循环，
又执行 task_id 次前缀求和，总工作量包含二次增长项。不能把 n64 当成只有 64 次运算。
初步方向是区分吞吐不足与无进展循环，仍保留 TIMEOUT 分类，不擅自改成 PASS 或死锁。
后续诊断将使用独立临时构建输出 cycle/retired/warp 状态，不修改本轮冻结二进制。
已提交独立 debug 诊断 job 12702898（2 CPU、4 GiB、10 分钟上限），依次运行
`-n1 -d8` 与 `-n64 -d8`，各自最多 180 秒，每 10000 cycles 记录进度。
其 timeout 只作为诊断窗口结束，不替代正式套件的 1500 秒结论。

诊断结果：小规模 exit 0，8045 execution cycles、409 flush cycles，host 输出 PASSED，
终态审计 BackingVisible=true。原规模 180 秒窗口结束 exit 124，进度如下：

| cycle | 每 Warp retired | 在途指令数 |
| --- | --- | --- |
| 10000 | 417 / 414 / 414 / 407 | 17 |
| 20000 | 840 / 832 / 832 / 830 | 14 |
| 30000 | 1261 / 1250 / 1250 / 1250 | 16 |
| 50000 | 2097 / 2094 / 2095 / 2086 | 14 |
| 60000 | 2515 / 2515 / 2515 / 2504 | 16 |

各 Warp 均持续退休，采样 PC 在 kernel 的循环/反馈附近变化；反汇编确认
0x80000068 起包含循环计数递减、hacker 调用、栈 load 和回跳，工作量未被编译器消去。
采样时出现 control-feedback / fetch-or-control-feedback 不等于死锁，因为相邻
采样间所有 Warp 的 retired 都在增加。该节点诊断窗口约每 29 秒推进 10000 cycles。
这支持“工作量大、周期仿真吞吐有限”的方向，但不证明后续不会死锁或出错；
正式 1500 秒运行缺少 heartbeat，无法追溯其最终停止位置。

建议后续把持续进度与硬 wall timeout 分开记录，并区分正确性小规模与性能大规模测试；
若要宣称该原规模支持，仍需保持参数不变并给足资源完成验证，不能仅缩规模后改记 PASS。
当前未修改正式参数、超时、核心时序或 cache 结构。
完整证据位于 `.cache/runtime-diagnosis/20260910/diverge-small/`、`diverge-original/`。

### 5.3 fence：再次确认与 conv3 同根因，不能归因于 FENCE 指令

原始 `-n64`，job 12702878，26.647 秒、2709 cycles 后退出 255。
诊断同参数在相同 cycle 2709 复现 global path 的片段身份误判：
父 transaction/tag 247、Token 772、CTA 0、Warp 2、WarpGeneration 1；
旧 BatchID (Slot 2, Generation 78) / 下层 transaction 477 / mask 4，
切换为新 BatchID (Slot 3, Generation 61) / transaction 478 / mask 2。
旧 port 1 仍然 valid，数据 `[145 2 0 0 146 2 0 0]` 保持不变。
证据：`.cache/runtime-diagnosis/20260910/fence/stderr.log`。

kernel 是先循环执行两路 load、加法、store，最后调用 vx_fence；本次直接失败点是
load 响应的 split 检查，不是 FENCE 控制回执错误。不能以 benchmark 名字判定
FENCE 语义有误，也不能在此失败之后声称已经验证 FENCE 正确。
修复和回归继续归入第 2 节，无需新增另一种模型根因。
后续 functional 对照也已通过（job 12702979），但不能代替 timing FENCE 的验收。

### 5.4 jacobi：超时，诊断窗口内持续推进，存在 double 软件辅助调用开销

正式 `-n64` / job 12702940，1500.130 秒超时 exit 124，launch 1、finish 0。
实际 grid 4 CTA × block 16 lanes；host iteration=1，因此不是多次 host kernel
迭代累积超时。原日志只有第一次 launch-start，不能知道最后周期或退休进度。

源码 TYPE=float，但 kernel 使用 double sum。反汇编在循环中明确包含
`__extendsfdf2`、`__adddf3`，结尾包含 `__divdf3`、`__truncdfsf2`。
不能仅按 64×64 次原生浮点加法估计执行量；double 辅助函数展开为更多指令和分支。
这只是性能方向的源码证据，尚不足以排除周期控制/收敛问题。

独立诊断 job 12703039 已提交到 debug：2 CPU、4 GiB、10 分钟上限，依次运行
`-n8` 和 `-n64`，各最多 180 秒，复用只添加诊断字段的临时共享库；每 10000 cycles
采集退休数和 Warp 状态。待结果到达后更新本节，不修改原始套件超时与参数。
原规模 functional 对照已通过（5.584 秒），因此至少同一输入的功能参考路径可完成；
这不排除 timing 路径的性能或控制缺陷。

22:08 诊断已结束：小规模 exit 0、host PASSED，28774 execution cycles、409 flush
cycles、BackingVisible=true。原规模在 180 秒诊断窗口结束时 exit 124，未发生内部异常，
采样如下：

| cycle | 每 Warp retired | 在途指令数 |
| --- | --- | --- |
| 10000 | 542 / 513 / 577 / 555 | 4 |
| 20000 | 1321 / 1297 / 1351 / 1332 | 10 |
| 30000 | 2102 / 2087 / 2134 / 2111 | 13 |

四个 Warp 均持续退休，PC 在 kernel 和软件 double 辅助函数范围变化，10k cycles
区间约需 50 秒。诊断没有观察到静止死锁，支持工作量/执行吞吐方向；但只覆盖开头
180 秒，不能排除原始 1500 秒运行后段的控制缺陷，也不能宣称原规模输出正确。
源代码的二重规模增长、double 软件展开和约 200 simulated cycles/s 的观察值共同解释
了为什么“64×64 小矩阵”不一定在 25 分钟内完成；该速率只是节点上墙钟观测，
没有 CPU profiling，不能进一步断言某一个 Go 函数就是热点。
建议保持 TIMEOUT 未验收状态，后续在完整原规模运行中加入持久 heartbeat/进度判断，
再决定是否需要性能优化或修控制。未将 double 改 float，也未改 Cache/调度结构。
证据统一保存在 `.cache/runtime-diagnosis/20260910/jacobi-small/`、`jacobi-original/`。

### 5.5 madmax：超时，256 CTA 密集 FMA，诊断窗口内持续推进

原始 `-n32` / job 12703043，1500.351 秒后 timeout exit 124；只有 launch-start，
无中间 heartbeat 或 finish，实际最后周期未知。functional 同参数已通过，耗时 37.706 秒。
launch grid=[8,32,1]、block=[4,1,1]，共有 256 CTA、1024 个输出元素。

`madmax_compute` 每元素执行 256 轮 × 16 条乘加更新，即源级共 4194304 次更新；
注释写 1024 iterations，但实际循环上限为 256，应以代码为准。
反汇编 0x800000b4–0x800000f8 确认循环中有 16 条 fmadd.s 和回跳，不是被优化掉的空循环。
部分更新跨迭代存在数据依赖，不能仅凭“independent accumulators”注释假定完全无依赖。

这支持高工作量方向，但缺少原始退休进度，暂不能排除死锁。独立 debug 诊断 job
12703162（2 CPU、4 GiB、10 分钟上限）依次运行 `-n1`、`-n32`，各 180 秒，
只加周期/退休/warp 采样，不修改 FMA、Cache、调度或正式套件参数。待结果更新本节。

22:43 诊断已结束：`-n1` exit 0、host PASSED，71348 execution cycles、409 flush
cycles、4679 retired，BackingVisible=true；只一个有效 lane 也需较长计算链。
`-n32` 在 180 秒窗口结束时 exit 124，无内部异常，进度如下：

| cycle | 每 Warp retired |
| --- | --- |
| 10000 | 504 / 504 / 504 / 504 |
| 20000 | 1017 / 1017 / 1017 / 1016 |
| 30000 | 1528 / 1527 / 1527 / 1527 |
| 40000 | 2040 / 2040 / 2039 / 2039 |
| 50000 | 2551 / 2551 / 2550 / 2550 |

所有 Warp 均持续退休，PC 在反汇编的乘加循环范围变化，10k cycles 约需 35 秒。
采样 PendingLSU=0，说明该观察段主要不是等待未完成 LSU 请求；但不能据此排除
指令 cache 或前端资源影响，也没有 RTL 对照来证明每个 stall 都周期精确。
结合 256 CTA 和每元素 4096 次更新，现有证据支持重计算/吞吐不足的解释，
没有观察到静止死锁；不能排除后段缺陷，也不能用小规模通过代替原规模验收。
正式结果仍为 TIMEOUT，后续需带完整进度记录完成原规模才可判定支持。
证据：`.cache/runtime-diagnosis/20260910/madmax-small/`、`madmax-original/`。

### 5.6 occupancy：超时，LMEM 配额限制与单 lane 长循环，观察期内持续推进

原始 `-c8` / job 12703105，1500.103 秒后 exit 124。只有 launch-start，没有 finish
或中间进度，不知道最终停在哪个周期。实际 8 CTA，每 CTA 1 warp、8192 bytes LMEM，
最多同时驻留 2 CTA；不是 8 CTA 同时执行。

host 使用 local_mem_size / max_concurrent 为每 CTA 分配一半 LMEM。本轮每 CTA
2048 words，kernel 只让 threadIdx.x=0 执行，其余 lane 返回；该 lane 先循环 store
2048 次，再循环 load/累加 2048 次。总计 8×4096 次源级 LMEM 访问及循环指令。
源代码不含等待所有 8 CTA 同时到达的全局 barrier，因此不能直接推断为超额驻留
产生的全局屏障死锁；但 LMEM/控制/资源回收缺陷仍需进度证据排除。

独立 debug 诊断 job 12703389（2 CPU、4 GiB、5 分钟上限）按原参数 `-c8` 运行
180 秒，采集周期和退休状态，不减少 LMEM 配额、不改变 CTA 并发容量或正式超时。
待结果更新本节；functional 对照仍待本轮执行。

23:07 诊断窗口结束 exit 124，无内部异常，两个驻留 Warp 保持 Mask=1，另两个
Warp 始终 inactive。进度证据如下：

| cycle | 每 Warp retired |
| --- | --- |
| 10000 | 862 / 862 / 0 / 0 |
| 30000 | 2635 / 2635 / 0 / 0 |
| 40000 | 3521 / 3521 / 0 / 0 |
| 50000 | 4407 / 4407 / 0 / 0 |
| 60000 | 5293 / 5293 / 0 / 0 |

PC 在 LMEM 写循环附近变化，PendingLSU 在 0/1 间变化，退休数持续增长。
10k cycles 约 29 秒，观察窗口内未出现静止死锁；该吞吐与大量单 lane 循环共同
支持执行开销方向。两个 inactive Warp 不能误判为调度器漏派发：8192 bytes/CTA
的配额本就只允许 2 CTA 驻留。
但窗口未覆盖完整 8 CTA 的退出、LMEM 释放和复用，不能排除后段回收问题或声称
结果正确；正式 TIMEOUT 状态保留。后续原规模完整运行需记录 CTA 完成/回收进度，
不要通过缩小 LMEM 配额或强行提高驻留数让 benchmark 提前通过。
证据统一位于 `.cache/runtime-diagnosis/20260910/occupancy-original/`。
后续原参数 functional 对照已通过（job 12703492，6.605 秒），不能替代 timing
的 CTA 回收与完整输出验证。

### 5.7 multikernel：整项共享时限，第三入口 acc_k 未完成

原始 `-n1024` / job 12703175，1500.314 秒超时 exit 124，3 launch / 2 finish。
这不是线程启动失败或“第二次 launch 就不能工作”：

| 入口 | 结果 | execution cycles | 写回 | 墙钟 |
| --- | --- | --- | --- | --- |
| add_k | 64 CTA 完成 | 36821 | 716 cycles，BackingVisible=true | 约 243 秒 |
| mul_k | 64 CTA 完成 | 36972 | 716 cycles，BackingVisible=true | 约 246 秒 |
| acc_k | 已启动，未见终态 | 未知 | 未见最终写回 | 剩余预算约 1011 秒 |

result.json 的 execution_cycles=73793 是前两个已完成 launch 的和，不包含第三个，
不能当作整项总周期。timeout 是 host 整项共用 1500 秒，不是每个 kernel 各 1500 秒。

第三入口每元素执行 8 次 noinline tls_add，通过真实 tp-relative load/store 更新
64-bit thread-local t_acc，再读取 t_tag；相较前两个简单 add/mul，增加了调用、
TLS 和访存工作量。还涉及 .init_array、.tdata/.tbss 初始化和 per-hart TLS 间距，
所以在缺少第三入口进度时，不能排除 TLS、访存或资源复用缺陷，更不能只按耗时
断言没有死锁。functional 原规模三入口已通过（job 12703089），但不替代 timing。

小规模诊断 job 12703497（debug、2 CPU、4 GiB、6 分钟上限）执行 `-n16`，
保留三个入口及所有 host 校验，最多 240 秒，只加诊断采样。
不跳过前两次初始化、不删除 TLS 测试、不修改正式总时限。待结果更新本节。

23:25 小规模诊断已完成，host PASSED、exit 0，三次分别为 4187 / 4338 / 6055
execution cycles，均完成 409 cycles flush，BackingVisible=true。三个入口、TLS
初始化和输出校验在小规模可工作，但只涉及 1 CTA，不能验证原规模多 CTA 复用。
原规模第三入口缺少进度，根因仍未完全确认，不将其直接等同于前三种重循环超时。

为补齐这一证据缺口，提交原参数带进度诊断 job 12703601：`-n1024`、仍为整项
1500 秒 timeout、debug 30 分钟 job 上限，保留三次 launch 和全部校验。
只替换为加诊断的临时库，GOMAXPROCS=2；因此墙钟结果还需考虑与正式任务的环境差异，
不会覆盖正式套件原始 TIMEOUT 记录。结果将继续更新本节。

23:51 原参数诊断已完整结束，exit 0、host PASSED；三入口均完成 64 CTA 并完成
最终写回。前两入口周期分别仍为 36821、36972，与正式运行相同；第三入口为
134320 execution cycles、42784 retired、708 flush cycles、BackingVisible=true。
总执行周期 208113、总 flush 2140。进度从 10k 到 120k 持续增长，最终全部完成。
这次没有放宽 1500 秒上限或修改模拟器调度/TLS/Cache 逻辑，证明原参数存在可完整
通过的周期执行路径，未复现确定性的第三入口死锁、TLS 输出错误或 CTA 回收失败。

诊断入口使用 GOMAXPROCS=2 和独立诊断共享库，节点同为 gpu3-9，但不是严格控制
主机负载/运行时配置的性能实验。可以将本次失败主要归为墙钟性能/时限敏感，不能
只凭这一次重跑就把速度差异精确归因于 GOMAXPROCS，也不能声称发现并修复了主体 bug。
原始套件仍保留 TIMEOUT；另记录“同参数带诊断重跑通过”，不混入正式 PASS 数量。
证据：`.cache/runtime-diagnosis/20260910/multikernel-original/`，job 12703601。

### 5.8 raycast：超时，观察期内持续遍历，验证与输入冻结范围有限

原始 `-n3 -w32 -h24 -s1 -d1` / job 12703658，1500.330 秒超时 exit 124，
launch 1、finish 0、实际最后周期未知。grid=[8,24,1]、block=[4,1,1]，共 192 CTA，
768 像素、每像素 1 sample、最大深度 1。这是 regression/raycast 的软件渲染，
不是 RT 扩展硬件路径。`render.h` 中包含 TLAS/BVH 栈遍历、叶节点三角形求交、
变换和 shading，像素数本身不等于指令/访存次数；不同射线遍历长度也不同。

functional 同参数流程通过（39.251 秒）。但 `main.cpp` 仅调用 tracer.init/setup/run，
保存 output.ppm 后输出 PASSED，没有逐像素 CPU/golden 比较。因此本轮 raycast 的
PASS 只证明 host 流程和运行时可见性门禁通过，不能声称图像内容已经参考校验正确。
这是一项测试覆盖边界，不是已发现的像素错误。

独立 debug 诊断 job 12703901（2 CPU、4 GiB、10 分钟上限）依次运行同场景参数下
4×4 与原始 32×24 图像，每项最多 180 秒。仅使用带诊断的临时库记录 cycle/retired/
Warp 状态，不降低原始正式测试标准，也不修改 Cache 或模拟器主体。待结果更新本节。

输入冻结边界：`tracer.cpp` 用 resolve_path 搜索 `assets/teapot.obj` 及 ceramic.png、
red.png、flower.png；Makefile 将源码目录编译进 ASSETS_PATHS。快照 inputs/raycast
只有 host 和 kernel.vxbin，manifest.sha256 没有上述资产。因此 binary/library
hash 校验不能证明 raycast 的模型/纹理输入也冻结；它会读取工作区内 vortex 源码
assets，不能把这一行为误称为工作区外泄露，也不能宣称所有输入均已不可变。
本轮未修改这些资产或事后改写 manifest；建议下一轮把实际使用资产也纳入快照/哈希。

2026-09-11 00:33 诊断结束：4×4 与 32×24 两个 180 秒窗口都 exit 124，均未完成，
不能把小图像记为 PASS。小图像到 60000 cycles 时 retired=[3284,3221,3415,1331]，
Warp 3 已退出，其余三个继续推进。原规模进度如下：

| cycle | 每 Warp retired |
| --- | --- |
| 20000 | 773 / 749 / 755 / 748 |
| 30000 | 1228 / 1245 / 1246 / 1226 |
| 40000 | 1904 / 1896 / 1916 / 1893 |
| 50000 | 2579 / 2556 / 2569 / 2513 |

原规模末次采样 Warp 3 已 inactive，其余 Warp 的 PC/mask 在遍历路径变化。
两个窗口都没有内部协议异常或静止死锁证据，支持场景遍历开销较大的解释；
但没有完整图像结果，不能排除后段收敛/回收问题或验证图像内容。
应保留 TIMEOUT，后续用完整进度运行及 CPU/golden 图像比较补齐验收。
本次诊断证据：`.cache/runtime-diagnosis/20260910/raycast-small/`、`raycast-original/`。

### 5.9 sgemm2：原规模超时但诊断窗口持续推进，小规模通过

正式 `-n32 -t4 -c8` / job 12703774，1500.391 秒超时 exit 124，launch 1、finish 0，
原始最后周期未知。`-c8` 是 chunk_k=8，不是 CTA 数；实际 grid 8×8=64 CTA，
每 CTA block 4×4=16 lanes、4 Warp、256 bytes LMEM，受 4 Warp 容量约束最多驻留 1 CTA。

每个 CTA 遍历 4 个 K 分块：协作从 global 加载 local_A/local_B，各线程参与拷贝，
同步后执行 8 轮计算（每轮两次 LMEM 读和一次乘加），再次同步才覆盖下一分块。每 CTA 共 8 次源级
__syncthreads；全矩阵 32768 次源级乘加，另有地址计算、循环、访存和同步工作量。
原日志没有明确 barrier/协议错误，不能据此把超时归为 barrier deadlock，也不能
在没有进度时排除这种缺陷。functional 对照仍待本轮执行。

独立 debug job 12704054（2 CPU、4 GiB、10 分钟上限）依次运行 `-n8 -t4 -c8`
和原始 `-n32 -t4 -c8`，各最多 180 秒，保持 tile/chunk/LMEM/同步逻辑，仅采集周期、
退休数、Warp 状态。小规模仍有多个 CTA，但仅一个 K 分块，不能替代原规模跨分块验证。
待结果更新本节，不修改正式测试参数或模拟器主体。

2026-09-11 00:43 诊断结束：小规模 exit 0、host PASSED，4 CTA 全部完成，
10648 execution cycles、409 flush cycles、3040 retired，最终 backing_visible=true。
原规模 180 秒 exit 124；10000/20000/30000 cycles 时每 Warp retired 分别为
[708,708,708,708]、[1557,1557,1557,1557]、[2390,2390,2389,2388]。
四个 Warp 都持续推进，PC、在途数和 LSU pending 也变化，未出现内部协议异常。
因此采样窗口内没有静止 barrier deadlock 证据；小规模已覆盖 CTA 完成/复用和
协作 LMEM 同步，但不能证明原规模跨 K 分块、后段所有 CTA 都正确。
正式结果仍为 TIMEOUT，不能用缩小规模 PASS 替代。证据位于
`.cache/runtime-diagnosis/20260910/sgemm2-small/`、`sgemm2-original/`。

2026-09-11 00:49 监控更新：42/56 已报告，31 PASS（timing 10、functional 21）、
4 内部错误、7 TIMEOUT、14 未报告。sgemv timing（index 44 / job 12704087）
以 `-m64 -n64` 通过：20625 execution cycles、410 flush cycles、157.981 秒，
launch/finish 各 1。后继 softmax functional 为 job 12704134；sgemm timing 仍运行。

2026-09-11 00:55 更新：43/56 已报告，32 PASS（timing 11、functional 21）、
4 内部错误、7 TIMEOUT、13 未报告。sgemm timing（index 38 / job 12704021）
原参数 `-n32` 正式通过，1294.176 秒、121052 execution cycles、748 flush cycles、
48400 retired，64 CTA 全部完成，最终 backing_visible=true。长时间无 finish 事件
本身不是死锁证据，本例在正式 1500 秒上限内完成。后继 sgemm2 functional 为
job 12704153；softmax functional job 12704134 已启动。

2026-09-11 00:57 更新：45/56 已报告，34 PASS（timing 11、functional 23）、
4 内部错误、7 TIMEOUT、11 未报告。softmax functional（job 12704134）和
sgemm2 functional（job 12704153）均通过。sgemm2 已有同原规模功能型对照 PASS，
但仍不替代 timing 跨 K 分块及全部 CTA 的完成验证。sort timing job 12704154
已运行，另一链后继 job 12704164。

2026-09-11 01:07 更新：46/56 已报告，35 PASS（timing 12、functional 23）、
4 内部错误、7 TIMEOUT、10 未报告。sgemmx timing（index 42 / job 12704164）
以 `-n32` 通过：92586 execution cycles、663 flush cycles、554.736 秒，
launch/finish 各 1。sgemv functional 后继 job 12704203；sort timing 继续运行。

### 5.10 softmax：已确认与 conv3/dogfood/fence 相同的片段身份问题

正式 index 46 / job 12704235，`-n32`，228.532 秒 exit 255，于 cycle 33563
报 `lost stalled path lanes`。launch-finish 是 outcome=5 的失败事件，不是正常完成；
2 CTA 仅 admitted=1、completed=0，尚未发生第一 CTA 正常回收。
functional 同参数通过（11.392 秒）。源程序每行依次找最大值、计算 exp 并原地写回，
然后读回并归一化；包含分歧与 load/store，报错本身不能归因于 exp 数值实现。
debug 诊断 job 12705576 以原参数运行最多 600 秒，采集错误时旧/新 batch 和父身份，
确认或排除与 conv3/dogfood/fence 的已知片段身份问题相同，结果待补。

10:21 诊断在同一 cycle 33563 复现：旧 batch Slot=1/Generation=171/Mask=2，
新 batch Slot=4/Generation=60/Mask=1；旧 wire transaction=1458（port 1）仍
Valid=true、Delivered=false，新选中的 wire transaction=1459（port 0）。两者
都还原到 Kernel=1、CTA=0、Warp=1、WarpGeneration=1、Token=8371、Epoch=1，
父 SIMD Transaction/Tag=739，返回 lane mask 从 4 切换为 2。数据均为零且没有
响应 error；不能把正常的零数据解释为数据损坏。旧片段仍在生产者端有效，丢失的是
检查所需的片段身份，不是真正丢失旧 lane。与第 2 节同根因，修复方向一致；
未放宽 guard、加串行路径或修改模拟器。证据：
`.cache/runtime-diagnosis/20260910/softmax-original/stderr.log`。

### 5.11 sort：512 元素的平方工作量，正式超时尚无死锁证据

正式 index 48 / job 12704154，`-n32`，1500.115 秒 exit 124，launch 1、finish 0；
汇总中的 0 cycles 仍代表没有完成事件可供累计，原始最后周期未知。
host 将 count 乘 total_threads，日志实际 num_points=512。每个元素扫描全部 512 元素，
按数值和相等时下标决定唯一排序位置，共 262144 次源级比较迭代，另有访存/循环控制。
functional 同参数通过（19.304 秒），host 有 CPU 参考结果比较。
debug 诊断 job 12705577 依次运行 `-n1`（16 元素）和原始 `-n32`，各 180 秒，
用于区分持续推进与静止等待；缩小规模通过也不能替代完整原规模周期型验证。

10:23 诊断完成：16 元素 exit 0、host PASSED，2546 execution cycles、409 flush
cycles、824 retired，1 CTA 完成且 backing_visible=true。原规模 180 秒 exit 124，
10000 cycles 的 retired=[905,905,905,905]，20000 时为 [1846,1846,1848,1847]，
PC、pending 和 inflight 也变化；采样窗口内未出现静止死锁或内部协议错误。
这支持较大工作量和执行吞吐造成超时的方向，但没有全程结果，不能排除后段问题。
证据：`.cache/runtime-diagnosis/20260910/sort-small/`、`sort-original/`。

### 5.12 stencil3d：512 CTA、27 点邻域计算，正式超时待进度诊断

正式 index 50 / job 12704263，`-n16`，1500.244 秒 exit 124，launch 1、finish 0，
原始最后周期未知。16³=4096 个点，block 2×2×2、grid 8×8×8，共 512 CTA，
每 CTA 2 Warp，最多驻留 2 CTA。每点访问 27 个钳制边界后的邻居，累计 110592 次
源级邻居读取，外加三层循环、边界判断、地址计算和浮点求和；不是仅 4096 次简单访问。
functional 同参数通过（6.850 秒）。debug 诊断 job 12705578 依次运行 `-n4` 和
原始 `-n16`，各 180 秒；记录进度，不改变 Cache、存储延迟或正式超时标准。

10:23 新证据：小规模 `-n4` exit 255，在 cycle 7548 触发 `lost stalled path lanes`，
不是 PASS。旧 batch Slot=4/Generation=20/Mask=2，当前 Slot=1/Generation=95/Mask=1，
wire transaction 478（port 1）仍 Valid=true/Delivered=false，切换到 479（port 0）。
两片段的父身份同为 Kernel=1、CTA=1、Warp=0、WarpGeneration=2、Token=2369、
Epoch=1、SIMD Transaction/Tag=350，lane mask 4→2；旧数据仍有效且无 error。
这是第 2 节同一片段身份检查问题在 stencil3d 的额外反例。统计 generated=7、
admitted=6、completed=4，不说明已完成全部 8 CTA，也不能只因处于复用阶段就
归为 stale residency；新旧片段的 residency 完全相同，已有直接 batch 切换证据。
正式原规模仍为 TIMEOUT，不能事后改写成已复现的内部错误；但更不能将本 benchmark
整体标成纯性能问题。原规模进度诊断继续。证据：
`.cache/runtime-diagnosis/20260910/stencil3d-small/`。

10:26 最终诊断：原规模 180 秒 exit 124，10000/20000 cycles 时 retired 分别为
[853,853,836,835] / [1843,1842,1824,1823]，四 Warp 均推进、PC 和在途数变化。
原规模观察窗口内没有内部异常或静止死锁，但它没有跑完全程，小规模已有真实内部
失败反例，不能排除原规模后段同类错误。证据：`stencil3d-original/`。
三项最后诊断作业 12705576/12705577/12705578 均已终结，队列无残留；包装脚本
Slurm COMPLETED 只说明诊断脚本执行完毕，实际内层退出码分别为 softmax=255、
sort small/original=0/124、stencil3d small/original=255/124，不将其误记为全部通过。

## 6. 全轮汇总与处理优先级

正式结果为 42/56 PASS，不是全轮验收通过。functional 28/28 PASS；timing 14/28 PASS，
5 内部错误、9 TIMEOUT。timing 通过项为 demo、dotproduct、dotproduct2、dropout、
io_addr、mstress、packld、pathfinder、relu、sgemm、sgemmx、sgemv、vecadd、wgather。
diagnostic 的小规模或原规模结果不计入上述正式数字。

| 正式 timing 失败项 | 分类 | 当前证据与边界 |
| --- | --- | --- |
| async_barrier | 内部错误 | 旧合法 BAR 外部事件被 PC frontier 拒绝；单 CTA 复现，见 §1 |
| conv3 | 内部错误 | 不同 batch 还原为相同父身份，稳定性检查误判，见 §2 |
| dogfood | 内部错误 | 单独 imul 子测复现同一 batch 身份问题，其后子测未执行，见 §5.1 |
| fence | 内部错误 | 同一 load 响应身份问题，不是已证实 FENCE 指令错误，见 §5.3 |
| softmax | 内部错误 | 同一周期复现同一 batch 身份问题，不是 exp 算术错误，见 §5.10 |
| diverge | 超时 | 小规模通过，原规模诊断窗口持续推进，全程未知，见 §5.2 |
| jacobi | 超时 | double 软件辅助计算开销，小规模通过、原窗口推进，见 §5.4 |
| madmax | 超时 | 密集 FMA 工作量，小规模通过、原窗口推进，见 §5.5 |
| multikernel | 超时 | 正式第三入口超时；独立原规模诊断全部通过，存在墙钟敏感性，见 §5.7 |
| occupancy | 超时 | 配额下两 CTA、单 lane 长 LMEM 循环，窗口推进，完整回收尚未验证，见 §5.6 |
| raycast | 超时 | 两个规模窗口均推进但未完成；另有图像 oracle/资产冻结缺口，见 §5.8 |
| sgemm2 | 超时 | 协作 LMEM/分块计算，小规模通过，原规模窗口推进，见 §5.9 |
| sort | 超时 | 512 元素平方扫描，小规模通过、原窗口推进，见 §5.11 |
| stencil3d | 超时 | 小规模另暴露已知 batch 身份错误，不可归为纯性能问题，见 §5.12 |

建议后续按以下优先级处理，本次只分析，不实施主体修复：

1. 修复响应片段身份在 adapter/coalescer/split 边界的信息丢失；覆盖四项正式内部失败
   与 stencil3d 小规模反例，保留真实稳定性、重复回执及旧 residency 防护。
2. 分离 PC 可见前沿与异步 barrier 外部事件交付；增加跨周期延迟与年轻指令先完成测试。
3. 完善终态日志原子收尾，以及 timeout 的最后 cycle/retired、CTA 进度与阶段耗时；
   缺失事件显示 unknown，不能用汇总零周期解释执行状态。
4. 做受控吞吐 profiling：固定二进制、参数、节点资源与并发，比较 GOMAXPROCS，
   区分主机执行开销、模型 stall、工作量和静止等待；当前证据不足以指定唯一热点，
   不以扩大 timeout 或强制串行 drain 代替正确性修复。
5. 在修复后补跑原规模失败项及 dogfood 未执行子测；raycast 增加输入资产冻结和
   参考图像检查。所有 PASS 仍仅覆盖本轮参数与现有 host oracle，不代表 RTL 周期等价。

终态完整性检查：manifest 56 个索引均有 result.json；两链到 position 27，末项
job 12704262、12704405 的 Slurm 状态均 COMPLETED。63 个 manifest 文件哈希全部
一致（不含 §5.8 已指出的 raycast 外部资产）。全部正式 PASS 的 launch/finish 序号
计数匹配、outcome=0、各序号最终 backing_visible=true。sort/stencil3d 的 Slurm FAILED
对应包装脚本 exit 1、内部 benchmark timeout 124，不是被 Slurm OOM 或时限杀死。
主体 timing/emu/isa/Vortex_rtl 对 HEAD 无差异，git diff --check 通过；未提交 commit。

## 7. CPU 节点原规模不限程序时长重跑（2026-09-11）

按用户要求，9 项正式 TIMEOUT 单独提交作业数组 **12705673**：index 0–8 依次为
diverge、jacobi、madmax、multikernel、occupancy、raycast、sgemm2、sort、stencil3d。
保留原规模参数、4 CPU/8 GiB、GOMAXPROCS=4、external latency=100；最多并发 2 项。
CPU-only `long_cpu` 分区最大允许 14 天，没有无限时长 CPU 分区，因此 Slurm 申请
14-00:00:00；程序侧没有总时长限制，30 秒 wait 仅用于写心跳，绝不据此 kill 子进程。

新结果目录：`.cache/runtime-cpu-retry-20260911-3QRdUl/`，与正式原结果分离。
原快照输入及库复制并校验，Go 库替换为此前仅加入进度/错误身份诊断的 progress-lib，
不是主体修复版本。每 10000 cycles 的 cycle/retired/Warp 状态写 stderr.log，
每 30 秒将最后进度与 elapsed/node 写 `results/<benchmark>/status.json`；进程结束时
写 result.json。心跳存活不证明模拟器逻辑推进；取消/节点失败须以 Slurm 状态为准。
raycast 仍有 §5.8 的工作区资产引用边界，本轮不暗中修改其输入或验收口径。

查看：`bash .cache/runtime-cpu-retry-20260911-3QRdUl/monitor.sh`（在 Simulator_timing 下）。
持续显示可用 `watch -n 30 bash .cache/runtime-cpu-retry-20260911-3QRdUl/monitor.sh`。
实际入口为 retry.sbatch / retry.py；目录中的 runner.py / run.sbatch 是原快照留档，
不用于此次启动。新轮后续分析仍追加本文，不另建报告。

11:10 首次运行状态：数组 0/1（diverge/jacobi）在 CPU 节点 cpu1-1 并行运行，
其余 7 项因数组并发上限正常等待。约 11.5 分钟时，diverge 已到 140000 cycles，
retired=[5878,5870,5870,5869]；jacobi 已到 80000 cycles，
retired=[5955,5934,6003,5971]。两项 30 秒心跳均新鲜，PC、pending 和 inflight
在变化，没有内部错误或静止证据。本记录只说明当前活跃推进，不预判最终正确性。

11:23 更新：diverge/jacobi 均已运行超过原正式轮的 1500 秒程序超时点，数组任务
仍为 RUNNING，证明新入口未继承旧 timeout kill。diverge 到 300000 cycles，
retired=[12675,12663,12689,12715]；jacobi 到 180000 cycles，
retired=[13441,13419,13396,13369]。两者周期、PC 与资源状态仍变化，无内部错误。

### 7.1 长墙钟是否正常：真实 workload 放大与宿主模拟器开销叠加

11:45 状态：diverge 570000 cycles、四 Warp 合计约 96665 retired；jacobi
350000 cycles、合计约 102495 retired，仍持续推进。由 cycle=0 与当前采样时间计算，
两者吞吐约为 207 / 127 simulated cycles/s，对应聚合 retired/cycle 约 0.170 / 0.293。
这些数值说明运行没有静止，但周期模拟吞吐很低。

工作量本身有明确放大。diverge 的 host 将 `-n64` 乘 16 个线程得到 1024 points，
又把 1024 作为每个线程的 `samples`；仅 `while(samples--)` 就是 1024×1024 次
源级 noinline `hacker`/分支，随后 `for (i < task_id)` 累计另约 523776 次迭代，
再叠加深度 8 的分歧控制。jacobi 是 64×64，共 4096 次源级矩阵项迭代，但用
`double sum` 累加 float 乘积，目标 ISA 缺少原生 double 路径时会展开软件辅助调用。
因此这两项比表面的 64 参数重得多，不应把全部墙钟都归因于模拟器错误。

同时存在明确的宿主侧性能设计放大：

1. `timing/runner/kernel.go:176-184` 对 budget 中的每个周期调用一次
   `k.runner.Run(1, ...)`；`timing/model/clock.go:39-42` 每次 Run 都新建、注册并运行
   一个 Akita SerialEngine。因此 N 个模拟周期会发生 N 次事件引擎创建/销毁，
   而不是同一 engine 连续推进一个 chunk。这是可避免的宿主开销。
2. Kernel 每周期还调用 residency 和完整 Status；MultiRunner 每周期推进完整 memory
   hierarchy。启用 hierarchy 时 `multi.go:223-233` 为确定 ready/accepted 关系会先后
   Evaluate Core 两次。它们可能是当前正确性设计的一部分，但也增加每周期成本。
3. external latency=100 采用严格逐周期推进，miss 等待的每一个空转周期仍执行上述
   宿主逻辑；当前没有安全的 next-event/idle-cycle 跳跃。因此 100-cycle 延迟同时
   放大建模周期数和宿主循环次数。是否能跳跃必须证明期间无可见调度/握手事件，
   不能直接批量加 cycle 绕过周期语义。

资源侧不支持“只是没拿到 CPU/内存”的解释。Slurm sstat 在约 45.9 分钟时显示每项
AveCPU 约 1:37:51，即平均约 2.13 个核，MaxRSS 约 40 MiB；内存远未到 8 GiB，
但 4 个分配核没有线性用满，符合 serial engine/串行周期协调占主导的结构。
同一 progress-lib 在先前 debug 节点诊断约为 diverge 345、jacobi 200 cycles/s，
当前 cpu1-1 分别约 207、127 cycles/s，节点单核性能或同节点并发又造成约 35%–40%
下降。换节点可缓解墙钟，但不能消除每周期宿主开销。

结论分两层：长运行**部分正常**，因为 workload 与真实 cache/memory stall 会产生大量
模拟周期；但当前实现也有显著、已由调用路径直接确认的宿主性能设计问题，尤其是
每周期重建事件引擎。现有证据尚不能证明模拟器生成了“不应存在”的额外硬件周期，
那需要与 RTL trace/逐阶段计数对照；因此此处记录为性能架构瓶颈，不当作周期正确性
bug，也不在本监控任务中直接修改主体。后续优化应先用 CPU/alloc profile 定量排序，
再分别验证 engine 复用、状态检查降频和可证明安全的空闲周期跳跃，且要求周期结果不变。

### 7.2 diverge/jacobi 约一小时快照、时间预估与人工取消

11:58 取消前最终快照：数组元素 0/1 均在 cpu1-1 连续运行 59:52，Slurm RUNNING，
程序侧约 3571 秒。diverge 最近采样 cycle=730000，retired=
[30831,30849,30900,30958]；jacobi cycle=440000，retired=
[32634,32620,32593,32554]。两项从 cycle=0 到取消前始终有 PC、retired、pending
和 inflight 变化，没有静止或内部协议错误。sstat AveCPU 均约 2:07:37，折算平均
约 2.13 个 CPU 核；MaxRSS 约 40 MiB。

取消前的粗略墙钟预估（仅用于资源决策，不是完成证明）：

* jacobi 的 CTA/warp 阶段变化约每 16–18 万 cycles 出现一次；原规模共 4 CTA。
  若后续阶段相近，预计总量约 65–75 万 cycles、总墙钟约 1.4–1.7 小时，取消时
  可能还需约 30–45 分钟。软件 double helper 和不同 CTA 尾部会造成偏差。
* diverge 有 64 CTA、256 Warp；每 Warp 都执行 1024 次 samples 循环，另有随
  task_id 增长的分歧循环。按当前约 203 cycles/s、当前累计退休量与静态循环规模
  外推，完整运行更可能是十几至二十多小时，保守记为约 15–25 小时；CTA 后段
  分歧、cache 行为与宿主负载可能显著改变结果，不能把它视为精确 ETA。

应用户指示，在保存上述快照后只取消数组元素 12705673_0（diverge）和
12705673_1（jacobi），不取消整个数组；二者结果应标记为 USER_CANCELLED，不能记为
PASS/FAIL 或沿用正式 TIMEOUT。释放的两个数组槽位用于继续运行剩余 7 项。

12:00 Slurm 终态确认：两个元素均由用户 205220 取消，elapsed=01:00:48，batch
exit=0:15。取消信号前最后一次落盘心跳比预取消快照又前进：diverge cycle=750000，
retired=[31670,31688,31738,31798]；jacobi cycle=450000，retired=
[33414,33361,33338,33251]。这不改变上述 ETA 的量级与不确定性。监控层以独立
manual-status.json 将两项显示为 USER_CANCELLED，保留原 status.json 作为进程最后
心跳，不伪造 benchmark result.json。

数组元素 2/3 已接替：madmax 在 cpu1-33、multikernel 在 cpu1-81 运行；元素 4–8
继续因 JobArrayTaskLimit 等待。两项都使用原规模参数和相同诊断/心跳入口。

12:03 multikernel 的第一个 add_k 入口正式完成：36821 execution cycles、716 flush
cycles、8736 retired，64 CTA 全部完成且 backing_visible=true；周期数与上一轮相同，
随后 sequence=2 的 mul_k 已启动，所以进度日志 cycle 重新从 0 计数。madmax 同时
推进到 40000 cycles，尚无内部错误。本轮仍需等 multikernel 三个入口及 host oracle
全部结束，不能用第一个入口完成记整项 PASS。

12:07 multikernel 第二个 mul_k 入口也完成：36972 execution cycles、716 flush
cycles、8736 retired，64 CTA 全部完成并可见；同样与上一轮周期数一致。sequence=3
的 acc_k 已启动，这才是原正式运行未完成的阶段。此前第二入口 cycle=10000 时四 Warp
短暂 inactive/PendingLSU=1，之后恢复并完成，证明该单点采样是正常 memory tail/CTA
切换而非死锁。madmax 此时到 90000 cycles，持续推进。

12:26 multikernel 整项完成并判定 PASS，墙钟 1628.902 秒。结构化结果记录 3 次
launch/3 次 finish、总 execution_cycles=208113、进程 exit_code=0；第三个 acc_k 为
134320 execution cycles、708 flush cycles、42784 retired、64/64 CTA 完成，且
cache-flush 后 backing_visible=true。前两个入口分别仍为 36821/36972 cycles，三项
outcome 均为 0，宿主 oracle 输出 `PASSED!`。因此 PASS 同时满足退出码、launch/finish
配对、CTA 完成、最终可见性和宿主结果，不是仅凭短测试或 Slurm 退出状态得出。

multikernel 释放并发槽后，元素 4 occupancy 已自动在 cpu1-102 启动；元素 5–8 继续
受 `%2` JobArrayTaskLimit 等待。同期 madmax 已到 280000 cycles、四 Warp retired
约 14340，各项 PC/pending/inflight 仍变化，无停滞迹象。

### 7.3 madmax/occupancy 剩余时间评估与人工取消

12:45 按用户要求评估是否继续占用两个数组槽。取消前最新落盘快照如下：madmax
运行约 2791 秒，cycle=470000、retired=[24079,24079,24078,24078]；occupancy
运行约 1171 秒，cycle=210000、retired=[18574,18574,0,0]。两项从启动到该快照
持续改变 PC、retired、pending 和 inflight，无静止、panic 或协议错误。因此取消原因
是已确认的成本/吞吐决策，不是把运行中的任务误判为死锁或功能失败。

madmax 当前吞吐约 168 cycles/s。已完成的 `-n1` 对照需 71348 execution cycles；
原参数 `-n32` 有 256 CTA，当前 4 个 Warp/CTA 并行，按约 64 个 residency 波次粗估
总量约 4.6M cycles。扣除 470k 后，若后续波次相近，尚需约 6.5–7.5 小时；考虑
cache 热身、CTA 尾部和节点负载，资源规划采用约 6–8 小时。该估计不是完成证明。

occupancy 当前吞吐约 179 cycles/s。210k cycles 时首批两个 CTA 仍驻留，但每 Warp
已经退休 18574 条，符合两个 2048 次 LMEM store/load 长循环接近尾段的量级。
冻结 LMEM 配额一次只允许 2 CTA、全任务共 8 CTA，按约 4 个相近 residency 波次
外推总量约 0.8–0.9M cycles，预计总墙钟约 75–85 分钟，当前尚需约 55–65 分钟。
后续回收/flush 行为可能使该估计偏移。

两项与 diverge 的性质相同：存在真实的大规模循环/CTA 工作量，但墙钟又被 §7.1
确认的逐模拟周期重建 SerialEngine、逐周期全层级 Evaluate/Status 和无法跳过固定延迟
空闲周期显著放大。已有同一 runner 资源采样约 2.1 CPU 核、40 MiB，也不支持内存
不足或等待 CPU 配额的解释。本轮目的为尽快覆盖其余 benchmark；经用户明确授权，
记录上述 ETA 后仅取消数组元素 12705673_2（madmax）和 12705673_4（occupancy），
将它们记为 USER_CANCELLED，不记 PASS/FAIL，并让元素 5/6 自动接替。

12:46 Slurm 终态确认：madmax element elapsed=00:47:43、occupancy element
elapsed=00:20:33，均为用户 205220 取消；batch step 接收信号后 exit=0:15。取消信号
前最后落盘分别为 480000/220000 cycles；监控层 manual-status.json 已固化这两个终值，保留原 status.json
心跳且不伪造 result.json。两个槽位随即由元素 5 raycast（cpu1-3）和元素 6 sgemm2
（cpu1-20）接替，元素 7/8 继续按 `%2` 等待。

### 7.4 raycast/sgemm2 成本评估与释放最后两个测试槽

12:49 raycast 运行约 181 秒到 30000 cycles，retired=[1228,1245,1246,1226]；
数值与 §5.8 原规模旧诊断 30000-cycle 样本逐项一致，PC/mask/inflight 持续变化。
原规模有 192 CTA，最多约 4 CTA/warp 同时推进；此前 4×4 小图到 60000 cycles
仍未全部结束。按约 48 个 residency 波次及每波约 0.08–0.12M cycles 粗估，总量
约 4–6M cycles；当前约 166 cycles/s，对应尚需约 6–10 小时。BVH 路径分歧会令
各波工作量变化，ETA 只用于资源调度。

sgemm2 运行约 181 秒，已确认到 10000 cycles、retired=[708,708,708,708]，与
§5.9 原规模旧诊断同周期样本完全一致；30 秒进程心跳持续刷新，未见 barrier 或内部
协议错误。小规模 4 CTA、1 个 K 分块共需 10648 cycles；原规模 64 CTA、每 CTA
4 个 K 分块，且冻结配置一次只驻留 1 CTA。按工作量比例及热身摊销外推约
0.65–0.8M cycles，当前约 110–130 cycles/s，对应约 1.4–2 小时。

两项都复现已知的正常退休路径，长墙钟来自 workload 波次数与 §7.1 宿主逐周期开销
叠加，性质与 diverge/madmax 相同；当前继续等待的边际价值低于让最后两个不同 workload
进入测试。经用户授权，记录快照与 ETA 后仅取消元素 12705673_5（raycast）和
12705673_6（sgemm2），标为 USER_CANCELLED，不记 PASS/FAIL；元素 7 sort 与
元素 8 stencil3d 将接替，数组不整体取消。

12:54 Slurm 终态确认：raycast 与 sgemm2 element 均 elapsed=00:07:42、由用户
205220 取消，batch step exit=0:15。取消信号前最后落盘进度分别到 100000 cycles
（raycast，两个 Warp 已结束、另外两个仍活动）和 40000 cycles（sgemm2，四 Warp
仍活动）；它们进一步支持持续推进而非死锁，不改变上述数量级判断。manual-status.json
已固化这两个最终周期。最后两个元素 7 sort 与 8 stencil3d 已同时在 cpu1-1 启动，
数组已无待启动项。

### 7.5 sort 提前释放；stencil3d 保留作错误复现

12:57 sort 与 stencil3d 均首次到 10000 cycles。sort retired=
[905,905,905,905]，stencil3d retired=[853,853,836,835]，两者与 §5.11/§5.12
旧原规模诊断的 10000-cycle 样本逐项完全一致，说明输入和执行路径没有漂移。

sort 从 cycle=0 到 10000 约耗时 94 秒，约 106 cycles/s。已通过的 16 元素对照为
2546 cycles，而原规模 512 元素的全扫描比较工作量约为其 1024 倍；考虑固定开销和
缓存复用，粗估总量仍约 2.5–3.0M cycles、总墙钟约 6.5–8 小时。它持续退休且
inflight/PC 变化，没有错误或死锁迹象，属于大工作量被逐周期宿主设计放大的同类任务。
经用户授权，仅取消元素 12705673_7（sort）并记 USER_CANCELLED，不记 PASS/FAIL。

stencil3d 不按相同理由立即取消：§5.12 的 `-n4` 已在 cycle 7548 真实触发
`lost stalled path lanes`，涉及同一 residency 下 batch 片段身份切换；原规模此前
20k 窗口未复现，但后续 CTA 复用仍可能触发。继续运行元素 12705673_8，优先取得
真实错误或跨 CTA 复用的结果，而不是仅凭性能外推结束。

13:14 sort 的取消请求最终由 Slurm 生效：element elapsed=00:20:09，用户取消，
batch exit=0:15；信号前最后落盘 cycle=110000、retired 约 10506/Warp，仍持续推进。
取消请求到实际终止间继续运行不改变 2.5–3.0M cycles 的总量估计。manual-status.json
已固化该终态。stencil3d 同期到 110000 cycles、四 Warp retired 约 10770，未出现
内部错误；数组现在只剩元素 8 运行。

### 7.6 stencil3d 剩余时间与终止边界

13:16 stencil3d 独占运行到 120000 cycles，retired=[11774,11773,11772,11770]，
PC、LSU pending 和 inflight 继续变化，没有在本窗口复现小规模的 batch 身份错误。
以 `-n4` 在 cycle 7548 已完成 4/8 CTA 后才触发错误为下界，若该规模无错跑完约需
1.5 万周期；原规模 16³ 点相对 4³ 点/CTA 数放大 64 倍，粗估完整执行约
0.9–1.2M cycles。当前 cpu1-1 独占后的吞吐约 90–110 cycles/s，扣除现有进度仍需
约 2–3 小时，CTA/cache 差异会影响误差。

该墙钟仍是实际 27 点邻域 workload 与 §7.1 每周期宿主开销叠加的结果，属于可提前
结束的同类长任务。另一方面，本次原规模 120k 窗口未报错不能推翻 §5.12 的 `-n4`
真实失败，也不能宣称 batch 身份缺陷已修复。数组已无其他待启动 benchmark；按用户
允许终止同类异常长任务的指示，取消元素 12705673_8，记为 USER_CANCELLED，并保留
“原规模窗口持续推进、小规模稳定反例尚未关闭”的结论，不记 PASS/FAIL。

13:18 Slurm 终态确认：stencil3d element elapsed=00:24:09、由用户 205220 取消，
batch exit=0:15；信号前最后落盘 cycle=130000、retired=
[12777,12777,12769,12769]，仍未复现内部错误。manual-status.json 已固化该终态。

至此数组 12705673 全部终结：multikernel 1 项严格 PASS；diverge、jacobi、madmax、
occupancy、raycast、sgemm2、sort、stencil3d 共 8 项为 USER_CANCELLED，其中前 2 项
按用户早先指定取消，后 6 项在记录进度、ETA 和宿主性能放大依据后按用户授权取消。
人工取消项均不计入 PASS/FAIL；没有数组元素仍在 Slurm 队列。stencil3d 的小规模
batch 身份错误继续作为未关闭的模拟器主体问题，不能被本次原规模窗口覆盖。

## 8. occupancy/sgemm2 独立不限程序时长复测（2026-09-11）

按用户要求再次完整运行 occupancy `-c8` 与 sgemm2 `-n32 -t4 -c8`。新快照结果目录为
`.cache/runtime-cpu-rerun-occ-sgemm-20260911/`，Slurm 数组 **12707840**，元素 0/1
依次对应 occupancy/sgemm2。每项 4 CPU、8 GiB，long_cpu 分区申请其允许的最长
14 天；程序侧没有 timeout，30 秒 wait 只写 status 心跳，不会终止 benchmark。

新 runner 不复用旧数组的 results/manual-status；它直接使用 §7 已冻结的 binary、
kernel.vxbin 和共享库，并在执行前后校验相关 SHA-256，因此参数和模拟器构件未变化，
只有结果目录与 Slurm job ID 不同。提交后数组暂为 PENDING，完整原因是所需节点当前
DOWN/DRAINED 或为更高优先级分区保留，属于外部调度等待，不是模拟器或 runner 错误。
后续启动、周期进度和终态继续追加本节。

16:22 调度器为数组两个元素分配节点：occupancy 在 cpu1-71、sgemm2 在 cpu1-73；
独立 results 目录与首次 RUNNING 心跳均成功生成，证明冻结构件哈希校验、动态库加载和
benchmark 启动通过。约 13.5 分钟时，occupancy 到 160000 cycles、retired=
[14154,14154,0,0]，sgemm2 到 70000 cycles、retired=
[5794,5793,5792,5792]。两项 PC、pending 和 inflight 持续变化，无内部错误或静止。
由启动时间粗算吞吐约为 197/86 cycles/s；occupancy 的两个 inactive Warp 仍符合
每 CTA 8192-byte LMEM、最多两个 CTA 驻留的冻结配置，不是漏调度。

### 8.1 排队追加 basic/bfs/wsync

按用户要求，在当前数组之后增加 regression/basic、bfs、wsync 三项 timing 测试。
它们不在此前 28 项 supported 快照清单中，因此没有把工作区 build 目录直接作为运行
输入：现有 host binary 与 kernel.vxbin 已复制并冻结到
`.cache/runtime-cpu-followup-basic-bfs-wsync-20260911/inputs/`，manifest 记录了每个文件
的 SHA-256；runner 在执行前后同时校验这些输入与 §7/§8 使用的同一套 timing runtime
库。参数取各 benchmark Makefile 的原默认值：basic `-n256`、bfs `-n1024`、wsync
`-i1024`。

Slurm 数组 **12708106** 为 3 个元素、最多并发 2 项，每项 4 CPU/8 GiB、long_cpu
14 天上限且无程序 timeout。提交依赖经 `scontrol show job` 核验为
`afterany:12707840_*`，当前状态 `PENDING (Dependency)`；只有 occupancy/sgemm2
数组全部终结后才具备调度资格，不会与当前两项抢占测试槽。后续运行与终态继续追加
本节。

17:16 左右依赖数组获得 cpu1-33。basic 在 43.038 秒内完成并严格 PASS：1/1
launch-finish、15509 execution cycles、1297 retired、1/1 CTA 完成、409 flush cycles
后 backing_visible=true，host 输出 `Test PASSED`。bfs 在 150.747 秒内完成并严格
PASS：10/10 launch-finish，总 execution_cycles=31455；每个 sequence 的 outcome=0、
CTA generated/admitted/completed 一致，且每次 cache-flush 后 backing_visible=true，
host 输出 `PASSED!`。十次 launch 是 BFS 迭代 frontier 的正常多入口过程，不是 runner
重复提交。

wsync 随后在同一节点启动；约 5 分钟时到 120000 cycles、retired=[8908,0,0,0]，
唯一活动 Warp 的 StallReason=control-drain，pending/inflight 非零，符合 WSYNC 等待旧
指令排空的控制路径。此时仍持续推进且无 simulator-internal，不把单次 control-drain
采样误判为死锁，继续完整运行。

18:12 独立不限程序时长复测已全部通过严格验收：

* occupancy / cpu1-71：exit 0，墙钟 6620.137 秒，1/1 launch-finish，
  1296227 execution cycles、229632 retired；8/8 CTA generated/admitted/completed，
  409 flush cycles 后 backing_visible=true，host 输出 `PASSED`。
* sgemm2 / cpu1-73：exit 0，墙钟 4106.419 秒，1/1 launch-finish，
  402402 execution cycles、135952 retired；64/64 CTA generated/admitted/completed，
  664 flush cycles 后 backing_visible=true，host 输出 `PASSED!`。

两项都同时满足进程退出码、launch/finish 配对、outcome=0、CTA 生命周期计数、最终
backing memory 可见性和 host oracle，不是仅依赖 runner/Slurm 标签。occupancy 实际
约 110 分钟，较 §7.3 的 75–85 分钟资源估计更慢（此前只观察首批 CTA 且低估后续
波次/节点速率）；sgemm2 约 68 分钟，快于 §7.4 的 1.4–2 小时保守估计。结果证明
两项原规模在当前实现存在完整正确执行路径，同时仍保留 §7.1 的宿主性能瓶颈结论。

数组 12707840 终结后，12708106 的 dependency 已解除；当前 PENDING 原因为 long_cpu
节点 DOWN/DRAINED 或更高优先级预留，属于外部调度等待，而非依赖或 runner 失败。

wsync 随后也完整结束并严格 PASS：exit 0，墙钟 1139.055 秒，1/1 launch-finish，
495073 execution cycles、36923 retired、1/1 CTA generated/admitted/completed；outcome=0，
409 flush cycles 后 backing_visible=true，host 输出 `PASSED!`。从 120000 到 490000
cycles 的持续采样中 retired、PC、pending 与 inflight 一直变化，因此中途出现的
`control-drain` 是瞬时硬件等待状态，不是死锁。至此数组 **12708106** 的 basic、bfs、
wsync 三项均通过进程、协议事件、CTA 生命周期、最终可见性和 host oracle 的联合验收。

### 8.2 与 RTLSIM 的同参数周期数对比

为避免把不同输入规模的历史数字硬拼成比值，新建同参数 RTLSIM 对照作业
**12708818**，依次运行 occupancy `-c8`、sgemm2 `-n32 -t4 -c8`、basic `-n256`、
bfs `-n1024`、wsync `-i1024`。输入 binary/kernel.vxbin 均复制到
`.cache/rtlsim-cycle-compare-20260911/inputs/` 并记录 SHA-256；runner 启动前校验输入，
逐项解析 RTLSIM 的 `PERF: instrs=..., cycles=..., IPC=...`，并与上述 timing PASS
result.json 自动生成汇总和 `timing/rtlsim` 比值。BFS 有 10 次 kernel launch，统计时
对全部 PERF 样本求和，而不是只取最后一行。

RTLSIM 最终在作业内以 release（不定义 DEBUG）、`PERF=1 THREADS=8` 构建，避免 DEBUG3
逐周期 trace 反过来主导墙钟且保留周期计数；
五项顺序执行以便错误定位。作业原依赖 `afterany:12708106`，提交后依赖已经解除，当前
PENDING 的完整原因是所需 long_cpu 节点 DOWN/DRAINED 或被更高优先级分区预留，属于
外部调度等待。最终结果将在本节追加。需要注意：本次 RTLSIM 构建日志明确显示冻结配置
`L2_ENABLED=0`、`L3_ENABLED=0`，两侧均比较到 L1；但 RTLSIM 在 L1 外使用 Ramulator
HBM2/platform memory 模型，Simulator_timing 则使用固定 100-cycle external backend。
因此端到端周期比可用于识别总体偏差，但不能单独归因于某一个 cache 或流水级。

首次作业在 cpu1-33 获得节点后仅运行 10 秒即 exit 2，尚未进入任何 benchmark，
`run_compare.py status` 仍为 NOT_STARTED。失败点是 Verilator 生成的 Makefile 默认执行
`ccache g++`，而该计算节点环境中的 ccache shim 不可执行（`execvp: ccache: Permission
denied`）；这是构建环境问题，不是 RTLSIM 或 timing 周期结果。进一步检查还发现原命令
传入 `DEBUG=0` 会被 Makefile 的 `ifdef DEBUG` 当成已定义，从而错误选择 `-O0` 和 trace
分支。重试脚本改为不定义 DEBUG、显式 `PERF=1` 保留周期计数，并通过命令行
`OBJCACHE=` 传播到 Verilator 的递归 make 以绕过 ccache。没有修改 RTL、cache、runtime
接口、benchmark 或冻结输入；首次失败作业不计入对比样本。

第二次作业 **12708929** 已在 cpu1-17 正确完成 O2、无 trace、PERF_ENABLE 的 RTLSIM
构建，证明 ccache 与 DEBUG 分支修正有效；但五个 host 进程均在约 0.01 秒的动态加载
阶段退出，未执行 RTL，原因是 runner 将 `LD_LIBRARY_PATH` 覆盖为私有 lib 目录，丢掉
GCC 12 runtime 路径，系统旧 `libstdc++.so.6` 缺少 `GLIBCXX_3.4.29/30`。重试同时在
SBATCH 中导出 `g++ -print-file-name=libstdc++.so.6` 对应目录，并让 runner 在私有 RTLSIM
库后保留原 LD_LIBRARY_PATH。失败目录单独归档，不作为周期样本；模拟器与输入仍未改动。
修正后的第三次作业编号为 **12708991**。

第三次作业在 cpu1-17 完整结束，Slurm 状态 COMPLETED、exit 0；五项 stderr 均为 0 字节，
每项 stdout 均同时包含非零 RTLSIM PERF 和 host PASS。比较结果如下，差值和百分比均以
`timing - RTLSIM` 为正方向：

| benchmark（参数） | timing cycles | RTLSIM cycles | 差值 | timing/RTLSIM | timing 偏高 |
|---|---:|---:|---:|---:|---:|
| occupancy (`-c8`) | 1,296,227 | 1,295,944 | 283 | 1.000218 | 0.022% |
| sgemm2 (`-n32 -t4 -c8`) | 402,402 | 382,156 | 20,246 | 1.052978 | 5.298% |
| basic (`-n256`) | 15,509 | 13,983 | 1,526 | 1.109133 | 10.913% |
| bfs (`-n1024`) | 31,455 | 21,995 | 9,460 | 1.430098 | 43.010% |
| wsync (`-i1024`) | 495,073 | 409,292 | 85,781 | 1.209584 | 20.958% |

五项 RTLSIM PERF 的 instructions 分别为 229632、135952、1297、3548、36923，与 timing
侧 retired 逐项相等，说明周期差不是因为少执行/多执行了架构指令。BFS 的统计口径也已
单独核验：timing 的 10 次 launch 周期为 2629、2546、2885、4414、4917、4575、2756、
2564、2413、1756，求和恰为 31455；retired 求和恰为 3548。RTLSIM 将这十次 device
start 累计为一条 `PERF: instrs=3548, cycles=21995`，因此表中是累计对累计，并非用
timing 总和误比 RTLSIM 的单次末值。

结论分两层：occupancy 仅差 283 cycles（0.022%），表明长 LMEM/occupancy 主路径在该
workload 上已经非常接近冻结 RTL；sgemm2 的 5.3% 也处于较小偏差。basic、wsync、bfs
依次偏高约 10.9%、21.0%、43.0%，并且指令数完全相同，差异集中在每条指令之外的控制、
同步、launch/cache 冷启动和 memory service 周期。尤其 BFS 有十次短 launch，固定的
每次启动/清空成本会被重复放大；wsync 则对 pending/control-drain 的精确释放周期敏感。
这些是由结果支持的定位方向，不足以仅凭五个端到端数字断言某一个组件错误；后续若做
RTL 周期精度收敛，应按 launch 分解 Fetch/LSU/cache miss、barrier/WSYNC 和 backend
等待计数，而不是改 benchmark 或用统一比例校正。

## 9. 全部 Timing PASS benchmark 与 RTLSIM 周期对比（2026-09-14）

在 §8.2 已完成的 5 项基础上，枚举当前全部不同 benchmark 的 Timing PASS，共 20 项；
新增 RTLSIM 作业 **12731308** 只运行尚未对比的 15 项，不重复执行 occupancy、sgemm2、
basic、bfs、wsync。新增输入直接复制自各 Timing PASS 的冻结 suite，旧 suite、后续
multikernel 复测和 RTLSIM runtime 的 `libvortex.so` SHA-256 均为
`357346ea...24a77cd`；binary、kernel 和三套 RTLSIM library 均在执行前后校验哈希。
使用与 §8.2 相同的冻结单 Core配置、release+PERF RTLSIM，L2/L3 均关闭；程序没有
timeout，Slurm long_cpu 上限 14 天。

作业在 cpu1-78 获得节点后 8 秒完成全部 15 项仿真。每项 host exit 均为 0、stdout
均含 `PASSED!`、stderr 均为空、PERF cycles 均非零。14 项的 RTLSIM instructions 与
Timing retired 逐项相等；packld 的特殊计数口径见表后说明。完整 20 项结果如下，误差
定义为 `(Timing / RTLSIM - 1) * 100%`：

| benchmark | Timing cycles | RTLSIM cycles | 差值 | 误差 |
|---|---:|---:|---:|---:|
| occupancy | 1,296,227 | 1,295,944 | 283 | 0.022% |
| mstress | 31,702 | 31,422 | 280 | 0.891% |
| sgemm2 | 402,402 | 382,156 | 20,246 | 5.298% |
| sgemm | 121,052 | 114,909 | 6,143 | 5.346% |
| dotproduct | 104,514 | 98,451 | 6,063 | 6.158% |
| dotproduct2 | 79,051 | 72,739 | 6,312 | 8.678% |
| basic | 15,509 | 13,983 | 1,526 | 10.913% |
| demo | 7,436 | 6,503 | 933 | 14.347% |
| sgemv | 20,625 | 17,337 | 3,288 | 18.965% |
| io_addr | 27,259 | 22,607 | 4,652 | 20.578% |
| sgemmx | 92,586 | 76,738 | 15,848 | 20.652% |
| wsync | 495,073 | 409,292 | 85,781 | 20.958% |
| multikernel | 208,113 | 166,489 | 41,624 | 25.001% |
| packld | 3,893 | 3,088 | 805 | 26.069%* |
| dropout | 47,010 | 37,003 | 10,007 | 27.044% |
| pathfinder | 56,017 | 40,674 | 15,343 | 37.722% |
| bfs | 31,455 | 21,995 | 9,460 | 43.010% |
| wgather | 1,792 | 1,247 | 545 | 43.705% |
| relu | 31,918 | 22,065 | 9,853 | 44.654% |
| vecadd | 28,401 | 17,532 | 10,869 | 61.995% |

`packld` 的星号不是仿真不完整：同一 binary/kernel 在两侧均 host PASS，RTLSIM 完整
执行到 3088 cycles。通用门禁发现 RTLSIM PERF instructions=860，而 Timing kernel
事件 retired=604。差值恰为 256：4 个 Warp × 16 个 point × 每 point 额外 4 个 packed
uop；PACKLB/PACKLH 每个 point 从两个宏指令展开为 4+2 个 uop。RTLSIM scheduler PERF
按提交 packed uop 计数，而 Timing 的 kernel event 使用 effect Reap 后的宏指令计数；
Timing 内部 hardware `Instret` 本身也注明包含 packed uop。因此这是跨层计数口径差异，
不是少执行了 packed 访存，周期对比仍保留；但作业包装器因刻意设置的通用“指令必须
相等”硬门禁返回 exit 1，不能把该 Slurm FAILED 误写成 RTLSIM/host 失败。

### 9.1 准确率评估

20 项全部为正误差，即当前 Timing 模型系统性地比 RTLSIM 保守/偏慢，没有正负误差
互相抵消。按 benchmark 等权计算，MAPE 为 **22.10%**，中位绝对百分比误差为
**20.61%**，几何平均 Timing/RTLSIM 比为 **1.2104**。如果必须把 `1-MAPE` 表述成单一
“平均准确率”，对应 **77.90%**；但该数字会掩盖 workload 分布，推荐直接使用 MAPE
和逐项表。

按全部 execution cycles 加权，Timing 合计 3,102,035 cycles、RTLSIM 合计
2,852,174 cycles，Timing 高 **8.76%**。这个指标看起来明显更好，主要因为 occupancy
单项占 RTLSIM 总周期约 45%、且误差仅 0.022%，不能代表短 benchmark 的普遍精度。
误差阈值分布为：2/20 在 5% 内，6/20 在 10% 内，9/20 在 20% 内，15/20 在 30% 内。

按 RTLSIM 规模分组也显示清楚的摊销效应：小于 25k cycles 的 9 项平均误差 31.58%，
25k–100k 的 6 项为 16.86%，至少 100k 的 5 项为 11.33%。occupancy、mstress 和矩阵/
dotproduct 长路径较接近，说明已建模的长期流水、LMEM 或重复计算资源关系可以逼近 RTL；
vecadd、relu、wgather 等短而访存敏感的单 launch 仍偏高，说明问题不只是 launch 固定开销，
还可能包括固定 100-cycle backend 与 RTLSIM Ramulator 响应差异、LSU/cache backpressure
偏保守。BFS/pathfinder 的 10/31 次短 launch 又叠加了每次 frontend/cache/control 生命周期
成本；wsync 对 control-drain 释放边界敏感。这些是由 workload 分布支持的定位方向，不能
仅凭端到端周期断言某一单组件有错。

总体判断：模型已达到“功能闭合且长 workload 周期量级可信”，但尚不能称为普遍 RTL
周期等价；当前代表性等权误差约 22%，并存在最高 62% 的 memory/control 离群项。下一轮
精度收敛应优先为 vecadd/relu/wgather、BFS/pathfinder 分解每 launch 的 fetch miss、D-cache
hit/miss、backend wait、LSU pending、control drain 和 flush 计数，再分别判断是 backend
边界差异还是内部仲裁/释放过度保守，不应以统一缩放系数修正。

## 10. 双侧实际 trace 分析已独立归档

完整的 wgather/vecadd 逐事件对齐、隔离因果实验及后续全通过集小规模双侧复测，统一见
[RTLSIM 与 Timing trace 对比分析](rtlsim-timing-trace-analysis-20260914.md)。

## protocol-and-audit 里程碑收尾（2026-09-16）

本次仅处理冻结的首个里程碑；不实施 port 0 新缓冲、设备存储复用、CTA 生命周期或
宿主性能改造。以下是可离线复验的机制证据，不是原规模 benchmark 的重跑结果。

| 验收项 | 实现与回归证据 |
| --- | --- |
| AC-001 | `timing/memsys/coalescer.go` 的 SIMDResponse.Batch 保留 Slot/Generation；split 以父 Identity + Batch 比较响应片段。`fragment_identity_test.go` 的 adapter→coalescer→split 跨周期背压重选验证完整数据与一次完成，拒绝隐藏 producer 变化、重复、未发送 lane、提前完成、旧 generation/residency。 |
| AC-002 | `emu/state/effect_stream.go` 分离 activation 截止线与 PC frontier，迟到非阻塞 arrive 不回退 PC。`emu/state/async_barrier_test.go`、`timing/effects/concurrent_test.go`、`timing/runner/async_barrier_test.go` 验证年轻 ALU/branch 在更早 edge 完成，拒绝重复、旧 activation/epoch/residency 与过期阻塞控制；保留 LSU gate。 |
| AC-003 | 同一 memsys 组合反例覆盖 conv3/dogfood/fence/softmax/stencil3d 的共享失败机制；runner 的真实 load 延迟 WCTL，四 Warp 均在年轻 ALU/branch 之后恰好交付一次 arrive。不是仅同 edge 排序用例。 |
| AC-004 | `integration/vortexruntime/device.go` 在终态审计 Write/Close 后发布 idle；`audit_test.go` 覆盖成功/执行失败、open/write/short-write/close 注入、Busy 并发查询、真实 JSONL、禁用审计、flush 发布顺序。原执行失败保留，审计失败进入错误通道。 |
| AC-005 | `scripts/vortex-supported.py` 与 `scripts/test-vortex-supported.py` 校验配对、顺序、模式、描述、complete、计数、visibility、显式周期、manifest 身份与 JSON 结构；重新汇总不信任旧 PASS。不完整周期 null/unknown，功能模式无 timing 测量。 |

后续调用方须保留 global-load Batch；local/progress/store 继续使用零值。
异步控制收据仅在成功交付后记录，activation 截止线不能与 PC frontier 合并。
不增加响应缓冲、不串行化返回、不修改冻结 RTL、外部服务 100-cycle 契约或真实背压。

当前 worktree 未提供兼容 CP ABI 的原生 benchmark 二进制、raycast 资产或 RTLSIM
构建产物，本轮未执行原规模 benchmark/RTLSIM；不得用本表宣称它们已经 PASS。
历史 trace 文档两处外部 checkout 相对路径已改为来源说明，保留历史含义且不作为
当前离线依赖；环境检查规则和冻结验证命令未改。

本轮验证结果（固定 `/opt/simulator-environment`、Go 1.26.2、SoftFloat、vendor、禁网）：

- `source env/env.sh; go test -race ./integration/vortexruntime -count=1`：PASS，281.809 秒，包含本轮全部新增审计用例。
- `python3 scripts/test-vortex-supported.py`：PASS，6 项测试，包含合法模式、反向证据、execute 与 aggregate 入口。
- `bash scripts/verify-offline.sh`：PASS，空缓存、vendor only、禁网完成 go list/build/test/vet；runner 全套 616.750 秒，runtime 40.220 秒，memsys 44.333 秒，effects 145.676 秒。未调整原门禁时限或周期断言。
- `git diff --check`：PASS。

AC-001 至 AC-005 的本地实现与冻结离线验收链已闭合；原规模 benchmark/RTLSIM
外部输入限制仍如上，不将机制回归通过扩展为未执行实验的结论。
