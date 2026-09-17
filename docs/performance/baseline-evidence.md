# 宿主执行性能基线：证据与验收

本页闭合 `performance-baseline`，仅增加自包含测量、回归和证据；没有引擎复用或状态构造优化。固定输入和逐 op 边界见 [baseline-harness.md](baseline-harness.md)，生产路径仍为 Kernel → MultiRunner → scheduled Core/memsys → Akita Clock。性能数字不是测试阈值，也不是优化后的收益。

## 环境、版本与复现

采样基线 HEAD：`a9c10b78f408ac75a4ce300187e49ff3a283dea7`。本 Worker 只新增脚本/文档，两个测量源码的 SHA-256 为：

```
aa01f9b17cb1eac4f442e9b41714c2d0daf836da41c10140e0fd24cad5050796  timing/model/performance_baseline_test.go
f789ded14231b53b153f342c3b163a10a322728f3de71f19b14b45a2596b36fd  timing/runner/performance_baseline_test.go
```

2026-09-17，Go 1.26.2 linux/amd64，GOROOT=/opt/simulator-environment/go1.26.2，GOTOOLCHAIN=local，CGO_ENABLED=1，CC=/usr/bin/gcc，预装 SoftFloat archive，vendor；Linux 5.4.0-155-generic x86_64。Go benchmark 检出 Intel Xeon Gold 6348H @ 2.30GHz，默认 GOMAXPROCS=96。未设置 CPU affinity，共享宿主存在调度/GC 噪声。缓存及临时二进制均在 HARNESS_RUNTIME_DIR。没有下载依赖。

仓库根目录复现（这些均是小规模固定程序，不运行原规模 benchmark）：

```bash
source env/env.sh
mkdir -p "$HARNESS_RUNTIME_DIR/performance-closure"
go test -mod=vendor -run '^$' -bench '^BenchmarkClockBaseline$' -benchmem -benchtime=1000x -count=3 ./timing/model
go test -mod=vendor -run '^$' -bench '^BenchmarkRunnerBaseline$' -benchmem -benchtime=1x -count=3 ./timing/runner
bash scripts/profile-performance-baseline.sh "$HARNESS_RUNTIME_DIR/performance-closure"
go test -mod=vendor -count=1 -timeout=20m ./timing/model ./timing/runner
```

普通 benchmem、profile 与完整测试顺序执行，没有让本 Worker 的测试与测量争用资源。Clock 每 execute op 为 1024 个边沿；Multi 为 155，Kernel 为 160；所有三种调用粒度和诊断开关具有同一工作量。runner 每 op 是新实例的有限执行，fixture 在 execute 停表阶段构造；不是无限稳态循环。init 是独立的完整宿主构造计时，包含 RAM、owner、IR 读取和模型构造。execute 包含完成查询和模拟 cache 初始化边沿，不含事后 cache flush。回归另验证 flush。

profile 脚本固定 Clock 1000x、Multi/Kernel 的 single/diag=false、true 各 5x；Go benchmark 校准的 1x 也进入 profile。CPU profile 是约 100Hz 采样；heap 为 Go 默认抽样（MemProfileRate=524288），`alloc_space`/`alloc_objects` 是累计分配估计而非 live heap。普通 benchmem 的 B/op/allocs/op 是计时区间的 runtime 计数，与 heap 抽样比例不能混用。

**StopTimer 不停止 profiler。** 全进程 profile 包含初始化、框架和 GC；`*.execute.txt` 以 `baselineFixture.drive`（Clock 以 Clock.Run）筛选调用栈并重新计算百分比，只回答该路径内样本的分布。异步 GC worker 栈不能归入 drive，因此该筛选不等于执行的全部宿主成本。cum 是含子调用占比，嵌套行不能相加。

profile 文件后缀 `.cpu`/`.alloc` 是可直接读取的 pprof protobuf；文本报告保留总样本和完整函数名，二进制不作为交付内容。需要源码逐行报告时在同一源码下重新编译：

```bash
source env/env.sh
go test -mod=vendor -c -o "$HARNESS_RUNTIME_DIR/performance-closure/runner.test" ./timing/runner
go tool pprof -top -cum -alloc_space "$HARNESS_RUNTIME_DIR/performance-closure/runner.test" docs/performance/evidence/kernel-false.alloc
go tool pprof -list='MultiRunner.*step' "$HARNESS_RUNTIME_DIR/performance-closure/runner.test" docs/performance/evidence/kernel-false.cpu
```

## 语义复核与后续不可改变的边界

