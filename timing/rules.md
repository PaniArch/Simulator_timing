# 周期规则与计量边界

本页对应 `timing-rules`，以 [ir.yaml](ir.yaml) 的 `rules`、`timing_measurements` 和资源条目为事实来源。数值与完整条件只维护在 YAML；这里解释组合方式和示例。既有功能契约已在本次 T8 开始前完整阅读，后续分析继续遵守原有 owner/effect 边界，不修改 ISA 或 RTL。

## 如何读一个周期数

T9 已实现 `r-software-edge` 的复合组件旧状态计算和统一提交、`r-software-clock` 的 Akita 边沿驱动，以及 `r-software-components/r-software-wait-pool` 的组件子范围；定向验证及剩余连接见 [implementation.md](implementation.md)。这不闭合完整反馈可达性或整机时序。

YAML `measurement_convention` 区分传输边沿、组合信号与边沿后状态。一个寄存器的输出在接受边沿后出现，下游最早在下一边沿接受；不要再给“输出出现”和“下游接受”各加一次相同寄存器延迟。背压会拉长实际时间；minimum 不是 guaranteed，II 是同一入口连续接受的最短间隔，不是执行延迟，也不是整机稳态吞吐。容量条目用 allocation/release 定义占用，不用周期差衡量。

`r-buffer-detail` 和 `tm-buffer-*` 描述实际底层 primitive。大 FIFO 的 OUT_REG 配合 look-ahead RAM 和 head bypass，不能按 RAM 名字再加一层容量。SIZE=2 的两种 OUT_REG 都保存 valid/data；满后是否可同拍入出须看 registered ready，而不能一概使用软件 queue 的“先 pop 再 push”。软件若按阶段推进，计算旧状态、组合下一状态、统一提交的做法仅为 `PROVISIONAL` 的 `r-software-edge`。

## 取指、相关性与资源竞争

`r-scheduler-edge` 补全两种 schedule fire 的区别：内部选择被 out_buf 接受时占用 ibuffer 信用并设 stall；C-disabled PC 前进发生在 schedule_if 被 fetch 接受时。decode 的注册 unlock、branch/SIMT 返回和新的选择不能在软件一次调用中任意串穿。局部 RTL 赋值覆盖顺序已登记，但同时出现哪些控制事件仍须受合法可达条件约束，不能从优先级表推导错误路径投机执行。

`r-scoreboard`、`r-scoreboard-edge` 将每 warp staging、依赖位释放/预留、FU 信用和仲裁输出分开。消费者必须等 registered operands_ready；writeback 当拍释放并不等于消费者也在该拍离开 staging。`r-opc-detail` 说明请求 bank 冲突会保留未读取源，多拍汇入相同 operand payload；其流水延迟与冲突重试不同。`tm-opc-read` 只适用于无 bank 冲突、无背压的已接受请求。

**普通 ALU 依赖示例（FROZEN 的条件化推导）**：同 warp 的 ADD 写一个非零目标，后继 ADD 读取它。前者经过 `tm-int-result` 与 ALU merge 后，由 `r-commit` 产生 WB；`r-scoreboard-edge` 先清 busy，再检查后一条依赖。若消费者已经在 staging、其他资源全可用，它最早按 `tm-wb-dependent-stage` 的边界离开；随后仍要经过 scoreboard 输出、operand collection 和 FU dispatch。不能把 ALU 的局部结果延迟直接当成两条指令之间的距离。该示例不声称整个程序有固定周期数。

## 执行、CSR 与完成

`r-muldiv-detail`、`tm-imul-*`、`tm-idiv-*` 分别记录 RV32 乘法移位流水与非 DPI 串行除法。除法完成后 adapter 还保留 loaded，必须先接受结果再重新接受请求；同拍 pop/push 不是其协议。DPI 分支单独条件化，不能把 IDIV_DPI 当作非 DPI 的快速路径或默认路径。

**长延迟示例（FROZEN 的条件化推导）**：非 DPI DIV 被接受后，serial_div 按计数器迭代；即使商已产生，若响应仲裁未接受，loaded 仍阻止下一 DIV。与此同时，其他 warp 的独立 INT 可以在其自身 FU 信用和 PE ready 允许时推进。`tm-idiv-accept` 是无响应等待时的最短入口间隔；冲突/背压会增加它，不能以除法算术延迟直接设置 ready。

