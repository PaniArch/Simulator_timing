# RTLSIM 与 Timing 模拟器：双侧 trace 证据及全通过集小规模复测

本报告将原运行汇总中的逐事件分析独立保存。功能 PASS、周期数接近和内部时序正确是三个不同结论，必须分别检验。
历史运行结果见 [运行汇总](runtime-error-diagnosis-20260910.md)。

## 全通过集小规模复测（已完成）

2026-09-14 完成：**全部 20 个 benchmark 双侧重跑并 PASS**，35 次 launch，
105,927 条 macro、106,183 个 uop 的逻辑线程 PC/mask 序列及事件数核验通过。
作业与最终核验均已结束；没有遗留运行或排队任务。

本轮小参数系统级平均绝对相对周期误差为 **36.69%**，范围 **0.044%–77.10%**。
周期加权误差只有 3.88%，但被占 RTL 总周期约 88% 的 occupancy 主导，不能用它声称
普遍达到 96% 精度。这里还混合了固定 100 后端对 Ramulator 的边界差异，
不是同 memory 响应条件下的纯 Core 精度，也不能与原规模测试的约 22% 直接比较。

阅读顺序：§11 的全量结果与每项事件链；§12 的跨 benchmark 实证与设计问题；
§13 的最终核验。§10 保留先前 wgather/vecadd 隔离实验，未与本轮新输入混算。

重新运行全部 20 个历史 Timing PASS benchmark，Timing 与 RTLSIM 均重新执行，使用相同 host/kernel 二进制与参数。
证据目录：`.cache/trace-suite-small-20260914/`。正式模型和 RTL 不作修改；Timing 的观测 overlay 只增加输出。
外部 backend 保留模型原有固定 100 cycles；RTL 保留原有 Ramulator。因此总周期差包含外部系统边界差异，不能全算为 Core 建模错误。
多 launch 独立配对，保留原始日志行号；packed 指令区分 macro decode 与 uop commit；不以跨 Warp 全局顺序相同作为前提。

## 先前诊断实验（原报告 §10，保留其内部编号）

## 10. 双侧实际 trace 与隔离因果实验（2026-09-14）

本节补足 §9 只有 release/PERF 总数、没有逐事件 RTL 对照的证据缺口。§9 的
memory/control 归因是候选方向，不能当作已经证明的建模错误。本节所有诊断源码
位于忽略目录的 Go overlay；正式 model、RTL 和 Timing IR 未修改，也未提交修复。

证据根目录：`.cache/trace-audit-20260914/`。主要文件：

- `rtl/stdout.log`：wgather 的 RTL 原始文本 trace（包含 reset 前无意义输出，解析时排除）。
- `timing/stderr.log`：同输入 Timing Core 边沿、backend offer/接受/返回。
- `detail/run/stderr.log`：追加 Cache/coalescer 接口观测，周期结果保持 1792。
- `aligned.csv`、`alignment-summary.json`、`analyze.py`：逐指令对齐与可重跑解析器。
- `port0/run/`：仅补 D-cache port 0 两槽普通请求缓冲的隔离诊断；不是完整控制路径修复。
- `vecadd-baseline/`、`vecadd-port0/`：同一诊断变化对另一 workload 的验证。
- `artifacts.sha256`、`overlay*.json`、`*.sbatch`：库哈希、诊断源码映射、运行命令。

### 10.1 实验有效性和事件口径

RTLSIM 文本 trace 库保留 release 编译、PERF 和原 Ramulator，只开启
DBG_TRACE_PIPELINE/MEM/CACHE；没有开启 VCD 或更改硬件结构。源码对照：
`Vortex_rtl/hw/rtl` 与 `../vortex/hw/rtl` 的源文件相同（后者额外有 .DS_Store）；
两份 VX_config.toml SHA-256 都是
`3f020a15364555da8da610b5f78501f670299440685846fb40bb0f757593fb93`。
沿用 §9 冻结的 host/kernel 输入。

wgather 参数 `-n8 -t4`：RTL 重现 **1247 cycles / 440 instructions / host PASS**；
Timing 重现 **1792 execution cycles / 440 retired / host PASS**，flush 单独为 410。
双侧每 Warp 的 110 条译码 PC 序列逐项相同。作业 12735149 完成双侧 trace；
12735169 完成细粒度 Timing 观测；12735178 完成 port 0 对照；12735186 完成 vecadd 对照。
此前 debug 排队作业 12735133 未运行即取消，12735138 的 RTL 库构建成功，
Go overlay 因观测变量重名编译失败；修正后才运行以上实验，不能把它们算作模拟器失败。

RTL 文本时间戳每个完整周期增加 2。为展示指令相对时间，定义
`rtl_cycle=(timestamp-121)/2`，零点是第一个 scheduler dispatch。
这不是 PERF busy 计数的起点；只在同端点区间内比较延迟。
按 Warp、PC、出现次序匹配，不能使用不同模拟器自己的 UUID/token 直接匹配。
RTL commit 文本打印的是 FU→commit 仲裁入口握手，模型对应
`n-commit enter`，不是再下一拍的 `writeback complete`。
有片段返回的 load 只用 EOP=1 对齐最终 commit，不能把第一次片段当成下一次同 PC 指令。

440 条指令的区间对比：

| 相同起止端点 | 间隔完全相同的指令数 |
| --- | ---: |
| scheduler dispatch → decode | 424/440 |
| decode → dispatcher 出口接受 | 337/440 |
| dispatcher 出口接受 → 最终 commit 入口接受 | 370/440 |

这些数目不能相加，也不是全局周期等价率；等待的依赖和竞争已受其它差异影响。
但足以否定“所有热取指或普通流水段统一多一拍”的笼统解释。

### 10.2 I-cache 的四次 miss：启动边界与外部服务差异有明确 trace

下表使用同一 Warp 0 的指令，RTL 周期按上述零点归一化：

| PC / line | Timing schedule→decode | RTL schedule→decode | 本段间隔差 | decode 时累计偏移 |
| --- | --- | --- | ---: | ---: |
| 0x80000000 | 0→172 | 0→45 | 127 | 127 |
| 0x80000040 | 299→475 | 172→250 | 98 | 225 |
| 0x80000080 | 612→723 | 387→430 | 68 | 293 |
| 0x800000c0 | 860→971 | 567→596 | 82 | 375 |

这 4 个 miss 各影响四个 Warp，恰好对应前表 16 条不同的 schedule→decode 区间。
其余 424 条在相同端点没有额外延迟。375 是该位置的实际累计相位偏移，
不是可以与其它重叠指令延迟任意相加的全程归因百分比。

首个 miss 的原始证据：

- RTL `rtl/stdout.log:187` 首 schedule，timestamp 121；
  `:221` I-cache core 接受，timestamp 145；
  `:240` I-cache memory 请求，timestamp 153；
  `:258` memory 返回，timestamp 203；
  `:271` decode，timestamp 211。
- Timing `timing/stderr.log:2` schedule cycle 0；
  `:130` core 接受 cycle 64；
  `:137` backend 接受 cycle 68；
  `:337` backend 返回 cycle 168；
  `:346` decode cycle 172。

于是首个 decode 的 127 周期差可在这些端点上分解为：
schedule→Cache 接受多 **52**；Cache 接受→memory 接受均 **4**；
memory 接受→返回分别 **100/25**，多 **75**；
返回→decode 均 **4**。这里 52 包括启动/初始化相位边界，
不能未经进一步对齐 init 内部流水就全部称作 Cache 内部 latency 错误。
RTL 的 tags-init 在 timestamp 19 已开始，而首 dispatch 在 121；
Timing 则在 Kernel 新建存储系统后从 cycle 0 推进初始化。
对应构建问题在 Runtime/Kernel 生命周期衔接与测量起点，而非指令通用执行延迟。

四个 I-cache memory 请求在 RTL 的接受/返回 timestamp 依次为
153/203、479/613、909/973、1269/1305，即 L1 memory 端点区间
**25、67、32、18 cycles**。Timing 同类区间都是 **100 cycles**。
RTL 区间包括该端点以下互连和后端；不将其直接等同于 Ramulator 内部服务时间。
遵循任务指定的固定 100 本身不是实现 bug，拿不同外部条件的结果验收内部时序才有问题。

第二个 miss 还显示了共享在途容量的实际影响：
Timing cycle 306 已展示 I-cache 请求，但 16 项 D-cache 请求占满 backend；
cycle 370 返回最早在 270 接受的 D-cache 请求，旧 credit 规则使 I-cache
直到 371 才接受，471 返回、475 decode。原始行号为
`timing/stderr.log:613,741,743,943,952`。
RTL 在该指令 schedule 后 7 周期就发出 L1 memory 请求，Timing 需要 72 周期。
因此本段 98 周期差等于 **65 接受等待 + (100−67) 服务区间差**。
这是可以从 offer/accept/response 直接重建的背压，不是凭 benchmark 类型推测。

### 10.3 确认漏建：只有 D-cache port 0 存在的请求缓冲被省略

冻结 `Vortex_rtl/hw/rtl/core/VX_mem_unit.sv:381` 的
`g_flush_port` 明确只把 port 0 接入 `VX_dcr_flush`，
`REQ_OUT_BUF=3` 解码为 SIZE=2、OUT_REG=1；其它端口直通。
这个缓冲也承载普通 load/store，不能因为当时没有 flush 指令就省略。
IR 的 `b-dflush` 已记录该边界；架构图也画了 n-dflush。

