# 原生 Vortex runtime：功能型与周期型接入

## 接入路径

参照 `Simulator_dev/docs/vortex-runtime-integration.md`，复用同一工作区 Vortex 的
`libvortex.so`、CommandProcessor 和现有 host benchmark，不重新实现 module loader、
队列、event 或命令编码。

```text
host benchmark → libvortex.so → 原生 CommandProcessor
  → libvortex-simtiming.so → libsimtiminggo.so → vortexruntime.Device
      ├─ timing（默认）：timing/runner.Kernel → 原有 L1/LMEM → 外部后端 → Sparse
      └─ functional：emu/device.KernelExecutor → Sparse
```

两种模式使用独立 Device 实例内的一份规范 RV32 内存映像。新增 `support/memory.Sparse`
沿用 Simulator_dev 的实现：4 GiB 逻辑空间、按需分配 4 KiB 页、未写入字节重复
`0xbaadf00d`、范围检查及原子 WriteBatch。原有稠密内存没有改动。

新驱动名称为 `simtiming`，不与 Simulator_dev 的 `simdev` 混用。
`SIMTIMING_MODE=timing|functional` 在 Device 创建时确定；非法值拒绝，不静默回退。

## 100-cycle 外部服务与 Cache 边界

已核对 `timing/ir.yaml` 的 `mc-backend` 及 `timing/memsys/backend.go`：

- 默认 latency=100、accepts_per_cycle=1、max_inflight=16、returns_per_cycle=1。
- 接受于 cycle C 的请求在 C+100 执行 backing 读写；返回还可能受带宽或背压阻塞。
- 排队等待发生在接受之前；Cache 检测 miss、bank 仲裁、refill 和返回路径并不包含在这 100 cycles 内。
- 因而“整个 miss 恰好耗时 100 cycles”不成立；100 是 L1 以下外部服务时间。

runtime 周期入口读取 IR 的带宽/容量参数，并显式选择外部 latency=100。
新增单测用 Sparse 后端在 cycle 7 接受、cycle 107 返回实际数据，确认不会直接立即完成。
只替换外部后端的字节存储 owner；未修改 Cache、MSHR、coalescer、LMEM、LSU、流水线、
调度器或冻结 RTL，也没有叠加旧 FetchCycles/MemoryCycles。

## launch、flush、可见性与错误

原生 CP 的 KMU DCR 映射到已有 `LaunchState`，仍由原有 ValidateLaunch 校验。
非零高地址字、非法 descriptor 等归类为 `external-connection`。
Start 异步运行，首个 Busy 查询至少返回一次 true，避免短 Kernel 被 CP 漏观察。
功能型按 instruction-attempt budget 续跑；周期型按 cycle budget 在同一 Kernel 对象上续跑。

周期型执行结束仅解除 execution busy，**不**立即将 dirty Cache 复制到 backing。
原生 runtime 在 launch 后发送 `CMD_CACHE_FLUSH`，CP 对 core 0 发 DCR read：
适配层调用现有 `Kernel.FlushCaches`，实际推进 D 写回、I 失效及外部响应，完成后才应答。
JSONL 分别记录 execution_cycles 和 flush_cycles，backing_visible 仅在真实刷新成功后置位。
未刷新或已失败的旧 Kernel 不允许被新 launch 替换。功能型 flush 是统一内存语义下的空操作。

进入原有执行器后发生的 decode、ISA、存储组件、控制协议异常归类为 `simulator-internal`；
不自动重试、不切换成功模式、不修改主体设计来掩盖问题。失败应保留事件及单独登记。
C ABI 使用整数 handle，不长期暴露 Go 指针；失败的 busy destroy 不移除 handle。
C++ CP 只访问已登记 host region 或受 RV32 范围检查的设备内存。

## 构建与小规模验证

在 Simulator_timing 根目录：

```bash
./scripts/build-vortex-runtime.sh
# 当前工作区已有 benchmark 使用较新的 libstdc++，运行时需要：
module load compilers/gcc-12.2.0
./scripts/run-vortex-benchmark.sh --mode timing vecadd -n16
./scripts/run-vortex-benchmark.sh --mode functional vecadd -n16
./scripts/test-vortex-runtime-smoke.sh
```

默认从相邻 `vortex`、`build` 目录定位原生源码/产物，可设置 VORTEX_HOME、VORTEX_BUILD。
native backend 的拓扑参数来自本仓库冻结 VX_config.toml；静态断言要求 RV32、单 Core、
4 Warp × 4 lane。被加载的 benchmark/runtime 仍须与该配置和当前 CP ABI 匹配。
本次 Vortex 源码为 `e2b9745b637ce8ac462be2f0e01b5d76542dc6c0`。
生成库位于 `.cache/vortex-runtime`，不入库。

