# 宿主状态与诊断构造：里程碑验收

本文件闭合 `profile-guided-state-cost`（冻结计划 v3、AC-011–AC-017）。
这是第三位 Worker 的集成验证，不是新的 benchmark 收尾 milestone。
仅本地有限程序/RAM；没有运行原规模 benchmark、RTLSIM 或外部资产测试。
生产基底 `171a1586db6b7acf97136a22fa95d541ab43589c`，本 Worker 只补测试和文档，
没有修改前两位 Worker 的生产实现、原始证据或已完成的 Clock/基线阶段。

仓库 `.agents`、`.codex` 没有提供专名 Construction Skill/Boundary 文件。
沿用 `emu/docs/architecture.md`、`timing/architecture.md`、
`timing/multiwarp-contract.md`、`timing/memory-contract.md` 的现有契约，
不补造缺失契约。新证据在 [state-cost-closure-evidence/](state-cost-closure-evidence/)。

## 最终实现和保留边界

- `timing/number.go`、`ir.go` 仅缓存私有 embedded IR 的只读解析树/参数；不缓存
  caller 配置、owner 或组件。独立输入仍独立解析，错误、可变输入和实例隔离不变。
- Kernel 私有 `executionComplete` 聚合原生命周期门槛；Run/可见性/转交控制不依赖
  完整公开 Status。公开 Status、Resources、Residents、owner Snapshot 保持 detached。
- Core 的 `EvaluateExecution` 省略 Resources 构造；公开 Evaluate 返回完整资源。
  MultiRunner 保留 memory step 前后两次求值，第二次接入真实 acceptance；需要
  外部诊断时采集一次旧态资源，提交后按原时点生成 ResourcesAfter/Events。
- Kernel 始终执行私有退休/barrier 控制回调，nil 用户 observer 不省略 effects、
  Finished、硬件计数、反馈或错误。恢复诊断仍在原边沿消费，不在后来启用时重播。
- 未改 owner Snapshot、执行模型、统一提交和部分推进错误语义；未跳模拟边沿。
  每个 Clock 的持久 SerialEngine、完整身份尾部和跨 launch 所有权转交维持原约。
  不共享跨边沿可变快照，不新增 canonical owner。

详细接口和源码依据见 [状态构造交接](state-construction-handoff.md) 与
[诊断构造交接](diagnostic-construction-handoff.md)。既有 Services 跨 epoch
清单的排序限制仍存在；只在已有回归中规范化该清单，不排序执行事件或握手序列。

## profile 依据与收益

每个优化均有独立对照，原始 ns/op、B/op、allocs/op 和 CPU/alloc profiles
保留在前两位 Worker 的证据目录。本 Worker 校验了四个已有 evidence manifest
共 170 项 SHA256；优化生产源码与诊断 Worker 的最终版本一致，故沿用其 profile，
没有为相同源码重新制造 CPU profile。源码 hash、工具链在本轮 evidence 中。

| 修改 | 实测依据 | 有界前后证据及边界 |
| --- | --- | --- |
| Number 只读 IR 解析缓存 | 基线 Kernel drive CPU 73.08%、alloc space 42.19%、objects 79.67% 在 Number 路径 | `state-cost-evidence/number-bench.txt`：旧查询约 3.764 MB/69,420 次分配，新查询 184 B/4 次；Kernel execute 分配中位数 45,735,296 → 26,711,392 B/op、453,882 → 106,743 allocs/op。收益包含执行，也包含初始化。 |
| 内部完成谓词 | Status 约占执行分配 0.40%，无 CPU 样本，故不作为主性能热点 | `completion-bench.txt`：公开 Status 完成查询 352 B/2 次，新私有查询 0 B/0 次；Kernel execute 26,711,392 → 26,608,816 B/op、106,743 → 106,329 次。不把小样本时间差解释为独立 Run 加速。 |
| Buffer 只读配置缓存 | Number 优化后全进程 Buffer alloc space 72.25%，drive 中没有该路径 | `buffer-bench.txt`：旧查询约 3.794 MB/70,760 次，新查询 0 B/0 次；init 中位数 310,661,080 → 64,470,360 B/op、5,799,375 → 1,200,066 次。仅初始化收益；冷进程首个 init 约 72.0 MB，不能当作暖实例 64.5 MB。 |
| 去重旧态资源、分离诊断与控制 | 缓存后 drive Resources alloc space 58.66%，CPU 37.14%（130/350ms） | `diagnostic-cost-evidence/before.txt`、`after.txt` 与 off/on CPU/alloc：Kernel off 分配 26,609,440 → 11,404,816 B/op、106,334 → 73,967 次；on 26,614,400 → 19,163,952 B/op、106,351 → 90,916 次。off 执行栈 Resources 分配降至 2.43%，on 41.81%。 |