`TestClockBaselineChunkEquivalence` 比较完整 callback 序列，覆盖预算、提前停止、失败边沿不计数，以及新 callback 替换旧 callback 后续跑。runner 回归对 Multi/Kernel、成功/非法指令错误、三种分块、诊断开关做组合比较；不是只比较最终摘要：

- `baselineResult` 比较运行和 flush 后周期、硬件 Cycle/Instret、四 Warp 宏退休、完整 owner 快照、flush 前后全部 4096 字节 RAM、KernelStatus、完整有序 KernelEvent 和错误字符串。
- 诊断开启时，逐字段比较全部有序 MultiRecord（内存片段、token、资源、绑定和事件均保留）；回调时序列化保存，再在运行/flush 后重新比较，检测历史记录被后续修改。关闭诊断时仍比较上述架构结果及 Kernel lifecycle events。
- 独立 Kernel 在首次执行前固定纯观察 DeviceID=7001；launch/generation/路由身份不删除或归零。生产 identity allocator 不变。
- 成功断言宏退休 Multi=16、Kernel=5，以及 backing 0x804 在 flush 前为 0、后为 17；错误后再次 Run 必须返回错误且周期不前进。

这组基线不宣称穷举生命周期。冻结包回归还包含 NextLaunch 转移/旧 Kernel 禁止推进、dirty/LMEM 隔离、取消传输尾部、增量驻留、barrier、身份与硬件计数测试。引擎复用必须继续保留它们。

源码语义：`multi.go` 第一次 Core.Evaluate 以 FetchReady/MemoryReady=false 产生请求，hierarchy.step 根据实际存储边界得到 FetchAccepted/MemoryAccepted，第二次 Evaluate 在同一旧态上形成可提交 proposal。它不是两次提交；不能按调用次数直接删除第二次。Kernel 内部 observer 执行 advanceRetirement/releaseBarriers，即使用户 observer=nil 也必须保留控制语义。对公开 Snapshot/Status/trace 的优化不能引入第二可写 owner 或使已返回切片与后续状态别名。

未收到专名 Construction Skill/Boundary 文件，仓库 `.agents`、`.codex` 无对应文件；依据 `emu/docs/architecture.md`、`timing/architecture.md`、`timing/rules.md`、`timing/multiwarp-contract.md`、`timing/memory-contract.md` 审查 owner、旧边沿提交、身份、计数和可见性边界。没有读取历史聊天/执行轨迹。

## 普通 benchmem 重复测量

下表为三次独立子 benchmark 的各列中位数；ns/op 范围保留共享宿主波动。完整三次样本见 [Clock](evidence/clock-bench.txt)、[runner](evidence/runner-bench.txt)。每个 init op 的粒度标签不影响构造；重复列出用于检查噪声，不把它解释为分块收益。