可以设置 `SIMTIMING_EVENT_LOG` 指定 JSONL 文件；父目录需预先存在。
设置后，`launch-start` 审计失败会拒绝启动；`launch-finish` 和 `cache-flush` 的
打开、写入（含短写）及关闭全部结束后，才发布 `LastRun` 并解除 Busy。
日志 I/O 不持有设备状态锁，因此 Busy/LastRun/LastError 查询仍能运行。
记录之间使用独立锁串行写入。审计错误以 `external-connection/op=audit` 返回，
同时发生的模拟器错误仍保留原始 origin 和 cause；不尝试把审计失败再写入同一坏日志。
未设置环境变量时审计明确禁用，完成顺序仍经过同一发布入口。
这里保证 Write/Close 返回，不声称 fsync 或断电持久性。

`execution_cycles`/`flush_cycles` 总是显式输出，包括零；功能模式没有硬件周期测量，
支持集汇总中表示为 null。周期模式 finish 仅证明执行终态，仍须匹配后续成功 flush
才能证明 backing visibility。日志成功写入不覆盖 native CP 的真实 flush 契约。
smoke 脚本自动生成独立日志目录，先周期型、再功能型，仅执行以下三项：

| benchmark | 参数 | 周期型 execution cycles | flush cycles | 功能型/周期型 host checker |
|---|---|---:|---:|---|
| vecadd | -n16 | 744 | 409 | 均通过 |
| demo | -n4 -x4 -y1 | 1317 | 409 | 均通过 |
| relu | -n16 | 796 | 409 | 均通过 |

初次调试 `timing vecadd -n1` 也通过；六项小规模记录位于
`.cache/runtime-smoke/run-OLCA9q/`；最终构建复跑位于
`.cache/runtime-smoke/run-y4N1z6/`，同样六项通过且周期数一致。这不是完整 benchmark 或完整模拟器验收，
launcher 中其他 allowlist 项不代表已验证的周期覆盖范围。

接入与 Sparse 的普通单测、局部 go vet、shell 语法检查通过。
竞态版首次运行触发 30 秒测试墙钟超时（没有报告 race），随后只将测试等待上限调整为
3 分钟；未调整模拟周期或结果断言。竞态复验通过（integration/vortexruntime 158.927 秒、
support/memory 1.049 秒），未报告 race。复验命令：

```bash
source env/env.sh
GOMAXPROCS=4 go test -race -timeout 8m ./integration/vortexruntime ./support/memory ./cmd/simtiming-go
go vet ./integration/vortexruntime ./cmd/simtiming-go ./support/memory
```

## 当前保留问题/边界（不改主体）

本次小样例未暴露新的模拟器主体失败。以下边界明确保留：

- Timing 原生 MPM 已连接设备累计的 44 位 busy Cycle 与 Warp/uop EOP Instret；
  宏 Retired 和 execution_cycles 仍单独记录。功能模式/未实现统计明确 unavailable，
  不再以零伪装。tag 编码、trace 关联及本地验证见
  [身份与计数交接](identity-performance-handoff.md)，外部原生 PERF 展示仍待验证。
- 同设备后继 Kernel 通过 NextLaunch 续用已排空的存储系统和连续时钟，旧 Kernel
  仍须先真实 flush；不会重复 Cache reset。首次 host reset/DCR 的完整外部时序尚未
  闭合，不宣称跨 launch 硬件周期等价。见 [生命周期交接](device-lifecycle-handoff.md)。
- 周期推进较慢：本次 744 cycles 的初次原生调试约 13 秒墙钟时间。
  这是当前模型运行开销，不是外部内存延迟；不为加速而改变结构或周期推进规则。
- 周期 Kernel 已改为注册的逐 Warp 分配与局部安全复用，CTA/LMEM 独立预留；
  事件和验证范围见 [增量驻留交接](incremental-warp-handoff.md)。Barrier RAM 抽象、
  未决冲突 store 语义等边界继续保留。
- 未覆盖完整 benchmark、大规模压力、DRAM 内部、L2/L3、coherence 或 RTLSIM 精度对齐。

功能型/周期型均已接入，而不是以功能执行结果冒充周期执行通过。

后续 28 项 × 两模式的 Slurm 扩展测试及查询入口见
[支持集测试记录](runtime-supported-tests.md)。

生命周期里程碑的当前实现、跨层回归和最终离线证据见
[收尾验收](lifecycle-integration-closure.md)。硬件 Cycle 同时接入注册 Warp busy
与真实 CTA dispatcher busy；MPM 在 launch-finish/cache-flush 审计完成前不可读，
不提前发布累计快照。上述历史 smoke 数字未作为本轮重跑结果。
