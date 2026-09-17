# 增量 Warp 驻留：第 2 Worker 交接

本记录覆盖 `incremental-warp-residency`，不是整个里程碑验收结论。
仓库未提供独立 Construction Skill / Boundary 文件（`.agents`、`.codex` 无文件）；
使用用户冻结的 Worker 边界，以及功能/时序架构契约和上一 Worker 的设备交接。
没有修改冻结 RTL、拓扑、100-cycle 默认外部服务或验收脚本。

## 所有权与 API

`emu/core/ResidencyMemory` 仍唯一持有 CTA metadata、Warp→CTA/rank 和 LMEM。
原 `Admit` 的完整绑定语义保留，新增 `Reserve`、`BindWarp`、`DetachWarp`：

- Reserve 校验完整 launch/rank 模板、占用固定 stride LMEM 和 CTA slot，但不占物理 Warp。
- BindWarp 将实际 wid 绑定到指定逻辑 rank；每 rank 在该 reservation 内只能绑定一次。
  mask/坐标从原功能方程生成，CTA CSR Size 始终是完整 block 所需 Warp 数。
- DetachWarp 只删除一个物理绑定，保留 CTA metadata/LMEM 和其他成员。
  pending barrier（包括 expect_tx、地址 Warp、到达和 waiter）禁止解绑。
  调用者必须先证明该 Warp 的流水、效果和存储完全排空。
- Release 只释放该 CTA 仍持有的绑定，不能清除已复用于别的 CTA 的 wid。
  LMEM 仍是同一 SRAM 数组；不在每次绑定时清零。

功能 Core 的完整 CTA admission/reclaim API 未改成增量调度。
`CTAManager.ViewForWarp` 根据逻辑 rank 查找当前成员，不再假定 rank 等于 slice 下标。

## Kernel 状态机与 RTL 边沿

生产路径在 `timing/runner/kernel_dispatch.go`。冻结依据是
`Vortex_rtl/hw/rtl/core/VX_cta_dispatch.sv` 的 IDLE/DISPATCH、`warp_fire_r`、
`dispatched_warps`、`slot_valid_r`、`rem_warps_ram` 和 `rem_warps_write_r`；
`VX_scheduler.sv:cta_warp_done` 由 TMC 零 mask 驱动。

1. IDLE 接受一个 CTA 并预留 LMEM/slot；不再等待足够容纳整个 CTA 的 free Warp。
2. 下一边沿进入 DISPATCH，按最低可用 wid 选择，锁存一个 offer。选择时绑定 metadata，
   该成员尚未执行；`selectedMask` 保留本 CTA 已选择的 wid，防止同一 CTA 重复选择。
3. 再下一边沿消费注册 offer，调用原 DispatchWarp，更新 canonical owner/scheduler；
   可同时选择下一个 wid。无背压时连续边沿每拍 dispatch 一个 Warp。最后 fire 后返回
   IDLE，下一边沿才接受后续 CTA。首个接受为 cycle 0 的局部 witness 中 dispatch 为
   cycles 2、3、4、5；这些值来自注册状态推进，不是经验 dispatch latency。
4. 每个 TMC 零 mask 捕获 CTA slot、slot generation、逻辑 rank，经过两级退休寄存
   状态后设置 RetiredRanks；下一拍 table write 占用期间阻止新 CTA accept。
   回收还要求所有 rank 已分配/退休、barrier 清空、完整存储和效果排空。
   Kernel 完成也等待退休寄存和最后 write 尾部；不能在无 active Warp 时停止时钟。

物理槽独立检查 `MultiRunner.WarpQuiescent`，允许原 CTA 其他 Warp 继续执行。
选择新的 CTA 成员时，先从旧 CTA Detach，再绑定新逻辑 rank。尚未被其他 CTA 使用的
inactive 成员仍保留原 membership，兼容现有同 CTA WSPAWN；被别的 CTA 使用后不允许
旧 WSPAWN 跨 owner 激活。WSPAWN 成功会清除目标 rank 的退休位，等待重新结束。
未 fire 的选择不进入 spawn target pool。

CTA slot/LMEM 的回收可以晚于 RTL active-bit 释放，因为软件还必须保护不可撤销的
transport、completion 和 barrier owner；这不是额外的固定周期补偿。退休流水保存
逻辑身份，不在延迟到达时从已复用 wid 反查旧 CTA。stale retirement 显式报错。

## 安全复用与身份

`runnerMemory.warpPending` 现显式纳入 held fetch/data offer、accepted、cancelledFetch、
cancelledData、待消费 completion、fetch/data token 和 applied-store receipt。
不能依赖这些 maps 恰好始终与某个 token map 共存来证明可复用。原 port 0 缓冲、
coalescer/adapter/Cache、延迟响应及错误处理继续由生产 memsys/token 生命周期保护。
parked cancellation 不可自动作为 free Warp，也不能提前回收 CTA。
协议或执行故障仍停止 Kernel；本 Worker 没有新增丢弃 memsys 的自动故障恢复接口。
已有 MultiRunner 显式 Flush/Restart 的 epoch 及失败边沿恢复契约保持。

`Kernel.generations[slot]` 仍是 CTA slot generation；新增
`warpGenerations[wid]` 是物理 Warp 每次新 binding 的 generation。memsys Identity 的
WarpGeneration 现在取后者；LocalOwner 同时检查 launch、CTA slot、wid 和物理 generation。
同一 launch 的 token 序列不重置。跨 launch 仍由上一 Worker 的 NextLaunch 转交同一
memsys/连续时钟，使用递增 launchID 隔离；新 Kernel 的物理 generation 可重新从 1 开始。