`r-fpu-std` 的 STD 子路径为已核对的条件事实。serializer 的全宽路径仍有 tag 延迟链及输出 buffer；顶层 header pool 和 FPU 响应仲裁独立限制接收。`tm-fp-*` 从指定 PE/serializer 边界计量，不把局部流水 II 写成整个 FPU 的持续吞吐。外部定义尚未提供时，STD 的数值不得冒充具体运行的后端。FPU response handshake 后的 flags sideband 与 commit/WB 是不同事件。

`r-csr-detail`：CSR 先等待 CTA read context，即使读取普通 CSR 也经过相同 ready 门控。还发现 RTL `csr_data.write_enable` 使用 csr_req_valid 而不包含 csr_req_ready；不能以理想化“所有副作用只在 execute fire”替换源码。在结果背压期间的重复 CSR RMW/old-value 观察影响记为 `u-csr-stall`，需要后续适配/验证；此处如实登记使能方程，不改功能语义。

`r-completion-detail` 分别跟踪结果 eop、scheduler pending、TMC warp_done、CTA done 和顶层 busy。硬件计数器宽度或 SIZE 是记账范围，不代表等量硬件可同时驻留指令。全局完成还须检查 downstream store tail 和 flush；功能 KernelComplete 不是任意一个硬件信号的同义词。

## 访存排队与背压

`r-lsu-request` 和 `r-lsu-response` 将 LSU slice 接受、request queue 入出、load tag 分配、partial mask response、slice 结果缓冲和 WB 串起来。普通 store 可在向 scheduler 交付后进入 no-response completion 队列，不等待外部写回完成；FENCE 有独立 flush 属性和 fence_lock。`r-lsu-drain` 的 empty 包含未完成 load tags，却不能说明下游写请求已经对其他 warp 可见。

`r-coalescer-detail` 的 WAIT/SEND 状态决定批次进度，整条输入只有最后 batch 才 ready；某些输出批次可先于整条输入最终 handshake 发出。不要把输入最终接受当作所有内部工作的起点，也不要按 input valid 每拍重新生成一条独立请求。

`r-local-detail` 记录 local memory 的 request xbar、单端口 SRAM、tag/idx 对齐 buffer、response xbar。相同 bank 请求需要仲裁，同一地址紧接 store 的 read 还有显式 RDW hazard；这与下游 ready 等待分别计量。持有 response 数据副本只服务流水稳定性，不构成第二份 canonical memory。

`r-cache-queues` 已展开 L1 bank 内 MREQ/CRSQ 和按 memory port 的 MRSQ，MSHR 是每 bank 实参而不是全 cache 汇总。`r-cache-service` 保留 init/replay/fill/flush/core 请求优先级以及 full/response stall 门控。`tm-cache-bank-pipeline` 仅是 bank 内的推进深度；cache wrapper 的请求/响应 xbar、NC bypass 还必须分别计入路径。`r-memory-fabric` 将基线的 wrapper 实参进一步求值：冻结 L2/L3 命中 DIRECT_PASSTHRU，实际输出 buffer 为直通；不能仅凭外层 OUT_BUF 实参添加延迟。缺失外部 response 时间仍为 `u-memory`，不设固定 miss penalty。

**访存背压示例（FROZEN 的条件化推导）**：外部 mem_req_ready 拉低，已出现的 memory request 持有，bank MREQ 占用增长到 guard threshold 后抑制新 core request；fill/eviction 也要遵守各自门控。load tag 在 response coverage 完成前不释放，最终可能阻止 LSU 接收。解除 ready 并不等于所有队列与注册信用在同一个边沿立即恢复。沿 `tm-cache-mreq-*`、`r-cache-service`、`r-lsu-response` 可以定位每一步，无法从这条链推出固定总等待。

