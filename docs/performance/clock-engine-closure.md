# 持久 Clock 引擎验收（persistent-clock-engine）

本页闭合 AC-006 至 AC-010，范围仅为 Clock 引擎复用及集成验证。
不实施后续 YAML、Status、Resources、诊断或 Evaluate 优化，不设置 benchmark 收尾阶段。
原 `baseline-evidence.md`、`evidence/` 和两个 `performance_baseline_test.go` 均保持不变。

## 实现复核与契约

生产实现来自上一 Worker，见 [实现交接](clock-engine-reuse-handoff.md)。
本 Worker 未再修改生产代码。每个 NewClock 创建一个私有 SerialEngine，注册嵌入的
edgeDriver；Run 仅更换预算和 callback，每边沿仍安排一个事件。成功停止计数，
失败边沿不计数；Akita 丢弃 handler error，因此 driver.err 显式传播原错误。
引擎在 handler 前弹出事件，仅成功且非终止时安排后继，返回时队列为空；defer
释放 callback、预算和错误。nil callback、零预算及完整预算的溢出预检顺序不变。
Clock 不可复制、并发或重入；独立 Clock 不共享任何可变驱动状态。

NextLaunch 按既有所有权协议转移同一 Clock 与排空的 memsys，封闭旧 Kernel 推进权限。
引擎时间随 Clock 连续转移，而上一次 callback 已释放；独立设备从新 Clock 开始。
Flush 不重建 Clock。失败后仅当 memsys 已提交该边沿时，用一个空 callback 推进
一次 Clock；未提交的边沿不能跳过。普通 Run 不自动重放失败边沿的架构效果。
MakeVisible 与 FlushCaches 使用各自 callback，继续同一时间轴和原硬件空闲计数规则。
没有改变唯一 owner、设备/launch/epoch/CTA generation、旧传输尾部、CSR/MPM 单位、
两次 Evaluate 的握手反馈、退休/barrier observer 或审计完成后发布结果的契约。

未提供专名 Construction Skill/Boundary，仓库查找也未发现对应文件；使用用户的
Worker 边界及 `emu/docs/architecture.md`、`timing/architecture.md`、`timing/rules.md`、
`timing/multiwarp-contract.md`、`timing/memory-contract.md`。不引入额外契约。

## 验收映射

| 条件 | 证据 |
| --- | --- |
| AC-006 引擎生命周期与隔离 | `TestClockPersistentEngineIsolation/Boundaries` 检查引擎身份、独立嵌套执行、时间隔离；`TestDeviceMemorySuccessor` 检查转移、旧 owner 拒绝推进和独立设备冷启动；生命周期组合回归检查 launch/CTA/warp 身份及旧尾部。 |
| AC-007 预算、错误和溢出 | `TestClockPersistentEngineBoundaries/Overflow` 覆盖 nil、零预算、耗尽、stop、stop+error、替换 callback、错误后续跑、周期/时间上界；`TestClockBaselineChunkEquivalence` 保留三种分块的边沿顺序。 |
| AC-008 无残留事件/callback/error | `assertClockQuiescent` 直接再运行私有引擎，验证空队列及清空字段；显式 sentinel error 原样返回；源码复核弹出事件和后继安排顺序。 |
| AC-009 集成等价 | 未改动的 `TestRunnerBaselineChunkEquivalence` 比较 Multi/Kernel、诊断开关、正常/错误、三种分块的完整记录、寄存器、RAM、计数和 FlushCaches；新增组合回归及加强失败边沿断言见下。既有 accepted-store、cancel/restart、跨 launch、barrier、硬件计数回归由冻结包测试覆盖。 |
| AC-010 分配测量 | `clock-engine-evidence/` 保存 benchmem、CPU/alloc 原始 profile、pprof 报告、精确分配路径和源码 SHA；执行路径没有引擎构造，逐周期分配下降，初始化成本单列。 |

