# T12 里程碑 03：L1 cache 组件

当前交付 `timing/memsys` 中公开的 `Cache`、bank、数据阵列和 MSHR 组件。
I/D-cache 可通过逐端口握手连接共享 backend；Kernel/Fetch/LSU 的运行时接线属于后续里程碑。
此前外部 backend 与已验证存储契约继续有效。

`FrozenCacheSpec` 直接读取 IR 的容量、bank、way、word、端口、MSHR、流水级和各物理队列记录。
补入的 I-cache word/core/memory port 与 D-cache core/memory port 来自实际 socket/package 实例。
byte address 的 line 位选 bank，随后选 set/tag；不会把 D 的 8-byte word 误当 bank 交错粒度。

`cacheArray` 保存 valid、dirty、tag、sector bytes 和每 set 的 FIFO 指针。fill 推进 FIFO、hit 不推进；
store 只改 enabled bytes 和 dirty。dirty victim 被按值捕获，后续替换无法改写已排队的 writeback。
初始化逐 set 进行；flush 按选中 set/way 失效，不凭一个全局 clear 丢弃 dirty 数据。

`cacheMSHR` 按 RTL 的表更新关系处理同边沿事件：注册 free-index、LSB priority 分配、
S0 allocate/S1 finalize、same-line tail 链接、fill 启动 replay、dequeue 释放和同时 finalize 的 late join。
正在出队的链尾被排除出新分配匹配；hit finalize 不连接 miss 链。额外 generation 是软件迟到响应
保护，不声称 RTL tag 含该字段。bank 的 reserved 计数还包含已接受但未到 S0 的请求。

`cacheBank.step` 持有 S0/S1，按 init、replay、fill、flush、core 优先级选择入口；请求推进受
MSHR reservation、MREQ almost-full 和 response backpressure 控制，不存储预定完成周期。
连续链首普通读可从 fill 数据转发，写关闭该窗口并经过 replay，commit read 优先于 forwarding。
store application receipt 是软件可见事件，不占用一个虚构的 RTL store response slot。
S0 的数据阵列更新和 detached read snapshot 是当前 bank 的功能表达；还未宣称完整 tag/data SRAM
以及外层路径的周期等价。运行错误经 load response 或 store application receipt 返回，失败 fill 不安装数据。

bank flush 等待本 bank 的 MSHR/pipeline/MREQ 条件，再遍历 set/way。bank 0 在最后一次 flush
调度后发 done，非零 bank 还等待 pipeline/MREQ 空；因此 bank done 不是外部写回可见性完成。
初始化期间到达的 flush 使用 init 扫描完成通知，不再重复扫描。`empty`、`drained` 和
resident valid/dirty 统计保留不同含义。

定向测试已覆盖 cold miss、连续同 line word hit、部分字节 store/load、MSHR 满与恢复、
同边沿链尾 finalize/dequeue、slot 复用后的旧 fill 拒绝、dirty FIFO eviction、flush、
MREQ/response 背压，以及 fill fault。bank 测试通过真实 backend 的 Step/ready 握手和一个明确的
response register 连接原 backing owner；不直接修改 backend 私有队列来伪造服务完成。

## 新 hit 与旧 miss chain 的顺序

修复独立审查 AC-011：删除 `orderingConflict`，不再添加 RTL 没有的同 line 准入门控。
`VX_cache_bank.sv` 的 `fwd_head`（975）、`replay_mux`（313）、`creq_grant`（321）和
`core_req_ready`（334）表明：链首普通读从 staged sector 转发时，输入可以接受新请求。
`lk_st0.is_hit`（566）仅由 tag matches 决定；hit 在 S1 释放 MSHR（725），不会排到旧 miss 链后。
因此“链内顺序”不能推广为“整个 cache 按同址接受先后顺序”。这里实现明确的 bank 行为，
不通过未证实的上游不变量或软件串行规则修补它。

`TestCacheBankNewHitDuringForwardChain` 使用真实 backend 构造 miss chain：read 1/2/3、
store 4（77）、read 5。以 refill 接受为 F，无背压且 MSHR 有空间时，新请求 6 在 F+1 接受。
LATENCY=2 的 fill 在 F+1 写数据阵列，新请求在 F+2 访问阵列：

