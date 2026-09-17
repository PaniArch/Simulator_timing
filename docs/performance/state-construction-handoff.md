# 状态构造优化交接（第 1 Worker）

仅交付 `state-construction-cost` 范围，不关闭 milestone AC-011–AC-017。
后续按冻结顺序由 diagnostic-construction-cost、state-cost-integration-closure 接续。
原 performance-baseline、persistent-clock-engine 的实现、测试与原始证据未修改。
本轮基底 HEAD 为 `86deb6e84d915f3d9e0a051c82dd076f5ed868e9`，未提交 commit。

## 依据与实现边界

仓库未提供专名 Construction Skill 或 Boundary 文件；`.agents`、`.codex` 为空。
架构依据为 `emu/docs/architecture.md`、`timing/architecture.md`、
`timing/multiwarp-contract.md`、`timing/memory-contract.md` 与已有性能交接。
没有引入缺失契约或新的执行模型。

1. `timing/number.go`：既有 baseline-evidence.md 的 drive 过滤 profile 显示
   Kernel off 的 Number 占执行 CPU 73.08%、alloc space 42.19%、objects 79.67%。
   `Concurrent.begin → effects.New → Number → yaml.Unmarshal` 每条指令读取 JOIN
   注册参数，确实在持续执行阶段重新解析完整 IR。现以 `sync.OnceValues` 惰性解析
   私有 embedded document，缓存只读 YAML 节点树及解析错误。查询仍使用原字段、
   ID、整数 tag/Decode 校验；没有硬编码参数或缓存 caller path。`number(data, ...)`
   保留每次解析，修改输入、null、非整数、溢出与错误行为仍独立。
2. `timing/runner/kernel.go`、`cache_flush.go`：新增私有 `executionComplete()`，
   从现有 lifecycle owner 派生原完成谓词；Run、MakeVisible、FlushCaches、NextLaunch
   不再为了一个布尔值调用完整 Status。Status 仍公开完整、按原时点生成的快照。
   原完成门槛逐项保留：failed、retirementWrite、两级 retirePipe、walker、pending、
   非空 resident、barrierEvents、barrierReleases；转交后读 transferredStatus.Complete。
   不额外添加 dispatch 或 MemoryDrained 条件，也不把完成等同于 backing visible。
   原 `observeCTA` 回收逻辑、传输尾部与错误处理不变。Status 在既有 profile 中只占
   约 0.40% 执行分配且无 CPU 样本，因此仅做边界分离，不声称这是主要热点。
3. `timing/ir.go`：上述两项完成后的新 profile（after.cpu/after.alloc）显示
   Buffer 在全进程 alloc space 占 72.25%（1425.75 MB），属于停表 fixture 初始化；
   drive 执行栈没有该路径。缓存私有 typed bufferDocument 的解析结果，仍从 IR
   解析所有值/encoding，不建立影子默认值表。每次返回的 BufferSpec 只有标量；
   `buffer(data, id)` 继续独立解析，沿用全部校验与错误文本。此项只归为初始化优化。

所有共享数据只来自不可由公共 API 修改的 embedded IR，私有指针/map/节点不返回
调用者。不缓存 Options、MemoryConfig、WarpState、资源实例或 mutable component；
不改变可变配置输入或多设备隔离。增加一个进程生命周期的只读解析树：中间 profile
的 inuse_space 对 Number 路径采样约 1.50 MB（采样估计，非精确常驻大小）。Buffer
额外长期保留小型 typed 参数表。成本是有界配置常驻空间换取重复解析分配的减少。

owner Snapshot 在基线不是已证明的大热点，未改其完整值拷贝。两次 Evaluate、
旧态采样、FetchAccepted/MemoryAccepted 反馈、统一 CommitEdge、部分推进的错误
边沿均未改。Kernel 内部 observer 仍承担退休/barrier 控制；外部诊断与内部控制的
进一步分离留给下一 Worker。没有给事件排序或扩大 Services 清单规范化范围。

## 固定工作量与测量

所有新证据在 [state-cost-evidence/](state-cost-evidence/)，旧证据不覆盖。
工具链 Go 1.26.2、linux/amd64、vendor、cgo/预装 SoftFloat；CPU 为 benchmark
自报 Xeon Gold 6348H，GOMAXPROCS 后缀 96。源码 hash 见 source-environment.txt。
既有 RunnerBaseline 入口未修改，single/diag=false Kernel 每 op 160 edges；init 与
execute 分开。execute 排除 fixture 宿主初始化，仍包含真实模拟 cache 初始化边沿；
入口自身的公共 Status 完成查询继续计时，只有 Kernel 内部查询被优化。

每组 3×1 op 的原始 ns/op、B/op、allocs/op 全部保留。以下为三样本中位数：