新增 `timing/runner/clock_integration_test.go::TestPersistentClockEpochVisibilityChunks`
在同一 Clock 上执行到第 5 边沿、epoch Flush、执行完成、MakeVisible、FlushCaches；
比较 single=1、fixed=32、irregular=1/7/3/29。各阶段结束周期均为 156/306/538，
硬件计数均为 Cycle=149/Instret=16，宏退休各 warp=4，RAM 输出为 17。
检查每个阶段 memsys.NextCycle 与 Clock 相等、Clock 指针不变、零预算不推进、
已完成 MakeVisible 不重复服务，并比较完整架构快照和诊断事件顺序。
`runner_test.go::TestFlushAfterExecutionFaultAdvancesPastFailedEdge` 现在还验证错误后
再次 Run 拒绝且周期不变、Flush 恰好推进一个已由内存提交的失败边沿。
既有 `TestMemoryFaultResetAlignsCommittedEdges` 分别检查 response-before-step 与
effects-after-step，要求 Flush 后 Clock 恰好等于 System.NextCycle、架构 owner
不变；还覆盖中途放弃 MakeVisible 后旧控制回复排空及 refill 只服务两次。

**诊断列表限制**：新 epoch 回归首次严格比较时发现 Services 的旧/新 epoch 同 ID
条目顺序不同，其余周期、状态和计数相同。未改动的 `runnerMemory.services()`
从 map 收集，只按 ID/Uop/Resource 排序，没有 epoch 决胜字段。因此新回归仅对
Services 当前服务清单的复制值按完整 JSON 值排序，比较所有身份和字段；Events、
Memory.Transfers、Resources 等所有其他序列仍严格保持顺序。原基线完整记录比较
未放宽。未修复这项原有诊断清单排序问题，也不宣称它获得跨 epoch 稳定顺序保证。
这不是执行事件顺序变化；本里程碑禁止无关诊断改造。

## 固定测量与结果

基于 HEAD `d416aead9294874d9c4526790e9e09eebc82f28d`，本 Worker 仅有测试/文档增量，
生产 Clock 与该 HEAD 一致。环境 Go 1.26.2、linux/amd64、vendor、预装 SoftFloat，
Xeon Gold 6348H，benchmark GOMAXPROCS=96。环境及源文件 SHA 另存。

复用原入口与固定输入：Clock 每 execute op 为 1024 边沿；runner Multi 为 155，
Kernel 为 160 边沿。runner 配置和计时边界见 [入口说明](baseline-harness.md)。
Clock 六子项各 1000x、三次；runner 仅 single/diag=false 的 init/execute，Multi
和 Kernel 各 1x、三次。以下为三次中位数；原基线数据来自冻结 `evidence/`。

| 场景 | 原 ns/op → 本次 ns/op | 原 B/op → 本次 B/op | 原 allocs/op → 本次 allocs/op |
| --- | ---: | ---: | ---: |
| Clock single init | 99.56 → 1222 | 16 → 624 | 1 → 7 |
| Clock single execute | 2185725 → 409567 | 696331 → 49152 | 9216 → 1024 |
| Clock fixed execute | 395729 → 322755 | 69376 → 49152 | 1280 → 1024 |
| Clock irregular execute | 529605 → 331689 | 114880 → 49152 | 1856 → 1024 |
| Multi single init | 6465733721 → 6411968446 | 751420088 → 751447032 | 13921177 → 13921200 |
| Multi single execute | 641147641 → 643566994 | 91124400 → 91024664 | 1220987 → 1219732 |
| Kernel single init | 6615995582 → 6907431568 | 751413688 → 751494248 | 13921200 → 13921216 |
| Kernel single execute | 255709949 → 257171507 | 45808944 → 45722328 | 455157 → 453886 |

Clock single execute 分配次数减少 88.9%，字节减少约 92.9%；构造移至 NewClock，
因此 init 增加，不应宣称初始化也改善。剩余每边沿事件仍分配一次。runner 的
主导构造及执行开销仍在；本次数据不支持端到端显著加速结论。

**测量限制**：共享宿主，且本次测量与冻结回归有执行重叠，ns/op 仅为原始观察，
不能用前后小差值推断速度收益。普通 benchmem 与 memprofilerate=1 的扰动耗时分开。
CPU profile 仅约 450ms 样本，主要用于核对调用路径，不作细粒度热点排名。
原 baseline 的长 profile 不覆盖本次 runner，未重采 runner CPU/alloc profile；
本次只重采 Clock 以验证当前里程碑目标。

