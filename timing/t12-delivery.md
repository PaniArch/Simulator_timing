# T12 存储与 Kernel 交付记录

本记录描述当前实现；最终门禁结果记在文末。历史 T9–T11 文档保留当时的实现背景，当前运行接口以本记录、kernel-usage.md、runner/README.md 和 Timing IR 为准。

## 实际执行与数据所有权

Runner、MultiRunner、Kernel 共用 scheduled Core 和 memsys.System。Fetch 经过 I-cache；普通及 packed SIMD 访存经过 split、coalescer、word adapter、D-cache 或 LMEM。结构参数取自 IR，外部后端默认延迟 100 cycles，可用 MemoryConfig 调整；后端只在真实接受后计时。固定 FetchCycles/MemoryCycles 兼容字段不参与生产执行，timing-token 的显式延迟仅用于独立诊断。

全局 backing 与原 CTA LMEM owner 保留字节服务职责。writeback store 修改 cache 数据和 dirty 状态，load 用实际返回数据完成 effects，不能再次读取 backing。组件与运行时按身份记录分批接受/响应/store 应用，取消不回滚已经发生的副作用，也不能撤销已经展示给无 abort 接口的请求。

## 生命周期与输出

- MultiRecord.Warps.Active 是注册取指状态；canonical TMC 停止状态由 KernelCTA.StoppedWarps 表示。停止后仍可有存储与流水尾部。
- KernelCTA.Generation 标识物理位置绑定。MemoryPending 包括缓存/MSHR、路由/合并、适配器、响应及运行时传输引用；Reclaimable 另检查成员 Core/effects 和 CTA Barrier。状态和实际释放共用判定，不等待无关 CTA 排空。
- KernelStatus.Complete 是执行和 CTA 回收结束；MemoryDrained 单独表示请求排空，BackingVisible 表示已完成显式输出写回。有效/dirty cache line 本身不是执行中的 residency 引用。
- MakeVisible(budget) 用真实 D-cache 扫描、写回及后端完成获取 backing 输出，可续跑预算；不复制 cache 数据或无条件清空缓存。
- FlushCaches(budget) 是外部 D→I 缓存控制，先等 D，再请求 I，联合完成才成功。ISA FENCE 走原 flush-marked 请求并返回原身份；软件 epoch Flush 保留缓存并放弃旧架构消费者，四者不能混用。

## 验收证据

| 项目 | 回归与实现 |
| --- | --- |
| AC-026/027 | observeCTA、WarpQuiescent、System.HasResidency；Kernel 混合路径检查 generation 复用、停止后尾部、回收状态和执行/可见性分离 |
| AC-028 | Kernel、MultiRunner、Runner 的 MakeVisible 与 FlushCaches；缓存更新同址程序、D-only 仍命中旧指令、D→I 后新指令生效；写回错误不得报告完成 |
| AC-029 | TestKernelRepeatedBarrierLocalExchange 的多 Warp 两轮协作、原 LMEM、CTA 重叠接纳；TestKernelMixedMemoryLifecycle 的单 SIMD mixed 请求、六 CTA/四 slot、最终寄存器及输出 |
| AC-030 | mixed Kernel 的 baseline/slow/dense/throttled/repeat 场景：独立预期数据，重复运行观测一致；慢 backend 增加周期、stall 和传输驻留，密集地址降低 refill，背压增加阻塞与周期 |
| AC-031 | kernel-usage.md、CLI backend-cycles/visibility-cycles、两个 CLI 的预算成功/失败测试、architecture.md、IR、本文及完整门禁 |
| AC-032 | 保留原 Scheduler、Scoreboard、执行流水与 Kernel orchestration；仅修复背压暴露的控制问题，并明确软件 recovery/dispatcher 边界 |

### Kernel 观测对比

同一计算结果的确定性场景实测如下；service edges 是每周期在途服务记录数的累积，
stall 是活动 Warp 不可运行的逐周期计数，不宣称等同某个未实现的 RTL 性能计数器。

| 场景 | 执行周期 | Warp stall | service edges | LSU 请求阻塞 | global refill |
| --- | ---: | ---: | ---: | ---: | ---: |
| baseline | 751 | 2167 | 1704 | 15 | 6 |
| slow backend | 1279 | 3503 | 4098 | 13 | 6 |
| dense addresses | 752 | 2167 | 1712 | 15 | 2 |
| throttled requests | 1034 | 3123 | 1592 | 54 | 6 |
| baseline repeat | 751 | 2167 | 1704 | 15 | 6 |

密集地址减少 refill，但仲裁与返回相位变化使周期不保证单调减少；测试不把低 miss 数强行等同于固定周期收益。

## 保留抽象与未决边界

CTA dispatcher 仍以 whole-CTA 接纳和软件 context 安装表达，没有复现逐 Warp dispatcher pipeline。Barrier coordinator/phase RAM 使用原软件 owner 和事件票据，没有声称综合 RAM 每个端口的周期精度。相关控制以现有 RTL 可证明的 LSU/pending/注册反馈条件推进。

IR 中 u-t12-memory-order 保留 mixed 子路径 valid/整体 ready 的 RTL 接受缺口，软件以一次性子集提交跟踪避免重复副作用；跨组不同值的重叠写在接受前显式 UNRESOLVED 拒绝。u-t12-cache-hit-order 保留上游同时事件组合的证明边界；cache bank 使用已核对的 forwarding/replay/core grant，不增加未经证明的同址串行门控。generation、epoch、无 abort 取消和外部控制事务编号属于明确的软件身份契约。

不扩展 DRAM 内部、L2/L3、多 Core coherence、VM/TLB、host runtime。既定测试和静态 IR 检查不构成 RTLSIM 逐周期等价证明。

## 验证结果

里程碑 06 最终验证已通过，可独立复核：

- verify-timing.sh：PASS（最新 IR；一致性检查，不是周期等价证明）。
- verify-all.sh：PASS；包括冻结 RTL manifest、固定 Go/SoftFloat 环境、模块校验、功能构建/测试/vet、格式与差异检查、空缓存 vendor 离线全仓 build/test/vet。Runner 全包 549.100 秒。
- git diff --check：PASS。
- 最新定向补充：实际无关 CTA 存储尾部与新 CTA 接纳重叠断言通过（20.680 秒）；mixed 五场景通过（72.398 秒）；两个 CLI 默认/可见性预算回归通过。

本记录完成 T12 当前范围的实现与验证交付；独立验收仍以冻结计划为准，不声称额外硬件范围或 RTLSIM 周期精度收敛。