但 `timing/memsys/system.go:163` 将 `globalAdapter.Offers(batch)`
两个端口直接送入 `data.Step`，`global_adapter.go:59` 的普通请求路径是零缓冲；
生产代码没有实例化 `b-dflush`。在 T12 finish（06e944b）的 System 中已经如此。
因此可以把引入阶段定位到 **T12 存储组件组合/生产接线没有落实已有 IR 边界**，
而不是猜测为 RR 算法、lane 地址计算或 FPU latency 错误。

首组四 lane 栈 store 的 D-cache 实际接受：

| 次序 | RTL 原始 timestamp / port / word 地址 | Timing cycle / port / word 地址 |
| --- | --- | --- |
| 1 | 397 / 1 / 0xfffebff0 | 265 / 0 / 0xfffefff0 |
| 2 | 399 / 0 / 0xfffefff0 | 266 / 1 / 0xfffebff0 |
| 3 | 401 / 1 / 0xfffe9ff0 | 268 / 0 / 0xfffedff0 |
| 4 | 403 / 0 / 0xfffedff0 | 269 / 1 / 0xfffe9ff0 |

RTL 原始行号 `rtl/stdout.log:924,932,943,954`；
Timing Cache 输入和握手行号 `detail/run/stderr.log:1328,1333,1343,1348`。
Timing cycle 267 两端口都无请求；RTL 四项连续逐周期进入同一 bank。
不是 RTL RR 初始指针应该反转：RTL port 0 先被上游缓冲接纳，
port 1 先到 bank，缓冲吸收了 coalescer 的两拍 batch 生成节奏。
模型把“adapter 接受”直接等同于“Cache 接受”，改变了端口相位、仲裁输入和背压。

因果干预：仅在 Go overlay 中为 port 0 增加两槽普通请求缓冲，
并分别跟踪 adapter 接受与 Cache 接受，保持 100 周期后端及 Cache bank 不变。
对照 trace `port0/run/stderr.log:1328,1333,1338,1343` 变为
cycle 265/266/267/268、port 1/0/1/0，四地址顺序与 RTL 相同；
前 16 项 D-cache memory 请求也从有 batch 间空拍变为连续接受。

| 同输入 workload | 原 Timing | 仅补 port 0 缓冲 | 改善 | 原 RTLSIM |
| --- | ---: | ---: | ---: | ---: |
| wgather -n8 -t4 | 1792 | 1760 | 32 cycles | 1247 |
| vecadd -n1024 | 28401 | 28209 | 192 cycles | 17532 |

四次 Timing 执行均 host PASS，retired 各自保持 440/6160，最终输出可见。
这证明漏建确实改变周期，也证明它**不能解释全部误差**：vecadd 的
10869 周期差仅减少 192（约 1.77%）。不得把发现一个真实 bug 写成全局根因已解决。
诊断缓冲尚未覆盖独立/原位 flush、取消、故障、身份尾部和全部回归，
不应直接作为正式修复合入。

### 10.4 下次构建实验的具体防漏措施

1. 每个 Timing IR 物理边界记录 port/lane 范围、buffer 编码、实例路径、
   生产实现位置、可观测握手和对应 RTL trace 测试。不能只检查 RTL 文本锚点存在；
   b-dflush 必须验证“仅 port 0 普通访存也经过该边界”。
2. 组件组合里程碑验收使用真实实例化路径和双侧事件序列。本次最小门禁就是
   两个 port 同 bank、连续两个 coalescer batch，逐拍检查 adapter accepted、
   Cache accepted、端口/地址顺序和 backpressure。单独 adapter→Cache 的数据 PASS
   会复制同一个拓扑遗漏，不能替代系统接线门禁。
3. 将“内部周期等价实验”和“部署后端端到端估计”分开。前者要统一 L1 memory
   边界的延迟、接受/返回带宽、在途 credit、顺序和返回 ready；后者可以保留
   fixed-100 对 Ramulator，但只报告系统差异，不能把总周期差全部算作 Core 模型误差。
4. 统一 reset、DCR 配置、Cache init、首 dispatch、busy、最终 EOP commit、
   residency 回收和 backing visibility 的计数起止。保留执行和 flush 分项，
   并记录每个事件的原始 timestamp，避免手动减一个“启动常数”掩盖生命周期问题。
5. 验收记录必须包含 trace 原始文件、哈希、解析器、同端点匹配规则、
   第一个差异及其上游状态。尤其区分 FU commit 入口/WB 出口、
   load 部分响应/EOP、宏指令/uop；本次对齐显式处理了这些差异。
6. 发现偏差后先做单变量隔离干预，验证局部事件序列恢复，再扩大 benchmark。
   只看总周期更接近不能证明修对；本次端口顺序/节拍恢复与总周期变化同时成立，
   才将 b-dflush 缺失归为确认的时序建模问题。

### 10.5 vecadd 完整 trace：将 10869 周期差分成不重叠的实际区间

作业 12735197（COMPLETED，exit 0）完成第二个双侧完整 trace。
目录 `trace-audit-20260914/vecadd-trace/` 中的 `rtl/stdout.log` 重现
**17532 cycles / 6160 instructions / PASS**；`timing/events.jsonl`
重现 **28401 execution cycles / 6160 retired / PASS**。
精简 Timing 观测不改变周期。解析命令：

```bash
python3 .cache/trace-audit-20260914/analyze.py .cache/trace-audit-20260914/vecadd-trace
```

两侧每 Warp 的 1540 条译码 PC 序列完全相同。6160 条指令中，
6152 条 schedule→decode 区间相同，只有首次取两个 I-cache line 的 8 条不同；
不能把本例 62% 偏差主要归为所有热 I-cache 访问都多等待。

#### A. 数据 load 的额外时间能直接落在 L1 外部服务区间

选 Warp 0、PC=0x80000054、第 11 次执行（occurrence=10），读取 line 0x10280：

| 事件 | Timing cycle / 原始行 | RTL timestamp / 归一化 cycle / 原始行 |
| --- | --- | --- |
| FU dispatch 接受 | 4852 / timing/stderr.log:9706 | 6079 / 2979 / rtl/stdout.log:27266 |
| D-cache memory 请求接受 | 4862 / :9725 | 6099 / 2989 / :27374 |
| D-cache memory 返回 | 4962 / :9925 | 6135 / 3007 / :27602 |
| 最终 EOP commit 入口接受 | 4973 / :9948 | 6157 / 3018 / :27763 |

因此该条 load 的 dispatch→commit 为 **121 对 39 周期**：
前半路径 **10 对 10**，L1 memory 请求→返回 **100 对 18**，
后半路径 **11 对 11**。多出的 **82** 周期全部落在这条请求的外部服务区间，
不是这条 load 的 Cache 命中流水、返回拆分或 WB 普遍多加了拍数。
这不推导为所有其它 load 的内部路径都正确。

依赖这两条 load 的 PC=0x8000005c（FADD）：
Timing decode 4863、dispatch 4992，等待 129 周期；
RTL decode 2990、dispatch 3035，等待 45 周期。
其 dispatch→commit 分别 4992→5003、3035→3044（11 对 9）；
前面的 84 周期大额差是 load 依赖等待，不能直接算成 FPU 运算延迟错误。
两条 load 的完成/竞争相位已有差异，本次也不把这里的 2 周期差断言为 FPU 固有 latency bug。

#### B. Warp/CTA 生命周期抽象每轮实际多出 80 周期

同一轮 Warp 0 的末尾 TMC（PC=0x80000020，occurrence=10）：
Timing commit **5053**，下一轮首次调度 PC=0x80000010 为 **5134**，间隔 **81**；
RTL 对应 commit **3094**，下一轮调度 **3095**，间隔 **1**。
在全部 **63 次轮次衔接中，差值都恰好是 80**，不是单次随机竞争现象。

第一次衔接的原始 RTL 记录尤其直接：
`rtl/stdout.log:2995-2996` 在 timestamp 869 同时记录
Warp 0 旧 CTA 的 warp-done 与新 CTA 的 dispatch；
Warp 2/3 到 timestamp 895/901 才完成旧轮次并被复用。
实际 RTL 在旧 CTA 的其它 Warp 仍活动时已经开始复用先结束的 Warp。

Timing 在首轮四个 Warp 的最终 TMC commit 663/664/676/679 后，
等到 cycle 744 才调度下一轮 Warp 0；随后 745/746/747 才调度其余成员。
模型中 `Kernel.residency` 先检查整个旧 CTA 的
`observeCTA(...).Reclaimable`，释放后才找到完整的 `WarpsPerCTA` 个空闲 Warp；
`observeCTA` 又要求 `WarpQuiescent` 与存储 residency 引用都清空。
这是 whole-CTA 接纳及软件 effect 生命周期的真实执行后果。

在第 11 轮，PC=0x80000064 的 store 已在 cycle 5015 进入硬件 commit，
但其 line 0x12280 write-allocate 请求在 backend 5020 接受、5120 返回，
下一轮到 5134 才启动；这个等待跨过了 5053 的 TMC。
RTL 同 line memory 请求 timestamp 6243（cycle 3061）发出，
新轮次在 3095 已开始。因而要区分硬件 Warp 可复用与旧 store 数据/Cache 尾部：
不能让为了保护软件 owner 的广义 drain 自动成为新的硬件调度约束。