- 新请求为 read：F+4 返回旧值 0；其 commit 抢占 forward port，使 read 3 转入 replay，
  F+6 返回 0。旧 store 4 在 F+6 报告应用，read 5 在 F+8 返回 77。
- 新请求为 store（99）：F+3 报告应用；read 3 在 F+4 返回 staged sector 的 0，
  旧 store 4 在 F+6 报告应用并覆盖 99，read 5 在 F+8 返回 77。

两条路径最终 cache 值均为 77，backing 未被 store 直接修改。测试检查实际接受边沿、
返回值、store receipt、MSHR 排空；原 MSHR 满恢复测试继续验证已进入同一 miss chain 的顺序。
冻结 LATENCY=2 令 PIPE_EX=0，`g_no_fill_inflight` 明确令 fill_inflight=0。
`TestCacheBankFillAfterSingleForward` 检查第二个 refill 在第一个 refill 的 F+2 接受，
不因前一个 fill 尚在 S1 而添加软件互锁。
上述 bank 规则已由静态信号闭合；`u-t12-cache-hit-order` 仅保留上游是否禁止这种输入、
以及完整指令级顺序约束的组合问题，不能把 RTL 注释的“program order”推广到新 hit。

## 公开 cache 与外层资源

`Cache.Step` 通过 IR 指定的轮转仲裁连接 core ports 与 banks，以及 banks 与 response ports。
请求 crossbar 是零缓冲边界，不能凭软件调用顺序让两个同 bank 请求同时接受。
外层 core response、memory request、MRSQ、refill crossbar 和 D-cache NC switch 的队列容量
分别由物理边界记录读取；单槽可在 pop 时替换，多槽使用旧边沿 credit。
I-cache 的零容量 MRSQ 直接进入 refill crossbar，不能把零容量解释为无限缓冲。

D-cache NC 请求经旁路仲裁和实际后端读写；cached 与 NC response 竞争最终返回缓冲。
NC 不分配 cache line；同一物理地址的 cached/NC 别名一致性不由该旁路提供。
所有 refill 使用内部 transaction、原请求 identity/tag 和 MSHR generation 关联。
请求身份在最终外层队列分配，使 NC/cache 仲裁不能打乱每个后端端口的 transaction 单调性。
writeback 使用 cache 自身身份，错误按后端接受序号保留，不按端口循环次序选择。

外层 flush 锁定本 cache 的新请求，向所有 banks 发一次 pulse，汇集各 bank done，
再等待 pipeline/MREQ 与外层 writeback 队列的尾部被后端接受。之后经正常请求队列提交
软件 `Visibility` ticket，返回可背压的控制完成；写回错误不被成功 ticket 掩盖。
这不是强制其它 cache 或整个 Core 排空，也不要求旧 core read response 全部交付。
RTL `VX_cache_init` 的原 flush 请求释放由软件控制 ticket 表达：不模拟其 dummy address
再经过下游请求的逐拍轨迹。该下游控制映射与更严格的 backing 可见完成属于软件接口，
不声称是 RTL flush response 的相同周期；指令 FENCE/DCR 注入接线仍属后续里程碑。

`State` 分别观测初始化、flush、reserved MSHR、流水、各物理队列、外部在途以及 valid/dirty。
`Drained` 不把 resident line 算作请求；`HasResidency` 检查仍携带指定 kernel/CTA 身份的
已接受事务和待交付响应，不让 cache-owned dirty writeback 留住旧 CTA。
上游未获接受的请求仍由上游负责；软件 store receipt 是该边沿必须消费的完成事件。

公开接口定向测试补齐同 bank 多 port 冲突、跨 bank 并发、I/D 共享 backend、
连续 hit store/load、MSHR 饱和恢复、有限队列压力、refill/writeback 竞争、
NC 路径、响应与 flush 返回背压、写回故障、外部身份拒绝和 residency 尾部。
冷 miss 与同 line 不同 word 请求分别返回真实数据；无竞争热 hit 从接受到交付，
I-cache 为 4 个边沿、D-cache 为 5 个边沿，包含其不同的外层返回缓冲。
这些测试是组件数据和资源回归，不是 RTLSIM trace 精度证明。
