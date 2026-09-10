# 周期 Kernel 使用与 T12 存储生命周期

`runner.NewKernel` 接收已加载的全局内存和硬件可见 launch state，不创建 host runtime，也不调用功能 Core.Step/Warp.Run。程序、参数和输出均保留在调用者提供的 `device.BackingMemory` 中。以下示例假定已经加载测试兼容的 startup 和 kernel 程序：

```go
import (
    "fmt"
    "vortex.local/simulator/emu/device"
    "vortex.local/simulator/timing/runner"
    "vortex.local/simulator/timing/memsys"
)

func execute(memory device.BackingMemory) (runner.KernelStatus, error) {
    launch := device.LaunchState{
        StartupPC: 0x100, KernelEntryPC: 0x200, ParameterAddress: 0x7f0,
        GridDimensions: [3]uint32{4, 1, 1},
        BlockDimensions: [3]uint32{8, 1, 1}, BlockSize: 8,
        WarpStep: [3]uint32{4, 0, 0},
        ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: 64,
    }
    config, err := memsys.DefaultConfig()
    if err != nil { return runner.KernelStatus{}, err }
    config.Latency = 40
    k, err := runner.NewKernel(launch, memory, runner.Options{
        Backend: "std", PeriodPS: 1, MemoryConfig: &config,
        Ready: func(cycle uint64) bool { return cycle%3 == 0 },
    })
    if err != nil { return runner.KernelStatus{}, err }
    if err = k.Run(5000, nil); err != nil { return k.Status(), err }
    status := k.Status()
    if !status.Complete { return status, fmt.Errorf("cycle budget exhausted") }
    visible, err := k.MakeVisible(5000)
    if err != nil { return k.Status(), err }
    if !visible { return k.Status(), fmt.Errorf("visibility budget exhausted; resume MakeVisible") }
    return k.Status(), nil
}
```

可重复运行的程序加载示例见 `runner/kernel_barrier_test.go`。startup 通过 CTA entry CSR 跳转；kernel 从 mscratch 指向的参数内存加载基值。Warp 首次运行使用 StartupPC，复用时从停止后的 PC 减 20 字节回到五指令 dispatch 窗口；不能把任意以 TMC 结束的程序自动当成可复用的 startup。完整示例保留调用/返回和窗口结构。

`Run` 的 budget 是实际 pipeline edge 数；预算用完返回 nil 并保留状态，不代表完成。调用者必须检查 `Status().Complete`，可继续对同一对象调用 Run。错误表示非法指令/输入、访存失败或契约错误；执行对象在内部失败后不可继续推进。`Status` 和 `TakeEvents` 返回 detached 数据；后者清空已观察事件，应在 Run 返回后也调用，以取得最终回收事件。`MultiRecord` 观察流水线、硬件 pending、effects、服务队列、阻塞和精确唤醒 token。

需要外部 expect-event 的程序通过 `PendingBarrierEvents` 暴露一次性票据。外部操作真实完成后，调用 `CompleteBarrierEvent(ticket)`；不要仅因 Warp 正在等待就完成票据。票据绑定 Kernel、CTA 生成 ID、slot、barrier 和 expectation 序列，旧票据/重复票据不能唤醒复用后的 CTA。默认测试的普通 BAR 不需要外部服务完成调用。

## 状态职责与抽象边界

