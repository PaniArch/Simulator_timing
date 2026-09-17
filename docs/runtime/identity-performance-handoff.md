# Trace 身份和硬件计数：第 3 Worker 交接

本记录只覆盖 identity-and-performance-counters（AC-013、AC-014），不是整个
里程碑验收。仓库没有独立 Construction Skill / Boundary 文件；沿用用户冻结的
Worker 边界、功能/时序架构契约、memory contract 及前两步交接。冻结 RTL、IR、
默认 100-cycle 外部服务、验收脚本均未修改。未实施宿主吞吐优化。

## 身份字段与关联方法

`Kernel.Run` 的 `MultiRecord` 和 `Kernel.TakeEvents` 的 `KernelEvent` 带
`DeviceID`、`LaunchID`。DeviceID 是进程内生成的纯观测命名空间，新建独立 Kernel
设备不同，NextLaunch 保留；不参与调度、仲裁或 memsys 的状态查找。跨进程合并
trace 还需使用文件/运行 ID，比较两次运行时可重命名 DeviceID，不能要求数值相同。
`KernelStatus.DeviceID` 和 runtime summary 的 `trace_device_id/trace_launch_id`
将原生 launch 审计与此命名空间关联。

每条 Kernel `MultiRecord.Bindings[wid]` 是该边沿的 detached value：

| 字段 | 含义 |
| --- | --- |
| Valid | 已真实 dispatch 的 binding；空槽或仅 selected 的新成员为 false |
| CTA / Rank | GridWalker 逻辑 CTA ID / CTA 内逻辑 Warp rank |
| Slot / CTAGeneration | 物理 CTA slot / 该 slot 的 generation |
| WarpGeneration | 物理 wid 的本次 binding generation |

绑定直接读取唯一 ResidencyMemory/Kernel 分配状态，并由 `warp-dispatched`
事件验证；不通过 PC 次数或物理 wid 顺序猜测逻辑身份。`Token.Warp` 索引 Bindings；
`Token.ID, Epoch` 是宏指令身份，`Token.Uop` 是展开序号，Mask 是实际活动 lane。
这些字段适用于 Report、Events、Finished、ResourcesAfter、Services 和 Cancelled
中的 token。`Finished`/`Retired` 仍是宏指令效果收据，`Report.PendingRelease`
是注册硬件 EOP 通知；packed load 的每个 uop 都可有 End/EOP，不能仅以 End 判断
宏指令已完成。直接使用 NewMulti 的低层 API 没有 Kernel 逻辑 CTA，Bindings 无效。

`Options.TraceMemory=true` 开启 `MultiRecord.Memory.Transfers`；关闭时不建立
fragment 事件数组，不改变握手。`Memory` 仍提供真实响应、store application、
complete 和独立 adapter/cache 接受位。`memsys.Transfer` 记录实际接受而非 offer：

- `fetch-cache`：取指 word 的 Identity/Tag。
- `global-adapter`：Parent 是原 SIMD Identity，Batch 是 coalescer slot+generation；
  Request.Identity/Tag 是 adapter 的 wire transaction，Port 是 word fragment。
- `data-cache`：使用同一个 wire Identity/Tag/Port 关联前述 adapter 事件，可能晚于
  adapter 接受（port 0 的两槽注册缓冲），也可能同边沿（port 1）。Parent/Batch
  留空表示在 adapter 事件中查找，不从当前 coalescer batch 错配旧队列请求。
- `local-memory`：Parent 是原 SIMD Identity，Port/LaneMask 标识 local lane fragment。

内存 Identity 的 Kernel 对应 LaunchID，CTA 是物理 CTA slot（不是逻辑 CTA），
WarpGeneration 对应绑定；Token/Epoch/Subrequest 对应宏 ID/epoch/uop。Transaction
必须连同边界命名空间使用：fetch、SIMD parent、global adapter、local adapter
各有独立序列。Request 含地址、byte enable、读写/flush 属性及数据，可核对身份相同
时的实际负载。SystemEdge 的最终响应/Stores/Complete 返回 parent 身份，可与
上述接受链关联。Cache miss 合并或替换产生的外部 sector/writeback 不是新的 ISA
指令，本接口不伪造它们与单一宏指令的一对一关系，也不把它们计入 instret。