| 阶段/文件 | init ns/op | init B/op | init allocs/op | execute ns/op | execute B/op | execute allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 当前引擎基底 before.txt | 6592986223 | 751468184 | 13921214 | 269193268 | 45735296 | 453882 |
| 仅 Number number-only.txt | 2407803836 | 310658760 | 5799365 | 67590273 | 26711392 | 106743 |
| Number + 完成谓词 after.txt | 2339749926 | 310661080 | 5799375 | 65566116 | 26608816 | 106329 |

Number 每进程首次解析发生于首个 init 样本，首次约多 3.8 MB/6.96 万次分配，
后续样本为已经解析过 IR 的实例构造；不要把中位数当作冷进程启动成本。
before 与 number-only 进程部分重叠，宿主也有共享噪声，表内耗时仅为记录，
不作统计显著加速或端到端收益结论。分配下降不能外推为原规模 benchmark 加速。

独立入口 `BenchmarkNumberIR` 在同一二进制比较旧的重新解析路径与新读取路径：
10×3 样本，旧路径 3,763,585–3,764,167 B/op、69,420–69,421 allocs/op；新路径
184 B/op、4 allocs/op。耗时分别 28.96–33.98 ms/op 与 2.277–3.463 us/op。
`BenchmarkKernelCompletionRead` 比较相同存活 residency 的 Status().Complete 与
新谓词，100×3：352 B/op、2 allocs/op 对 0 B/op、0 allocs/op；耗时分别
2.491–2.637 us/op 对 30.02–33.14 ns/op。该微测量不代表完整 Run 的速度比例。

中间 profile `after.cpu/after.alloc` 使用 execute 5x（另外包含 Go benchmark
校准 1 op），全进程采样含停表初始化。drive 过滤 CPU 仅 360ms，Number 未采到
执行 CPU 样本；Core.Resources 占执行 alloc space 59.31%，Core.Evaluate 55.15%，
二者有包含关系不能相加。该证据支持下一 Worker 继续研究资源/诊断构造；不代表
整个 Resources 都能关闭。中间全进程 profile 的 Buffer 初始化热点促成第 3 项。

## 复现入口

仅使用本地有限工作量，不运行原规模 benchmark、RTLSIM 或外部资产。工作目录为
仓库根目录，先 `source env/env.sh`。性能值不设为回归阈值。以下命令可原样运行：

```bash
source env/env.sh
go test -mod=vendor -run '^$' -bench '^BenchmarkRunnerBaseline$/kernel/single/diag=false/(init|execute)$' -benchmem -benchtime=1x -count=3 ./timing/runner
go test -mod=vendor -run '^$' -bench '^Benchmark(Number|Buffer)IR$' -benchmem -benchtime=10x -count=3 ./timing
go test -mod=vendor -run '^$' -bench '^BenchmarkKernelCompletionRead$' -benchmem -benchtime=100x -count=3 ./timing/runner
go test -mod=vendor -run '^$' -bench '^BenchmarkRunnerBaseline$/kernel/single/diag=false/execute$' -benchmem -benchtime=5x -count=1 -cpuprofile "$HARNESS_RUNTIME_DIR/state-cost.cpu" -memprofile "$HARNESS_RUNTIME_DIR/state-cost.alloc" -o "$HARNESS_RUNTIME_DIR/state-cost.test" ./timing/runner
go tool pprof -top -nodecount=200 -focus='runner\.\(\*baselineFixture\)\.drive' -relative_percentages "$HARNESS_RUNTIME_DIR/state-cost.cpu"
go tool pprof -top -alloc_space -nodecount=60 -focus='runner\.\(\*baselineFixture\)\.drive' -relative_percentages "$HARNESS_RUNTIME_DIR/state-cost.alloc"
```

before.txt 对应上述基底；number-only.patch 为第 1 项；completion.patch 为第 2 项；
buffer.patch 为第 3 项，均相对上述 HEAD。可在另行授权的独立 checkout 中顺序
应用重建阶段；本 Worker 没有创建 worktree 或回退工作树。既有基线程序及测量
入口 hash 与已完成阶段相同，最终生产源码 hash 独立记录。

## 回归与后续职责

`timing/number_cache_test.go` 对 Number 的值/错误和 path 输入隔离、并发只读使用，
Buffer 全 boundary 的值/错误与独立解析逐项比较，并检查返回值修改隔离；原
ir_test.go 的改变 SIZE/encoding、null、非法值回归保持不变。没有改可变配置模型。
`timing/runner/kernel_completion_test.go` 用独立旧谓词 oracle 对比成功/错误逐边沿
完成状态，分别检查各 retirement/resident/barrier 门槛和 transferredStatus，
同时保存 Status 序列验证后续边沿及 caller 成员切片修改都不污染历史或 owner。
既有确定性基线继续覆盖固定/不规则分块、诊断开关、backpressure、flush、错误停止、
完整身份/事件顺序、寄存器/RAM、硬件与宏退休计数和不可变 trace。