这是 T11 明确保留、T12 沿用的生命周期抽象导致的精度差异；
不是本次报告擅自把已约定的抽象认定为违反任务范围。
但如果下一轮目标是 RTL 周期精度，就必须把这个抽象列为待改建模项。

#### C. 完整差值的区间账本

沿 Warp 0 的有序动态指令序列，把整个执行分成互不重叠的区间。
不将不同 Warp 同时发生的 stall 相加：

| 时间区间 | Timing−RTL |
| --- | ---: |
| 首次调度到第一轮 Warp 0 最终 TMC commit | 289 |
| 63 次旧 TMC commit→下一轮首次调度，逐次都是 81−1 | 5040 |
| 后续 63 轮首次调度→该轮最终 TMC commit，32 次差 88、31 次差 86 | 5482 |
| 总 execution/PERF 起止口径相对上述指令端点的剩余差 | 58 |
| 合计 | **10869** |

账本是原始事件区间的精确分解；5040/5482 不代表彼此完全独立的根因参数，
因为固定后端延迟也延长旧 CTA 的存储尾部。不能把不同对照实验的净改善再与它们相加。
最后 58 尚未通过统一 busy/执行完成端点进一步归因，明确保留为边界残差。

本次发现的优先级由证据决定：对 vecadd，大额差异集中在重复的数据等待、
以及 Warp/CTA 复用衔接；port 0 缓冲遗漏确实存在，但单变量对照只改善 192。
尚未对其它 18 项逐事件检查，不把本例结论扩展成它们的已证根因。

对应下一轮补充门禁：

- 加入“Warp 0 提前结束、其它 Warp 尚活动、旧 store 尚有 Cache 尾部”的 RTL 对照。
  分别验收 Warp 可复用、CTA descriptor/LMEM 可释放、旧请求 generation、
  effect 回执完成、输出可见；保留安全性但避免把所有状态统一成一个 Reclaimable。
- 请求一经接受且地址/数据/byte enable 已锁存，应明确后续还依赖哪份 residency 状态；
  可以独立存续的 store 不应仅因软件回执未回收就阻止 RTL 允许的 Warp 调度。
  具体实现还需覆盖迟到 load WB、LMEM 生命周期和部分取消，不能直接删检查。
- 固定后端与 RTL 基准的对齐必须先于 FPU/Scoreboard 调参。本例 FADD 的大部分
  等待来自 upstream load；仅看 PC 上总等待会误修执行单元。

<!-- SMALL-SUITE-BEGIN -->
## 11. 全部历史 Timing PASS benchmark：小规模双侧重跑

本轮不是复用旧 RTLSIM 结果：20 个 benchmark 的 host/kernel 输入快照不变，仅缩小运行参数，每项重新运行 Timing 与 RTLSIM。初始 Slurm 数组 `12736090_[0-19]`；`sgemmx` 参数纠正重跑 `12736674_16`。

| benchmark | 本轮参数 | Timing cycles | RTL cycles | 相对差 | launch | 状态 |
|---|---|---:|---:|---:|---:|---|
| occupancy | `-c3` | 648,321 | 648,033 | +0.04% | 1 | 双 PASS |
| sgemm2 | `-n8 -t4 -c4` | 12,014 | 10,143 | +18.45% | 1 | 双 PASS |
| basic | `-n32` | 2,335 | 1,971 | +18.47% | 1 | 双 PASS |
| bfs | `-n32` | 20,320 | 14,821 | +37.10% | 8 | 双 PASS |
| wsync | `-i16` | 9,324 | 7,236 | +28.86% | 1 | 双 PASS |
| demo | `-n4 -x4 -y4` | 1,317 | 873 | +50.86% | 1 | 双 PASS |
| dotproduct | `-n32` | 3,935 | 3,312 | +18.81% | 1 | 双 PASS |
| dotproduct2 | `-n32` | 3,225 | 2,526 | +27.67% | 1 | 双 PASS |
| dropout | `-n32` | 1,955 | 1,308 | +49.46% | 1 | 双 PASS |
| io_addr | `-n4` | 2,081 | 1,450 | +43.52% | 1 | 双 PASS |
| mstress | `-n4` | 2,730 | 2,388 | +14.32% | 1 | 双 PASS |
| multikernel | `-n32` | 17,618 | 12,439 | +41.64% | 3 | 双 PASS |
| packld | `固定默认` | 3,893 | 3,088 | +26.07% | 1 | 双 PASS |
| pathfinder | `-n8` | 7,791 | 4,958 | +57.14% | 7 | 双 PASS |
| relu | `-n32` | 1,290 | 799 | +61.45% | 1 | 双 PASS |
| sgemm | `-n8` | 4,445 | 3,508 | +26.71% | 1 | 双 PASS |
| sgemmx | `-n16` | 15,117 | 11,856 | +27.51% | 1 | 双 PASS |
| sgemv | `-m8 -n8` | 1,559 | 1,065 | +46.38% | 1 | 双 PASS |
| vecadd | `-n32` | 1,183 | 668 | +77.10% | 1 | 双 PASS |
| wgather | `-n4 -t4` | 1,188 | 732 | +62.30% | 1 | 双 PASS |

参数说明：`occupancy -c3` 保留超过两 CTA 同驻留上限的资源复用；单 CTA 的半区 LMEM 初始化/求和循环不可通过 CLI 缩小。`sgemm2 -n8 -t4 -c4` 保留四 CTA、LMEM 与同步。`packld` 无规模选项，保留固定测试。`sgemmx -n8` 首次被双侧 host 的 M/N 必须为 16 倍数检查拒绝，未启动仿真；改用最小合法 `-n16`，原日志保留在 `results/sgemmx-invalid-n8/`，未覆盖或计入 PASS。

### 11.1 比较边界与证据有效性

- Timing 用 runtime `launch-finish.execution_cycles` 的和；不使用打印为 0 的 PERF 占位计数。RTLSIM 用本进程最后一条累计 PERF cycles。多 launch 不重复累加累计 PERF。
- Timing 的最终 host 可见性 flush cycles 单独保存，未叠加到 execution cycles；RTL PERF 与软件执行结束边界未证明完全同义，所以不能把总数残差都归到流水线。
- RTL 原始时间每周期增加 2；按每 launch 首次真实 scheduler dispatch 定义零点，丢弃首条 kmu start 之前的 reset 噪声。所有表内阶段都是该归一化周期，不是 PERF 起点。
- 按逻辑 CTA 和 CTA 内 Warp 对齐，双侧 physical warp 可以不同；采用 Timing 的实际 `scheduler/cta-dispatch` 与 RTL `kmu-accept/cta_dispatch` 记录，不硬编码启动 trampoline 的 PC。
- 先检验每逻辑线程的完整 PC/mask 顺序及展开后的 uop 数。packed-load 的 macro decode 与各 uop dispatch/最后 EOP commit 分开处理；最后一个 uop 的 commit 入口不等于模型下一拍 writeback。
- load 证据要求 Timing token identity、sector 地址、同 launch 唯一 RTL 读请求，以及该指令的 dispatch/commit 区间同时吻合。无法满足的请求不做因果配对。
- 双侧使用相同冻结硬件配置；Timing 后端保持 100-cycle service，RTL 保持 Ramulator。比较得到的是这两个完整系统边界下的偏差，不是同 memory 响应序列下的纯 Core 误差。
- 未改正式 simulator、RTL、Timing IR、benchmark 源码；只构建观测 overlay。没有将上文 port0 诊断修正库用于本轮基线。

输入、动态库 SHA256、参数和源码 HEAD 见 [manifest.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/manifest.json)；运行器 [run.py](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/run.py)、[run.sbatch](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/run.sbatch)；解析器 [analyze.py](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/analyze.py)、[evidence.py](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/evidence.py)。所有产物位于本地忽略目录 `.cache/trace-suite-small-20260914/`，不是已提交到 Git 的附件。

### 11.2 逐 benchmark 的事件链

下面展示可严格匹配的一条 load 和一个分派等待样例；它们是局部定位证据，不声称单条样例解释整个 benchmark 的总差。完整 trace 和所有对齐行一并保留。


### occupancy：`-c3`

Timing **648,321**，RTLSIM **648,033** cycles，差值 **+288（+0.04%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 86,117，展开后 uop 86,117；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：86111/86117；decode-dispatch：86109/86117；dispatch-commit：86109/86117。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x8000003c`（LW，本 CTA 中第 1 次），sector `0x00011000`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 323 · [timing:649](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/timing/stderr.log:649) | 196 · [rtl:731](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/rtl/stdout.log:731) |
| L1 外部请求接受 | 333 · [timing:668](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/timing/stderr.log:668) | 207 · [rtl:767](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/rtl/stdout.log:767) |
| L1 外部响应 | 433 · [timing:868](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/timing/stderr.log:868) | 232 · [rtl:840](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/rtl/stdout.log:840) |
| 最后 EOP 到 commit 入口 | 443 · [timing:889](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/timing/stderr.log:889) | 242 · [rtl:894](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/rtl/stdout.log:894) |

因此这条指令的差值为 `(10 + 100 + 10) − (11 + 25 + 10) = +74` cycles；分别落在接受前 **-1**、外部请求/响应段 **+75**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 333（[timing:668](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/timing/stderr.log:668)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 10 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 0 / `0x80000048` BEQ，Timing 7、RTL 11，差 -4。证据：[timing:897](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/timing/stderr.log:897) → [timing:911](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/timing/stderr.log:911)；[rtl:877](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/rtl/stdout.log:877) → [rtl:896](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/rtl/stdout.log:896)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/evidence.json)。


### sgemm2：`-n8 -t4 -c4`

Timing **12,014**，RTLSIM **10,143** cycles，差值 **+1,871（+18.45%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 3,456，展开后 uop 3,456；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：3416/3456；decode-dispatch：2281/3456；dispatch-commit：2655/3456。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x80000034`（LW，本 CTA 中第 1 次），sector `0x00011000`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 287 · [timing:577](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/timing/stderr.log:577) | 160 · [rtl:1316](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/rtl/stdout.log:1316) |
| L1 外部请求接受 | 471 · [timing:944](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/timing/stderr.log:944) | 223 · [rtl:1941](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/rtl/stdout.log:1941) |
| L1 外部响应 | 571 · [timing:1144](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/timing/stderr.log:1144) | 271 · [rtl:2278](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/rtl/stdout.log:2278) |
| 最后 EOP 到 commit 入口 | 582 · [timing:1167](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/timing/stderr.log:1167) | 282 · [rtl:2408](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/rtl/stdout.log:2408) |

