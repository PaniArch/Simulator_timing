# T12 存储接口契约（里程碑 01）

本文件与 `ir.yaml:memory_contracts` 同步；记录结构与接口，不表示 L1 运行时已实现。
容量和物理 buffer 参数继续使用 `cfg-memory`、`res-*`、`b-*`，不建立第二份经验参数表。

冻结核对链是 `VX_config.toml` → `hw/VX_config.vh` → `VX_gpu_pkg.sv` →
`VX_socket.sv` 的 cache 实例和 `VX_mem_unit.sv` 的实际实例。默认启用 I/D-cache、LMEM，
L2/L3 禁用、单 Core；因此 D-cache 是 writeback LLC，I-cache 只读。`mc-cache`
引用的配置、实例和 cache 内部证据分别支持参数来源、实例覆盖与派生规则。

I/D 容量均为 16 KiB、四路；I 一个 bank、D 两个 bank，各 bank 的 MSHR 为 16 项，
并非每 cache 合计 16 项。每 bank 分别有 64/32 个 set。I 的输入 word 为 4 bytes，
D 为 8 bytes；line/sector/refill 均为 64 bytes。D 的 bank 选择来自 **byte address bit 6**，
不是 bit 3；8-byte 合并粒度不能误当 bank 交错粒度。两个 D 输入端口同 bank 时仍要仲裁。
I 有一个外存端口、D 有两个，最终在软件 external backend 接受边界竞争。

`VX_cache_bank` 的 LATENCY=2 是内部流水级数，不能直接称为总 hit latency。
MREQ 深度为 next-power-of-two(max(2×LATENCY, writeback ? MSHR : 0)+override)，
I/D 每 bank 分别为 4/16；CRSQ 两者均为 2。MRSQ 为每外存端口 I=0（旁路）、D=4。
外层 response/request xbar 与 wrapper 缓冲保留已有 `b-icache-*`、`b-dcache-*` 记录。
请求由 init、replay、fill、flush、core 优先级和队列/流水背压共同决定；不能遇 hit 就跳过资源检查。
FIFO replacement 每 set 初始化为 0，在 fill 的 repl_valid 更新，hit 不更新。
MSHR 在 S0 分配、S1 finalize；hit 释放，pending miss 链接同 line，refill 后 replay。
fill forwarding 只提前服务链首连续普通读，遇写/AMO 后恢复 replay；不能让后续读绕过链内写。
WRITEBACK 与 DIRTYBYTES=0 表示按 enabled bytes 更新 cache，但脏 sector 写回完整 sector。

合并器将 lane 分为 [0,1]、[2,3]，分别选剩余 seed 并比较 8-byte 输出地址；不跨组比较。
每组内 byte enable 逐字节合并；RTL `g_data_merged` 的递增 j 赋值使较后 lane 赢得同批重叠字节。
这个结论不推广到跨组冲突。WAIT/SEND 逐批构造注册输出，最后一批才给输入 ready。
load 批次占用 index buffer，保存 tag、lane mask、word offset；返回 mask 分批展开，全部输出
channel 回来才释放。store 不分配响应 slot，但 WAIT 的 ibuf_full 检查也会阻塞 store。
`VX_lsu_adapter` 使用 stream_unpack 逐输出接受、stream_pack 按 tag 组合响应，不能要求全 mask 同时返回。

LMEM 使用四个 4-byte bank，word address 低两位选 bank；每 bank 单个 LSU 读写端口。
请求 priority xbar OUT_BUF=3，SRAM OUT_REG=1 与 tag pipe 并行对齐；响应数据在背压时保存，
避免 SRAM 后续读覆盖待交付数据。响应 xbar OUT_BUF=3，local adapter 请求 OUT_BUF=3、响应为 0。
冻结配置无 DMA 输入；启用扩展时 DMA 的优先级不属于当前模型。local/global 按每 lane 的
MEM_ATTR_LOCAL_OFFS 分流，保留原 mask/tag；local AMO 被断言拒绝。

## request/response and identity

`mc-interface` 是软件接口选择，不能当作 RTL 字段布局事实。请求包含 operation、lane mask、
word address、byte enable、store data、attributes、tag 与 residency identity。每个边界仅在
valid && ready 接受一次，背压期间保持 payload；合并器输入还必须保持到最后一批 ready。
接口必须分别记录已接受、已应用、已返回的 lane/subrequest，避免部分成功后整组重试。

响应包含原 identity/tag、response mask 和真实返回 bytes。功能层负责地址及 load 解包语义，
load completion 必须消费 cache/LMEM 返回值，不能再次读取 backing。store 接受不等于 SRAM/cache
更新，也不等于 external backing 可见；接口不再强制整组 WriteBatch 原子成功。故障需要保留已发生
的部分效果，不能自动重放已应用的子请求。