混合 local/global lane 请求还存在一个必须公开的源级缺口：`r-mixed-split` 中两个子集各自 valid，而上游 ready 是两侧 ready 的 AND；当前源码没有已发送子集标记。一侧持续可接受而另一侧阻塞时，不可直接宣称 exactly-once，见 `u-mixed-split`。本阶段不修 RTL，也不静默在周期规则中添加已发送位。

## 控制反馈示例与局部优先级

**branch/SIMT 示例（FROZEN 的条件化推导）**：branch 指令从 decode 保持该 warp stalled，ALU result 被接受后产生注册 branch feedback；scheduler 在随后的消费边沿更新 PC/stall，再由更新后的状态参与选择。若 result 被仲裁背压，重定向也等待，不能在语义 Evaluate 得到 target 时立即选取新 PC。SPLIT/JOIN 经过 WCTL 和 scheduler 内 split_join，不能直接套用 branch 总延迟；相应边界为 `tm-branch-owner`、`tm-wctl-owner` 与 `r-simt-barrier-detail`。

BAR 的地址预读必须早于注册请求；barrier state/phase 的 RAM、同地址 forwarding 和 unlock register 是三种不同边界。`r-simt-barrier-detail` 只冻结可直接确认的配合关系与 local 分支，global 网络及冲突 memory visibility 仍未闭合。WSYNC 消费 per-warp almost-empty，BAR 消费 LSU drain，二者不能合并成一个“所有单元 empty”。

`r-scheduler-edge`、`r-csr-detail` 和 `r-scoreboard-edge` 的覆盖顺序来自 RTL，状态为 FROZEN；假设软件先处理所有完成事件再发射所有请求则为 PROVISIONAL。`u-feedback` 保留完整跨组件同时事件可达性与验证工作，不用未审计的软件顺序填充它。

`r-software-composition`（PROVISIONAL，T9 第三轮）：P/R 汇合由 `Merge` 实现，参数挂在命名 boundary 的 `arbitration` 中；MODEL=1/STICKY=0 的 R 以最低有效端口开始，仅在汇合缓冲输入接受时更新。下游停顿不轮转，输出缓冲是唯一新增注册边界。Frontend 已连到每类 Dispatch，ALU/SFU/显式 STD FPU 已组合。FPU header 在 execute-fire 分配、backend response-fire 释放，不能加算成额外数据队列；满时同拍释放仍背压。INT branch、WCTL、STD FFLAGS 只发注册身份通知，条件与局部起止点沿用原证据。LSU、完整反馈和架构效果仍待实现。

`r-software-lsu`：冻结 scheduler 为单 client、同宽通道、无内部 coalescer/batching、输出直通。Load tag 和请求队列联合准入；store 不占 load tag，但同时要求请求队列及 no-response buffer 可接收。fence 入队后锁住 slice，最后响应接受时解锁，同拍不旁路旧锁。部分响应保留未完成 lane mask，load 优先汇合。外部服务 idle 是排空条件的一部分，store WB 不等于外部完成。新增代码只落实这些边界，未给未知存储层赋固定延迟。

### r-software-memory-visibility（PROVISIONAL）

`Adapter.Service/observeMemory/observePacked` 将原 byte owner 的显式服务接到请求接受之后的未来边沿；服务时间由调用者提供，不推定 cache 延迟。普通/packed load 服务时采样字节，对应 WB 才按 lane/byte mask 修改当前 owner 状态。packed 部分 completion 与原完整 completion 共用校验和组装，原 API 仍要求全覆盖。store 即使早已 LSU 完成，也只在服务事件以原 Write/WriteBatch 提交一次。顺序 PC 等待 pending 和全部服务，属于保守模型条件。逆序 uop、分 lane 和逐字节可见性测试已覆盖成功路径；后续增量已补齐故障停止/reset、fence 与外部控制交付；不承诺跨片段精确异常，u-memory/u-visibility 不因此宣告解决。

第八轮补充 `r-software-memory-visibility`：读取服务失败逐 lane 形成原 MemoryResponse/PackedLoadResponse fault，经原 completion 返回 `warp.Fault`（保留 cause 和地址/宽度），失败片段不产生响应与状态写入。此前已经可见的片段不回滚，不宣称精确异常。fence ordering 已验证只在显式服务交付一次；单 participant barrier 注册反馈经原 BarrierCoordinator.Stage/Commit 修改唯一 owner。完整 driver drain、调度 slot 和多目标 spawn 仍待接线，未知项继续保留。

