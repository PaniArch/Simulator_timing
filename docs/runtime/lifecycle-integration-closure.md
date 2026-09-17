# device-and-warp-lifecycle 收尾验收

范围为冻结计划 v3 的 AC-010 至 AC-015；前两个里程碑 AC-001 至 AC-009
继续作为基线。本记录整合前三份 Worker 交接，逐项结论由当前源码和本轮离线
回归支持，不将历史 benchmark 结果计为本轮结果。仓库没有独立 Construction
Skill / Boundary 文件；采用用户给定 Worker 边界、`emu/docs/architecture.md`、
`timing/architecture.md`、`timing/memory-contract.md` 和冻结 RTL。

## 收尾发现与修复

核对 `VX_scheduler.sv` 的 `if (busy)`、`busy = busy_buf || cta_dispatcher_busy`
以及 `VX_cta_dispatch.sv` 的 `busy = (state == DISPATCH) || kmu_bus_if_fire`，
发现第三步的 Cycle 计数只接入 busy_buf，漏掉 CTA 接纳/分配期间的 busy。

`Kernel.Run` 现在将边沿前 DISPATCH 状态或本拍实际 CTA admission 传入
`MultiRunner` → `CoreInputs.DispatchBusy` → `instructionAccounting`。
计数使用布尔 OR，每拍最多加一；最后一个 Warp fire 仍计数，dispatcher 信号
不额外注册，也不产生人工尾拍。busy_buf 的原注册规则和 44 位回绕保持。
不改变模拟器 wall-clock、请求调度、外部响应或宏/uop 提交计数。
`TestIncrementalDispatchEdges` 检查实际生产分配前七拍的计数；
`TestCounterDispatchBusyIsCombinationalOR` 检查两路同时 busy 只计一次及无假尾拍。
这是接线修复，不使用 52、61、127 或其他经验补偿。

## 冻结验收映射

| 条件 | 实现与边界 | 离线证据 |
| --- | --- | --- |
| AC-010 设备存储生命周期 | `kernel.go:NextLaunch` 转移健康、完整排空的 memsys/Clock；runtime 仍要求 D/I flush 后才可 host 可见或下一启动；新设备独立构造 | `device_lifecycle_test.go` 的 Successor、TransferGuards、DirtyAndLocal；runtime 双 launch 与本轮 `TestLifecycleReuseTraceAcrossFlush` |
| AC-011 增量 Warp 分配 | `kernel_dispatch.go` 独立预留 CTA/LMEM、注册选择/fire、退休流水；`ResidencyMemory` 分离 Reserve/Bind/Detach；物理 Warp 与 CTA slot generation 独立 | IncrementalDispatchEdges、EarlyReuseAndNextLaunch、RetirementRegisteredEdges、IncrementalResidencyKeepsLogicalSizeAndLMEM；本轮带背压的双 launch 组合 |
| AC-012 跨层生命周期 | 每 Warp 完整排空才解绑；completion、取消身份、held offer、accepted、port 0、transport/store tail 均保留；失败 Kernel 禁止 transfer，显式独立新设备恢复 | 本轮两个 lifecycle_integration 测试；双 launch barrier/LMEM 交换；IncrementalReuseRetainsIdentityTails、IncrementalFaultRequiresNewDevice、DFlushQueuedRunnerCancellation、MemoryFaultResetAlignsCommittedEdges |
| AC-013 身份链 | Device/Launch + 真实 dispatch 的逻辑 CTA/rank、slot generations + macro/epoch/uop；adapter parent/Batch 与 Cache wire fragment 关联 | KernelTracePackedReuseAndCounters；本轮在早退复用、store tail 和双 launch 中检查 transfer、store application、completion；FragmentTraceIsObservational |
| AC-014 PERF/MPM | 累计 44 位 scheduler busy Cycle 和 Warp/EOP Instret；原生 DCR tag 高低字读取；未知统计报错；宏 Retired 单列 | accounting 单测、实际分配 busy 接线测试、performance_test.go、本地 `test-mpm-abi.py`；新增 MPMPublicationWaitsForAudit |
| AC-015 冷/稳态启动 | 首设备实际逐 set init；后继 memsys 沿连续时钟续用；flush/invalidate 不变成 reset；dispatcher/busy 沿真实控制推进 | DeviceMemorySuccessor 的冷/暖及独立冷设备对照；真实 flush 后续用；分配与 busy 逐边沿断言 |

## 新增组合覆盖

`timing/runner/lifecycle_integration_test.go:TestLifecycleReuseTraceAcrossFlush`
使用默认 100-cycle 后端及每三拍一次的请求 ready。第一 CTA rank 0 的长依赖链
仍执行时，第二 CTA 使用已安全结束的物理 Warp；同时观察实际 store tail。
每个接受片段、store application、completion 必须匹配当前真实 dispatch binding，
adapter→Cache 的 wire identity 与 payload 必须相等且一次消费。Store 不分配
coalescer 响应槽，不能要求其 Batch generation 非零；其 wire transaction 仍唯一。
两个 launch 用不同输出区域，检查全部逻辑线程值、TLS 一次初始化、旧输出不变、
旧 launch LMEM identity 拒绝，以及同 memsys/Clock/累计计数续用。