identity 保留 kernel、CTA residency、warp slot 的 generation、instruction token/epoch 与 subrequest ID。
这些是软件防迟到事件机制，不是宣称 RTL 总线上存在同名字段。请求、响应、合并映射、effects
均释放后才能回收其相关 CTA；不能为此要求其它 CTA 全部排空。valid/dirty cache line 本身不阻止回收。

原功能 backing owner 仍唯一；这个唯一性不禁止合法 cache 副本。dirty cache 持有较新数据，
backing 只通过正常 external 写请求更新。最终读取 backing 前发起明确写回/可见性操作，
不能直接复制 cache 数据或无条件清空 cache 假装写回。

## pending、同步与完成

`mc-control` 分开列出 hardware instruction pending、LSU scheduler drained、Core busy、
mem_unit_empty、bank_empty、software effect receipt 和 backing visibility。
Warp 停止取指、CTA 可回收、Kernel 执行结束、存储请求排空与输出可见也分别观测。
BAR/WSYNC 继续使用已有 T11 对应 pending/LSU drain 条件，不扩充为全 memory drain。
FENCE 在 LSU slice 设置 is_flush，跳过非 EOP packet；EOP request fire 设置 fence_lock，
fence EOP response fire 清锁。锁定期间阻止后续 execute 接受。它经过存储路径，并非空操作；
与 split/coalescer、DCR flush 的完整组合衔接仍需下述 UNRESOLVED 核对。

DCR flush injector 在一次请求期间只注入一次，并保持 done 到 req 下降。bank flush 先等待 MSHR
和 bank_empty，再走 set/way，dirty eviction 进入正常 MREQ；非零 bank 还等待 bank_empty 后发 done。
这里 bank_empty 是流水有效位全空与 MREQ 空，不包括外部后端完成，也不等价于所有 response 已交付。
因此 flush response 与软件最终可见性 ticket 必须有明确关系，不能从 Core busy=0 推导 backing 已更新。

## external backend

`mc-backend.parameters` 是唯一软件默认参数来源：latency=100 cycles、每周期接受 1 项、
最多 16 项在途、每周期返回 1 项。这个简单共享后端可以配置，完全不声称来自 RTL。
从实际接受边沿计算 due；同 due 使用接受序号。读在服务完成采样、写在服务完成应用 byte enable；
响应被背压时保留且占在途容量。可见性 ticket 等待其之前接受的写完成。
这只是 L1 以下边界，不模拟 DRAM；不得再叠加 FetchCycles/MemoryCycles。

## unresolved gaps

`u-t12-memory-order` 明确保留如下缺口，后续实现必须记录解决方式或软件偏离：

- `VX_lmem_switch` 子 buffer valid 仅检查输入 valid 和自身非空 mask，整体 ready 却要求两路 ready。
  无 accepted-subset 状态。一边持续 ready、另一边阻塞时，前者可能反复接受同一子集；注释的
  “独立接受”不是 once-only 证明。软件必须保证一次接受，但不能把该修复称作已确认 RTL 行为。
- `VX_mem_unit.empty` 实际只 AND coalescer.empty；后者不检查 ibuf_empty，更不直接检查
  DCR 请求 buffer 或 LMEM store tail。Core busy 不能作为完整存储排空证明。
- 跨组同址不同值、跨路径同时事件、flush/refill/backend 同边沿和指令 FENCE 的完整组合规则，
  尚未由当前静态核对闭合。不能用 Go 遍历顺序暗定硬件顺序。backend 的接受序号仅是已声明的软件边界。

检查器验证来源、引用、结构、派生容量与软件/RTL 分类；它不是 RTLSIM 周期等价证明。

里程碑 02 已在 `memsys/backend.go` 实现上述软件外部边界，使用原 AtomicMemoryService。
具体 Step 边沿、轮转接受、队首返回背压、错误及 Visibility ticket 范围见
[memsys/README.md](memsys/README.md)。请求退休空间从下一边沿可用；到期读写服务独立于返回 ready。
这些是明确的软件调度选择，不闭合上文 RTL 的跨组顺序缺口。

里程碑 03 在 `memsys/cache.go` 实现公开多端口 cache 与物理外层缓冲，详见
[cache-progress.md](cache-progress.md)。cache bank done、外层写回全部接受和后端 Visibility
是三个事件；公开 Flush 使用最后一个作为软件返回条件，不把该条件冒充 RTL 指令 FENCE 周期。
新 hit 在 forwarding 期间可先于旧 replay store 访问数据阵列；bank 使用 RTL grant，
不附加软件同址门控。`u-t12-cache-hit-order` 保留上游指令级约束的组合问题。
