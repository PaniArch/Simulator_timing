# 诊断构造优化交接（第 2 Worker）

仅交付 `diagnostic-construction-cost` 范围；不关闭 AC-011–AC-017 milestone。
基底为 `4ce774c4efbe8d0a7a76d84711cdb30ea0ffa000`，沿用上一 Worker 的只读
IR 缓存、私有 executionComplete、生命周期和配置隔离边界。未提交 commit。
仓库没有专名 Construction Skill 或 Boundary；采用 `emu/docs/architecture.md`、
`timing/architecture.md`、`timing/multiwarp-contract.md`、`timing/memory-contract.md`
及上一 Worker 交接。未读取历史聊天或执行轨迹。

## 依据与接口

上一 Worker 的 `state-cost-evidence/final.alloc_space.execute.txt` 显示 Resources
占 drive 执行栈分配 **58.66%**，CPU 报告为 **37.14%（130/350 ms）**，
observeWarps 占 CPU 8.57%，这些累计栈存在包含关系，不能相加。
源码确认每次正常边沿先后有两次 Evaluate 的 Resources，再有 observeWarps
的 Resources 和提交后的 ResourcesAfter；Kernel 的内部回调使无用户 observer
也构造这些快照。没有证据支持本轮重写 owner Snapshot 或双求值模型。

- `timing/model/core.go`：新增 `EvaluateExecution`，执行与原 Evaluate 相同的
  proposal、握手、反馈和 accounting，只不构造 Report.Resources。
  既有公开 Evaluate 调用该实现并返回完整 detached Resources，内容不变。
  EvaluateExecution 的调用者如需资源，必须在 CommitEdge 前采集旧态；不得在
  提交后补旧态快照，也不能把该 API 当作可以省略第二次求值的许可。
- `timing/runner/multi.go`：仍执行两次求值，中间仍推进 memory，并用真实
  FetchAccepted/MemoryAccepted 反馈第二次求值。需要诊断时，在第二次求值后、
  Begin/BeginSpawn 与 CommitEdge 前采集一次旧态 Resources；
  `observe_multi.go` 只读同一份 Resources，不再重新构造。Report.Resources、
  Warps 保持旧态，ResourcesAfter/Events 保持统一提交与 effects 后原时点。
- 新增私有 `MultiRunner.run(budget, diagnostics, callback)`。公开 Run 按 observer
  是否为 nil 选择诊断；Kernel.Run 显式以外部 observer 是否存在选择 diagnostics，
  但始终调用其原有 post-edge 回调。诊断关闭时只省略 Warps、Resources、
  ResourcesAfter、StageEvent 和 Services 的构造及 recovery/cancel 记录拷贝。
  回调仍收到完整控制 Report（除 Resources）、Finished、Retired、Counters、
  Memory 与原错误时点；Kernel advanceRetirement/releaseBarriers 不依赖诊断。
  effects、Reap、Wakeups、硬件计数、控制与错误边沿部分推进顺序均保留。
- recoveryEvents/cancelled 仍在原边沿消费并清空，即使该边沿无 observer。
  后续启用 observer 不补发过去的诊断；零预算不消费。TraceMemory 独立控制
  memsys fragment 记录，不因 observer 切换改变握手或事件交付。

没有跨边沿临时 slice 复用、共享可变资源、缓存 owner snapshot，也没有改动
Resources/Residents 的公开内容。Idle、身份排空、取消和 Kernel 生命周期中仍需
Resources 的路径继续保留。公开记录里所有可变 slice 仍由该次快照拥有。
原持久引擎、配置缓存、公开 Status、内存 owner、CSR/MPM 与 audit 发布路径未改。

## 固定工作量测量

新证据独立保存在 [diagnostic-cost-evidence/](diagnostic-cost-evidence/)。
`source-environment.txt` 给出源码 SHA256、父版本与工具链；`production.patch`
仅记录相对父版本的四个生产文件变更。原测量入口及前两阶段证据未修改，
`frozen-integrity.txt` 是这些路径的空 git diff。使用预装 Go 1.26.2、vendor、
linux/amd64、cgo/SoftFloat，benchmark 自报 Xeon Gold 6348H、96 P。

沿用 `BenchmarkRunnerBaseline`，每次 fresh fixture 的初始化不在 execute 计时内；
multi 每 op **155 edges**，kernel 每 op **160 edges**，前后均相同。
本 Worker 未改变初始化，不声称初始化收益。以下各为 3×1 op 的中位数；原始
ns/op、B/op、allocs/op 保存在 before.txt 与 after.txt。

