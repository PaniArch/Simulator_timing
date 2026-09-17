# Clock 引擎复用交接（clock-engine-reuse）

本页只交接 persistent-clock-engine 的串行第 1 Worker（AC-006/007/008），
不宣称整里程碑验收完成。基于交付 HEAD
`a3cef1158b244a6037533ee4640bb4522e07c2bb` 实施，未提交 commit。
已完成的 baseline 源码、测量入口与证据均保持原样。

## 实现与不变量

`timing/model/clock.go` 在 `NewClock` 中创建一次私有 SerialEngine，注册一次
嵌入 Clock 的 edgeDriver；`Run` 复用它们。独立 Clock 不共享 registry、队列、
时间、callback 或错误。Clock 按指针使用，不复制、不并发或重入 Run；不同 Clock
可独立嵌套运行。没有新增全局可变事件状态，也没有修改 vendor。

每个边沿仍调用 MakeEventBase、安排一个 Akita 事件并执行一次 callback。
保留全预算溢出预检（即使 callback 将提前停止），nil callback 检查优先于零预算。
成功边沿增加 Cycle，包括返回 stop=true 的边沿；错误边沿不增加 Cycle，
stop=true 与 error 同时返回时错误优先。Akita 忽略 Handle error，仍通过 driver.err
显式返回原 callback error；defer 在返回值求值后清空 callback、remaining 和 err。

vendor SerialEngine 在调用 handler 前弹出事件；driver 只在成功且非终止边沿后
安排下一个事件。因此预算耗尽、提前停止和错误返回时均已排空队列。
下一次成功推进的事件时间是 Cycle×period，不重设 engine 时间；失败后 Clock
允许新 callback 重试同一时间。此低层契约不放宽 runner 的粘滞错误停止，不能据此
自动重放部分成功的架构效果。事件 ID 生成和每个模拟边沿的执行没有被跳过。

源码核对 `Kernel.NextLaunch` 的既有路径仍转移同一个 Clock 指针，并封闭旧 Kernel
推进权限；此次没有改变生命周期、设备/launch/epoch/CTA generation 身份、计数、
flush、owner、公开诊断或两次 Evaluate 的握手反馈语义。引擎现在随该连续 Clock
一起转移，其上一次 callback 已释放。新设备/独立执行仍经 NewClock 获得新引擎。

初始化成本现在归入 NewClock，不能将 execute 的分配减少解释成初始化也降低。
事件本身仍按边沿构造；本 Worker 没有测量性能，不报告速度或分配收益。

## 契约来源

仓库未发现专名 Construction Skill/Boundary，也未提供外部引用；沿用用户 Worker
边界与 `emu/docs/architecture.md`、`timing/architecture.md`、`timing/rules.md`、
`timing/multiwarp-contract.md`、`timing/memory-contract.md` 的现有契约。
未读取历史聊天或执行轨迹。仅修改本 delivery 工作树。

## 确定性覆盖

新增 `timing/model/clock_test.go`：

- `TestClockPersistentEngineBoundaries`：零预算、预算耗尽、提前停止、最后预算边沿
  停止、错误及 stop+error、错误后零预算与续跑、callback 更换、nil callback，
  验证每次返回后的状态清理、空队列、引擎身份与实际 callback 时间。
- `TestClockPersistentEngineIsolation`：在一 Clock callback 内运行另一 Clock，
  验证同名 handler 不串扰、周期序列和不同 period 的时间隔离，新 Clock 从零开始。
- `TestClockPersistentEngineOverflow`：零 period 拒绝，周期加法、时间乘法与最大
  period 边界；超预算预检不调用 callback，最后合法边沿成功，耗尽后零预算仍成功。
- 沿用 `TestClockBaselineChunkEquivalence` 比较逐周期、固定分块、不规则分块在
  预算、停止和错误结束时的完整事件顺序与续跑；未修改该基线测试。

## 验证与后续职责

使用预装 Go 1.26.2、vendor 和 SoftFloat；先 `source env/env.sh`，缓存位于
HARNESS_RUNTIME_DIR。已执行：

- `go test -mod=vendor -count=1 -timeout=5m ./timing/model -run 'Test(AkitaClock|Clock)'`：
  退出 0，model 0.039s。
- `gofmt -l timing/model timing/runner`：退出 0，stdout 为空。
- `git diff --check`：退出 0。
- `go test -mod=vendor -count=1 -timeout=20m ./timing/model`：退出 0，model 83.105s。

以下留给唯一 closure Worker，当前**未验证**：

- 冻结整套命令 `go test -mod=vendor -count=1 -timeout=20m ./timing/model ./timing/runner ./timing/memsys`；
  以及集成结束后的 `gofmt -l timing/model timing/runner`。
- 第一阶段 runner 分块等价回归、epoch Flush、MakeVisible、FlushCaches、错误停止、
  跨 launch 交接与设备身份隔离；按需补充本次复用相关边界。
- 使用既有 BenchmarkClockBaseline/runner 入口进行有界 init/execute 分配测量及
  profile，单独保存新结果，不覆盖 `docs/performance/evidence/`。
  指针稳定性与源码支持每 Clock 只构造一次引擎，但不是分配测量的替代品。

本 Worker 没有环境阻塞。上述未验证项属于串行职责交接，不是执行失败。
不得在本里程碑做 YAML、Status、Resources、诊断构造或 Evaluate 优化；不运行
原规模 benchmark、RTLSIM、外部资产测试，不增加 benchmark 收尾 milestone。