本 Worker 不替代最终 closure 对 AC-015/016 的缺口审查：取消/重启、epoch、
跨 launch/CTA generation、旧传输尾部、MakeVisible/FlushCaches、硬件累计与 audit
发布仍须在后续诊断改动之后重新验收。下一 Worker 保留本私有完成接口及只读配置
边界，关注 MultiRecord、Core.Resources/Residents、stageEvents 的消费者。

原规模 benchmark、RTLSIM、外部资产、全仓 `go test ./...`、完整 race 检查均未验证
（按范围不运行）；不设 benchmark 收尾 milestone。全包冻结验证及本轮定向检查
的最终结果补充在下文，不能用旧阶段通过记录代替最终代码验证。

## 最终测量与验证结果（2026-09-17）

加入 Buffer 缓存后的 `final-bench.txt` 三样本中位数：init **455445571 ns/op、
64470360 B/op、1200066 allocs/op**；execute **67327712 ns/op、26609456 B/op、
106333 allocs/op**，仍为 160 edges/op。相对 Number+完成谓词阶段，Buffer 的收益
集中于初始化（约 310.7 MB → 64.5 MB），持续执行分配基本相同。首个 init 样本
72,027,704 B/op 包含 Number/Buffer 的首次解析，不能与已暖配置样本混为冷启动。
最终 bench 与较早启动的中间版本包回归有重叠，只作为分配证据和时间原始记录。

`buffer-bench.txt` 独立 10×3 查询：旧解析 3,793,954–3,796,123 B/op、
70,760–70,761 allocs/op，对缓存读取 0 B/op、0 allocs/op；时间分别
32.556–34.468 ms/op、231.4–303.5 ns/op。它与 number-bench、completion-bench
共同给出每项优化的独立对照，而非仅凭性能入口退出成功宣称改善。

最终 `final.cpu/final.alloc` 为相同 execute 5x，独立保存原始 profile 与三个
执行报告（CPU、alloc space、alloc objects）；只有 350ms 执行 CPU 样本。
Core.Resources 仍占执行 alloc space 58.66%，Core.Evaluate 55.87%，含重叠；
Number/Buffer 不在执行热点中。全进程初始化现在主要为未改的 Arbiter 解析
（alloc space 69.49%），不是持续执行成本，本 Worker 不继续扩大配置重写范围。
两份 profile 都能不依赖临时测试二进制读取 top 报告；若需要逐行报告，应按
final-source-sha256.txt 重编译对应源码。采样分配不是精确计数，不能相加重叠栈。

最终代码运行以下冻结命令，退出 **0**，结果见 `final-validation.txt` 和
`final-validation-exit.txt`，未修改 argv、未跳过任何测试：

```text
go test -mod=vendor -count=1 -timeout=20m ./timing/... ./emu/... ./integration/vortexruntime ./cmd/timing-run ./cmd/timing-multi
```

所有列出包通过：timing 3.001s、check 7.325s、effects 5.679s、memsys 8.490s、
model 4.586s、runner 98.059s；emu 的 core/device/state/warp、vortexruntime、
timing-run、timing-multi 也全部通过。包运行时间不是性能比较证据。

```text
gofmt -l timing emu integration/vortexruntime cmd/timing-run cmd/timing-multi
```

退出 **0** 且 stdout 为空（`gofmt.txt`、`gofmt-exit.txt`）。此外：

- `go test -mod=vendor -count=1 ./timing` 通过（2.987s），含配置与全部输入校验测试。
- `go test -mod=vendor -race -count=1 ./timing -run '^Test(Number|Buffer)'`
  退出 0，见 `config-race.txt`；只验证配置访问子范围，不声称完整模拟器并发安全。
- `git diff --check` 通过；前两阶段证据各自 `sha256sum -c SHA256SUMS` 全部通过。
  frozen-integrity.txt 确认原 Clock/model、固定 workload、时钟集成回归及基线证据未改。
- `validation.txt` 是 Buffer 优化前启动的中间版本验证；最终验收依据为上述
  `final-validation.txt`，不得将中间与最终记录混淆。

本次实施没有环境导致无法执行的必要检查；上文范围外检查仍明确为未验证。
当前 Worker 的 AC-011/012 状态构造部分及相关 AC-014 已可交接，完整 milestone
验收与后续诊断代码的重新验证仍由冻结 closure Worker 完成。