因此这条指令的差值为 `(184 + 100 + 11) − (63 + 48 + 11) = +173` cycles；分别落在接受前 **+121**、外部请求/响应段 **+52**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 432（[timing:866](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/timing/stderr.log:866)），直接等待 backend 接受为 39 cycles；dispatch 到首 offer 为 145 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 0 / `0x8000003c` LW，Timing 284、RTL 111，差 +173。证据：[timing:597](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/timing/stderr.log:597) → [timing:1165](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/timing/stderr.log:1165)；[rtl:1418](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/rtl/stdout.log:1418) → [rtl:2392](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/rtl/stdout.log:2392)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm2/evidence.json)。


### basic：`-n32`

Timing **2,335**，RTLSIM **1,971** cycles，差值 **+364（+18.47%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 177，展开后 uop 177；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：175/177；decode-dispatch：171/177；dispatch-commit：173/177。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x8000003c`（LW，本 CTA 中第 17 次），sector `0x00010040`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 1361 · [timing:2725](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/timing/stderr.log:2725) | 1077 · [rtl:2960](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/rtl/stdout.log:2960) |
| L1 外部请求接受 | 1371 · [timing:2744](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/timing/stderr.log:2744) | 1087 · [rtl:3001](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/rtl/stdout.log:3001) |
| L1 外部响应 | 1471 · [timing:2944](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/timing/stderr.log:2944) | 1103 · [rtl:3037](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/rtl/stdout.log:3037) |
| 最后 EOP 到 commit 入口 | 1482 · [timing:2967](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/timing/stderr.log:2967) | 1114 · [rtl:3070](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/rtl/stdout.log:3070) |

因此这条指令的差值为 `(10 + 100 + 11) − (10 + 16 + 11) = +84` cycles；分别落在接受前 **+0**、外部请求/响应段 **+84**、响应后 **+0**。两侧前后段相同，本例额外时间完整落在 L1 外部服务段。

Timing 首次向 backend offer 在 cycle 1371（[timing:2744](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/timing/stderr.log:2744)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 10 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 0 / `0x80000048` SW，Timing 108、RTL 24，差 +84。证据：[timing:2765](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/timing/stderr.log:2765) → [timing:2981](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/timing/stderr.log:2981)；[rtl:3023](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/rtl/stdout.log:3023) → [rtl:3071](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/rtl/stdout.log:3071)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/basic/evidence.json)。


### bfs：`-n32`

Timing **20,320**，RTLSIM **14,821** cycles，差值 **+5,499（+37.10%）**；共 8 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 1,842，展开后 uop 1,842；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：1733/1842；decode-dispatch：1519/1842；dispatch-commit：1632/1842。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 2、CTA 0、逻辑 Warp 0，PC `0x80000038`（LW，本 CTA 中第 1 次），sector `0x00011000`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 295 · [timing:6050](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/timing/stderr.log:6050) | 166 · [rtl:7039](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:7039) |
| L1 外部请求接受 | 372 · [timing:6203](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/timing/stderr.log:6203) | 176 · [rtl:7181](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:7181) |
| L1 外部响应 | 472 · [timing:6403](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/timing/stderr.log:6403) | 222 · [rtl:7505](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:7505) |
| 最后 EOP 到 commit 入口 | 483 · [timing:6426](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/timing/stderr.log:6426) | 233 · [rtl:7565](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:7565) |

因此这条指令的差值为 `(77 + 100 + 11) − (10 + 46 + 11) = +121` cycles；分别落在接受前 **+67**、外部请求/响应段 **+54**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 305（[timing:6069](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/timing/stderr.log:6069)），直接等待 backend 接受为 67 cycles；dispatch 到首 offer 为 10 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 4 / CTA 0 / 逻辑 Warp 0 / `0x800000e8` SLTIU，Timing 245、RTL 68，差 +177。证据：[timing:19502](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/timing/stderr.log:19502) → [timing:19992](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/timing/stderr.log:19992)；[rtl:22216](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:22216) → [rtl:22383](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:22383)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/evidence.json)。


### wsync：`-i16`

Timing **9,324**，RTLSIM **7,236** cycles，差值 **+2,088（+28.86%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 635，展开后 uop 635；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：628/635；decode-dispatch：632/635；dispatch-commit：484/635。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x8000011c`（LW，本 CTA 中第 8 次），sector `0x00010fc0`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 4691 · [timing:9385](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/timing/stderr.log:9385) | 3506 · [rtl:9256](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/rtl/stdout.log:9256) |
| L1 外部请求接受 | 4701 · [timing:9404](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/timing/stderr.log:9404) | 3516 · [rtl:9335](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/rtl/stdout.log:9335) |
| L1 外部响应 | 4801 · [timing:9604](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/timing/stderr.log:9604) | 3532 · [rtl:9389](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/rtl/stdout.log:9389) |
| 最后 EOP 到 commit 入口 | 4812 · [timing:9627](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/timing/stderr.log:9627) | 3543 · [rtl:9431](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/rtl/stdout.log:9431) |

因此这条指令的差值为 `(10 + 100 + 11) − (10 + 16 + 11) = +84` cycles；分别落在接受前 **+0**、外部请求/响应段 **+84**、响应后 **+0**。两侧前后段相同，本例额外时间完整落在 L1 外部服务段。

Timing 首次向 backend offer 在 cycle 4701（[timing:9404](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/timing/stderr.log:9404)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 10 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 0 / `0x800001a0` JALR，Timing 121、RTL 38，差 +83。证据：[timing:18327](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/timing/stderr.log:18327) → [timing:18569](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/timing/stderr.log:18569)；[rtl:19093](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/rtl/stdout.log:19093) → [rtl:19158](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/rtl/stdout.log:19158)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wsync/evidence.json)。


### demo：`-n4 -x4 -y4`

Timing **1,317**，RTLSIM **873** cycles，差值 **+444（+50.86%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 284，展开后 uop 284；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：272/284；decode-dispatch：179/284；dispatch-commit：211/284。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x80000088`（LW，本 CTA 中第 1 次），sector `0x00010100`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 704 · [timing:1411](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/timing/stderr.log:1411) | 413 · [rtl:3319](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/rtl/stdout.log:3319) |
| L1 外部请求接受 | 820 · [timing:1642](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/timing/stderr.log:1642) | 454 · [rtl:3841](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/rtl/stdout.log:3841) |
| L1 外部响应 | 920 · [timing:1842](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/timing/stderr.log:1842) | 472 · [rtl:4139](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/rtl/stdout.log:4139) |
| 最后 EOP 到 commit 入口 | 933 · [timing:1869](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/timing/stderr.log:1869) | 485 · [rtl:4245](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/rtl/stdout.log:4245) |

因此这条指令的差值为 `(116 + 100 + 13) − (41 + 18 + 13) = +157` cycles；分别落在接受前 **+75**、外部请求/响应段 **+82**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 820（[timing:1642](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/timing/stderr.log:1642)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 116 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 3 / `0x80000098` SW，Timing 244、RTL 84，差 +160。证据：[timing:1475](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/timing/stderr.log:1475) → [timing:1963](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/timing/stderr.log:1963)；[rtl:3701](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/rtl/stdout.log:3701) → [rtl:4561](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/rtl/stdout.log:4561)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/demo/evidence.json)。


### dotproduct：`-n32`

Timing **3,935**，RTLSIM **3,312** cycles，差值 **+623（+18.81%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 794，展开后 uop 794；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：774/794；decode-dispatch：618/794；dispatch-commit：667/794。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x8000002c`（LW，本 CTA 中第 1 次），sector `0x00011000`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 269 · [timing:541](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/timing/stderr.log:541) | 142 · [rtl:991](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/rtl/stdout.log:991) |
| L1 外部请求接受 | 372 · [timing:746](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/timing/stderr.log:746) | 159 · [rtl:1337](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/rtl/stdout.log:1337) |
| L1 外部响应 | 472 · [timing:946](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/timing/stderr.log:946) | 234 · [rtl:1908](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/rtl/stdout.log:1908) |
| 最后 EOP 到 commit 入口 | 483 · [timing:969](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/timing/stderr.log:969) | 245 · [rtl:1986](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/rtl/stdout.log:1986) |