- GridWalker 拥有生成次序；Kernel 拥有 pending CTA、固定步长 slot/window、成员绑定和回收；ResidencyMemory 拥有 CTA CSR 元数据及一个 16 KiB LMEM 字节数组。
- WarpState 拥有架构寄存器、PC、mask；model 拥有流水线和调度状态；effects 拥有尚未交付的效果；byte service 队列保存已接收请求的稳定身份。
- BAR 使用硬件 LSU scheduler 排空条件，WSYNC 使用本 Warp 硬件 pending，WSPAWN 使用注册 single-active 条件。资源回收另检查目标 CTA 的所有流水线、效果、服务与 Barrier 状态，不要求无关 CTA 排空。
- I/D-cache、访存合并与 LMEM 使用 IR 的有限资源和真实字节；external backend 延迟从实际接受开始，load 使用返回数据，store 更新 cache 或原 LMEM owner。DRAM 内部时序未建模。Ready 只限制新请求，不能撤销已展示请求。
- Status().Complete 表示执行和 CTA 回收完成，不保证 dirty 输出对 backing 可见。MakeVisible 通过 D-cache 扫描、写回及后端完成获得可见性，不复制 cache 数据。MemoryDrained 与 BackingVisible 单独报告。
- Resident 中 Generation 标识物理位置的本次绑定；StoppedWarps 是 canonical TMC 停止状态，实际注册取指状态见 MultiRecord.Warps.Active。MemoryPending 包含组件和传输身份引用；Reclaimable 与实际回收使用同一判定，另需该 CTA Barrier 与全部成员流水线/effects 已释放。
- FlushCaches 执行独立的 D→I 联合刷新，适合同址代码更新。它、MakeVisible、ISA FENCE 和软件 epoch Flush 语义不同，见 runner/README.md。
- 正常 CTA 接纳在 edge 前执行 whole-CTA 事务，未复现 RTL 的逐 Warp dispatcher/context pipeline。CSR、control 和 completion 的已实现周期事件见 cycle-control 文档；本项目不宣称 RTLSIM 周期精度已收敛。

## T12 生命周期验证

| 验收项 | 当前证据 |
| --- | --- |
| AC-026/027 | CTA 观察与实际回收共用 observeCTA；检查同 slot 的 System residency、各 Warp 传输、Core/effects 及 Barrier；dirty cache line 不构成活动引用 |
| AC-028 | Kernel MakeVisible 分预算扫描/写回，Complete 与 BackingVisible 分离；FlushCaches 另检查先 D 后 I |
| AC-029 | TestKernelRepeatedBarrierLocalExchange 覆盖多 Warp、重复 Barrier、原 LMEM owner 和跨 CTA 尾部重叠；TestKernelMixedMemoryLifecycle 覆盖单请求 mixed 路径、generation 复用及最终写回 |
| AC-030 | mixed Kernel 对比 baseline/slow/dense/throttled/repeat 的输出、寄存器、周期、stall、refill 和背压；全量证据见 t12-delivery.md |
| AC-031/032 | 使用既有 Scheduler、Scoreboard、执行流水和 Kernel orchestration；固定环境、RTL manifest、离线 build/test/vet 通过 verify-all.sh 验证。最终门禁结果见 t12-delivery.md |

## 正常停止条件审计

| 条件 | 行为及释放依据 |
| --- | --- |
| IBuffer、执行 credit、请求端背压 | 保持 token；空间或 Ready 可用后继续；不丢事件 |
| WSYNC/BAR 硬件排空等待 | 分别由注册 issue/EOP 记账与 LSU request/tag 状态解除；不使用软件记录存活代替 |
| BAR 到达/phase/event 等待 | CTA coordinator 维护；最后到达或合法外部完成票据触发下周期 wake；重复使用与 CTA 隔离有执行测试 |
| WSPAWN | 在 CTA owner pool 内按就绪操作数选择实际目标，等注册 single-active；不读尚未就绪的寄存器猜目标 |
| 资源不足 | 保留 pending CTA 并继续旧 CTA；不返回失败，不全 Core 排空 |
| TMC 停止取指 | 仍保留提交、服务、反馈、效果和 Barrier 尾部，直到局部回收条件成立 |
| 预算耗尽 | 可恢复，同一 Kernel 再调用 Run；不是 Kernel completion |
| 仍保留的拒绝 | 非法/不受冻结 ISA 支持的指令、未加载内存、非法 launch、跨 CTA Spawn/Barrier 地址、旧/重复身份、无对应请求响应：均有具体输入契约，不能当正常推进手段 |

保留边界包括未建模的逐 Warp dispatcher pipeline、抽象 Barrier RAM/coordinator、mixed once-only 软件契约、跨组不同值重叠写的 UNRESOLVED 拒绝、非冻结扩展/多 Core/全局 Barrier 和完整 RTL 周期等价。L2/L3、DRAM 内部、VM/TLB、coherence 和 host runtime 不在范围内。既有 `u-feedback` 的未来 producer 精度问题不阻止当前 Kernel 的 staged dispatch；正常 baseline branch/control/CSR 合并已实现并测试。以上不把软件抽象标为 RTL 已证明事实。