| 路径 | 前 ns/op | 后 ns/op | 前 B/op | 后 B/op | 前 allocs/op | 后 allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| multi / off | 73012687 | 35606654 | 30447680 | 13863256 | 108985 | 74281 |
| multi / on | 75697717 | 60654725 | 30459656 | 23038824 | 109030 | 93120 |
| kernel / off | 70326812 | 36619541 | 26609440 | 11404816 | 106334 | 73967 |
| kernel / on | 64843509 | 50981971 | 26614400 | 19163952 | 106351 | 90916 |

on 路径保留完整诊断，反映删除两份重复旧态 Resources 的收益；off 还包括
跳过无人消费的 post-edge 资源、Warp 观测和事件构造。off/on 的 TraceMemory
设置也不同，不能只将同一版本的 off/on 差值当成纯 observer 开销。
时间只给出本地原始记录，不作统计显著、原规模 benchmark 或端到端加速结论。
共享宿主与低样本造成噪声；profile 采集与新增 barrier 回归有部分时间重叠。

新增 kernel off/on 各 5x 的 CPU/alloc profile（Go benchmark 另含 1 op 校准；
profile 也包含停表 fixture 初始化，故提供 drive 过滤报告）。off 的 Resources
降到执行 alloc space **2.43%（1.51/62.07 MB）**，属于保留的控制/排空路径；
on 为 **41.81%（45.68/109.25 MB）**。持续执行 CPU 样本只有 off 190ms、on 300ms；
Resources 各 10ms、50ms。剩余主成本包括 EvaluateExecution 的组件 proposal、
combine 和 memory Step；两次 Evaluate 的正确性边界不因它仍是热点而改变。
公开诊断需要的完整资源与 owner Snapshot 不继续做无证据重写。

## 复现

仓库根目录，先 `source env/env.sh`。仅有限本地程序/RAM，不读取外部测试资产。
测量不设性能阈值，不新增 benchmark 收尾 milestone。

```bash
go test -mod=vendor -run '^$' -bench '^BenchmarkRunnerBaseline$/(kernel|multi)/single/diag=(false|true)/execute$' -benchmem -benchtime=1x -count=3 ./timing/runner
```

独立 profile 命令（将 diag 改为 true 可复测开启路径）：

```bash
diag=false
go test -mod=vendor -run '^$' -bench "^BenchmarkRunnerBaseline$/kernel/single/diag=$diag/execute$" -benchmem -benchtime=5x -count=1 -cpuprofile "$HARNESS_RUNTIME_DIR/diagnostic-$diag.cpu" -memprofile "$HARNESS_RUNTIME_DIR/diagnostic-$diag.alloc" -o "$HARNESS_RUNTIME_DIR/diagnostic-$diag.test" ./timing/runner
go tool pprof -top -cum -nodecount=60 -sample_index=cpu -focus='runner\.\(\*baselineFixture\)\.drive' -relative_percentages "$HARNESS_RUNTIME_DIR/diagnostic-$diag.cpu"
go tool pprof -top -cum -nodecount=60 -sample_index=alloc_space -focus='runner\.\(\*baselineFixture\)\.drive' -relative_percentages "$HARNESS_RUNTIME_DIR/diagnostic-$diag.alloc"
go tool pprof -top -cum -nodecount=60 -sample_index=alloc_objects -focus='runner\.\(\*baselineFixture\)\.drive' -relative_percentages "$HARNESS_RUNTIME_DIR/diagnostic-$diag.alloc"
```

沿用 `scripts/profile-performance-baseline.sh` 也可采集全部有界入口；该脚本不改。
证据中的 raw profiles 可直接读取 top，不要求交付临时二进制。逐行 pprof 需要
按源码 hash 重编译；不能混用下一 Worker 修改后的二进制解释当前逐行采样。

## 确定性回归与交接

`timing/runner/diagnostics_test.go`：

1. TestDiagnosticObserverSwitching：multi/kernel、正常/illegal decode 停止、
   TraceMemory 各自 on/off、逐周期/固定/不规则预算。reference 全程观察，目标
   在预算边界切换 observer；包含零预算。每个被观察边沿完整有序记录相等，
   每次返回比较 owners、RAM、Status、Kernel events、硬件与宏退休计数。
   错误后仍拒绝续跑；成功后比较分块 FlushCaches。交替保存 JSON 历史与递归
   修改交付记录的所有 slice（含 Residents、Wakeups、memory fragments），验证
   后续执行与既存历史不被污染。
2. TestDiagnosticRecoverySwitching：显式 Cancel/Restart 与 epoch Flush，恢复
   后第一拍各取有/无 observer，并插入零预算，随后交替观察、分块续跑。
   旧 recovery 通知不重播，全部观察边沿有序比较，最后 MakeVisible/FlushCaches
   的状态、字节和计数相等。仅 Services 跨 epoch 清单按既有测试方式规范化；
   不排序执行事件、资源或 memory handshakes。
