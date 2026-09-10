# 外部 memory backend

`DefaultConfig()` 从 `timing/ir.yaml` 的 `mc-backend.parameters` 读取默认值。
`New(originalBacking, config, clients)` 绑定原 `warp.AtomicMemoryService`，可直接传入
`support/memory.Memory`。后端没有另一份 backing storage；有限队列只保存已接受请求和返回数据。
调用者可以修改 Config 的 latency、接受带宽、在途容量、返回带宽，所有值必须为正。

每个固定 client port 提供一个 `Offer` 和 response ready，例如 I-cache=0、D-cache=1。
`Step(cycle, offers, ready)` 提交一个边沿并返回逐端口 Accepted 和 Replies：

- 从 port 0 开始轮转仲裁，获准后移动指针；每端口每边沿最多接受一项。
- 以边沿前在途数决定容量，不借用同边沿 response retirement 的空间。
- 在边沿 C 接受，则 C+Latency 服务并首次可返回；调用者须从任意起始周期连续调用 Step。
- 到期的请求按接受序号访问 backing 一次；即使 response 被阻塞也不会再次执行。
- 响应按全局接受序号返回，有队首阻塞；每端口每边沿最多一项，同时受总返回带宽限制。
- 未接受的 Offer 必须保持 Valid 与全部 payload；改变被拒绝。每端口 Transaction 必须严格递增，
  包括跨 CTA/warp generation，重复提交已接受请求会返回协议错误。

Request 使用 64-byte 对齐的 byte address，Read 返回完整 sector（response ByteEnable 全一）；
Write 仅应用 ByteEnable 中为一的字节，返回软件完成确认；Visibility 不访问地址，返回 mask 为零。
Visibility 是显式软件 ticket，不是模拟 RTL write response 或 DRAM 命令。它在服务时确认之前
**已接受**的写均已服务；提交者应先确认相关 cache writeback 请求已获接受，不能把尚在上游的写
算入该 ticket。更晚接受的写不会追溯影响它。失败写会使后续 ticket 返回首次写错误，避免误报成功。

读在服务边沿采样，写在服务边沿可见；同 due 的读写按接受序号执行。因此响应背压期间 backing
可能继续改变，但已有读响应保持原快照。Identity 包括 Kernel/CTA/Warp generation、Token、Epoch、
Subrequest、Transaction 与 Warp，另保留 Tag；writeback 可使用无 residency 的零字段并分配新的
Transaction。请求和响应使用值类型固定数组，不暴露内部队列数据。

稀疏写拆成不重叠连续 byte runs，以原 owner 的 WriteBatch 一次原子应用。owner 必须遵守原有
all-or-error 契约；失败范围不会导致部分 sector 可见。Read 失败时清零返回数据并保留错误。
backing 错误仅出现在对应 Response.Err，使用 errors.Is 检查原错误；返回背压不重试。
Step 的协议错误发生在任何该边沿状态或 backing 修改之前，可修正输入后重试同一边沿。

这是单线程周期组件，所有端口应在同一次 Step 调用中仲裁。它不实现 DRAM 内部时序。
本里程碑提供可接入 cache refill/writeback 的组件；Kernel、Fetch、LSU 的实际接线属于后续里程碑。

# L1 cache

`NewCache(InstructionCache)` / `NewCache(DataCache)` 创建冻结结构；`Spec()` 返回 IR 的解析值。
每个 `WordOffer` 访问一个对齐的物理 cache word（I 为 4 bytes，D 为 8 bytes），
`WordRequest.ByteEnable` 和 Data 从 word 起点编号。它不把同 sector 的不同 word 请求合并为一个响应。
端口上 Transaction 单调递增；未接受请求保持不变，接受后调用者撤销 valid 或提供新请求。
D 的 `NonCacheable` 通过 bypass 读写后端；cached store 只更新 cache bytes/dirty。

每个周期按以下顺序组合接口（多 cache 的端口拼成一次 backend 调用）：

1. 收集各 cache 的 `MemoryOffers()`。
2. 用 `Backend.PreviewResponses(cycle)` 得到可能返回的身份头；对 valid 的头调用所属 cache 的
   `MemoryResponseReady(localPort, header.Response)`，拼成后端 ready。preview 不访问 backing。
3. 调用一次 `Backend.Step`，把返回 Edge 按各 cache 的 memory ports 切片。
4. 调用各 cache 的 `Step(cycle, CacheInput)`，同时提交 core offers、core response ready 和控制输入。
   `CacheEdge.Accepted` 是本边沿接受；Replies 带原 identity/tag、byte mask 与真实数据。

`cache_test.go` 的公共接口测试桥提供完整多 cache 拼接示例。端口数不是额外带宽参数：
总接受/返回能力仍受 backend 配置限制。这里没有实现 socket 级外部互连的精确周期。
响应头必须对应此前接受的请求、operation、tag、mask 和接受/完成周期；非法输入拒绝提交本边沿。

cached store 通过 `Stores` 报告实际应用，不生成虚构的 RTL store response；NC store 在后端完成后
产生 receipt。调用者须消费这个事件。read response 在背压中保留；load 应使用其 bytes，不能重读 backing。
cache-owned writeback 的错误经 `WritebackErrors` 报告并保留用于后续 flush。