`KernelCTA.Dispatched` 记录真正 fire 的 rank 数，`RetiredRanks` 是逻辑 rank 位图。
`Resident.Members` 只包含当前绑定（可能为空、部分绑定或包含下一拍 fire 的选择），
不能再假定 admitted 就意味着所有 Warp 正在执行。StoppedWarps 不把未 fire 成员算结束。

## 交给 trace / PERF Worker 的事件

`KernelEvent` 保留 generated/admitted/reclaimed，并新增 warp-selected、warp-dispatched、
warp-released。admitted 是 CTA/LMEM 接受，Warps=0；真正的执行起点是 warp-dispatched。

所有事件包含设备连续 Cycle、LaunchID、逻辑 CTA（GridWalker ID）、Slot（物理 CTA slot）、
Generation（CTA slot generation）。逐 Warp 事件另含 Warp（物理 wid）、Rank（逻辑 CTA
内 rank）、WarpGeneration；Warps 是对应物理位图。generated 的 Slot=-1。
warp-released 出现在单独解绑及最终 CTA 回收两条路径。事件是 detached value，TakeEvents
消费后不影响执行。后续必须由这些真实 binding 事件关联 token/uop/fragment；本步未实现
完整 trace schema 或 PERF/MPM 导出，不可把本步事件计数当硬件指令计数。

## 回归与证据

- `emu/core/residency_test.go:TestIncrementalResidencyKeepsLogicalSizeAndLMEM`：部分绑定
  的完整 Size、非连续 rank、重复绑定拒绝、LMEM 配额/独立字节、barrier 地址保护，及旧 CTA
  Release 不破坏新 binding。
- `timing/runner/incremental_residency_test.go:TestIncrementalDispatchEdges`：注册选择、
  连续单 Warp fire，以及没有 free Warp 时仍可接受后续 CTA。
- `TestIncrementalEarlyReuseAndNextLaunch`：4-Warp CTA 的 rank 0 长依赖链，其他 rank
  提前结束并让下一 CTA 开始；实际 store tail、LMEM store/load、物理 TLS 一次初始化、
  全部逻辑线程输出，真实 D/I flush 后 NextLaunch 再次执行并核对 backing。
- `TestIncrementalReuseRetainsIdentityTails`：独立 completion、cancel、accepted、held
  offer、transport、parked、fault 的负向复用门槛，清除尾部后局部继续分配，旧 generation
  的 LocalOwner 拒绝。此测试直接注入尾部；真实取消传输由既有 port buffer 回归覆盖。
- `TestIncrementalRetirementRegisteredEdges`：真实 TMC 到退休 rank 位之间恰好两级边沿，
  不能绕过退休 RAM 状态回收 CTA。
- `TestIncrementalFaultRequiresNewDevice`：延迟 refill 故障后原 Kernel 不再分配或转移，
  显式创建独立新设备可恢复执行，不复用故障 hierarchy。
- 既有 Kernel barrier、异步 barrier、WSPAWN、混合访存、cluster prewrap、设备生命周期、
  真实 port-0 取消/延迟响应与失败边沿恢复继续回归。

`kernel_mixed_lifecycle_test.go` 只调整了观察端对空 Members 的处理，保留原输出、尾部、
复用与性能关系断言；没有批量修改周期容差或 timeout。

已完成的命令和最终结果在本文末尾记录。完整 verify-offline 和里程碑跨层收尾仍归
唯一 closure Worker。没有运行原生 benchmark、RTLSIM 或依赖外部资产的实验；本地
边沿测试不代表全链路 RTL 周期等价，也不以固定 100 与 Ramulator 差异校准模型。

已完成验证（2026-09-16，所有命令均使用仓库 env/env.sh 的离线已安装工具链）：

- `go test ./emu/core -count=1` 全包通过；新增 partial residency 测试通过。
- Kernel / DeviceMemory 原有回归集通过（289.863 秒）。退休流水和最终完成尾部调整后，
  再次执行 Incremental、KernelSpawn、KernelBarrier、KernelRepeated、DeviceMemorySuccessor
  选择集通过（170.625 秒）；最终尾部门槛选择集通过（56.365 秒）。
- `TestIncrementalFaultRequiresNewDevice` 通过（19.314 秒）。
- `GOMAXPROCS=4 go test -race ./emu/core ./integration/vortexruntime -run
  'TestIncrementalResidency|TestNativeLaunchCompletesAndWritesMemory' -count=1` 通过，
  runtime 用例 84.495 秒，未报告 race。
- `bash scripts/verify-timing.sh` 通过；成功 stdout 为空，诊断输出在 stderr。
- `go vet ./emu/core ./timing/runner`、`git diff --check` 通过。
- `GOMAXPROCS=4 go test ./timing/runner -timeout 20m -count=1` 全包通过
  （688.603 秒），覆盖既有异步 arrive、queued port-0 load/store/FENCE 取消、
  epoch Flush、延迟响应及故障恢复。最后的门槛补项另由上述定向集验证。

下一步保持既定顺序交给 identity-and-performance-counters；其后由 closure Worker
执行完整离线门禁与跨层验收。本 Worker 没有修改验证来源或任何冻结验收条件。