3. TestDiagnosticKernelBarrierControl：三个双 Warp CTA、每个 CTA 两次 barrier；
   nil observer 或续跑切换 observer 对照全程观察。每个预算边界状态/计数/事件
   相等，12 次 external-wake 和三个 CTA 回收均完成，覆盖无外部 observer 的
   barrier release、退休与 CTA 复用控制。

`diagnostic_compatibility_test.go` 固定四个完整 result+trace SHA256：
它们来自父版本真实执行（非当前实现自比较），含 multi/kernel 成功、错误和
成功后的 cache flush，固定 device 观测 namespace。JSON 所有字段、所有有序
slice 均参与 hash。父版本重复两次相同，记录见 parent-traces.txt。生成时用 Go
`-overlay` 从 git show 父版本的四个生产文件替换编译输入，未改动工作树；
当前版本必须通过这些固定指纹。不跨 epoch，因此没有 Services 规范化。

复验父版本可将 `git show 4ce774c4efbe8d0a7a76d84711cdb30ea0ffa000:<path>`
输出到 `$HARNESS_RUNTIME_DIR`，生成 Go overlay 的 `Replace` map，key 为当前
绝对路径，value 为对应旧源码绝对路径。四个 path 是 core.go、multi.go、kernel.go、
observe_multi.go（完整路径见 production.patch）。执行：

```bash
go test -mod=vendor -overlay "$HARNESS_RUNTIME_DIR/diagnostic-parent/overlay.json" -count=2 -run '^TestDiagnosticTraceCompatibility$' -v ./timing/runner
go test -mod=vendor -count=1 -run '^TestDiagnostic' ./timing/runner
```

已完成 AC-011–014 的诊断范围及相关 AC-015/016 回归；closure Worker 仍须审查
完整冻结验收映射，尤其复杂已接受存储取消尾部、跨 launch/device/epoch/generation
身份、硬件累计、audit 发布和更晚错误的组合覆盖。已有取消/store tail、生命周期、
计数与 audit 测试保留，不能仅凭本文件的新增小程序替代这些契约。

原规模 benchmark、RTLSIM、外部资产测试、全仓 `go test ./...`、完整 race 检查
**未验证**（按本 Worker 范围不执行）；不新增外部依赖，不安装或下载工具链。
当前阶段必要检查未遇到环境阻碍。最终冻结验证结果记录在下一节；后续生产代码
修改仍须由 closure Worker 重新验证，不将本 Worker 的通过当作整个 milestone 关闭。

## 最终验证结果（2026-09-17）

以下冻结命令在最终生产代码及全部新增测试上运行，退出 **0**，没有替换 argv
或跳过测试。`validation.txt`、`validation-exit.txt` 保存结果：

```text
go test -mod=vendor -count=1 -timeout=20m ./timing/... ./emu/... ./integration/vortexruntime ./cmd/timing-run ./cmd/timing-multi
```

timing（3.051s）、check（7.518s）、effects（5.885s）、memsys（8.537s）、
model（4.553s）、runner（139.842s）、emu 的 core/device/state/warp、
vortexruntime、timing-run 与 timing-multi 全部通过。包运行时间仅记录验证，
不能当作性能比较。最终 runner 包包含上述三个新测试以及冻结基线、时钟集成、
既有生命周期/旧传输尾部/计数回归；runtime 包包含 audit 发布门槛回归。

```text
gofmt -l timing emu integration/vortexruntime cmd/timing-run cmd/timing-multi
```

退出 **0**，stdout **0 字节**，见 gofmt.txt/gofmt-exit.txt。
`git diff --check` 也通过。source-environment.txt 的源码 hash 与最终代码一致。
父版本固定指纹的最终断言复验见 parent-compatibility.txt（两次运行），
原始指纹采集见 parent-traces.txt；二者不是新的工作负载。

initial-tests.txt 是新增测试前的 model/runner 回归；diagnostic-tests.txt 是
barrier 测试加入前的 observer/recovery/固定指纹检查；barrier-tests.txt 为修正
测试 launch 派生字段和 BAR CTA 地址后该定向测试的成功结果。最终冻结全包
验证覆盖所有最终测试，包括深层 slice 修改，验收依据以 validation.txt 为准。
证据完整性 manifest 为 diagnostic-cost-evidence/SHA256SUMS。

本 Worker 已可交接。仅 closure Worker 有权完成整个 milestone 的验收闭合；
下一 Worker 保留 EvaluateExecution 的 nil Resources 边界、私有控制回调、原
错误时点和 recovery 消费规则，继续按冻结计划完成组合覆盖审查及最终验证。
