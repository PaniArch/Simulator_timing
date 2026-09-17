# 设备存储生命周期：第 1 Worker 交接

本记录只覆盖 persistent-device-lifecycle，不能代替整个里程碑验收。
已核对 runtime 四份记录、`emu/docs/architecture.md`、`timing/architecture.md`、
`timing/memory-contract.md` 与现有完整排空实现。仓库未找到单独的 Construction
Skill 或 Boundary 文件；遵守用户提供的 Worker 边界，不虚构额外契约。

## 所有权和边沿

`runner.NewKernel` 创建独立设备的第一份 memsys 和时钟。Cache bank 从真实逐 set
初始化开始，初始化通过现有 bank pipeline 推进，不预填充、不扣减经验周期。
`Kernel.NextLaunch` 是串行所有权转移接口：创建新的架构/CTA/Core 执行上下文，
接管同一 runnerMemory、memsys、backing 和 Clock。后续 launch 不调用 NewSystem，
保留 Cache 的初始化进度、数组、替换状态、外部后端和存储 transaction 序列。
独立 NewKernel / runtime Device 之间无共享可变状态。

前驱必须健康、执行结束、完整排空，且无进行中的 visibility / cache flush。
排空沿用上一里程碑的完整条件：组件（含 port 0 buffer）、held offer、store receipt、
待消费 completion、取消身份、fetch/data token 和 accepted 记录。额外核对 memsys
NextCycle 与 Clock 一致；协议错误禁止继续。错误不通过丢弃 memsys 自动恢复。
非法 launch 或构造失败不会转移所有权。成功后旧 Kernel 的 Run、MakeVisible、
FlushCaches、NextLaunch 均拒绝；Status 保存交接时的快照。调用者须串行使用 API。

`Identity.Kernel` 从 1 递增，LMEM resolver 同时校验 launch、CTA、generation、Warp。
resolver 仅在完整排空后的成功转移时改绑，原存储请求不能进入新 residency。
fetch/data transaction 序列保留，D/I flush control sequence 也保留且带新 launch ID。
物理 Warp/CTA 增量分配和完整逻辑 trace 字段属于后续 Worker。

`KernelStatus.Cycle` 和 MultiRecord 周期是设备连续边沿；StartCycle 是本 launch
开始边沿，LaunchCycles = Cycle - StartCycle（若随后显式 flush，也包括那些边沿）。
runtime 在执行结束处取 LaunchCycles 写 execution_cycles，flush_cycles 单独取差值；
两者没有累计前驱 launch 的周期。硬件 PERF/MPM 单位与导出留给第 3 Worker。

runtime 仍要求真实 D writeback 后 I invalidate 完成才允许下一次启动或 host 可见性。
runner 接口允许设备内部、无 host 改写的已排空 launch 直接续用；它不暗含 host
一致性、flush 或 invalidate。无 flush 时可命中保留行；真实 flush 后可重新 miss，
但不重新 reset Cache。功能模式行为不变。审计完成先于 Busy=false 的顺序不变。

## RTL 依据和限制

- `Vortex_rtl/hw/rtl/cache/VX_cache_flush.sv`：reset 进入 STATE_INIT；普通 flush
  从 IDLE 经 WAIT1/FLUSH/WAIT2/DONE，不能等同于 reset/init。
- `Vortex_rtl/hw/rtl/cache/VX_cache_bank.sv` 与现有 `timing/memsys/cache_bank.go`：
  逐 set 初始化和正常请求共享真实流水优先级。
- `Vortex_rtl/hw/rtl/core/VX_core.sv`：I flush req 依赖 D flush done，保留 D→I 次序。
- runtime trace 分析 §12.1 的跨 launch 重建根因在本步修复。首次 reset 到 host DCR
  配置再到 dispatch 的完整外部逐拍时间线仍无输入；本地时钟从首次模拟边沿开始，
  不臆造 host DCR 耗时，不宣称消除了首次 RTL 接受前的全部差异。
- 固定 100-cycle 外部服务契约、冻结 RTL、拓扑和验证脚本未修改。

## 回归

`timing/runner/device_lifecycle_test.go` 验证同一存储/时钟续用、暖 Cache 对照、
真实 flush 后第三 launch 的身份、新设备冷启动可重复、旧 owner 无法推进、
非法描述符/构造失败保留所有权及 completion/取消/transport/fault/flush 拒绝条件。
`integration/vortexruntime/device_test.go` 的两 launch 测试验证 store 写回后重新装载
同 VMA 的新代码，连续设备时钟、独立 execution_cycles 与旧 launch 快照。
既有 port_buffer_lifecycle 与 cache flush 回归继续保护真实 store/response 尾部。

原生 benchmark、RTLSIM 和外部资产未执行；本步不作端到端周期等价或里程碑全通过声明。
后续 Worker 需要完成增量 Warp 驻留、trace/PERF，以及跨层组合和全仓门禁。

补充回归 `TestDeviceMemoryDirtyAndLocal`：不经过 host flush 的内部 launch 保留 dirty
全局数据，而新 CTA 的 LMEM owner 独立初始化；最后真实 flush 将前驱 store 写回。
`kernel_observe.go` 的 HasResidency 使用当前 launchID，避免后继 CTA 漏查存储尾部。

本 Worker 验证记录：`go test ./timing/runner` 的设备生命周期、queued port-0
取消及 Kernel 执行/flush 选择集通过；新增 dirty/LMEM 跨 launch 用例单独通过。
`go vet ./timing/runner ./integration/vortexruntime`、`git diff --check` 通过。
`bash scripts/verify-timing.sh` 通过，成功 stdout 为空。
全仓 `verify-offline.sh` 留给唯一收尾 Worker，本步未宣称该门禁已重新通过。
`GOMAXPROCS=4 go test -race ./integration/vortexruntime -count=1` 全包通过
（243.627 秒），包括终态审计、并发 Busy、双 launch 及故障发布回归。
