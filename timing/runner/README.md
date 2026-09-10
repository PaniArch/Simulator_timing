
T12 增量：`NewMulti` 默认启用真实 I-cache、混合 LSU、D-cache/LMEM
与共享后端。默认从 IR 读取后端配置；Options.MemoryConfig 可覆盖。MultiOptions.MemorySystem 用于动态
residency Bind 及 LocalOwner 服务时校验；默认静态路由见下文。
此模式不使用 FetchCycles、MemoryCycles、MemoryDelay 或旧 effects.Service；load 使用实际
返回字节，store receipt 不重复写 backing。`Completed` 不表示 dirty 数据已对 backing 可见。
FENCE 已经通过 inline cache flush 原请求返回完成。
epoch Flush 保留真实存储尾部，并按 System.NextCycle 对齐失败边沿；
单 Warp New 同样使用该 scheduled Core/System，仅将原 owner 对应的 Warp 激活。
Options.DataMemory 可指定单 Warp 的原 LMEM route；Flush 前先修改 owner 的 PC/mask。
Runner.MakeVisible 与 MultiRunner.MakeVisible 使用同一可恢复预算接口。
timing-run CLI 使用 backend-cycles，旧 FetchCycles/MemoryCycles 字段仅保留源码兼容。
已接受请求支持取消及重启，旧传输保留、架构结果丢弃；其余未接通调用明确拒绝，
不能把这个增量接口视为完整 T12。后续工作见 `../memory-integration-progress.md`。


NewKernel 默认启用真实存储；Options.MemoryConfig 可覆盖外部后端配置，nil 采用 IR 默认值。
Kernel 忽略旧 FetchCycles/MemoryCycles。原 CTA LMEM owner 在实际服务时校验 generation，
回收等待真实传输尾部。Ready 仅限制新请求展示，不撤销已展示请求。

KernelStatus.Complete 不表示 backing 输出已更新。执行完成后调用 MakeVisible(budget)，
返回 true 才保证 dirty D-cache 数据对原 backing 可见；预算不足可继续调用。
MemoryDrained 与 BackingVisible 单独报告，MakeVisible 不替代 ISA FENCE。

部分提交 SIMD 也可取消：已经展示的完整请求继续传输，local/global 已应用 bytes 不回滚，
未接受的剩余子集仍会应用。取消仅删除架构消费者；旧请求接受不会放行重启后的新指令。
这是无 abort 存储接口的软件契约，不是撤销尚未传输 lane 的承诺。

NewMulti 不再以 nil MemorySystem 选择固定延迟。默认静态身份以 Warp 区分，DataMemory
路由必须在 runner 生命周期内保持绑定；动态分配使用显式 MemorySystem callbacks。
没有显式原子 DataMemory 路由的 local 请求返回错误，不能回退到 global backing。
MultiRunner.MakeVisible 在 Completed 后执行真实 D-cache 扫描/写回，可按预算继续调用。
CLI timing-multi 使用 backend-cycles 配置接受至服务的外部延迟，不再提供统一取指延迟。

取消使用独立 Parked 软件门控，较老指令的 decode unlock 不会重新激活取指。
Restart 仍要求旧 frontend/control 已释放；旧 load 尚未完成但已离开 frontend 时可重启。
健康 Flush 不推进时钟，也不清空 cache；旧 epoch 的传输继续排空，新 Core/effects 使用新 epoch。

System 尚未提交失败周期时，Flush 不跳过该周期；若存储已经提交则只补记时钟，避免重复服务。
协议错误可能发生在组件部分推进之后，因此明确拒绝恢复，保留错误及已有状态。
放弃的显式可见性操作仍完成其传输并排空回复，但不会将 memoryVisible 标记为成功。


FlushCaches(budget) 是 Runner、MultiRunner、Kernel 的外部缓存控制入口：
要求执行已完成，先请求 D-cache 刷新，收到完成后再请求 I-cache 刷新，二者完成才返回 true。
false 表示预算不足，保留状态并继续调用；期间 Run、epoch Flush、MakeVisible 和 Dispatch/Cancel/Restart 会拒绝抢占。
它不执行 ISA FENCE，不重建执行 epoch，也不自动排空运行中的 Core。
MakeVisible 只保证 D-cache 写回，不会使缓存指令失效；重载同址程序应调用 FlushCaches，
之后修改 canonical PC/mask 并调用 epoch Flush 再执行。刷新边沿不改变架构寄存器或退休计数。
控制请求编号在多次操作和 epoch 间递增，避免旧完成或旧请求被当成新操作。
