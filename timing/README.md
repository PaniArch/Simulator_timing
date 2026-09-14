# 周期执行与 Timing IR

本目录同时保存周期模型实现、结构化 Timing IR、当前使用说明以及 T8–T12 的里程碑
证据。T12 已完成冻结单 Core 配置下从 Multi-Warp pipeline 到 L1/LMEM/external backend
的执行闭环；当前实现不宣称逐周期 RTL 等价。

## 权威事实与当前接口

| 文档 | 职责 |
| --- | --- |
| [ir.yaml](ir.yaml) | 配置、RTL 来源、节点、资源、端口、边界、周期规则和未知项的结构化权威来源 |
| [architecture.md](architecture.md) | 周期层级、状态职责、实现边界与维护原则 |
| [rules.md](rules.md) | 周期计量、阻塞、反馈和事件规则 |
| [integration.md](integration.md) | 功能 owner 与周期事件的衔接边界 |
| [kernel-usage.md](kernel-usage.md) | 当前 Kernel API、停止条件、回收和可见性 |
| [t12-delivery.md](t12-delivery.md) | 当前 T12 存储/Kernel 交付和最终验证证据 |
| [功能架构契约](../emu/docs/architecture.md) | 指令结果、canonical state owner 与 effect 语义的唯一权威来源 |

阅读当前实现时依次阅读功能架构契约、`architecture.md`、`ir.yaml`、`rules.md`、
`kernel-usage.md` 和 `t12-delivery.md`。修改结构化事实时先更新 YAML 的值、状态与
evidence，再同步相应 Markdown；不得把未启用 RTL 分支或功能 Step 顺序当成周期事实。

## 子系统说明

- [model](model/)：前端、调度、Scoreboard、执行、Commit 和周期状态组件。
- [effects](effects/README.md)：周期事件到唯一功能 owner 的交付。
- [runner](runner/README.md)：单/多 Warp 和 Kernel 运行、取消、恢复、flush 与可见性。
- [memsys](memsys/README.md)：split、coalescer、LMEM、I/D-cache 和 external backend。

## 历史里程碑证据

- T8：`architecture.md`、`rules.md`、`integration.md` 和初始 `ir.yaml`；原始任务见
  [T8](../docs/history/tasks/T8.md)。
- T9：[implementation.md](implementation.md)。
- T10：[multiwarp-contract.md](multiwarp-contract.md)、
  [task10-progress.md](task10-progress.md)、
  [concurrent-effects-progress.md](concurrent-effects-progress.md) 和
  [control-observability-progress.md](control-observability-progress.md)。
- T11：[task11-cycle-control.md](task11-cycle-control.md)、
  [task11-kernel.md](task11-kernel.md) 和 [task11-kernel-sync.md](task11-kernel-sync.md)。
- T12：[memory-contract.md](memory-contract.md)、[cache-progress.md](cache-progress.md)、
  [routing-progress.md](routing-progress.md)、
  [memory-integration-progress.md](memory-integration-progress.md) 和
  [t12-delivery.md](t12-delivery.md)。

历史 progress 文档保存当时的验收映射，不应覆盖上表中的当前接口。T8–T12 原始任务
统一归档在 [docs/history/tasks](../docs/history/tasks/)。

## 验证

在仓库根运行 `bash scripts/verify-timing.sh`。它使用固定工具链和 vendor，检查 YAML
语法、稳定 ID、类型化引用、来源文件、定量单位、起止事件、null 未知项及功能问题引用。
完整回归运行 `bash scripts/verify-all.sh`。两者验证结构和实现一致性，不证明动态时序
已经与 RTLSIM 完全相等。

周期组件诊断入口（只推进 token，不执行架构效果或分支跳转）：

```bash
source env/env.sh
go run ./cmd/timing-token -backend std -period-ps 1000 -fetch-cycles 2 -memory-cycles 9 -cycles 1000 -words 00100093,00002083,00102023,0000000f,00000053
```

输出每边沿一条 JSON，包括旧态资源占用、输出 token 身份、握手与注册通知。时间刻度和服务延迟是显式测试参数，不是硬件精度承诺。内存层位于 LSU scheduler vector port 之外；功能衔接由下文 Task10 MultiRunner 提供。`verify-timing.sh` 的诊断保留在 stderr，成功时 stdout 为空。

### Task9 程序运行器增量

`timing/runner` 复用 Core/effects 和原 state/memory owner，以单活动指令排空策略从 canonical PC 自动取指；取指与数据服务均采用显式正延迟，不代表 cache 时间。Akita Run 支持预算续跑，Flush 清除在途服务并增加 epoch，不回滚已可见效果。条件见 `r-software-program-runner`。

本地入口：在仓库根目录 `source env/env.sh` 后运行 `go run ./cmd/timing-run`；`-trace` 输出逐周期 JSON，`-program file.bin` 从 0x100 加载 raw little-endian RV32 镜像。默认四 lane、x1=64+8*lane，内建示例验证依赖 ADDI/MUL/DIV、store/load、分支跳过指令与 TMC 结束。显式 STD、1ps 模型时基、fetch=3/memory=19 cycles；可用对应 flags 修改服务延迟。命令行预算耗尽返回非零；库保留进度可续跑。多目标 spawn residency 仍需 effects.BindSpawn，当前程序入口明确拒绝，不实现多 Warp scheduler。