因此这条指令的差值为 `(103 + 100 + 11) − (17 + 75 + 11) = +111` cycles；分别落在接受前 **+86**、外部请求/响应段 **+25**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 294（[timing:590](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/timing/stderr.log:590)），直接等待 backend 接受为 78 cycles；dispatch 到首 offer 为 25 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 0 / `0x8000003c` CSRRS，Timing 202、RTL 7，差 +195。证据：[timing:597](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/timing/stderr.log:597) → [timing:1001](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/timing/stderr.log:1001)；[rtl:1522](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/rtl/stdout.log:1522) → [rtl:1596](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/rtl/stdout.log:1596)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/evidence.json)。


### dotproduct2：`-n32`

Timing **3,225**，RTLSIM **2,526** cycles，差值 **+699（+27.67%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 634，展开后 uop 634；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：610/634；decode-dispatch：414/634；dispatch-commit：463/634。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x8000002c`（LW，本 CTA 中第 1 次），sector `0x00011000`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 269 · [timing:541](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/timing/stderr.log:541) | 142 · [rtl:991](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/rtl/stdout.log:991) |
| L1 外部请求接受 | 372 · [timing:746](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/timing/stderr.log:746) | 159 · [rtl:1337](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/rtl/stdout.log:1337) |
| L1 外部响应 | 472 · [timing:946](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/timing/stderr.log:946) | 234 · [rtl:1908](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/rtl/stdout.log:1908) |
| 最后 EOP 到 commit 入口 | 483 · [timing:969](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/timing/stderr.log:969) | 245 · [rtl:1986](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/rtl/stdout.log:1986) |

因此这条指令的差值为 `(103 + 100 + 11) − (17 + 75 + 11) = +111` cycles；分别落在接受前 **+86**、外部请求/响应段 **+25**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 294（[timing:590](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/timing/stderr.log:590)），直接等待 backend 接受为 78 cycles；dispatch 到首 offer 为 25 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 0 / `0x8000003c` CSRRS，Timing 202、RTL 7，差 +195。证据：[timing:597](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/timing/stderr.log:597) → [timing:1001](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/timing/stderr.log:1001)；[rtl:1522](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/rtl/stdout.log:1522) → [rtl:1596](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/rtl/stdout.log:1596)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct2/evidence.json)。


### dropout：`-n32`

Timing **1,955**，RTLSIM **1,308** cycles，差值 **+647（+49.46%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 440，展开后 uop 440；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：424/440；decode-dispatch：291/440；dispatch-commit：324/440。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 1、逻辑 Warp 0，PC `0x800000c4`（FLW，本 CTA 中第 1 次），sector `0x00010040`。双侧物理 Warp 分别为 0 / 1。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 1674 · [timing:3351](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/timing/stderr.log:3351) | 1155 · [rtl:8775](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/rtl/stdout.log:8775) |
| L1 外部请求接受 | 1684 · [timing:3370](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/timing/stderr.log:3370) | 1161 · [rtl:8837](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/rtl/stdout.log:8837) |
| L1 外部响应 | 1784 · [timing:3570](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/timing/stderr.log:3570) | 1177 · [rtl:8933](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/rtl/stdout.log:8933) |
| 最后 EOP 到 commit 入口 | 1795 · [timing:3593](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/timing/stderr.log:3593) | 1191 · [rtl:9016](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/rtl/stdout.log:9016) |

因此这条指令的差值为 `(10 + 100 + 11) − (6 + 16 + 14) = +85` cycles；分别落在接受前 **+4**、外部请求/响应段 **+84**、响应后 **-3**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 1684（[timing:3370](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/timing/stderr.log:3370)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 10 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 1 / 逻辑 Warp 0 / `0x800000c8` FMUL.S，Timing 126、RTL 41，差 +85。证据：[timing:3355](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/timing/stderr.log:3355) → [timing:3607](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/timing/stderr.log:3607)；[rtl:8797](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/rtl/stdout.log:8797) → [rtl:9040](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/rtl/stdout.log:9040)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dropout/evidence.json)。


### io_addr：`-n4`

Timing **2,081**，RTLSIM **1,450** cycles，差值 **+631（+43.52%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 368，展开后 uop 368；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：356/368；decode-dispatch：220/368；dispatch-commit：248/368。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 1、逻辑 Warp 2，PC `0x8000004c`（LW，本 CTA 中第 1 次），sector `0x00010100`。双侧物理 Warp 分别为 2 / 2。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 971 · [timing:1945](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/timing/stderr.log:1945) | 653 · [rtl:4583](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/rtl/stdout.log:4583) |
| L1 外部请求接受 | 991 · [timing:1984](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/timing/stderr.log:1984) | 663 · [rtl:4685](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/rtl/stdout.log:4685) |
| L1 外部响应 | 1091 · [timing:2184](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/timing/stderr.log:2184) | 681 · [rtl:4823](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/rtl/stdout.log:4823) |
| 最后 EOP 到 commit 入口 | 1105 · [timing:2213](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/timing/stderr.log:2213) | 694 · [rtl:5000](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/rtl/stdout.log:5000) |

因此这条指令的差值为 `(20 + 100 + 14) − (10 + 18 + 13) = +93` cycles；分别落在接受前 **+10**、外部请求/响应段 **+82**、响应后 **+1**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 991（[timing:1984](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/timing/stderr.log:1984)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 20 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 0 / `0x8000005c` SW，Timing 230、RTL 84，差 +146。证据：[timing:949](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/timing/stderr.log:949) → [timing:1409](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/timing/stderr.log:1409)；[rtl:2247](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/rtl/stdout.log:2247) → [rtl:2769](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/rtl/stdout.log:2769)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/io_addr/evidence.json)。


### mstress：`-n4`

Timing **2,730**，RTLSIM **2,388** cycles，差值 **+342（+14.32%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 832，展开后 uop 832；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：807/832；decode-dispatch：214/832；dispatch-commit：426/832。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 3，PC `0x800000a8`（LW，本 CTA 中第 1 次），sector `0x00010100`。双侧物理 Warp 分别为 3 / 3。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 767 · [timing:1537](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/timing/stderr.log:1537) | 551 · [rtl:5915](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/rtl/stdout.log:5915) |
| L1 外部请求接受 | 904 · [timing:1810](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/timing/stderr.log:1810) | 583 · [rtl:6537](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/rtl/stdout.log:6537) |
| L1 外部响应 | 1004 · [timing:2010](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/timing/stderr.log:2010) | 601 · [rtl:6942](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/rtl/stdout.log:6942) |
| 最后 EOP 到 commit 入口 | 1015 · [timing:2033](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/timing/stderr.log:2033) | 625 · [rtl:7366](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/rtl/stdout.log:7366) |

因此这条指令的差值为 `(137 + 100 + 11) − (32 + 18 + 24) = +174` cycles；分别落在接受前 **+105**、外部请求/响应段 **+82**、响应后 **-13**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 904（[timing:1810](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/timing/stderr.log:1810)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 137 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 3 / `0x800000b4` LW，Timing 191、RTL 25，差 +166。证据：[timing:1575](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/timing/stderr.log:1575) → [timing:1957](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/timing/stderr.log:1957)；[rtl:6055](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/rtl/stdout.log:6055) → [rtl:6546](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/rtl/stdout.log:6546)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/mstress/evidence.json)。


### multikernel：`-n32`

Timing **17,618**，RTLSIM **12,439** cycles，差值 **+5,179（+41.64%）**；共 3 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 3,464，展开后 uop 3,464；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：3232/3464；decode-dispatch：2588/3464；dispatch-commit：2866/3464。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 1、逻辑 Warp 0，PC `0x80000094`（LW，本 CTA 中第 1 次），sector `0x00010040`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 4435 · [timing:8873](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/timing/stderr.log:8873) | 3031 · [rtl:21638](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/rtl/stdout.log:21638) |
| L1 外部请求接受 | 4450 · [timing:8902](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/timing/stderr.log:8902) | 3041 · [rtl:21827](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/rtl/stdout.log:21827) |
| L1 外部响应 | 4550 · [timing:9102](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/timing/stderr.log:9102) | 3057 · [rtl:21995](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/rtl/stdout.log:21995) |
| 最后 EOP 到 commit 入口 | 4561 · [timing:9125](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/timing/stderr.log:9125) | 3068 · [rtl:22107](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/rtl/stdout.log:22107) |

因此这条指令的差值为 `(15 + 100 + 11) − (10 + 16 + 11) = +89` cycles；分别落在接受前 **+5**、外部请求/响应段 **+84**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 4450（[timing:8902](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/timing/stderr.log:8902)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 15 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 3 / CTA 1 / 逻辑 Warp 3 / `0x80000250` JALR，Timing 231、RTL 24，差 +207。证据：[timing:35515](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/timing/stderr.log:35515) → [timing:35977](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/timing/stderr.log:35977)；[rtl:105636](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/rtl/stdout.log:105636) → [rtl:105837](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/rtl/stdout.log:105837)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/multikernel/evidence.json)。


### packld：`默认固定规模`

Timing **3,893**，RTLSIM **3,088** cycles，差值 **+805（+26.07%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 604，展开后 uop 860；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：403/604；decode-dispatch：68/604；dispatch-commit：350/604。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x8000002c`（LW，本 CTA 中第 1 次），sector `0x00011000`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 269 · [timing:541](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/timing/stderr.log:541) | 142 · [rtl:975](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/rtl/stdout.log:975) |
| L1 外部请求接受 | 372 · [timing:746](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/timing/stderr.log:746) | 159 · [rtl:1321](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/rtl/stdout.log:1321) |
| L1 外部响应 | 472 · [timing:946](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/timing/stderr.log:946) | 234 · [rtl:1904](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/rtl/stdout.log:1904) |
| 最后 EOP 到 commit 入口 | 483 · [timing:969](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/timing/stderr.log:969) | 245 · [rtl:1982](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/rtl/stdout.log:1982) |

