# T12 里程碑 04：请求组织与 LMEM

组件实现包含 `Coalescer`、`SIMDSplit`、`GlobalAdapter`、`LocalAdapter` 和 `LocalMemory`。
已通过公开接口组合 split 两条路径、D-cache/backend 和原 CTAMemory，覆盖实际数据及
逐边沿握手。Kernel/Fetch/LSU 指令接口、FENCE/DCR 与 socket fabric 接线属于后续里程碑。

## 已实现

`SIMDRequest` 表达四个对齐的 4-byte LSU word 和有效 lane mask。功能层应先将 byte/half
访问的 data/byte enable 移到所属 word 内；本组件不重新执行 ISA load 解包。
结构和容量从 `res-coalescer-tags` 读取，固定数组只承载经校验的冻结形状。

`makeBatch` 对 [0,1]、[2,3] 两组分别选最低剩余 lane 的 8-byte base，比较组内地址。
同 word 的 enable 按 RTL 递增 j 赋值合并，较后 lane 只覆盖其 enabled bytes。
`NoMerge` 逐组只选择 seed lane；同 cache line 不同 word 仍产生不同批次。

WAIT/SEND 两态构造注册输出，最后一次 SEND 才报告输入 Accepted；输出握手还可能更晚。
WAIT 在本边沿旧 index-full 时禁止开始下一批，包括 store。读在 SEND 分配 slot，
不是在输出握手时分配。registered acquire index 按 VX_allocator 的 acquisition 或
full-release 条件更新；正常非满 release 不立即重选 index。

读响应将 channel mask 按该批次参与 lane 和 offset 展开。原 identity/tag 不改变，
部分响应不释放 slot，全部 channel 返回后才释放。`Empty(inputValid)` 对应 RTL 的请求侧
empty，不包括读 index；`Drained` 和 `HasResidency` 还检查未返回的读。

## mixed split 增量

`SIMDSplit` 从 IR 读取两个单槽请求 buffer、单槽返回 buffer 和轮转仲裁。
单槽允许同边沿 pop/push 替换；两个子缓冲分别接受，输入等待所有非空子集进入缓冲。
submitted bits 在一个子集进入缓冲后抑制其重复 valid；这是显式的软件 once-only 处理，
不声称冻结 `VX_lmem_switch` 已有同名状态或已证明不会重复接受。

completion ledger 是软件身份账本，不增加硬件队列或资源 credit。它区分子集排队、
子路径释放输入、部分 batch 进展、响应到达、load 数据交付和 store 实际应用。
读经真实返回缓冲，store 通过应用事件报告，不占虚构的 RTL read-response slot。
所有 lane 完成且所有子请求引用释放后才发 Complete，保留无关 residency 的独立进展。
全局冲突检查先于两个 mixed 子集接受，避免 local 已生效后才发现未决全局覆盖。

`Progress` 是供分批 adapter 报告部分 lane 已前进的事件，必要时可早于子路径输入 ready。
这防止 coalescer 前几个批次的返回因其整条输入尚未释放而被误判为迟到/非法响应。
即使所有 lane 数据先到，仍须等待子请求 buffer 的引用释放后才允许回收身份。

测试覆盖两种方向的不对称背压（load/store 各一组）、完整输入接受前部分完成、
单槽返回替换与轮转、部分批次提前返回、重复响应、非法身份、背压 payload 稳定和预先冲突拒绝。

## software boundary

`BatchID.Generation` 是软件防迟到机制，不是 RTL tag 位布局；返回必须对应已实际输出的
读批次。重复 channel、已释放 slot、旧 generation 或被修改的背压响应在修改状态前拒绝。
返回为零缓冲 priority pack 路径：各独立 producer port 在背压时保持 payload，但
组合 packet 可以重新选择优先 tag 或加入新到达的同 tag channel。同 tag 已出现的 bytes
必须保持稳定；不能给该组合边界虚构额外的 payload 锁存器。

跨组不同值 byte overlap 在任何子请求发出前返回 `ErrUnresolvedStoreOrder`，
不选择 lane/map 顺序赢家；组内合并后的同值重叠允许。不同 memory attributes 被合到同
输出 word 时也显式报 UNRESOLVED。这里是明确的软件错误边界，不模拟硬件异常。

`CoalescedBatch.Words` 由 `GlobalAdapter` 映射为 cache word offers：递增的软件 wire
transaction/tag 关联原 batch、identity 和 lane mask，避免复用的 coalescer slot 混淆响应。
store 不占读 index；其已应用和 residency 尾部需要由 adapter/split 层继续持有，不能从
coalescer 的 empty 推断整条 SIMD store 已完成。

## global word adapter

冻结 REQ_OUT_BUF/RSP_OUT_BUF 均为 0；`Offers` 表达直通请求，部分端口接受后只保留
剩余端口 valid，最后一个端口接受才释放 batch。`Preview` 按最低 valid port 选 tag，
只组合匹配 tag 的响应，`ReadReady` 将整体响应 ready 分发到匹配端口。
这些方法只读取旧状态，调用者将实际 CacheEdge 交给 `Step` 提交边沿。