| 场景 | ns/op 中位数 | ns/op 范围 | B/op 中位数 | allocs/op 中位数 |
| --- | ---: | ---: | ---: | ---: |
| Clock/single/init | 99.56 | 98.35–111.80 | 16 | 1 |
| Clock/single/execute | 2,185,725.00 | 2,155,241.00–2,202,104.00 | 696,331 | 9,216 |
| Clock/fixed/init | 118.00 | 116.20–120.90 | 16 | 1 |
| Clock/fixed/execute | 395,729.00 | 391,953.00–398,908.00 | 69,376 | 1,280 |
| Clock/irregular/init | 110.40 | 97.99–119.60 | 16 | 1 |
| Clock/irregular/execute | 529,605.00 | 529,118.00–530,317.00 | 114,880 | 1,856 |
| multi/single/diag=false/init | 6,465,733,721.00 | 6,442,328,466.00–6,530,568,120.00 | 751,420,088 | 13,921,177 |
| multi/single/diag=false/execute | 641,147,641.00 | 636,691,799.00–655,226,863.00 | 91,124,400 | 1,220,987 |
| multi/single/diag=true/init | 6,635,192,552.00 | 6,616,953,074.00–6,655,320,410.00 | 751,408,368 | 13,921,187 |
| multi/single/diag=true/execute | 634,860,369.00 | 631,568,115.00–643,795,525.00 | 91,154,712 | 1,221,031 |
| multi/fixed/diag=false/init | 6,682,203,374.00 | 6,492,729,139.00–6,683,605,443.00 | 751,447,080 | 13,921,176 |
| multi/fixed/diag=false/execute | 628,983,515.00 | 618,766,453.00–632,101,642.00 | 90,998,656 | 1,219,635 |
| multi/fixed/diag=true/init | 6,567,976,832.00 | 6,552,326,700.00–6,628,215,297.00 | 751,405,376 | 13,921,163 |
| multi/fixed/diag=true/execute | 632,230,769.00 | 628,300,578.00–638,141,056.00 | 91,028,336 | 1,219,681 |
| multi/irregular/diag=false/init | 6,561,370,832.00 | 6,492,239,015.00–6,714,268,616.00 | 751,422,360 | 13,921,183 |
| multi/irregular/diag=false/execute | 656,083,068.00 | 632,297,468.00–684,949,280.00 | 91,019,472 | 1,219,732 |
| multi/irregular/diag=true/init | 6,509,901,307.00 | 6,494,256,018.00–6,643,424,342.00 | 751,474,536 | 13,921,195 |
| multi/irregular/diag=true/execute | 651,767,216.00 | 611,313,046.00–653,148,405.00 | 91,076,448 | 1,219,777 |
| kernel/single/diag=false/init | 6,615,995,582.00 | 6,573,007,123.00–6,636,990,524.00 | 751,413,688 | 13,921,200 |
| kernel/single/diag=false/execute | 255,709,949.00 | 232,521,152.00–256,257,482.00 | 45,808,944 | 455,157 |
| kernel/single/diag=true/init | 6,540,126,568.00 | 6,483,292,035.00–6,608,506,211.00 | 751,425,728 | 13,921,191 |
| kernel/single/diag=true/execute | 265,169,493.00 | 262,061,108.00–271,468,475.00 | 45,840,584 | 455,173 |
| kernel/fixed/diag=false/init | 6,718,227,045.00 | 6,475,612,207.00–6,801,972,127.00 | 751,537,088 | 13,921,228 |
| kernel/fixed/diag=false/execute | 254,049,312.00 | 222,704,457.00–281,736,388.00 | 45,736,120 | 454,762 |
| kernel/fixed/diag=true/init | 6,787,928,659.00 | 6,607,516,699.00–6,820,379,763.00 | 751,568,280 | 13,921,235 |
| kernel/fixed/diag=true/execute | 262,062,195.00 | 256,731,708.00–263,456,349.00 | 45,753,864 | 454,775 |
| kernel/irregular/diag=false/init | 6,592,836,626.00 | 6,540,514,139.00–6,818,628,468.00 | 751,520,872 | 13,921,205 |
| kernel/irregular/diag=false/execute | 263,074,571.00 | 258,098,057.00–287,377,199.00 | 45,740,296 | 454,786 |
| kernel/irregular/diag=true/init | 6,585,591,861.00 | 6,562,916,013.00–6,651,096,724.00 | 751,469,944 | 13,921,229 |
| kernel/irregular/diag=true/execute | 264,061,581.00 | 261,133,812.00–265,128,725.00 | 45,717,512 | 454,795 |

## Profile 证据与解释

Clock single/execute 的筛选样本为 2.07 CPU 秒，累计分配估计 672.60 MiB。`NewSerialEngine` CPU cum=44.44%、alloc_space cum=39.55%；`RegisterHandler` CPU cum=14.98%、alloc_space cum=40.97%。`edgeDriver.schedule` 为 CPU 26.09%、alloc_space 13.01%，其中包括事件和队列分配。这给出明确的引擎构造/注册证据，而不是仅按源码调用频率推断热点。NewClock 的独立 init 只有 16 B/op、1 alloc/op，不能把 NewClock 与 Run 内部引擎创建混为一谈。

普通 Clock 样本按 1024 个边沿比较，single/fixed/irregular 分别为 9216/1280/1856 allocs/op；调用 Run 次数分别为 1024/32/104。差额与每次 Run 额外 8 次分配一致。这是对测量差额的解释，不是已经实现引擎复用，也不预报完整 runner 的收益。

runner 的下表百分比全部以 `drive` 筛选后的执行栈为分母，来自未截断 `*.detail.txt`；不是整个进程占比。同类 runner 的 off/on 输入完全相同，诊断开关仅改变用户 observer/TraceMemory；Multi 与 Kernel 仍分别使用前述各自的固定程序。

| 路径（cum） | Multi off CPU / space / objects | Multi on CPU / space / objects | Kernel off CPU / space / objects | Kernel on CPU / space / objects |
| --- | --- | --- | --- | --- |
| timing.Number | 86.80% / 65.93% / 88.78% | 89.02% / 67.60% / 92.88% | 73.08% / 42.19% / 79.67% | 71.83% / 43.40% / 77.49% |
| Core.Evaluate（两次合计） | 6.45% / 16.71% / 7.71% | 6.94% / 15.95% / 5.21% | 15.38% / 31.41% / 14.69% | 19.01% / 29.78% / 14.78% |
| Core.Resources | 3.81% / 17.20% / 2.68% | 5.20% / 15.97% / 2.04% | 10.77% / 32.07% / 3.72% | 7.04% / 30.82% / 6.72% |
| stageEvents | 无样本 / 1.03% / 0.042% | 0.87% / 0.89% / 0.039% | 无样本 / 0.20% / 0.072% | 无样本 / 0.20% / 0.015% |