观测包含旧边沿 CoreReport、提交后的 ResourcesAfter/Residents、Services 及 Events。按 ID/epoch/warp/uop/mask 对应资源生成 enter/stay/advance/leave，读、执行、WB/反馈另有事件；Flush 取消事件保留旧 epoch。位置与 Remaining 是本地资源状态，不推定外部 cache 时间。

### Task9 对象驻留观测与验证

每个资源通过 `Residents()` 返回 detached value，含 Token、位置、局部 Remaining 与 readiness 原因；runner 不访问组件队列。CoreReport.Resources 是旧边沿，ResourcesAfter 是本次统一提交后状态。Events 的 enter/leave 对应本周期提交的转移，stay/advance 表示资源仍持有该对象；tag/context alias 保持独立资源身份，不能累加为指令数量。FIFO 中的 queue-order、输出 awaiting-transfer、执行 execution-latency、tag response-coverage 与外部 backpressure/control-drain 明确区分；这些原因不宣称解析全部 RTL 仲裁信号。Services 列出原 byte owner 服务队列及显式 due cycle。

程序 trace 测试验证驻留记录闭合、除法 33 周期占用、packed 请求背压后按逐 uop WAW 恢复且每 uop 只 WB 一次（独立 LSU 组件另测队列容量）；增加 memory 服务延迟或请求背压会延长完整运行，功能结果不变。重复运行记录确定；快照修改不会改变组件 owner。基础注册边界与满队列同时接收/释放继续由 timing/model 门禁覆盖。

Task10 当前交付 [四 Warp 周期事件契约](multiwarp-contract.md)：`cycle_contracts` 补齐调度、依赖、仲裁和控制边界，`verify-timing.sh` 检查契约分类、RTL 锚点及冻结配置一致性。`NewScheduledCore` 已接入四 Warp Scheduler、Scoreboard 与每 Warp IBuffer/Sequencer，详见 [实施记录](task10-progress.md)。旧功能运行器保留 Task9 单活动策略；新增四 Warp 功能运行器见下文。

Task10 `runner.NewMulti` 与 `effects.NewConcurrent` 已接入四个显式 owner，支持
同 Warp 在途重叠、按身份服务与逐事件交付。接口、功能对比测试及控制恢复边界见 [并发效果实施记录](concurrent-effects-progress.md)。

第四里程碑已接入控制流、定向恢复、完整观测及显式 Barrier/WSPAWN owner
边界，见
[控制与观测进度](control-observability-progress.md)。

### 四 Warp 可复现示例

```bash
source env/env.sh
go run ./cmd/timing-multi -cycles 2000 -backend-cycles 60
go run ./cmd/timing-multi -trace -cycles 2000 -backend-cycles 60
bash scripts/verify-timing.sh
bash scripts/verify-all.sh
```

示例显式构造四个 Warp owner，混合整数乘法、AUIPC、FADD/FMUL、普通 load/store、
packed byte load 和 TMC。trace 为逐边沿 JSON，摘要输出到 stderr；预算不足返回
非零。库 Run 可按预算续跑。timing-run 使用同一个 scheduled Core/System，仅初始激活一个 Warp；
其 backend-cycles 参数配置外部后端，执行完成与显式 MakeVisible 分离。

`MultiRecord.Warps` 记录旧边沿前端 active/stalled/runnable、PC/mask、pending
宏指令数与 LSU receipt 数；inactive 也可能仍有待完成效果。`IssueCandidates`
记录 staging token、注册 eligibility 与当前 RAW/WAW/特殊状态/credit/lock
阻塞输入，`IssueSelected` 是仲裁选择，真正握手由 Issued.Valid 判断。
`Events` 包含资源驻留变化、读/执行/WB/反馈、wakeup、定向/epoch 取消和显式
restart。资源别名不能累加为指令数，外部服务延迟不代表 Cache 时间。

恢复 API：`Cancel(scope)` 取消有界年轻工作并暂停 Warp；`Restart(warp, context)`
使用显式前端上下文恢复并跳过已取消的身份上界，保留较老执行。仍有同 Warp
前端或控制工作时拒绝 Restart，防止旧反馈覆盖新上下文。`Flush()` 取消所有
未交付工作、增加 epoch，并从 live canonical owner 重建前端；不回滚已可见
效果，也不保证重放被放弃的旧指令。它是显式软件恢复边界，不是 RTL squash。

Barrier owner 可调用 `Release(token)` 排队注册唤醒；WSPAWN 通过
`MultiOptions.Spawn` 显式绑定目标 owner。未绑定或未释放保持阻塞，不扩展 CTA
或 Kernel orchestration。具体边沿和保留限制见最终验收映射。

T12 存储层级的参数推导、请求/响应、可见性与未决缺口见 [memory-contract.md](memory-contract.md)，结构化记录位于 `ir.yaml:memory_contracts`。


T12 当前运行与生命周期见 [交付记录](t12-delivery.md) 和 [Kernel 使用说明](kernel-usage.md)。
两个程序 CLI 使用 `-backend-cycles`；`-visibility-cycles 4000` 在执行完成后显式写回，
零预算默认不请求输出可见性。输出分别报告 execution_cycles、cycles、backing_visible。
执行预算或显式可见性预算不足均非零退出；库 API 则可保存状态继续调用。
