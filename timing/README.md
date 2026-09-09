# 初版静态 Timing IR

本目录按 [Task/T8.md](../Task/T8.md) 建立冻结 Vortex 配置的静态时序基线。

T9 当前增量进度见 [implementation.md](implementation.md)：已建立 IR 参数读取、Akita 统一边沿及前端、操作数、执行资源、Commit 组件；完整连接、effects、程序运行器和驻留观测均已实现。下述 T8 交付记录是历史基线。

- [architecture.md](architecture.md)：抽象层级、维护约定、结构解释、功能衔接与未知项。
- [rules.md](rules.md)：周期计量边界、复杂阻塞/反馈与可追溯示例。
- [ir.yaml](ir.yaml)：配置、来源、节点、资源、端口、通道、缓冲边界与规则索引，结构化数值的唯一维护位置。
- [功能契约](../emu/docs/architecture.md)：指令结果、canonical state ownership、effect 与已有 `RESOLVED` 审计的唯一契约。

T8 历史交付覆盖 `timing-baseline`、`timing-rules` 与 `timing-integration-validation`：可审查的静态结构和局部周期规则，不包含周期执行器、Scheduler、cache 实现或 RTLSIM 对齐。定量条目均限定起止事件；剩余外部后端、变量等待与完整跨组件同时事件验证仍为未决；YAML 中未定值显式为 `null` 并关联未知项。离线 Akita 依赖可供后续实现使用，本阶段不固定其调用组织。

修改结构化事实时，在 YAML 更新值、状态与证据，再更新 Markdown 中对应稳定 ID 的解释。不得把未启用 RTL 分支或功能 Step 次序当作时序事实。

阅读顺序：先完整阅读功能契约，再读 architecture → ir.yaml 的来源/结构 → rules → [integration.md](integration.md) 的职责、复用评估与未决索引。三个静态 IR 里程碑均已形成交付；后续实现仍须按 unknowns 的 deadline 闭合相关缺口。

在仓库根运行 `bash scripts/verify-timing.sh`：脚本加载 `env/env.sh`，使用固定 Go 工具链及 vendor 中的 `go.yaml.in/yaml/v3`，运行 [专用检查器](check/main.go) 和 [无效样例测试](check/main_test.go)。检查 YAML 语法/重复键、重复 ID、类型化引用、来源文件、状态、定量单位与起止事件、null 未知项及功能问题引用；失败返回非零。样例从真实 IR 内存副本分别破坏单一约束，不修改冻结输入、不下载依赖。

完整回归另运行 `bash scripts/verify-all.sh`，最后运行 `git diff --check`。现有完整门禁保持原范围，Timing 专用门禁单独执行；结构检查与功能回归都不能证明动态时序等价或所有 RTL 结论正确。

本次交付验证记录：`test -s timing/integration.md`、`bash scripts/verify-timing.sh`（含 15 个无效样例）、`bash scripts/verify-all.sh` 和 `git diff --check` 全部通过。完整门禁涵盖 RTL manifest、固定环境、`go mod verify`、功能 build/test/vet，以及独立空缓存 vendor build/test/vet。环境提供方补齐固定模块缓存后，原先的离线元数据缺失阻塞已解除。按授权仅调整 `scripts/verify-offline.sh` 的临时目录创建与位置，使用 `SIMULATOR_RUNTIME_ROOT`；保留空缓存、网络限制、断言和失败退出行为。未下载依赖或修改冻结 RTL、现有功能语义。

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
go run ./cmd/timing-multi -cycles 1000 -fetch-cycles 2 -memory-cycles 60
go run ./cmd/timing-multi -trace -cycles 1000 -fetch-cycles 2 -memory-cycles 60
bash scripts/verify-timing.sh
bash scripts/verify-all.sh
```

示例显式构造四个 Warp owner，混合整数乘法、AUIPC、FADD/FMUL、普通 load/store、
packed byte load 和 TMC。trace 为逐边沿 JSON，摘要输出到 stderr；预算不足返回
非零。库 Run 可按预算续跑。原 timing-run 保留单活动诊断入口。

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
