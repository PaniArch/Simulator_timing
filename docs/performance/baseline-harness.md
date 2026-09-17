# 宿主性能基线入口（baseline-harness 交接）

closure 的正式重复测量、CPU/alloc profile 与冻结验证结果见
[baseline-evidence.md](baseline-evidence.md)。下文“待下一 Worker/未验证”保留为
本入口交接时的状态，不代表 closure 的最终结果。

本 Worker 仅新增测试与测量入口，不改变生产执行路径。不执行引擎复用、状态构造优化、原规模 benchmark、RTLSIM 或外部资产测试；profile 采集和里程碑完整验收由串行的 baseline-evidence-closure Worker 完成。

## 固定场景及边界

- `timing/model/performance_baseline_test.go`：`BenchmarkClockBaseline`。period=1ps；execute 每 op 恰好 1024 个成功边沿，同一 Clock 跨 op 持续运行；init 每 op 仅 NewClock，结果保留到包级 sink。single=1，fixed=32，irregular 循环 1/7/3/29，最后预算裁剪至 1024。init 的粒度标签不影响构造。
- `timing/runner/performance_baseline_test.go`：`BenchmarkRunnerBaseline`。4096 字节 RAM，程序在 0x100；数据在 0x800+w*16，初值 10+w。Multi 四个 warp、各四 lane，分别执行 LW/ADDI(+7)/SW(+4)/TMC，共 16 条宏退休。Kernel 一个四 lane CTA，先从 CSR 读取 parameter address=0x800，再执行上述程序，共 5 条宏退休。同 warp 各 lane 写相同值；没有数据竞争造成的不确定值。STD backend、period=1ps、外存 latency=11、accept/return=1、max inflight=16、Ready(c)=c%5!=0，其余使用仓库 IR cache 配置。
- runner init 每 op 构造 RAM、程序、owner、完整 runner；execute 每 op 在 StopTimer 中构造同一新实例，然后 StartTimer 执行至完成，报告实际 `edges/op`。每次 Run 预算为 1、32 或循环 1/7/3/29。调用边界的完成查询也计时（Kernel 使用 Status），并设 10000 次调用的失败上限。不是 warmed-cache 无限循环；包含模拟 cache 初始化边沿，不包含宿主 fixture 构造、不包含事后 flush。init 标签中的粒度不影响构造。
- diag=false 为 TraceMemory=false 且 nil observer；diag=true 为 TraceMemory=true 且消费 Events/Finished 长度的 observer，不序列化或持久化日志。Kernel 自身退休/barrier observer 在两种模式下均保留。性能结果不代表 runtime JSONL 文件输出成本。
- Go `StopTimer` 排除 benchmark ns/op、B/op、allocs/op 的构造成本，但 CPU/heap profile 覆盖进程（含停表初始化、框架、GC）。分析必须区分 init 与 execute、使用调用树定位，不把整个 execute profile 的比例解释为纯执行占比。每 op 为一个完整 workload，不能把 ns/op 当 ns/cycle；Clock init op 也不是 1024 edges。

## 确定性回归

`TestClockBaselineChunkEquivalence` 比较完整 callback 周期序列，覆盖预算耗尽、提前 stop、失败边沿不计数及替换 callback 后续跑。

`TestRunnerBaselineChunkEquivalence` 对 Multi/Kernel 成功与非法指令失败场景，比较三种分块和诊断开关的执行周期、flush 后周期、硬件 Cycle/Instret、宏退休数组、四 owner 完整 WarpSnapshot（含寄存器）、全 RAM 的 flush 前后内容、KernelStatus 和完整 KernelEvent 顺序。诊断开启时逐字段比较全体有序 MultiRecord（包括 memory fragments、bindings、tokens、资源和事件）。回调当时保存 JSON，全部执行与 flush 后再次序列化历史记录以检测 detached ownership。关闭诊断时仍比较相同架构结果和 Kernel lifecycle events。独立 Kernel 在首次执行前固定纯观测 deviceID=7001；不删除任何记录中的身份字段，不改变生产分配器、launch/generation 或路由身份。

成功用例显式断言宏退休数、cache flush 前 backing 输出为零且 flush 后为 17；错误用例验证重复 Run 返回错误且不推进周期。这是小规模基线，不替代现有 lifecycle/barrier/spawn/cancel/身份隔离回归。

## 可直接复现的命令

在仓库根目录执行；只使用预装依赖，不安装或联网。产物放 Harness 临时目录，需发布时由 closure 选择可审查的摘要放入交付文档。

```bash
source env/env.sh
mkdir -p "$HARNESS_RUNTIME_DIR/performance-baseline"
go version
go env GOROOT GOTOOLCHAIN GOOS GOARCH CGO_ENABLED CC GOCACHE
git rev-parse HEAD
git diff --stat
sha256sum timing/model/performance_baseline_test.go timing/runner/performance_baseline_test.go
go test -mod=vendor -count=1 -timeout=5m ./timing/model ./timing/runner -run 'Test(Clock|Runner)Baseline'
go test -mod=vendor -run '^$' -bench '^BenchmarkClockBaseline$' -benchmem -benchtime=10x -count=3 ./timing/model
go test -mod=vendor -run '^$' -bench '^BenchmarkRunnerBaseline$' -benchmem -benchtime=1x -count=3 ./timing/runner
```

有界 profile 示例（每次只选一个子场景；可将 single 替换为 fixed/irregular、false 替换为 true、execute 替换为 init，使用不同输出名）：