第九轮补充 `r-software-memory-visibility`：WSPAWN 在 Begin 前绑定唯一 active source 与原 target Expected 快照，注册控制通知以原 StageWarpSpawn 原子提交 source/targets；保留 source/target stale 与 MScratch/inactive 校验，成功一次 receipt。CTA membership/residency 仍属调用者。`ControlAllowed` 使用与 Observe 相同的旧边沿 context，在 PendingLSU/PendingPriorWork 时分别阻止 BAR/WSYNC 执行，不能将 wait-only effects 当成完成。混合指令测试以同一 Akita clock/Core/Adapter 逐条排空（含 store tail），对照原功能状态。此条件不扩展为多 Warp 调度，也不关闭外部时序未知项。

### Task9 程序运行器增量

`timing/runner` 复用 Core/effects 和原 state/memory owner，以单活动指令排空策略从 canonical PC 自动取指；取指与数据服务均采用显式正延迟，不代表 cache 时间。Akita Run 支持预算续跑，Flush 清除在途服务并增加 epoch，不回滚已可见效果。条件见 `r-software-program-runner`。

本地入口：在仓库根目录 `source env/env.sh` 后运行 `go run ./cmd/timing-run`；`-trace` 输出逐周期 JSON，`-program file.bin` 从 0x100 加载 raw little-endian RV32 镜像。默认四 lane、x1=64+8*lane，内建示例验证依赖 ADDI/MUL/DIV、store/load、分支跳过指令与 TMC 结束。显式 STD、1ps 模型时基、fetch=3/memory=19 cycles；可用对应 flags 修改服务延迟。命令行预算耗尽返回非零；库保留进度可续跑。多目标 spawn residency 仍需 effects.BindSpawn，当前程序入口明确拒绝，不实现多 Warp scheduler。

观测包含旧边沿 CoreReport、提交后的 ResourcesAfter/Residents、Services 及 Events。按 ID/epoch/warp/uop/mask 对应资源生成 enter/stay/advance/leave，读、执行、WB/反馈另有事件；Flush 取消事件保留旧 epoch。位置与 Remaining 是本地资源状态，不推定外部 cache 时间。

### Task9 对象驻留观测与验证

每个资源通过 `Residents()` 返回 detached value，含 Token、位置、局部 Remaining 与 readiness 原因；runner 不访问组件队列。CoreReport.Resources 是旧边沿，ResourcesAfter 是本次统一提交后状态。Events 的 enter/leave 对应本周期提交的转移，stay/advance 表示资源仍持有该对象；tag/context alias 保持独立资源身份，不能累加为指令数量。FIFO 中的 queue-order、输出 awaiting-transfer、执行 execution-latency、tag response-coverage 与外部 backpressure/control-drain 明确区分；这些原因不宣称解析全部 RTL 仲裁信号。Services 列出原 byte owner 服务队列及显式 due cycle。

程序 trace 测试验证驻留记录闭合、除法 33 周期占用、packed 请求队列填满四项后恢复且每 uop 只 WB 一次；增加 memory 服务延迟或请求背压会延长完整运行，功能结果不变。重复运行记录确定；快照修改不会改变组件 owner。基础注册边界与满队列同时接收/释放继续由 timing/model 门禁覆盖。

### Task10 四 Warp 事件契约

`cycle_contracts/cc-*` 细化上述源级规则，完整说明见 [multiwarp-contract.md](multiwarp-contract.md)。特别注意 `cc-ibuffer-accounting` 的 L1 all-full 例外、`cc-reserve-release` 的替换输入选择、`cc-fu-credit-lock` 的旧 goingfull/next lock、`cc-issue-arbitration` 的 sticky 请求保持，以及 `cc-packed-release` 的逐 uop WAW 序列化。Task9 无完整 hazard 的 packed queue 测试不构成 Task10 调度吞吐证据；所有未知同时事件继续由 `cc-control-collisions/u-feedback` 管理。