`TestLifecycleCancelledPortTailThenNextLaunch` 在真实 load 已进入 port-0 注册
缓冲时取消该 Warp 的已发出工作。取消后不能报告 Warp quiescent 或转移 Kernel；
显式 Restart 跳到终止指令，旧响应只能排空，不写寄存器。真实 flush 后修改输入，
后继 launch 复用同一 hierarchy 并读到新值。此用例使用已有低层 Cancel/Restart
软件恢复接口，不新增 native reset 或故障自动重试语义。

`kernel_barrier_test.go` 的 backpressure 场景扩展为两次 launch：每次四个 CTA、
两个 Warp、两轮 LMEM 交换与 barrier；D/I flush 后更改输入，验证新的输出和
32 次真实唤醒，防止残留 phase、LMEM owner 或 Cache 数据冒充新结果。
既有异步 arrive、expect_tx、资源配额、旧 generation 拒绝和故障测试继续运行。

`integration/vortexruntime/performance_test.go:TestMPMPublicationWaitsForAudit`
在两次 launch 的 launch-finish 和 cache-flush 审计 Close 内阻塞。此时并发 Busy
保持 true、MPM 拒绝、LastRun 保留前一份 detached 快照；解除后审计与公开快照
逐字段相同，MPM 与累计 Instret 一致。MPM 查询不修改执行或审计错误状态。

## 必须保留的接口与证据边界

- `NextLaunch` 是串行 ownership transfer；旧 Kernel 不可推进。只检查硬件队列
  为空不足以放行，软件 completion/cancel/transport 尾部仍是必要条件。
- 正常 CTA 释放仍保护 barrier、LMEM/TLS 和 generation；不以全 Core drain
  替代局部安全复用。故障 hierarchy 不自动丢弃重建以伪装成功。
- Runtime execution_cycles 是每 launch wall-clock；flush_cycles 单列；硬件
  Cycle/Instret 是设备累计值，不能再次按 launch 求和，Instret 也不是 lane 计数。
- Trace 的 Batch 对 load 有响应槽含义，store 使用 adapter wire transaction
  关联；外部 writeback 不能伪造与单条 ISA 指令的一对一关系。
- AC-001～AC-009 由全仓回归继续保护：async arrive 的 activation/epoch gate、
  batch provenance/背压稳定性、审计先于 idle、缺失证据 unknown、完整排空，
  以及 `timing/check/dflush_contract.go` 的真实 port-0 接线检查。
- 冻结 RTL、IR、拓扑、默认 100-cycle 服务及两条门禁脚本未修改；不放宽原有
  周期断言、timeout 或响应稳定性检查。宿主吞吐优化留给后续里程碑。

## 验证结果

使用已安装 `env/env.sh` 的 Go 1.26.2、vendor-only、禁网环境：

- `bash scripts/verify-timing.sh`：exit 0，成功 stdout 为 0 bytes；IR/RTL 和
  生产 port-0 witness 通过。
- `GOMAXPROCS=4 go test -race ./integration/vortexruntime -timeout 8m -count=1`：
  最终全包通过（412.874 秒），没有 race 报告。新增 publication 测试最初使用的
  局部 30 秒墙钟等待不适用于 race 开销，现按审计 channel 事件同步，缺失事件
  仍受原包级有限 timeout 约束；未改模拟预算、原有断言或门禁 timeout。
- 本地 `go build -buildmode=c-shared` 和 `test-mpm-abi.py`：通过，实际 C ABI
  双 launch、packed EOP、flush 及 unsupported counter/output 保留均验证。
- 新增 trace/提前复用/双 launch 定向组合通过（43.945 秒）；barrier/LMEM
  backpressure 双 launch 通过（42.636 秒）；计数接线与取消后后继 launch
  选择集通过（runner 21.024 秒）。这些测试也纳入全仓门禁。
- `python3 scripts/test-vortex-supported.py`：6 项通过，保护缺失、错序、重复
  终态不能汇总为 PASS；不表示实际 56 项 benchmark 已重跑。
- 受影响三包 `go vet`、`git diff --check` 通过；冻结 RTL、IR 及两条验证脚本
  对当前交接基线无差异。

- `GOMAXPROCS=4 bash scripts/verify-offline.sh`：exit 0；空缓存的 list/build/test/vet
  全部通过。runner 全包 805.949 秒，memsys 63.317 秒，model 77.156 秒，
  effects 117.286 秒，runtime 普通测试 57.286 秒。最终事件同步版 runtime
  测试另由上述全包 race 验证；没有改动冻结门禁或扩大其 timeout。

AC-010 至 AC-015 的本地实现与离线里程碑验收完成，可交接后续既定里程碑。
下述外部实验限制继续保留，不将尚未取得的证据标为 PASS。

## 外部限制

未提供兼容原生 runtime/benchmark/RTLSIM 和 raycast 外部资产，本轮未运行原规模
benchmark、RTLSIM 或图像 oracle。离线回归与本地 c-shared ABI 验证不替代这些实验。
首次 device reset、host DCR 配置与 Cache init 重叠的完整外部时间线仍缺证据；
本地时钟从首个模拟边沿起算。固定 100 后端与 Ramulator 的系统级差异单列，
不用于校准内部边沿或承诺全链路周期等价。其他既有 UNRESOLVED（冲突 memory
ordering、异常 barrier teardown 等）不因本里程碑而升级为硬件事实。