所有 trace 值均不持有可写 owner。物理复用继续要求完整局部排空：completion、
取消身份、held/accepted 请求、port 0、transport 和 store tail，不能移除此约束
来使绑定快照“看上去”成立。生成、选择、dispatch、release 事件沿用第 2 Worker
已核对的真实分配边沿。

## 计数 owner、边沿与原生路径

唯一 owner 仍是 `timing/model/instructionAccounting`：

- `Cycle`：44 位 `VX_scheduler` busy-qualified counter；使用旧 busy_buf 寄存器
  与当前 CTA dispatcher busy 的 OR 计数，不是设备 wall-clock，也不是
  execution_cycles 的别名。dispatcher 接线由收尾核对补齐，见
  [收尾验收](lifecycle-integration-closure.md)。
- `Instret`：44 位 committed Warp/EOP 通知累计值；不是 active lane 总数，不是
  软件宏指令 Retired。packed byte load 每宏 4 EOP，half load 每宏 2 EOP。
- `MultiRecord.Counters` 是旧边沿 CSR view；`Kernel.Counters()` 是调用时已提交
  状态。runtime 在执行终态保存后者到 `hardware_counters`，功能模式为 null。
- `Core.ContinueCounters` 只向新 launch pipeline 转移累计值及 busy 寄存器；拒绝
  未排空 issued/pending。NextLaunch 不清零统计，独立 NewKernel 从零开始。
  旧 Kernel 保存自己的计数快照，后继推进不会反向修改它。
- 显式 FlushCaches / MakeVisible 在真实 memory-only 边沿调用 ClockIdleCounters。
  最后一个 busy 寄存器尾部可能在第一拍累加一次，随后空闲拍不累加；没有固定补偿。
  Instret 不变。runtime 的 cache-flush 审计发布更新后的硬件快照。
- execution_cycles/flush_cycles 仍按前一步的 launch/device wall-clock 边界记录，
  与硬件 Cycle 并列，不互相替换。多 launch 的累计硬件快照不得再次求和。

RTL 依据：`Vortex_rtl/hw/rtl/core/VX_scheduler.sv` 的 instret、
committed_warps_cnt_v、busy_buf 和 cycles；`VX_commit.sv` 的 EOP 通知；
`VX_csr_data.sv` 的 MCYCLE/MINSTRET 与 CSR_READ_64；`VX_dcr_data.sv` 的
mpm_target_cid/tag_idx/class；常量与 `Vortex_rtl/VX_types.toml` 互证。

原生调用链保持 ABI 不变：
`integration/vortex-runtime/native/vortex.cpp` 的 vortex_dcr_read hook →
`cmd/simtiming-go/main.go:simtiming_dcr_read` → `Device.ReadDCR(0x001, tag)` →
`performance.go:readMPM`。tag[15:0] 是 core（冻结仅 0），[21:16] 是 index，
[29:22] 是 class；index[5] 选择高字，index[4:0] 选择 B00/B80 起的 CSR。

支持 slot 0 cycle、slot 2 instret 的高低字，class 不影响这两个 base CSR；
高字限于 12 位。BASE class 的 user window 按冻结 RTL 明确返回零；reserved
slot 1、未实现的 PERF class/slot、非零 core 和无效 tag 返回错误，不能冒充零。
功能模式没有硬件统计，MPM 返回 unavailable。C ABI 返回 -1 且不覆盖 output；
原生 hook 会走既有 transport-error 通道，因此调用方不能把 unsupported PERF
请求当成成功。原生第三方 host 如何展示 unavailable 仍需外部 runtime 验证。

读取要求 idle/open/healthy；运行或 flush 中拒绝，不读取正在异步变化的 Kernel。
readMPM 不修改 Busy/busyLatch、执行错误或审计，LastRun 对计数指针做深复制。
有效的 MPM 读取不推进模拟时钟、不触发 flush、不重复写终态审计。首次 launch 前
base 计数有真实 reset 零值；未知统计则明确错误。未模拟原生 DCR 读取传输本身的
周期，不把它补入 kernel timing；host reset/DCR 与首次接受的外部时间线仍未闭合。