`Flush` 使用独立控制 identity/tag，未获接受时保持稳定，每次新 Transaction 必须递增。
接受后停止该 cache 新 core 请求，等待 bank 扫描与写回尾部，再向后端提交 Visibility；
`FlushReady` 可阻塞最终控制响应。此软件可见性完成比 bank done 更强，不能当成指令 FENCE 的
已确认 RTL 返回周期。dirty 数据只能经过真实 Write 请求更新原 backing。
`Drained()`、`State()`、`HasResidency(kernel, cta)` 分别用于请求排空、资源观测和身份尾部检查；
有效/dirty line 本身不占 CTA 身份。flush 不以所有旧 read response 交付为先决条件。

详见 [../cache-progress.md](../cache-progress.md) 的状态机、证据与精度边界。
新 hit 可在 fill-forward 期间接受并越过旧 replay store，符合 bank 的 grant 和 tag lookup；
不附加同址准入保护。链内顺序不等于 cache 全局同址顺序。上游指令级组合问题仍记为 `UNRESOLVED`。
Kernel、Fetch、LSU 和 FENCE 的运行时接线留给后续里程碑。

# SIMD coalescer（里程碑 04 增量）

`NewCoalescer()` 提供独立 WAIT/SEND 组件。每边沿调用 `Step`，上游保持 `SIMDOffer`
至 Accepted；`OutputDelivered` 是批次下游握手。返回 `BatchReply` 可分 channel 报告，
`ResponseReady` 直接控制展开后的 lane 响应握手。store 不生成读响应。

该组件已由组合测试连接 cache word adapter、mixed switch 和 LMEM；接口约束、软件错误边界及
剩余工作见 [../routing-progress.md](../routing-progress.md)。

`NewSIMDSplit()` 提供 mixed 路由组件：先读取 `Outputs()` 递交两条子路径，再以实际
ready/响应调用 `Step`。`SubsetAccepted` 表示进入子缓冲，`OutputDelivered` 表示子路径
释放该输入；`Progress` 可报告更早的部分批次进展。`Reads` 通过轮转返回缓冲，
`Stores` 是应用事件，`Complete` 等待全部 lane 完成和子请求引用释放。
这些软件事件须由调用方消费；它们不代表 backing visibility。

`NewGlobalAdapter()` 提供两个 8-byte 端口的零缓冲适配。`Offers(batch)` 生成尚未接受的
word；`Preview(cache.Responses())` 和 `ReadReady` 计算按 tag 分组的返回与 ready，
随后以实际 CacheEdge 调用 `Step`。将 `Accepted`、`Response` 和实际端口接受 mask
传给 coalescer 的 `OutputReady`、`Response`、`OutputAcceptedMask`。
`Progress`/`Stores` 保留原 SIMD identity/tag/lane mask；store 事件表示 cache 应用，
不表示 backing 已可见。完整示例见 `TestGlobalAdapterCacheCoalescerData`。

`NewLocalMemory(resolver)` 提供四端口 LMEM bank 组件。resolver 必须验证完整 residency，
返回既有 `warp.AtomicMemoryService`（如 `core.CTAMemory`）；它不分配另一份 LMEM bytes。
`Responses()` 用于旧边沿 ready 计算，`Step(cycle, offers, ready)` 提交请求及返回握手，
并以 `CacheEdge.Stores` 报告实际应用事件。四端口 `LocalAdapter` 提供逐 lane 请求缓冲和按 tag 返回组合。

`NewLocalAdapter()` 提供四个带两槽请求缓冲的 local word 端口。用 `Offers()` 驱动 LMEM，
以 `Preview`/`ReadReady` 计算返回 ready，再将实际 Memory CacheEdge 交给 `Step`。
`Accepted` 表示所有有效 lane 已进入 adapter 缓冲，`Stores` 是实际应用事件。
完整 mixed 组合示例见 `TestMixedPathCacheAndLMEM`。零缓冲 pack 的组合输出可在背压期间
加入同 tag 端口或重选优先 tag，已出现的同 tag 数据及各独立端口 payload 保持稳定。

`NewSystem(globalOwner, localResolver, config)` 提供上述组件的生产组合。先读取 `Responses()`
决定消费者 ready，再每周期调用一次 `Step`，以返回的 FetchAccepted/MemoryAccepted
推进生产者。`Stores` 必须逐事件消费，`Complete` 不表示 backing 可见。
I/D flush 独立输入输出，仍需 Runner 按各自硬件条件发起。协议错误后不可重试 System，
此前实际副作用保留；访存错误则通过原响应/receipt 返回。
共享 backend 使用拼接 cache memory ports 的软件外部边界；未声明 RTL socket 周期等价。
Runner 的 Fetch/LSU 接线尚未完成，见 `../memory-integration-progress.md`。


`WordRequest.Flush` 是原位 flush 属性。cached word 在 bank 扫描后才接受，并沿普通 read
路径返回原 tag/identity 和数据；它不生成独立 FlushReply 或后端 Visibility。普通 cached
请求受当前 flush mask 锁定，外层 NC bypass 不受这个锁影响。SIMD Flush 属性经 coalescer
和 adapter 保留。独立 `CacheInput.Flush` 继续使用其原软件可见性协议。