因此这条指令的差值为 `(103 + 100 + 11) − (17 + 75 + 11) = +111` cycles；分别落在接受前 **+86**、外部请求/响应段 **+25**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 294（[timing:590](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/timing/stderr.log:590)），直接等待 backend 接受为 78 cycles；dispatch 到首 offer 为 25 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 2 / `0x80000034` LW，Timing 201、RTL 90，差 +111。证据：[timing:565](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/timing/stderr.log:565) → [timing:967](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/timing/stderr.log:967)；[rtl:1233](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/rtl/stdout.log:1233) → [rtl:1966](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/rtl/stdout.log:1966)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/packld/evidence.json)。


### pathfinder：`-n8`

Timing **7,791**，RTLSIM **4,958** cycles，差值 **+2,833（+57.14%）**；共 7 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 1,106，展开后 uop 1,106；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：1015/1106；decode-dispatch：879/1106；dispatch-commit：840/1106。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 1，PC `0x800000b4`（LW，本 CTA 中第 1 次），sector `0x00010000`。双侧物理 Warp 分别为 1 / 1。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 854 · [timing:1711](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/timing/stderr.log:1711) | 577 · [rtl:3621](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/rtl/stdout.log:3621) |
| L1 外部请求接受 | 864 · [timing:1730](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/timing/stderr.log:1730) | 587 · [rtl:3674](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/rtl/stdout.log:3674) |
| L1 外部响应 | 964 · [timing:1930](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/timing/stderr.log:1930) | 605 · [rtl:3758](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/rtl/stdout.log:3758) |
| 最后 EOP 到 commit 入口 | 975 · [timing:1953](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/timing/stderr.log:1953) | 616 · [rtl:3830](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/rtl/stdout.log:3830) |

因此这条指令的差值为 `(10 + 100 + 11) − (10 + 18 + 11) = +82` cycles；分别落在接受前 **+0**、外部请求/响应段 **+82**、响应后 **+0**。两侧前后段相同，本例额外时间完整落在 L1 外部服务段。

Timing 首次向 backend offer 在 cycle 864（[timing:1730](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/timing/stderr.log:1730)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 10 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 1 / `0x800000b8` ADD，Timing 127、RTL 45，差 +82。证据：[timing:1715](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/timing/stderr.log:1715) → [timing:1969](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/timing/stderr.log:1969)；[rtl:3631](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/rtl/stdout.log:3631) → [rtl:3848](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/rtl/stdout.log:3848)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/pathfinder/evidence.json)。


### relu：`-n32`

Timing **1,290**，RTLSIM **799** cycles，差值 **+491（+61.45%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 240，展开后 uop 240；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：232/240；decode-dispatch：193/240；dispatch-commit：176/240。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 1、逻辑 Warp 0，PC `0x8000004c`（FLW，本 CTA 中第 1 次），sector `0x00010040`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 938 · [timing:1879](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/timing/stderr.log:1879) | 586 · [rtl:4336](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/rtl/stdout.log:4336) |
| L1 外部请求接受 | 948 · [timing:1898](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/timing/stderr.log:1898) | 596 · [rtl:4450](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/rtl/stdout.log:4450) |
| L1 外部响应 | 1048 · [timing:2098](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/timing/stderr.log:2098) | 612 · [rtl:4605](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/rtl/stdout.log:4605) |
| 最后 EOP 到 commit 入口 | 1059 · [timing:2121](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/timing/stderr.log:2121) | 623 · [rtl:4733](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/rtl/stdout.log:4733) |

因此这条指令的差值为 `(10 + 100 + 11) − (10 + 16 + 11) = +84` cycles；分别落在接受前 **+0**、外部请求/响应段 **+84**、响应后 **+0**。两侧前后段相同，本例额外时间完整落在 L1 外部服务段。

Timing 首次向 backend offer 在 cycle 948（[timing:1898](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/timing/stderr.log:1898)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 10 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 1 / 逻辑 Warp 2 / `0x80000058` XOR，Timing 125、RTL 36，差 +89。证据：[timing:1917](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/timing/stderr.log:1917) → [timing:2167](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/timing/stderr.log:2167)；[rtl:4572](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/rtl/stdout.log:4572) → [rtl:4791](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/rtl/stdout.log:4791)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/relu/evidence.json)。


### sgemm：`-n8`

Timing **4,445**，RTLSIM **3,508** cycles，差值 **+937（+26.71%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 1,360，展开后 uop 1,360；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：1328/1360；decode-dispatch：922/1360；dispatch-commit：827/1360。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x800000e4`（FLW，本 CTA 中第 1 次），sector `0x00010180`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 1010 · [timing:2023](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/timing/stderr.log:2023) | 637 · [rtl:5615](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/rtl/stdout.log:5615) |
| L1 外部请求接受 | 1123 · [timing:2248](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/timing/stderr.log:2248) | 663 · [rtl:5983](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/rtl/stdout.log:5983) |
| L1 外部响应 | 1223 · [timing:2448](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/timing/stderr.log:2448) | 681 · [rtl:6181](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/rtl/stdout.log:6181) |
| 最后 EOP 到 commit 入口 | 1234 · [timing:2471](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/timing/stderr.log:2471) | 692 · [rtl:6313](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/rtl/stdout.log:6313) |

因此这条指令的差值为 `(113 + 100 + 11) − (26 + 18 + 11) = +169` cycles；分别落在接受前 **+87**、外部请求/响应段 **+82**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 1123（[timing:2248](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/timing/stderr.log:2248)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 113 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 0 / `0x80000130` FMADD.S，Timing 131、RTL 14，差 +117。证据：[timing:2559](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/timing/stderr.log:2559) → [timing:2821](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/timing/stderr.log:2821)；[rtl:8106](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/rtl/stdout.log:8106) → [rtl:8217](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/rtl/stdout.log:8217)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemm/evidence.json)。


### sgemmx：`-n16`

Timing **15,117**，RTLSIM **11,856** cycles，差值 **+3,261（+27.51%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 2,920，展开后 uop 2,920；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：2207/2920；decode-dispatch：592/2920；dispatch-commit：1148/2920。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x80000484`（FLW，本 CTA 中第 1 次），sector `0x00010800`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 12901 · [timing:25805](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/timing/stderr.log:25805) | 10559 · [rtl:84385](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/rtl/stdout.log:84385) |
| L1 外部请求接受 | 13137 · [timing:26276](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/timing/stderr.log:26276) | 10626 · [rtl:85263](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/rtl/stdout.log:85263) |
| L1 外部响应 | 13237 · [timing:26476](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/timing/stderr.log:26476) | 10658 · [rtl:85438](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/rtl/stdout.log:85438) |
| 最后 EOP 到 commit 入口 | 13250 · [timing:26503](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/timing/stderr.log:26503) | 10672 · [rtl:85546](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/rtl/stdout.log:85546) |

因此这条指令的差值为 `(236 + 100 + 13) − (67 + 32 + 14) = +236` cycles；分别落在接受前 **+169**、外部请求/响应段 **+68**、响应后 **-1**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 13137（[timing:26276](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/timing/stderr.log:26276)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 236 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 1 / `0x8000040c` LW，Timing 470、RTL 62，差 +408。证据：[timing:24535](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/timing/stderr.log:24535) → [timing:25475](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/timing/stderr.log:25475)；[rtl:81757](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/rtl/stdout.log:81757) → [rtl:82696](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/rtl/stdout.log:82696)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemmx/evidence.json)。


### sgemv：`-m8 -n8`

