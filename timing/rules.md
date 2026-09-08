# 周期规则与计量边界

本页对应 `timing-rules`，以 [ir.yaml](ir.yaml) 的 `rules`、`timing_measurements` 和资源条目为事实来源。数值与完整条件只维护在 YAML；这里解释组合方式和示例。既有功能契约已在本次 T8 开始前完整阅读，后续分析继续遵守原有 owner/effect 边界，不修改 ISA 或 RTL。

## 如何读一个周期数

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