## 回归与交接约束

- `timing/runner/kernel_trace_test.go:TestKernelTracePackedReuseAndCounters`：
  3-lane packed load，6 个 CTA、相同 PC、实际物理复用、两个 launch；逐事件核对
  dispatch binding、generation、macro/uop、Batch、adapter→port-buffer→cache
  身份；每 launch 30 宏与 48 EOP，24 uop 内存身份；真实 flush 和独立设备。
- `timing/memsys/trace_test.go:TestFragmentTraceIsObservational`：相同输入下 trace
  开/关的所有 SystemEdge（排除观测数组）与 Drained 完全相同，含 mixed local/global
  fragment 和响应背压，保护真实接受边沿。
- `timing/model/accounting_test.go:TestCounterContinuationAndIdleTail`：累计值、
  busy 注册尾部、44 位回绕、live pending 拒绝；既有 CSR/EOP 测试保持。
- `integration/vortexruntime/performance_test.go`：tag/core/class/高低字、显式未知、
  LastRun 深复制、读取无副作用；原生 Go adapter 双 launch、packed 宏/EOP、flush。
- `integration/vortex-runtime/test-mpm-abi.py`：针对本地 Go c-shared 产物，实际
  ctypes C ABI 双 launch、packed EOP、flush、高字和 unsupported error/output 保留。
  不需要外部 Vortex runtime；不等于原生 benchmark 通过。

复验方式（使用已安装离线环境）：

```bash
source env/env.sh
go test ./timing/memsys ./timing/model -count=1
GOMAXPROCS=4 go test ./timing/runner -timeout 20m -count=1
GOMAXPROCS=4 go test -race ./integration/vortexruntime -timeout 8m -count=1
go build -buildmode=c-shared -o "${SIMULATOR_RUNTIME_ROOT}/libsimtiming-mpm.so" ./cmd/simtiming-go
python3 integration/vortex-runtime/test-mpm-abi.py "${SIMULATOR_RUNTIME_ROOT}/libsimtiming-mpm.so"
bash scripts/verify-timing.sh
```

验证结果记录在下方。完整 verify-offline.sh 与 AC-010～AC-015 的最终跨层组合验收
仍由唯一 closure Worker 执行。原规模 benchmark、RTLSIM、raycast 外部资产未提供，
未运行这些实验，不宣称端到端周期等价，不用固定 100 与 Ramulator 差异校准内部边沿。

已完成验证（2026-09-16）：

- memsys 全包通过（78.460 秒）；model 全包通过（86.646 秒）。后续将 busy 尾部
  测试改为直接调用公开 ClockIdleCounters API，定向复验通过（9.225 秒）。
- packed/reuse/双 launch trace 定向用例通过（24.051 秒）；既有 DeviceMemorySuccessor
  无 flush 续用测试通过（26.123 秒）。
- runtime MPM 定向回归通过（13.821 秒）；runtime 全包 race 通过（334.926 秒），
  包括终态审计、并发 Busy/LastRun、双 launch 和新增 MPM 读取，未报告竞态。
- 本地 Go c-shared 构建及 test-mpm-abi.py 通过。
- verify-timing.sh 通过；另捕获成功 stdout，确认为空。相关包 go vet 与
  git diff --check 通过；冻结 RTL、IR 及两条验收脚本无改动。
- runner 全包通过（742.557 秒，`GOMAXPROCS=4 go test ./timing/runner -timeout 20m
  -count=1`），覆盖既有 async barrier、增量 Warp/设备生命周期、port-0 取消/排空、
  失败恢复和真实 flush，以及新增身份与累计计数测试。未调整既有周期断言或超时。

第 3 Worker 范围可交接；下一步保持冻结顺序交给 lifecycle-integration-closure。
收尾须保留上述 identity 命名空间和累计计数口径，执行完整 verify-offline.sh，
并核验 AC-010～AC-015 的组合证据。本文不声明整个里程碑完成。