精确 Clock profile（10x，加 Go benchmark 的一次 warmup，共 11264 边沿）显示
Clock.Run 累计 11267 个分配：11264 个事件、两个新 Clock 的首次 queue backing
分配和一次进程 ID generator 初始化；路径内无 NewSerialEngine。完整报告的
NewSerialEngine 累计 10 个对象来自两次 NewClock（每次五个内部对象），不是
11264 次 Run 调用。源码及引擎指针回归共同确认构造次数随 Clock 数量增长，
不随 Run(1) 次数增长。该 profile 使用 rate=1，不能与普通计时结果混合。

## 复现命令

在仓库根目录执行，输出目录应为新的目录，避免覆盖冻结证据。只用预装依赖，
不联网。测试二进制及缓存放 Harness runtime，交付仅保留 profile/文本。

```bash
source env/env.sh
out="$HARNESS_RUNTIME_DIR/clock-engine-recheck"
mkdir -p "$out"
go test -mod=vendor -run '^$' -bench '^BenchmarkClockBaseline$' -benchtime=1000x -benchmem -count=3 ./timing/model > "$out/clock-bench.txt"
go test -mod=vendor -run '^$' -bench '^BenchmarkRunnerBaseline$/(multi|kernel)/single/diag=false/(init|execute)$' -benchtime=1x -benchmem -count=3 -timeout=10m ./timing/runner > "$out/runner-bench.txt"
go test -mod=vendor -run '^$' -bench '^BenchmarkClockBaseline$/single/execute$' -benchtime=1000x -benchmem -count=1 -cpuprofile "$out/clock.cpu" -memprofile "$out/clock.alloc" -o "$out/clock.test" ./timing/model
# 精确对象路径；耗时仅作诊断，不用于速度比较。
go test -mod=vendor -run '^$' -bench '^BenchmarkClockBaseline$/single/execute$' -benchtime=10x -benchmem -count=1 -memprofilerate=1 -memprofile "$out/clock-exact.alloc" -o "$out/clock.test" ./timing/model
go tool pprof -top -cum -nodecount=0 -nodefraction=0 -alloc_objects "$out/clock.test" "$out/clock-exact.alloc"
go tool pprof -top -cum -nodecount=0 -nodefraction=0 -alloc_objects -focus='model\.\(\*Clock\)\.Run' "$out/clock.test" "$out/clock-exact.alloc"
go tool pprof -top -cum "$out/clock.test" "$out/clock.cpu"
go test -mod=vendor -count=1 -timeout=20m ./timing/model ./timing/runner ./timing/memsys
gofmt -l timing/model timing/runner
```

## 验证清单与后续约束

最终命令状态以 `clock-engine-evidence/validation*.txt` 等文件为依据。
冻结包测试运行时新增回归尚在编写；新增/加强测试另以最终源码定向执行。
生产源码在所有验证期间不变。

- 冻结 `go test -mod=vendor -count=1 -timeout=20m ./timing/model ./timing/runner ./timing/memsys`：退出 0；model 83.160s、runner 1021.466s、memsys 67.603s。
- 新增 epoch/visibility 组合回归：退出 0，29.267s。
- 加强失败边沿回归：退出 0，11.039s。
- 最终 `gofmt -l timing/model timing/runner`：退出 0，stdout 为空；`git diff --check` 退出 0。
- 冻结基线证据 `sha256sum -c SHA256SUMS`：69 项全部通过；原基线源码未修改。

本环境未遇到必须安装依赖或外部资产的阻塞。原规模 benchmark、RTLSIM、外部资产
测试、全仓其他包和外部设备的周期等价均未执行，标为**未验证**，不能从本包回归
推导通过。lscpu 拓扑探测未执行，环境只记录 benchmark 的 CPU 字符串与工具链。
后续阶段仍须按自身冻结范围验证，不将 Clock 分配收益外推为整个 runtime 加速。
原 Services 清单跨 epoch 的排序限制保留为已知问题；未来若单独处理，必须维持
完整身份与 detached ownership，不能删除记录以绕过差异。