分母分别为：Multi off 3.41 CPU 秒 / 534.03 MiB / 7,435,249 objects；Multi on 3.46 秒 / 508.94 MiB / 6,815,427；Kernel off 1.30 秒 / 248.88 MiB / 2,520,738；Kernel on 1.42 秒 / 252.39 MiB / 2,949,477。分配抽样噪声明显，off/on 的小差值不构成统计显著收益；两者均有大量相同构造。

1. **反复解析配置是实测主要热点。** `Concurrent.begin → effects.New → timing.Number → number → yaml.Unmarshal`：`adapter.go` 每个新 adapter 查询 JOIN 的 OUT_REG，`number.go` 每次从 embedded document 重新解析完整 YAML。该路径在 execute 停表初始化之外仍存在，不是只发生在 NewMulti/NewKernel。后续可评估减少不变配置重复解析/构造，但本轮不改实现，也不能用硬编码参数或全局可变状态代替 IR 权威来源和错误契约。
2. **诊断资源构造有明确分配证据。** `Core.Resources` 同时被 Evaluate、记录和 idle/pending 查询调用，因此不能把其全部比例归为可关闭诊断。Kernel off 的逐行报告中，`multi.go:297` 的 ResourcesAfter 为 18.09 MiB、40ms；`:298` 的 Events append（含 stageEvents）为约 1 MiB。其余 Resources 成本分布在 CoreReport 和控制查询中。无 observer 时这些构造仍出现；精简可观察内容之前必须保留控制真正消费的字段和 detached 历史。
3. **Status 和 owner Snapshot 在本输入不是已证明的大热点。** Kernel off 的 Status/observeCTA 各有约 1 MiB cum 分配（均约执行分配的 0.40%，互有包含，不能相加），Status flat 约 0.50 MiB；没有 CPU 样本。Kernel on 的 observeCTA 约 0.50 MiB（0.20%），Status 未采到分配。WarpState.Snapshot 仅在 Multi off 采到 10ms（0.29%），四组均未采到它的 heap 分配；其实现为含寄存器数组的值拷贝，未采样不代表没有复制成本。boundaryEvents/traceBindings 在四组也未显示显著热点。不能因源码构造存在就断言高收益，较小路径精确收益仍未验证。
4. **两次 Evaluate 都有实测开销，语义不同。** Kernel off 的逐行 CPU 为第一次 120ms、第二次 80ms；对应分配 38.58/39.60 MiB（累计共 78.18 MiB）。其子树含 Resources，不能与上表资源比例相加。第二次接入 FetchAccepted/MemoryAccepted；可研究复用纯诊断或组合中间结果，不能把整个第二次调用当冗余删除。
5. **runner 内的引擎创建样本很小。** Kernel on 的 NewSerialEngine 与 RegisterHandler 各 10ms（各 0.70%）；Kernel off 的 NewSerialEngine 分配约 0.50 MiB（0.20%）。Multi 的 RegisterHandler 分配约 0.50 MiB（off 0.094%，on 0.098%），其他相关行可能未采到。Clock 微测量充分暴露每 Run 构造成本，但不能宣称它是此 runner workload 的主导瓶颈。

初始化与全进程的区别也可由 profile 复查：Kernel off 全进程共 49.51 CPU 秒、4582.64 MiB 分配，`newBaselineFixture` 为 34.60 秒（69.88%）、4317.36 MiB（94.21%）；`NewScheduledCore` 为 20.57 秒、2546.52 MiB。全进程 YAML Unmarshal 与 Number 子树含初始化及执行，不能直接用其占比描述持续执行。全进程 gcBgMarkWorker cum=9.27 秒（18.72%），它也不能按 drive 筛选自动分摊。普通 init 的约 6.6s / 751MB / 1392 万次分配，和 execute 的 155/160 边沿成本已分别列出。

## 证据文件与检查状态

[evidence/](evidence/) 包含五组 `.cpu`/`.alloc` 原始 profile、普通 benchmem 三次样本、采样期间的 benchmem 输出、全进程/执行栈 top 报告、未截断 detail 报告及 SHA256SUMS。profile 内嵌符号可无测试二进制读取；逐行报告重编译同版源码即可。`kernel-false.cpu.step.txt` 与 `kernel-false.alloc_space.step.txt` 保留两次 Evaluate 和诊断构造的行级证据。获取同类报告：