`Cache.Responses()` 暴露值副本用于计算 ready，不修改 cache 队列。组合顺序为：
coalescer.Output → adapter.Offers → cache ready/Step → adapter.Step → coalescer.Step。
`OutputAcceptedMask` 向 coalescer 报告独立 channel 接受，允许已接受 channel 在其余
channel 背压期间返回；不能再以“整批已接受”作为所有返回的统一门槛。

store receipt 映射回 channel 的全部参与 lane，保留错误。适配器账本仅作软件 identity
映射，不引入新硬件 credit。Progress 是实际 word 接受事件；组合 split 时，仅在该
split 子路径尚未释放整条输入时向其报告所需的早期进展，不能重复报告同一 lane。

测试验证端口不对称接受、不同 batch tag 不误组合、部分返回先于全 batch 接受、
过期响应拒绝，以及真实 cache/backend 下分批 store/load 的 lane 数据和 backing 可见性。

## LMEM component

`NewLocalMemory(LocalOwner)` 提供四个 4-byte word 端口。地址窗口、bank 数、word 大小、
请求和响应 queue 容量、SRAM/tag 输出深度及优先仲裁均读取 IR。
地址低 word 位选择 bank；每个 bank 每边沿从旧请求队列服务一项。请求与响应 xbar
各两槽，tag/SRAM 并行输出是一槽，不把两个相同输出延迟重复相加。
无冲突读从进入请求 xbar 到端口返回共 3 个边沿；local adapter 的每 lane 两槽请求 buffer 另占一个边沿，因此组合无冲突 local read
从 adapter 接受到返回通常为 4 个边沿，不含 split 返回缓冲。

`is_rdw_hazard` 在前一边沿向同 bank 同地址写后阻塞读一拍。store 不需要读响应 credit，
但仍受输入队列顺序和单 SRAM 端口约束。返回 data 是 owner.Read 的快照，背压中不重读。
store 在实际 SRAM 服务边沿用启用 byte runs 调用原 owner.WriteBatch；不另建本地字节副本。
owner 错误经对应 read response 或 store receipt 返回，不重试部分副作用。

`LocalOwner` 是软件绑定边界：每次实际服务都必须按传入 kernel/CTA/warp generation 验证
身份，再返回原 `CTAMemory`。它不能只用当前 Warp slot 查找新 CTA。`HasResidency` 覆盖
已接受请求、SRAM 输出及返回 buffer；上游尚未接受的 offer 仍归上游负责。
当现有功能模型为不同 CTA 提供相同虚拟 LMEM 地址时，bytes 仍由其各自 canonical owner
映射；bank/last-write 地址比较使用请求地址，保持这里的冻结 bank 接口语义。

回归以原 `core.CTAManager`/`CTAMemory` 验证字节更新与 CTA 隔离，检查 request2 + SRAM1 +
response2 的容量上限，以及延迟请求在 owner resolver 拒绝旧 generation 后返回错误。
这尚不代表 Kernel 自动回收已接通；运行时必须按 HasResidency 阻止过早解绑或复用。

## local adapter 与混合路径验证

`LocalAdapter` 的四个 lane 各有 IR 指定的两槽请求 buffer，返回为零缓冲 priority pack。
每 lane 独立进入 buffer，已进入的 lane 不重复入队；最后一个 lane 入队才释放输入。
返回选最低 valid port 的 tag，合并匹配 tag 的有效端口。wire transaction 与软件账本
保留原 SIMD identity/tag；store 应用事件不占读响应缓冲。

`TestMixedPathCacheAndLMEM` 完整展示组合顺序：读取 split 子输出、cache/LMEM 旧响应，
用 adapter/coalescer Preview 展开，再由 split 返回仲裁计算两条路径 ready，随后提交各
组件边沿，最终用真实 child-ready、partial progress 和 store events 更新 split。
全局早期 Progress 只在该 coalescer 输入尚未整体释放时报告，避免重复报告同一 lane。

回归覆盖 external latency=1/30、周期性响应背压、mixed load/store、同 bank LMEM 地址、
每 lane 实际返回数据、缓存 store 未直接修改 backing，以及相关组件 drained/identity 尾部。
独立 split 测试还覆盖两种方向的输入不对称背压；独立 adapter/LMEM 测试覆盖队列满恢复、
部分接受、重复返回拒绝、不同宽度 enable、同址合并与跨组不同值的 UNRESOLVED 拒绝。

## 后续运行时边界

本里程碑交付周期组件和组合验证，不表示 Kernel 已切换到此路径。后续仍须接入指令
load 解包、SIMD store 部分应用、FENCE/DCR、socket 外部 fabric 和 CTA 回收生命周期。
原 T11 运行时固定延迟服务尚需在相应接线里程碑移除。LocalOwner resolver 的完整 identity
校验契约、mixed once-only 软件处理和跨组未决冲突策略仍有效，不宣称 RTLSIM 周期等价。