Number 分阶段 execute 耗时中位数为 269,193,268 → 67,590,273 ns/op；完成谓词后
65,566,116 ns/op。Buffer init 前后为 2,339,749,926 → 455,445,571 ns/op，execute
仍约 67,327,712 ns/op。诊断优化前后 Kernel off 为 70,326,812 → 36,619,541 ns/op，
on 为 64,843,509 → 50,981,971 ns/op。完整 Multi 数据及所有原始样本见上述交接。
这些是共享宿主的小样本记录，部分早期测量与回归重叠，**不是统计显著的速度结论**。

本轮在测试结束后独立重测原入口 3×1 op，结果如下（各列三样本中位数）。
先前与定向测试重叠的一次保留为 `runner-bench-overlap.txt`，不混入本表。

| 路径 | edges/op | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Multi / off | 155 | 39,940,618 | 13,876,928 | 74,283 |
| Multi / on | 155 | 61,217,329 | 23,024,928 | 93,116 |
| Kernel / off | 160 | 29,399,221 | 11,404,800 | 73,966 |
| Kernel / on | 160 | 54,268,920 | 19,163,792 | 90,916 |

执行分配下降可复现，工作量保持一致；不外推为端到端或原规模 benchmark 加速。
StopTimer 排除 fixture 构造的 benchmem/计时，但 profile 包含它，必须看 drive
过滤栈区分初始化。累计 profile 栈有包含关系，百分比不能相加。off/on 的既有
入口同时改变 TraceMemory，因此同版本 off/on 差值不能称为纯 observer 成本。
CPU 执行样本仅数百毫秒，owner Snapshot 没有足够热点证据，未做进一步重写。

## 验收映射

| 条件 | 实现/确定性证据 |
| --- | --- |
| AC-011 | 上述独立测量、两阶段 CPU/alloc、当前源码与证据完整性校验；对 Snapshot/双求值保守保留。 |
| AC-012 | `kernel_completion_test.go` 的旧谓词独立 oracle、每个门槛、历史 Status 与调用方修改；`number_cache_test.go` 的输入/并发读取/实例隔离；诊断记录深层 slice 修改与历史不变性。 |
| AC-013 | `TestDiagnosticObserverSwitching` 同时独立切换 TraceMemory 和 observer；`TestDiagnosticKernelBarrierControl` 检查 12 次 wake、三 CTA 回收及 nil observer 控制；本轮跨 launch 和存储尾部 on/off 组合。 |
| AC-014 | Core/MultiRunner 两次求值/acceptance 源码复核；`TestDiagnosticTraceCompatibility` 对父版本完整有序 trace 固定指纹；本轮延迟后端错误与既有 `TestMemoryFaultResetAlignsCommittedEdges`、Clock 失败边沿测试。 |
| AC-015 | 冻结 baseline 的成功/预算/背压/非法指令矩阵，`TestPersistentClockEpochVisibilityChunks`，诊断 recovery/switching；本轮两 launch 六 CTA 的 single/fixed/irregular × on/off，部分 mixed-store 取消/Restart 与 epoch Flush × 分块 × on/off、真实写回；既有 identity tail、device transfer、LMEM、generation 和早复用测试。 |
| AC-016 | 本轮 packed load 每 launch 30 宏退休/48 EOP、跨 launch 96 EOP、flush busy 尾拍与保存 trace 不变；`TestMultiRunnerArchitecturalCountersUseOldHardwareEvents`、model accounting/wrap；runtime `TestNativeMPMCumulativeLaunchAndFlush`、`TestMPMPublicationWaitsForAudit`、终态 audit 失败测试。 |
| AC-017 | 本文件、两份 Worker 交接、原固定入口说明和所有 evidence；以下冻结验证原命令与未验证清单。 |

本轮新增 `state_cost_integration_test.go`：

- `TestStateCostLaunchChunkEquivalence`：冷启动后两次 launch，中间 MakeVisible、
  D/I FlushCaches、修改输入；共享同一 Clock/memsys，旧 Kernel 禁止推进，累计计数
  不重置。六 CTA 强制 generation 复用，后端 Ready 有周期性背压。比较完整
  WarpSnapshot、全 RAM、周期、Status、严格有序 Kernel events；诊断开启时比较
  完整有序 MultiRecord，第二次 launch 后复验历史 JSON 不变。独立 device 分配
  仍由既有 `TestKernelTracePackedReuseAndCounters`/`TestDeviceMemorySuccessor` 检查。