```bash
source env/env.sh
# 重建已有 profile 的所有报告，不再运行 workload（要求输出目录仍有 .test 文件）。
bash scripts/profile-performance-baseline.sh "$HARNESS_RUNTIME_DIR/performance-closure" reports-only
# 行级证据；切换 cpu/alloc_space 及对应 profile 后缀可得到另一种指标。
go tool pprof -sample_index=alloc_space -focus='runner\.\(\*baselineFixture\)\.drive' -relative_percentages -list='MultiRunner\).step' "$HARNESS_RUNTIME_DIR/performance-closure/kernel-false.test" "$HARNESS_RUNTIME_DIR/performance-closure/kernel-false.alloc"
```

普通 Clock/runner 测量退出 0，分别 9.448s / 491.938s，共 30 子项、各 3 样本。五个 profile 子命令均 PASS，采样期间结果单独保存，未混入普通测量表。最初 wrapper 在全部采样后因运行中编辑脚本造成 `name: unbound variable` 退出 1；最终脚本经 `bash -n`，新增 reports-only 模式重建五组全部报告退出 0。该故障已恢复，不以失败 wrapper 冒充成功，也没有重新采样掩盖已有数据。

环境检查 `lscpu`：**未验证**，退出 1，原因是容器缺少 `/sys/devices/system/cpu/possible`。CPU 型号采用 Go benchmark 自报，`/proc/self/status` 检出 Cpus_allowed_list=0-95、Mems_allowed_list=0-3；不声称验证了物理 socket/NUMA 拓扑。GCC=9.4.0，GOMAXPROCS/GOGC/GOMEMLIMIT 未显式设置，SoftFloat archive hash 见 environment-extra.txt。

原规模 benchmark、RTLSIM、外部资产测试及 benchmark 收尾 milestone 按本轮范围不执行，均**未验证**。race 检查、全仓测试、native runtime 外部集成及优化后的性能收益也未在本轮执行/验证；本基线不关闭既有 RTL 同时事件、首次 host DCR 时间线等外部未决项。

冻结完整验证（2026-09-17）：

```text
go test -mod=vendor -count=1 -timeout=20m ./timing/model ./timing/runner
ok  vortex.local/simulator/timing/model   83.621s
ok  vortex.local/simulator/timing/runner  1038.039s
exit 0
```

这包含本里程碑新增的 Clock/runner 确定性回归以及上述两包全部既有测试；未修改冻结命令、未跳过测试，也未以缓存结果替代。runner 在 20 分钟时限内完成。记录见 [validation.txt](evidence/validation.txt)。`bash -n scripts/profile-performance-baseline.sh`、`git diff --check`、测量源码哈希复核、profile/报告 SHA256SUMS 校验通过；`git diff --exit-code -- timing emu/state emu/core Vortex_rtl` 退出 0，生产路径、既有测试及 RTL 未修改。本 Worker 未提交 commit。

| 冻结验收项 | 本里程碑证据与结论 |
| --- | --- |
| AC-001 固定输入、工作量、调用粒度与诊断 | 两个 performance_baseline_test.go；Clock 6 子项、runner 24 子项的三次测量均成功，所有 execute 边沿数固定；程序/RAM 自包含，无外部 benchmark 资产。 |
| AC-002 可复现性能与 profile、初始化分离 | 本页环境/源码哈希/命令/三次 ns/op、B/op、allocs/op；profile 脚本、五组原始 CPU/alloc、三种分配/CPU 报告；明确 StopTimer 与全进程采样不同。性能不作测试门禁。 |
| AC-003 确定性与诊断开关 | 完整冻结回归退出 0；完整有序记录、保留身份、架构快照、全部 RAM、周期、计数、flush、错误停止和 detached 历史检查经语义复核。 |
| AC-004 热点证据与 Evaluate 反馈 | Clock 引擎构造证据、runner YAML/Resources 热点、Status/Snapshot 的低样本事实、两次 Evaluate 的行级分配/CPU；明确实际存储握手反馈及 Kernel 内部控制 observer 不可删除。 |
| AC-005 无法验证的检查 | lscpu 命令、原因及替代信息来源已明示；范围外原规模 benchmark、RTLSIM、外部资产/全仓/race 检查和优化收益均未验证，不声称通过。 |

交接：本基线里程碑完成，可供下一里程碑复用同一固定工作量做比较。引擎复用及 profile 驱动的构造优化仍未实施；后续必须保留生命周期转交、身份隔离、旧边沿握手/提交、独立计数单位、flush 可见性、错误停止和 detached ownership，不能为性能修改这些契约。
