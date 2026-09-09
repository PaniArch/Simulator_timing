# 周期 Kernel 使用与 T11 验收

`runner.NewKernel` 接收已加载的全局内存和硬件可见 launch state，不创建 host runtime，也不调用功能 Core.Step/Warp.Run。程序、参数和输出均保留在调用者提供的 `device.BackingMemory` 中。以下示例假定已经加载测试兼容的 startup 和 kernel 程序：

```go
import (
    "fmt"
    "vortex.local/simulator/emu/device"
    "vortex.local/simulator/timing/runner"
)

func execute(memory device.BackingMemory) (runner.KernelStatus, error) {
    launch := device.LaunchState{
        StartupPC: 0x100, KernelEntryPC: 0x200, ParameterAddress: 0x7f0,
        GridDimensions: [3]uint32{4, 1, 1},
        BlockDimensions: [3]uint32{8, 1, 1}, BlockSize: 8,
        WarpStep: [3]uint32{4, 0, 0},
        ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: 64,
    }
    k, err := runner.NewKernel(launch, memory, runner.Options{
        Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 40,
        Ready: func(cycle uint64) bool { return cycle%3 == 0 },
    })
    if err != nil { return runner.KernelStatus{}, err }
    if err = k.Run(5000, nil); err != nil { return k.Status(), err }
    status := k.Status()
    if !status.Complete { return status, fmt.Errorf("cycle budget exhausted") }
    return status, nil
}
```

可重复运行的程序加载示例见 `runner/kernel_barrier_test.go`。startup 通过 CTA entry CSR 跳转；kernel 从 mscratch 指向的参数内存加载基值。Warp 首次运行使用 StartupPC，复用时从停止后的 PC 减 20 字节回到五指令 dispatch 窗口；不能把任意以 TMC 结束的程序自动当成可复用的 startup。完整示例保留调用/返回和窗口结构。

`Run` 的 budget 是实际 pipeline edge 数；预算用完返回 nil 并保留状态，不代表完成。调用者必须检查 `Status().Complete`，可继续对同一对象调用 Run。错误表示非法指令/输入、访存失败或契约错误；执行对象在内部失败后不可继续推进。`Status` 和 `TakeEvents` 返回 detached 数据；后者清空已观察事件，应在 Run 返回后也调用，以取得最终回收事件。`MultiRecord` 观察流水线、硬件 pending、effects、服务队列、阻塞和精确唤醒 token。

需要外部 expect-event 的程序通过 `PendingBarrierEvents` 暴露一次性票据。外部操作真实完成后，调用 `CompleteBarrierEvent(ticket)`；不要仅因 Warp 正在等待就完成票据。票据绑定 Kernel、CTA 生成 ID、slot、barrier 和 expectation 序列，旧票据/重复票据不能唤醒复用后的 CTA。默认测试的普通 BAR 不需要外部服务完成调用。

## 状态职责与抽象边界

- GridWalker 拥有生成次序；Kernel 拥有 pending CTA、固定步长 slot/window、成员绑定和回收；ResidencyMemory 拥有 CTA CSR 元数据及一个 16 KiB LMEM 字节数组。
- WarpState 拥有架构寄存器、PC、mask；model 拥有流水线和调度状态；effects 拥有尚未交付的效果；byte service 队列保存已接收请求的稳定身份。
- BAR 使用硬件 LSU scheduler 排空条件，WSYNC 使用本 Warp 硬件 pending，WSPAWN 使用注册 single-active 条件。资源回收另检查目标 CTA 的所有流水线、效果、服务与 Barrier 状态，不要求无关 CTA 排空。
- 服务延迟从请求接收开始；load 在服务边沿读取字节，store 在服务边沿写入字节，结果保持到接收。Kernel 使用固定正延迟和可配置请求背压；内部 cache/bank/DRAM 时序未实现。
- 正常 CTA 接纳在 edge 前执行 whole-CTA 事务，未复现 RTL 的逐 Warp dispatcher/context pipeline。CSR、control 和 completion 的已实现周期事件见 cycle-control 文档；本项目不宣称 RTLSIM 周期精度已收敛。

## 完成标准到验证的映射

| 验收项 | 证据 |
| --- | --- |
| AC-019 完整 launch、参数、多 Warp 协作、超过驻留容量 | `TestKernelRepeatedBarrierLocalExchange`：4 CTA、每 CTA 2 Warp、2 个驻留位置、2 轮 local exchange；不同 startup/entry；真实参数 load；逐 lane 独立预期值 |
| AC-020 有限延迟、背压、尾部和保持 | 同一 Kernel 的 fast/backpressure/store-tail 三场景：MemoryCycles=4/40/120，请求端分别每 1/3/5 周期接收；每场景 5000 edge 上限；回收周期不得早于该 CTA 最晚服务 due；120 场景必须观察 inactive Warp 的有效服务尾部 |
| AC-020 响应保持与延迟反馈补充 | `TestWaitPoolPartialOutOfOrderBackpressureAndEpoch`、`TestLSUResponseIdentityAndBackpressure`、`TestCSRRequestWindowHeldUntilResultAcceptance`；`TestKernelBarrierEventRejectsOldGeneration` 在外部完成前运行有限预算但不得报告完成；Spawn 等待回归覆盖延迟控制 |
| AC-021 结果与事件分别验证 | exchange 检查所有输出，另检查 32 次 wake、每 CTA 各一次 generated/admitted/reclaimed，以及新接纳时旧 CTA 的 pending/service 仍存在 |
| AC-022 正常路径与停止审计 | 下表和 `task11-cycle-control.md`、`task11-kernel-sync.md` 的身份/完成审计 |
| AC-023 文档与 IR | 本说明、`architecture.md`、三个 T11 文档及 `ir.yaml` 的 cc-kernel-*、cc-software-control-merge 契约 |
| AC-024 冻结输入及完整验证 | `verify-all.sh` 检查 RTL manifest、环境、格式、build/test/vet 和空缓存离线回归；另运行 `verify-timing.sh`、`git diff --check` |

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

未决项限定为未建模 dispatcher pipeline 的延迟、cache/LMEM bank 与更低层访存内部、非冻结扩展/多 Core/全局 Barrier 和完整 RTL 周期等价。既有 `u-feedback` 的未来 producer 精度问题不阻止当前 Kernel 的 staged dispatch；正常 baseline branch/control/CSR 合并已实现并测试。以上不把软件抽象标为 RTL 已证明事实。