Timing **1,559**，RTLSIM **1,065** cycles，差值 **+494（+46.38%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 218，展开后 uop 218；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：206/218；decode-dispatch：137/218；dispatch-commit：134/218。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x800000a8`（FLW，本 CTA 中第 1 次），sector `0x00010100`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 868 · [timing:1739](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/timing/stderr.log:1739) | 566 · [rtl:4061](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/rtl/stdout.log:4061) |
| L1 外部请求接受 | 980 · [timing:1962](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/timing/stderr.log:1962) | 614 · [rtl:4703](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/rtl/stdout.log:4703) |
| L1 外部响应 | 1080 · [timing:2162](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/timing/stderr.log:2162) | 632 · [rtl:4876](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/rtl/stdout.log:4876) |
| 最后 EOP 到 commit 入口 | 1091 · [timing:2185](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/timing/stderr.log:2185) | 643 · [rtl:4971](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/rtl/stdout.log:4971) |

因此这条指令的差值为 `(112 + 100 + 11) − (48 + 18 + 11) = +146` cycles；分别落在接受前 **+64**、外部请求/响应段 **+82**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 980（[timing:1962](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/timing/stderr.log:1962)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 112 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 0 / `0x80000038` LW，Timing 207、RTL 87，差 +120。证据：[timing:579](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/timing/stderr.log:579) → [timing:993](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/timing/stderr.log:993)；[rtl:1371](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/rtl/stdout.log:1371) → [rtl:2016](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/rtl/stdout.log:2016)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/sgemv/evidence.json)。


### vecadd：`-n32`

Timing **1,183**，RTLSIM **668** cycles，差值 **+515（+77.10%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 208，展开后 uop 208；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：200/208；decode-dispatch：140/208；dispatch-commit：141/208。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 1、逻辑 Warp 0，PC `0x80000058`（FLW，本 CTA 中第 1 次），sector `0x000100c0`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 910 · [timing:1823](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/timing/stderr.log:1823) | 542 · [rtl:4647](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/rtl/stdout.log:4647) |
| L1 外部请求接受 | 923 · [timing:1848](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/timing/stderr.log:1848) | 552 · [rtl:4765](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/rtl/stdout.log:4765) |
| L1 外部响应 | 1023 · [timing:2048](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/timing/stderr.log:2048) | 568 · [rtl:4986](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/rtl/stdout.log:4986) |
| 最后 EOP 到 commit 入口 | 1034 · [timing:2071](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/timing/stderr.log:2071) | 579 · [rtl:5159](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/rtl/stdout.log:5159) |

因此这条指令的差值为 `(13 + 100 + 11) − (10 + 16 + 11) = +87` cycles；分别落在接受前 **+3**、外部请求/响应段 **+84**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 923（[timing:1848](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/timing/stderr.log:1848)），直接等待 backend 接受为 0 cycles；dispatch 到首 offer 为 13 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 1 / 逻辑 Warp 3 / `0x80000064` FSW，Timing 145、RTL 44，差 +101。证据：[timing:1869](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/timing/stderr.log:1869) → [timing:2159](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/timing/stderr.log:2159)；[rtl:5125](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/rtl/stdout.log:5125) → [rtl:5353](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/rtl/stdout.log:5353)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/vecadd/evidence.json)。


### wgather：`-n4 -t4`

Timing **1,188**，RTLSIM **732** cycles，差值 **+456（+62.30%）**；共 1 次 launch。双侧 host 输出检查 PASS。

逐 launch / 逻辑 CTA / CTA 内 Warp 的 PC 与 mask 序列：全部相同。Timing macro decode 228，展开后 uop 228；uop 事件数不匹配项 0。

三个区间中持续时间相同的指令数（分母为可对齐 macro 数，不是精度百分比）：schedule-decode：212/228；decode-dispatch：212/228；dispatch-commit：198/228。区间可以重叠，不能将其差值直接累加成 Kernel 总差。

可复核的 load 链：launch 1、CTA 0、逻辑 Warp 0，PC `0x8000002c`（LW，本 CTA 中第 1 次），sector `0x00011000`。双侧物理 Warp 分别为 0 / 0。

| 事件 | Timing cycle / 原始日志 | RTL 归一化 cycle / 原始日志 |
|---|---:|---:|
| FU dispatch | 269 · [timing:541](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/timing/stderr.log:541) | 142 · [rtl:979](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/rtl/stdout.log:979) |
| L1 外部请求接受 | 372 · [timing:746](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/timing/stderr.log:746) | 159 · [rtl:1325](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/rtl/stdout.log:1325) |
| L1 外部响应 | 472 · [timing:946](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/timing/stderr.log:946) | 234 · [rtl:1914](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/rtl/stdout.log:1914) |
| 最后 EOP 到 commit 入口 | 483 · [timing:969](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/timing/stderr.log:969) | 245 · [rtl:1988](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/rtl/stdout.log:1988) |

因此这条指令的差值为 `(103 + 100 + 11) − (17 + 75 + 11) = +111` cycles；分别落在接受前 **+86**、外部请求/响应段 **+25**、响应后 **+0**。接受前段包含 LSU/coalescer/Cache 队列、miss 依赖及 backend 接受等待；该区间差本身还不能证明某个具体内部模块的延迟错误。

Timing 首次向 backend offer 在 cycle 294（[timing:590](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/timing/stderr.log:590)），直接等待 backend 接受为 78 cycles；dispatch 到首 offer 为 25 cycles。后者发生在请求到达 backend 之前，不能直接称为本请求的 backend 排队。

最大绝对 decode→dispatch 区间差样例：launch 1 / CTA 0 / 逻辑 Warp 0 / `0x80000020` TMC，Timing 77、RTL 7，差 +70。证据：[timing:2157](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/timing/stderr.log:2157) → [timing:2311](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/timing/stderr.log:2311)；[rtl:5726](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/rtl/stdout.log:5726) → [rtl:5806](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/rtl/stdout.log:5806)。这是等待和分派区间，不是该 opcode 的固有 FU 执行延迟。

完整逐指令表、全部区间直方图、事务匹配与 launch 起点见 [aligned.csv](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/aligned.csv)、[analysis.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/analysis.json)、[memory.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/memory.json)、[evidence.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/wgather/evidence.json)。
<!-- SMALL-SUITE-END -->

## 12. 本轮新 trace 揭示的问题与结论边界

### 12.1 冷启动和 launch 生命周期：不是统一的 I-cache hit latency 错误

basic 第一条指令的 schedule→decode 是 Timing 172、RTL 45 cycles。
按同一 instruction sector 的外部请求分解，分别为 68 + 100 + 4 和
16 + 25 + 4：额外 127 周期中，52 在首个外部请求之前，75 在外部服务，
refill 返回到 decode 的 4 周期反而相同。
这与先前 wgather/vecadd 证据一致，不能通过把整个 Fetch 延迟减 127 来修复。

启动差还可以继续落到实际 tag 初始化进度：BFS 的 RTL I-cache tags-init 从
[raw 19 / line 0](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:25)
开始，到
[raw 145 / line 63](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:221)
结束；在首个 scheduler dispatch 的 raw 121，同拍已经在
[初始化 line 51](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:191)。
也就是说，RTL 在发出第一条指令之前就已推进了大部分 Cache 初始化，
而 Timing 是新建存储系统后从 Kernel cycle 0 才开始推进 initNext。
两侧都有逐 set 初始化，不能把这个差异直接解释成“Cache 初始化拍数设错”；
关键是它和设备 reset、DCR 配置、Kernel 计数起点之间的重叠关系。
整个 BFS 的八次 launch 中，RTL 只有这一次 64 条 tags-init 记录。

多 launch 又暴露了更明确的生命周期差异。以 BFS 的第二次 launch 为例：

| 边界 | Timing cycle | RTL cycle |
|---|---:|---:|
| 首个 scheduler dispatch | 0 | 0 |
| I-cache 首个外部读被接受 | 68 | 7 |
| 外部读返回 | 168 | 39 |
| 首条 decode | 172 | 43 |

Timing 证据：第二 launch 的
[schedule:5460](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/timing/stderr.log:5460)、
[请求:5595](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/timing/stderr.log:5595)、
[返回:5795](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/timing/stderr.log:5795)、
[decode:5804](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/timing/stderr.log:5804)；
RTL 对应
[schedule:5849](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:5849)、
[请求:5883](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:5883)、
[返回:5901](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:5901)、
[decode:5914](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/bfs/rtl/stdout.log:5914)。

BFS 的后七次 launch、pathfinder 的后六次、multikernel 的后两次都重复
68+100+4 对 7+32+4。因此增加 launch 数会反复遇到这类启动差异，
不只是输入数据量增大才产生偏差。具体每次 launch 的行号在各自 evidence.json 的 startup 中。

源码链条也明确：runtime 的 [runTiming](../../integration/vortexruntime/device.go)
每次调用 runner.NewKernel；[newRunnerMemory](../../timing/runner/memory_system.go)
新建 [NewSystem](../../timing/memsys/system.go)，重新 NewCache；[cache_bank](../../timing/memsys/cache_bank.go)
的 initNext 从零逐 set 初始化。RTL runtime 持有同一个 Processor，
后续调用 [processor.run](../../../vortex/sim/rtlsim/processor.cpp) 不等于重新构造和 reset 设备。

已经证明的是：模型把存储系统对象生命周期绑定到每次 Kernel launch，且实际启动等待
与 RTL 不同。还不能声称 RTL 的 I-cache 跨 launch 始终命中：上表明确仍有外部读，
说明 flush/invalidate 与“重新 reset 整个 Cache 对象”必须分别建模。
首次 52 周期及后续 61 周期的接受前差属于启动/初始化时间线差；
尚未将每个 reset 与控制边沿全部逐拍闭合，不能把这两个数直接改成经验常量。

### 12.2 物理 Warp 不是稳定的比较身份，CTA 抽象还会改变竞争顺序

dotproduct -n32 双侧都退休 794 条 macro，但按物理 Warp 统计会出现：
Timing [252,190,176,176]，RTL [214,221,183,176]。
这不是证实功能执行错了，而是下一 CTA 的逻辑线程在不同物理 Warp 上执行。

第二 CTA 的实际分配是：

| CTA 内逻辑 Warp | Timing 物理 Warp / 分配周期 | RTL 物理 Warp / 原始时间 |
|---|---|---|
| 0 | 0 / 2313 | 1 / 3291 |
| 1 | 1 / 2313 | 2 / 3293 |
| 2 | 2 / 2313 | 3 / 3295 |
| 3 | 3 / 2313 | 0 / 3669 |

Timing 同一拍四条实际 cta-dispatch 记录：
[stderr:4629](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/timing/stderr.log:4629)。
RTL：
[8896](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/rtl/stdout.log:8896)、
[8902](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/rtl/stdout.log:8902)、
[8911](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/rtl/stdout.log:8911)、
[10843](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/dotproduct/rtl/stdout.log:10843)。
最后一个 Warp 晚了 189 个 RTL cycles 才分配，前面三个已经在推进。

改用真实 dispatch 记录恢复逻辑身份后，794 条 PC/mask 序列和 uop 数全部对齐。
这既说明旧的“按物理 Warp+PC 次数”比较方法不够，也证明 T11 的整 CTA 分配/回收抽象
会改变 Warp 间并发和后续 Cache、LSU、执行单元的竞争条件。
不能只校准一个“CTA dispatch latency”，而忽略部分 Warp 提前复用及其造成的资源顺序变化。

### 12.3 接受前的长等待不能全部归为固定后端 100 cycles

三条本轮实际样例（完整请求身份和日志见 §11 对应 benchmark）：

| benchmark / PC | Timing dispatch | 首次 backend offer | 接受 | 请求到达 backend 前 | backend 拒绝等待 |
|---|---:|---:|---:|---:|---:|
| dotproduct / 0x8000002c | 269 | 294 | 372 | 25 | 78 |
| sgemm2 / 0x80000034 | 287 | 432 | 471 | 145 | 39 |
| sgemmx / 0x80000484 | 12901 | 13137 | 13137 | 236 | 0 |

sgemmx 这一例的 236 周期发生在首次 offer 之前，本请求没有被 backend 拒绝过。
不能据此说“backend 队列满，排了 236 周期”，更不能直接把这段当成 LMEM 或某个 FU
固有 latency。上游 Cache miss 依赖、MSHR、coalescer、先前请求仍可能受到外部服务间接影响；
需要继续增加这些接口的可关联 occupancy / ready / stall-reason 才能细分。

本轮已将差距准确定位到接受前、外部服务及返回后三个可观察区间；
没有在证据不足时把接受前残差任意归到某一个 Cache bank 实现。
上文 §10.3 的 port0 请求缓冲漏建有独立 RTL+IR+隔离实验支持，但不能把这里所有残差都算到它上面。

### 12.4 固定 100 是服务完成，不保证消费者在第 100 拍拿走响应

解析器同时保存 AcceptedCycle、CompletedCycle 和实际 Delivered 周期。
multikernel 的 616 个 backend 事务，服务完成延迟全部是 100；
交付延迟分别为 100×594、101×11、102×2、103×9。
源代码 [backend.Step](../../timing/memsys/backend.go) 先完成到期服务，
再受返回带宽、消费者 ready 与 FIFO 头阻塞约束交付。
应当保留这种区别，不能为了让 trace 中 request→Delivered 都等于 100 而绕过 backpressure。
这里的 100 同样不能与 RTL 的 Ramulator 可变延迟混为一谈。

### 12.5 packed-load 和等待区间：避免解析器自己制造“模型错误”

packld 是 604 条 macro decode、860 个展开后的 uop；双侧 PC/mask 和 uop 数吻合。
如果把 RTL 的每个 packed-load uop commit 当作新的宏指令，会从第一次 packed load 起错位。
本轮先对 macro，再将所属 uop 的 dispatch 和最后 EOP commit 归入该 macro。
对齐到相同事件之后，仍能看到 frontend、dispatch 和返回的真实区间差，
但这些区间包含当时的背压，不能直接当成某 opcode 的常量 latency。

例如本轮 sgemm 的 FMADD.S 有很大的 decode→dispatch 等待，
同一 workload 中 load 链已经显示明显的接受前及外部返回差。
这只证明执行单元之前在等待，不证明 FMA 单元本身被设置了错误的长延迟。
下一轮必须先固定/回放相同 memory 返回序列、排除上游依赖，再测试 FU latency 与 throughput。

### 12.6 下一次构建与验收应采用的实验门禁

1. **先固定比较边界。** 明确设备 reset、cache 初始化、每次 launch、Kernel completion、
   flush、host 可见性和 PERF 计数起止；分别报告冷启动、稳态、CTA 切换、launch 切换。
   小参数下冷启动占比变大，误差比例变高不能直接称为模型退化。
2. **先做同外部响应，再做真实后端。** 内部模型精度测试使用同一接受/返回契约或
   transaction replay；另列固定 100 对 Ramulator 的系统级误差。
   不通过全局比例缩放或调 FU latency 吸收 DRAM 边界差。
3. **把 RTL/IR 结构到实现的覆盖表变成检查项。** 每个启用的 buffer、port、MSHR、
   仲裁及 flush/empty 条件必须能对应实现和最小 trace 测试；尤其防止 IR 已有 b-dflush，
   实现却直接旁路。UNRESOLVED 应限制结论，不能由 Go 遍历顺序暗中补规则。
4. **用逻辑身份贯通证据。** device/launch、CTA grid rank、CTA 内 Warp、physical slot
   generation、macro token、uop、fragment、memory transaction 全链关联；允许物理 Warp
   分配不同。先验证指令/请求身份，再比较周期；不只看最终输出和总周期。
5. **微测试必须覆盖相邻边沿。** 包括 full 时本拍释放能否借位、refill 与 replay 同拍、
   bank/port 仲裁、mixed local/global 部分接受、最后 EOP、pending 清除、store tail 与 Warp
   复用。不能只用一个 Kernel PASS 代替这些时序契约测试。
6. **维护固定的小规模全通过集与原规模性能集。** 小规模用于完整 trace 的定位，
   原规模用于检查竞争、摊销和吞吐；保留合法 tile 尺寸等 host 前置条件。
   对每次变更保存同二进制、同输入、同配置的前后 trace，定位第一处分歧及传播，
   不放宽阈值来掩盖模型误差。

这些是后续设计与验证建议；本轮没有实施结构修复，也没有修改 harness 提示词或验收设置。

### 12.7 occupancy：长墙钟运行不等于模拟周期膨胀

最终 occupancy -c3 为 Timing 648,321、RTL 648,033 cycles，
差 288（0.04444%）；双侧均为 86,117 条 macro/uop。
86,111 条指令的 schedule→decode 区间相同；
decode→dispatch 和 dispatch→commit 分别有 86,109 条相同。
这说明该 LMEM 主导 workload 的主要稳态循环在当前输入下非常接近，
不能因为 trace 模式的 Go 程序跑得慢，就断言它插入了海量额外模拟周期。

逻辑 CTA 0 / Warp 0 的实际循环：

| 指令 | Timing 前三次 decode | RTL 前三次 decode（归一化） | 全部相邻迭代间隔 |
|---|---|---|---|
| LMEM SW，PC 0x80000060 | 509、588、667 | 304、383、462 | 双侧 2,047 次全部为 79 |
| LMEM LW，PC 0x80000084 | 162423、162502、162581 | 162136、162215、162294 | 双侧 2,047 次全部为 79 |

SW 的首条证据：
[Timing:1021](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/timing/stderr.log:1021)、
[RTL:1100](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/rtl/stdout.log:1100)；
LW：
[Timing:324849](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/timing/stderr.log:324849)、
[RTL:527525](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/results/occupancy/rtl/stdout.log:527525)。
全部 2,048 次迭代可从该项 aligned.csv 按 CTA、逻辑 Warp 和 PC 复核。
不是只抽三条就假设其余迭代相同。

本轮 occupancy 双侧作业总墙钟约 28 分 33 秒，绝大部分在 Timing 端。
其中包含约 3 GB 的 Timing trace 输出成本，不能据此精确估算无 trace 版本吞吐；
要定位宿主程序的计算瓶颈还需单独 CPU profile。
这里可证明的是周期模型误差很小，而不是已经证明所有 LMEM 冲突和同步场景都周期等价。

## 13. 最终核验与产物

- 范围：旧两份通过集 manifest 的并集恰好 20 项，无遗漏；40 个原 host/kernel
  文件 SHA256 与历史输入快照一致，双侧使用同一份输入和参数。
- 全部 20 项 host 返回码为 0，输出检查 PASS；35 次 launch 双侧数量一致，
  逐逻辑线程 PC/mask 序列及所有展开后 uop 数一致。packld 明确为 604 macro / 860 uop。
- 所有观测到的 backend 事务 CompletedCycle−AcceptedCycle 均为 100。
  Delivered 延后单列，没有伪造为固定第 100 拍返回。
- 以正式源码、无观测 overlay 的库额外重跑 basic、packld、multikernel，
  每次 launch 的 execution cycles、retired 和 cache-flush cycles 均与观测版完全一致。
- Vortex_rtl 中冻结 RTL 与本轮 RTL 构建来源逐文件相同；配置 SHA256 为
  3f020a15364555da8da610b5f78501f670299440685846fb40bb0f757593fb93。
- 初始数组、sgemmx 合法参数重试、三项观测对照和最终核验 Slurm 作业均已 COMPLETED。
  注意 Slurm 进程完成不等于 benchmark PASS；本轮是另外检查每个 backend 的 host
  返回码和输出。初始 sgemmx -n8 的双侧参数拒绝仍保留，不伪装成有效通过。

汇总与 180 个证据文件的大小、SHA256：
[verification.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/verification.json)；
作业终态：
[slurm-accounting.txt](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/slurm-accounting.txt)；
无观测对照：
[observer-control/result.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260914/observer-control/result.json)。
上述核验覆盖的产物约 3.93 GB，保留在本地忽略目录，不随 Markdown 自动进入 Git。
正式修改仅为这份新报告、旧报告的迁移链接及两处文档入口；没有修改模拟器主体或提交 commit。