- `TestStateCostDelayedFaultChunks`：实际请求进入后端后注入 refill fault，比较
  三种预算分块、诊断开关的错误、错误边沿周期、状态及完整有序 trace；失败后
  Run(0/1/32)、NextLaunch、MakeVisible、FlushCaches 均拒绝且不再访问 backing。

扩充 `partial_cancel_test.go`：保留已有混合 LMEM/global store 的部分应用触发点
和禁止旧 token 写回/接受断言，在该精确边界取消或 epoch Flush；续跑与 cache flush
采用三种分块并切换整段 observer on/off。比较架构结果、全部 backing 字节、周期、
硬件/宏计数和逐 transport identity 的 local 应用次数；已应用子集恰一次，global
held offer 保留并排空。触发取消前仍单步到相同边界，避免因 host 预算改变注入时点。

## 复现与验证

从仓库根目录 `source env/env.sh`，使用预装 Go 1.26.2、vendor、cgo/SoftFloat；
不得安装或下载依赖。临时缓存/profile 放 `$HARNESS_RUNTIME_DIR`。固定入口见
[baseline-harness.md](baseline-harness.md)，原脚本 `scripts/profile-performance-baseline.sh`
保持不变。精确重现本轮测量：

```bash
go test -mod=vendor -run '^$' -bench '^BenchmarkRunnerBaseline$/(kernel|multi)/single/diag=(false|true)/execute$' -benchmem -benchtime=1x -count=3 ./timing/runner
```

初始化/配置和完成判定微测量：

```bash
go test -mod=vendor -run '^$' -bench '^BenchmarkRunnerBaseline$/kernel/single/diag=false/init$' -benchmem -benchtime=1x -count=3 ./timing/runner
go test -mod=vendor -run '^$' -bench '^Benchmark(Number|Buffer)IR$' -benchmem -benchtime=10x -count=3 ./timing
go test -mod=vendor -run '^$' -bench '^BenchmarkKernelCompletionRead$' -benchmem -benchtime=100x -count=3 ./timing/runner
```

CPU/alloc 复现命令及 drive 过滤报告见诊断交接；原始 profiles 可直接用 `go tool
pprof` 读 top，不依赖已删除的临时二进制；逐行分析必须用对应源码 hash 重建。
本轮未复测 init/微测量，沿用已校验的前两位 Worker 原始结果。

冻结验证（本轮最终测试树）：

```text
go test -mod=vendor -count=1 -timeout=20m ./timing/... ./emu/... ./integration/vortexruntime ./cmd/timing-run ./cmd/timing-multi
gofmt -l timing emu integration/vortexruntime cmd/timing-run cmd/timing-multi
```

结果见下方最终验证记录。没有修改冻结 argv、跳过子测试或以定向测试替代全包验证。

未验证/范围限制：原规模 benchmark、RTLSIM、依赖外部资产的测试、全仓
`go test ./...`、完整 `go test -race ./...` 均按本任务范围未执行；不代表这些检查
已经通过。配置 race 子范围沿用 state Worker 的成功证据，不扩大为整个执行器
并发安全保证。当前环境能执行全部冻结检查，没有环境阻塞项。外部周期等价、
Services 已有清单排序限制及共享宿主计时不确定性未由本工作关闭。

### 最终验证记录（2026-09-17）

冻结 `go test` 命令退出 **0**，13 个列出包全部通过；runner **168.625s**，
vortexruntime **4.478s**。runner 包包含本轮全部新增/扩充用例和冻结基线、Clock、
诊断兼容指纹、生命周期与尾部回归。包耗时只描述验证，不作性能比较。
`gofmt` 命令退出 **0**，stdout **0 字节**。完整输出与退出值分别为 evidence 中
`validation.txt`/`validation-exit.txt` 和 `gofmt.txt`/`gofmt-exit.txt`。
`git diff --check` 通过。证据目录的 `SHA256SUMS` 覆盖本轮所有证据文件，
`source-sha256.txt` 覆盖优化生产源码及关键回归/测量入口。

AC-011–AC-017 在上述本地范围内已可验收。没有待修复生产问题或环境导致的
冻结检查缺项；保留前述范围外未验证项，不将其描述为本地验收已经证明的能力。
没有提交 commit、修改冻结验收条件或创建新的 Worker/milestone。