```bash
go test -mod=vendor -run '^$' -bench '^BenchmarkRunnerBaseline$/kernel/single/diag=false/execute$' -benchtime=10x -benchmem -count=1 -cpuprofile "$HARNESS_RUNTIME_DIR/performance-baseline/kernel.cpu" -memprofile "$HARNESS_RUNTIME_DIR/performance-baseline/kernel.alloc" -o "$HARNESS_RUNTIME_DIR/performance-baseline/runner.test" ./timing/runner
go tool pprof -top -nodecount=40 "$HARNESS_RUNTIME_DIR/performance-baseline/runner.test" "$HARNESS_RUNTIME_DIR/performance-baseline/kernel.cpu"
go tool pprof -top -alloc_space -nodecount=40 "$HARNESS_RUNTIME_DIR/performance-baseline/runner.test" "$HARNESS_RUNTIME_DIR/performance-baseline/kernel.alloc"
go tool pprof -top -alloc_objects -nodecount=40 "$HARNESS_RUNTIME_DIR/performance-baseline/runner.test" "$HARNESS_RUNTIME_DIR/performance-baseline/kernel.alloc"
go test -mod=vendor -run '^$' -bench '^BenchmarkClockBaseline$/single/execute$' -benchtime=1000x -benchmem -count=1 -cpuprofile "$HARNESS_RUNTIME_DIR/performance-baseline/clock.cpu" -memprofile "$HARNESS_RUNTIME_DIR/performance-baseline/clock.alloc" -o "$HARNESS_RUNTIME_DIR/performance-baseline/model.test" ./timing/model
go test -mod=vendor -count=1 -timeout=20m ./timing/model ./timing/runner
```

默认 heap profile 为采样；需要准确分配调用路径时单独使用 `-memprofilerate=1`，其扰动后的耗时不要混入普通 benchmem 结果。共享宿主上的时间不设置测试阈值。profile 若样本不足，明确记录，closure 可在小规模固定输入下调整重复次数。

## 生产边界及待取证项

源码可确认 Clock.Run 创建 SerialEngine/edgeDriver，Kernel.Run 每 edge 调用 MultiRunner.Run(1) 并查询完整 Status，Status/observeCTA 与 MultiRunner.step 多处 Snapshot，step 构造 MultiRecord/Resources/Events。这些是待 profile 核验的调用路径，不是已测热点排名。第二次 Core.Evaluate 将 hierarchy.step 提供的 FetchAccepted/MemoryAccepted 反馈到同一旧态的提交计划，不能仅因调用两次而删除。Kernel 内部观察器参与 advanceRetirement/releaseBarriers，不能等同于可关闭用户诊断。

未提供专名 Construction Skill、Architecture Contract 或 Boundary 引用；仓库 `.agents`/`.codex` 没有相关文件。参照 `emu/docs/architecture.md`、`timing/multiwarp-contract.md`、`timing/memory-contract.md`、`timing/rules.md` 的 owner、共同旧边沿/提交、detached 观测、身份和 flush 边界。冻结接口、计数单位与已存在的生命周期行为不变。

## 验证状态

待本 Worker 运行完成后记录于下方。CPU/alloc profile、正式多次性能采样及冻结完整验证由 closure Worker 执行；目前均未验证，不声称任何性能收益。原规模 benchmark、RTLSIM、外部资产测试按范围明确不执行。

### 本 Worker 已执行结果（2026-09-17）

生产基线 HEAD：`1f146ba13191f5ffd95bf7f6bec12e272472d3d4`，新增文件尚未提交（按任务禁止提交）。Go 1.26.2 linux/amd64，预装 SoftFloat/cgo、vendor，Intel Xeon Gold 6348H @ 2.30GHz，benchmark 后缀报告 GOMAXPROCS=96。

- 最终 `go test -mod=vendor -count=1 -timeout=5m ./timing/model ./timing/runner -run 'Test(Clock|Runner)Baseline'`：退出 0；model 0.038s，runner 167.282s。包含硬件计数和历史记录检查。
- `go test -mod=vendor -run '^$' -bench 'Baseline' -benchmem -benchtime=1x -count=1 ./timing/model ./timing/runner`：退出 0；6 个 Clock 和 24 个 runner 子项全部执行。Clock execute 全为 1024 edges/op，Multi 全为 155，Kernel 全为 160；所有三种分块和两种诊断模式工作量一致。runner 包总计 165.012s。
- 新增文件 `gofmt` 已执行；`git diff --no-index --check /dev/null <新增文件>` 均无空白诊断（新增文件的 no-index diff 返回 1 表示存在差异）。生产文件未修改。

以下仅保留入口 smoke 的 single/diag=false 样本，**每项只有一次且与定向回归并行运行，不用于性能结论或前后收益比较**。其用途是证明计时/分配输出可获得；正式重复测量由 closure 独立运行。

| 场景 | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Clock init | 23978 | 16 | 1 |
| Clock execute（1024 edges） | 2330204 | 696336 | 9217 |
| Multi init | 6311579362 | 751417592 | 13921326 |
| Multi execute（155 edges） | 636212710 | 91069928 | 1220979 |
| Kernel init | 6595864903 | 751331784 | 13921185 |
| Kernel execute（160 edges） | 280431742 | 45822792 | 455159 |

构造样本成本较高，后续 profile 建议先用所列单子场景 10x 的有界入口，不直接运行大矩阵长时间校准。构造分配具体来自何处尚无 profile 证据，不作归因。

待下一 Worker：上述 CPU/alloc profile 命令及 pprof 调用树分析、多次隔离性能采样、冻结命令 `go test -mod=vendor -count=1 -timeout=20m ./timing/model ./timing/runner` 均**未验证**（按串行职责交由 closure 执行，并非本轮遇到环境阻塞）。本轮已运行的检查没有不可执行项；如后续环境不支持某项，需逐项记录命令、原因及未验证。不得以此入口 smoke 代替 AC-002/AC-004/AC-005 的完整闭合。
