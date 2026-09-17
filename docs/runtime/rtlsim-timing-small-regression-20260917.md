# 修复后 Timing 与 RTLSIM 全支持集小规模回归（2026-09-17）

本轮已完成：正式支持清单 28 项，加历史已通过的 basic、wsync、bfs，共 **31 项 / 62 次双侧仿真，全部 PASS**。未修改模型、RTL、Timing IR 或 benchmark 功能；仅增加隔离的观测插桩与分析脚本。PASS 表示本轮小输入的功能与 runtime 审计通过，不表示逐周期等价，也不代表原规模已通过。

基线 `aeb19f99640d961b9a7b84256965cc339724a6d7`；产物目录 [trace-suite-small-20260917](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917)；[冻结参数、二进制 SHA256 与运行清单](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/manifest.json)。Timing Go 和 native bridge 从当前版本重新构建；RTLSIM 复用上一轮已核对冻结 RTL/config 的 trace 库，**所有仿真均重新执行，未复用旧结果**。benchmark 二进制使用归档的相同输入，便于前后对比。

作业：`12770596_[0-29]`（long_cpu、每项 2 CPU / 8 GB、最多并行 4 项）、`12770657_30`（BFS 补充）、`12770655`（全 trace 分析）、`12770671`（无插桩控制）。`12770656` 是控制实验的首次环境失败，已保留日志并重跑。每对 benchmark 内先 RTL 后 Timing，不同 benchmark 并发；单后端保护上限 3600 秒，非模型周期限制。

## 1. 判定与对比口径

- Timing：退出码 0、host PASS、launch/finish 配对、CTA 生成/接收/完成一致、outstanding 显式归零、host flush 后 backing visible；RTL：退出码 0、host PASS、有效 PERF。每项详见 `results/<name>/result.json` 与 `timing/events.jsonl`。
- 主周期口径采用双方最后一条设备累计 PERF cycles，Instret 按 EOP Warp/uop，而非 lane 数。另列 Timing 执行边数总和与 flush 边数；硬件 busy 计数与执行 clock 边界不同，三者不能混用或直接相加。
- Timing external backend 从实际接受起固定 100 周期；RTL 使用原参考库的 Ramulator。总周期误差包含后端差异和启动边界，不能直接称为纯流水线建模误差。
- 后续微测试还确认 RTLSIM 启用 `IDIV_DPI`，而 Timing 使用非 DPI 串行 divider；因此上述全量误差也含执行配置差异。TOML/RTL 文件一致不保证编译展开路径一致，详见第 8 节。
- Trace 以 launch + 逻辑 CTA + CTA-local Warp + PC occurrence 对齐，packed 指令展开 uop；使用新版显式 Bindings，不能将 Token.Epoch 当作 WarpGeneration。每次 launch 的首个真实 schedule 归零，保留原设备周期供审计。只有 PC/mask/uop 对齐通过才将阶段差认作同一指令的差异。
- 小参数不删测试功能：dogfood 保留 0..21 全子测试，packld 无缩放参数；sgemmx 最小合法尺寸 16；occupancy 保留 -c3 以覆盖 residency/reuse。raycast host PASS 的 oracle 较弱，额外比较输出 PPM。


## 2. 全量结果

| benchmark / 参数 | RTL PERF | Timing PERF | 偏差 | Timing 执行边 | flush 边 | RTL/Timing 秒 | PC/mask/uop |
| --- | --- | --- | --- | --- | --- | --- | --- |
| async_barrier `-n8 -t4` | 18112 | 20052 | +10.71% | 20051 | 411 | 1.0/5.0 | 一致 |
| conv3 `-n4 -l` | 1431 | 2165 | +51.29% | 2199 | 410 | 1.0/2.0 | 一致 |
| demo `-n4 -x4 -y4` | 873 | 1313 | +50.40% | 1312 | 410 | 1.0/1.0 | 一致 |
| diverge `-n1 -d4` | 5725 | 7629 | +33.26% | 7628 | 410 | 1.0/3.0 | 一致 |
| dogfood `-n4 -s0 -e21 -c` | 160874 | 178268 | +10.81% | 178246 | 9021 | 4.2/48.1 | 一致 |
| dotproduct `-n32` | 3312 | 3864 | +16.67% | 3863 | 411 | 1.0/3.0 | 一致 |
| dotproduct2 `-n32` | 2526 | 3143 | +24.43% | 3142 | 411 | 1.0/2.0 | 一致 |
| dropout `-n32` | 1308 | 1896 | +44.95% | 1954 | 410 | 1.0/1.0 | 一致 |
| fence `-n4` | 1482 | 2237 | +50.94% | 2236 | 410 | 1.0/1.0 | 一致 |
| io_addr `-n4` | 1450 | 2001 | +38.00% | 2066 | 410 | 1.0/2.0 | 一致 |
| jacobi `-n4` | 15210 | 18572 | +22.10% | 18618 | 412 | 1.0/4.1 | 一致 |
| madmax `-n2` | 67058 | 71407 | +6.49% | 71460 | 410 | 2.1/16.0 | 一致 |
| mstress `-n4` | 2388 | 2840 | +18.93% | 2839 | 410 | 1.0/3.0 | 一致 |
| multikernel `-n32` | 12439 | 17346 | +39.45% | 17427 | 1230 | 1.0/6.0 | 一致 |
| occupancy `-c3` | 648033 | 648318 | +0.04% | 648325 | 410 | 11.3/215.3 | 一致 |
| packld `(默认)` | 3088 | 3395 | +9.94% | 3394 | 497 | 1.0/2.0 | 一致 |
| pathfinder `-n8` | 4958 | 7108 | +43.36% | 7437 | 2870 | 1.0/3.0 | 一致 |
| raycast `-n1 -w4 -h4 -s1 -d1` | 168290 | 188884 | +12.24% | 188883 | 411 | 4.1/37.1 | 一致 |
| relu `-n32` | 799 | 1228 | +53.69% | 1285 | 410 | 1.0/1.0 | 一致 |
| sgemm `-n8` | 3508 | 4433 | +26.37% | 4432 | 410 | 1.0/2.0 | 一致 |
| sgemm2 `-n8 -t4 -c4` | 10143 | 11617 | +14.53% | 11616 | 410 | 1.0/4.0 | 一致 |
| sgemmx `-n16` | 11856 | 14911 | +25.77% | 14910 | 410 | 1.0/6.0 | 一致 |
| sgemv `-m8 -n8` | 1065 | 1512 | +41.97% | 1553 | 411 | 1.0/1.0 | 一致 |
| softmax `-n4` | 95212 | 100402 | +5.45% | 100401 | 412 | 2.1/20.0 | 一致 |
| sort `-n2` | 7804 | 8240 | +5.59% | 8239 | 410 | 1.0/3.0 | 一致 |
| stencil3d `-n4` | 9035 | 10556 | +16.83% | 10555 | 410 | 1.0/4.0 | 一致 |
| vecadd `-n32` | 668 | 1111 | +66.32% | 1172 | 410 | 1.0/1.0 | 一致 |
| wgather `-n4 -t4` | 732 | 1175 | +60.52% | 1174 | 411 | 1.0/1.0 | 一致 |
| basic `-n32` | 1971 | 2336 | +18.52% | 2335 | 410 | 1.0/1.0 | 一致 |
| wsync `-i16` | 7236 | 9324 | +28.86% | 9323 | 410 | 1.0/3.0 | 一致 |
| bfs `-n32` | 14821 | 19944 | +34.57% | 19936 | 3288 | 1.0/6.0 | 一致 |

等权 MAPE = **28.48%**；按 RTL 周期加权的总周期差 = **6.53%**。前者容易受小 workload 的启动/存储成本支配，后者受长 workload 支配，不能只报较小的那个数字。

共 67 次 kernel launch，206,278 条动态 macro，206,534 个 uop。

## 3. 同输入修复前后（旧 20 项）

这里专门用相同执行边口径：旧版本 PERF 不可用，不能与新版 PERF 直接相减。输入参数和二进制哈希需一致；RTL 仍使用本轮新执行结果。负数表示 Timing 执行边减少，不自动意味着更接近 RTL。

这 20 项按相同执行边口径的等权 MAPE 从 **36.69% → 34.65%**，改善约 2.04 个百分点。不能把本轮全 31 项 PERF MAPE 28.48% 与旧 20 项 36.69% 直接比较：集合和计数口径都不同。

| benchmark | 旧执行边 | 新执行边 | 变化 | 本轮 RTL PERF |
| --- | --- | --- | --- | --- |
| demo | 1317 | 1312 | -5 | 873 |
| dotproduct | 3935 | 3863 | -72 | 3312 |
| dotproduct2 | 3225 | 3142 | -83 | 2526 |
| dropout | 1955 | 1954 | -1 | 1308 |
| io_addr | 2081 | 2066 | -15 | 1450 |
| mstress | 2730 | 2839 | 109 | 2388 |
| multikernel | 17618 | 17427 | -191 | 12439 |
| occupancy | 648321 | 648325 | 4 | 648033 |
| packld | 3893 | 3394 | -499 | 3088 |
| pathfinder | 7791 | 7437 | -354 | 4958 |
| relu | 1290 | 1285 | -5 | 799 |
| sgemm | 4445 | 4432 | -13 | 3508 |
| sgemm2 | 12014 | 11616 | -398 | 10143 |
| sgemmx | 15117 | 14910 | -207 | 11856 |
| sgemv | 1559 | 1553 | -6 | 1065 |
| vecadd | 1183 | 1172 | -11 | 668 |
| wgather | 1188 | 1174 | -14 | 732 |
| basic | 2335 | 2335 | 0 | 1971 |
| wsync | 9324 | 9323 | -1 | 7236 |
| bfs | 20320 | 19936 | -384 | 14821 |

## 4. 逐项 trace 证据

下面的数字来自本次原始日志，不是按总周期推断。访存样例仅接受同 token、同物理 Warp、同 launch、同地址、唯一 read，并要求 req/rsp 落在双方对应 dispatch/commit 区间内；无唯一配对就不做归因。三段是 dispatch→外部接受 / 外部接受→响应 / 响应→commit；不是将整条 load 的差全归到 DRAM。完整匹配与分布位于每项 `aligned.csv`、`analysis.json`、`memory.json`、`evidence.json`。


### async_barrier

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/timing/stderr.log:12)、[rtl:203](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/rtl/stdout.log:203)、[rtl:251](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/rtl/stdout.log:251)、[rtl:269](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/rtl/stdout.log:269)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x8000002c LW 第 1 次，地址 0x11000。Timing 三段 [103, 100, 11]，RTL [17, 75, 11]；dispatch→commit 差 +111 周期。Timing 四点 267→370→470→481；RTL 142→159→234→245。证据：[timing:110](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/timing/stderr.log:110)、[timing:261](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/timing/stderr.log:261)、[timing:291](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/timing/stderr.log:291)、[timing:300](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/timing/stderr.log:300)；[rtl:990](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/rtl/stdout.log:990)、[rtl:1336](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/rtl/stdout.log:1336)、[rtl:1919](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/rtl/stdout.log:1919)、[rtl:1997](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/async_barrier/rtl/stdout.log:1997)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### conv3

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/timing/stderr.log:2)、[timing:16](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/timing/stderr.log:16)、[timing:17](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/timing/stderr.log:17)、[rtl:199](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/rtl/stdout.log:199)、[rtl:252](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/rtl/stdout.log:252)、[rtl:270](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/rtl/stdout.log:270)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x8000011c FLW 第 1 次，地址 0x10000。Timing 三段 [10, 100, 11]，RTL [10, 18, 13]；dispatch→commit 差 +80 周期。Timing 四点 1532→1542→1642→1653；RTL 950→960→978→991。证据：[timing:922](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/timing/stderr.log:922)、[timing:932](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/timing/stderr.log:932)、[timing:996](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/timing/stderr.log:996)、[timing:1002](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/timing/stderr.log:1002)；[rtl:7336](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/rtl/stdout.log:7336)、[rtl:7444](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/rtl/stdout.log:7444)、[rtl:7679](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/rtl/stdout.log:7679)、[rtl:7890](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/conv3/rtl/stdout.log:7890)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### demo

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/timing/stderr.log:12)、[rtl:203](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/rtl/stdout.log:203)、[rtl:249](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/rtl/stdout.log:249)、[rtl:267](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/rtl/stdout.log:267)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x80000088 LW 第 1 次，地址 0x10100。Timing 三段 [117, 100, 13]，RTL [41, 18, 13]；dispatch→commit 差 +158 周期。Timing 四点 702→819→919→932；RTL 413→454→472→485。证据：[timing:400](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/timing/stderr.log:400)、[timing:477](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/timing/stderr.log:477)、[timing:493](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/timing/stderr.log:493)、[timing:503](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/timing/stderr.log:503)；[rtl:3319](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/rtl/stdout.log:3319)、[rtl:3841](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/rtl/stdout.log:3841)、[rtl:4139](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/rtl/stdout.log:4139)、[rtl:4245](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/demo/rtl/stdout.log:4245)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### diverge

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/timing/stderr.log:12)、[rtl:200](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/rtl/stdout.log:200)、[rtl:246](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/rtl/stdout.log:246)、[rtl:264](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/rtl/stdout.log:264)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x80000030 LW 第 1 次，地址 0x11000。Timing 三段 [94, 100, 11]，RTL [10, 73, 11]；dispatch→commit 差 +111 周期。Timing 四点 275→369→469→480；RTL 150→160→233→244。证据：[timing:127](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/timing/stderr.log:127)、[timing:259](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/timing/stderr.log:259)、[timing:289](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/timing/stderr.log:289)、[timing:298](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/timing/stderr.log:298)；[rtl:1164](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/rtl/stdout.log:1164)、[rtl:1336](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/rtl/stdout.log:1336)、[rtl:1921](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/rtl/stdout.log:1921)、[rtl:1995](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/diverge/rtl/stdout.log:1995)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### dogfood

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/timing/stderr.log:12)、[rtl:207](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/rtl/stdout.log:207)、[rtl:253](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/rtl/stdout.log:253)、[rtl:271](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/rtl/stdout.log:271)。

访存实例：launch 5 / CTA 0 / Warp 1 / 0x80000220 FLW 第 1 次，地址 0x10140。Timing 三段 [117, 100, 13]，RTL [47, 17, 13]；dispatch→commit 差 +153 周期。Timing 四点 1073→1190→1290→1303；RTL 738→785→802→815。证据：[timing:6347](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/timing/stderr.log:6347)、[timing:6420](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/timing/stderr.log:6420)、[timing:6505](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/timing/stderr.log:6505)、[timing:6519](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/timing/stderr.log:6519)；[rtl:50463](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/rtl/stdout.log:50463)、[rtl:51020](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/rtl/stdout.log:51020)、[rtl:51247](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/rtl/stdout.log:51247)、[rtl:51355](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dogfood/rtl/stdout.log:51355)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### dotproduct

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/timing/stderr.log:12)、[rtl:204](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/rtl/stdout.log:204)、[rtl:252](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/rtl/stdout.log:252)、[rtl:270](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/rtl/stdout.log:270)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x8000002c LW 第 1 次，地址 0x11000。Timing 三段 [103, 100, 11]，RTL [17, 75, 11]；dispatch→commit 差 +111 周期。Timing 四点 267→370→470→481；RTL 142→159→234→245。证据：[timing:110](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/timing/stderr.log:110)、[timing:257](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/timing/stderr.log:257)、[timing:287](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/timing/stderr.log:287)、[timing:296](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/timing/stderr.log:296)；[rtl:991](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/rtl/stdout.log:991)、[rtl:1337](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/rtl/stdout.log:1337)、[rtl:1908](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/rtl/stdout.log:1908)、[rtl:1986](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/rtl/stdout.log:1986)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### dotproduct2

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/timing/stderr.log:12)、[rtl:204](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/rtl/stdout.log:204)、[rtl:252](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/rtl/stdout.log:252)、[rtl:270](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/rtl/stdout.log:270)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x8000002c LW 第 1 次，地址 0x11000。Timing 三段 [103, 100, 11]，RTL [17, 75, 11]；dispatch→commit 差 +111 周期。Timing 四点 267→370→470→481；RTL 142→159→234→245。证据：[timing:110](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/timing/stderr.log:110)、[timing:257](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/timing/stderr.log:257)、[timing:287](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/timing/stderr.log:287)、[timing:296](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/timing/stderr.log:296)；[rtl:991](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/rtl/stdout.log:991)、[rtl:1337](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/rtl/stdout.log:1337)、[rtl:1908](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/rtl/stdout.log:1908)、[rtl:1986](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct2/rtl/stdout.log:1986)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### dropout

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/timing/stderr.log:12)、[rtl:201](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/rtl/stdout.log:201)、[rtl:249](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/rtl/stdout.log:249)、[rtl:267](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/rtl/stdout.log:267)。

访存实例：launch 1 / CTA 1 / Warp 0 / 0x800000c4 FLW 第 1 次，地址 0x10040。Timing 三段 [10, 100, 11]，RTL [6, 16, 14]；dispatch→commit 差 +85 周期。Timing 四点 1671→1681→1781→1792；RTL 1155→1161→1177→1191。证据：[timing:1154](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/timing/stderr.log:1154)、[timing:1164](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/timing/stderr.log:1164)、[timing:1203](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/timing/stderr.log:1203)、[timing:1208](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/timing/stderr.log:1208)；[rtl:8775](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/rtl/stdout.log:8775)、[rtl:8837](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/rtl/stdout.log:8837)、[rtl:8933](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/rtl/stdout.log:8933)、[rtl:9016](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dropout/rtl/stdout.log:9016)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### fence

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/timing/stderr.log:12)、[rtl:201](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/rtl/stdout.log:201)、[rtl:247](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/rtl/stdout.log:247)、[rtl:265](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/rtl/stdout.log:265)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x80000068 LW 第 1 次，地址 0x10000。Timing 三段 [117, 100, 13]，RTL [13, 36, 13]；dispatch→commit 差 +168 周期。Timing 四点 598→715→815→828；RTL 360→373→409→422。证据：[timing:425](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/timing/stderr.log:425)、[timing:475](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/timing/stderr.log:475)、[timing:575](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/timing/stderr.log:575)、[timing:590](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/timing/stderr.log:590)；[rtl:3142](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/rtl/stdout.log:3142)、[rtl:3299](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/rtl/stdout.log:3299)、[rtl:3647](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/rtl/stdout.log:3647)、[rtl:3758](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/fence/rtl/stdout.log:3758)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### io_addr

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/timing/stderr.log:12)、[rtl:266](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/rtl/stdout.log:266)、[rtl:314](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/rtl/stdout.log:314)、[rtl:332](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/rtl/stdout.log:332)。

访存实例：launch 1 / CTA 1 / Warp 2 / 0x8000004c LW 第 1 次，地址 0x10100。Timing 三段 [13, 100, 13]，RTL [10, 18, 13]；dispatch→commit 差 +85 周期。Timing 四点 967→980→1080→1093；RTL 653→663→681→694。证据：[timing:522](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/timing/stderr.log:522)、[timing:536](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/timing/stderr.log:536)、[timing:563](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/timing/stderr.log:563)、[timing:577](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/timing/stderr.log:577)；[rtl:4583](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/rtl/stdout.log:4583)、[rtl:4685](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/rtl/stdout.log:4685)、[rtl:4823](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/rtl/stdout.log:4823)、[rtl:5000](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/io_addr/rtl/stdout.log:5000)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### jacobi

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/timing/stderr.log:12)、[rtl:203](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/rtl/stdout.log:203)、[rtl:249](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/rtl/stdout.log:249)、[rtl:267](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/rtl/stdout.log:267)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x80000024 LW 第 1 次，地址 0x11000。Timing 三段 [10, 100, 11]，RTL [10, 25, 11]；dispatch→commit 差 +75 周期。Timing 四点 248→258→358→369；RTL 123→133→158→169。证据：[timing:91](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/timing/stderr.log:91)、[timing:101](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/timing/stderr.log:101)、[timing:149](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/timing/stderr.log:149)、[timing:154](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/timing/stderr.log:154)；[rtl:811](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/rtl/stdout.log:811)、[rtl:919](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/rtl/stdout.log:919)、[rtl:1196](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/rtl/stdout.log:1196)、[rtl:1333](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/jacobi/rtl/stdout.log:1333)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### madmax

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/timing/stderr.log:2)、[timing:10](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/timing/stderr.log:10)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/timing/stderr.log:11)、[rtl:194](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/rtl/stdout.log:194)、[rtl:227](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/rtl/stdout.log:227)、[rtl:239](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/rtl/stdout.log:239)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x80000024 LW 第 1 次，地址 0x11000。Timing 三段 [10, 100, 11]，RTL [10, 25, 11]；dispatch→commit 差 +75 周期。Timing 四点 248→258→358→369；RTL 123→133→158→169。证据：[timing:88](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/timing/stderr.log:88)、[timing:98](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/timing/stderr.log:98)、[timing:154](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/timing/stderr.log:154)、[timing:159](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/timing/stderr.log:159)；[rtl:521](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/rtl/stdout.log:521)、[rtl:588](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/rtl/stdout.log:588)、[rtl:727](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/rtl/stdout.log:727)、[rtl:828](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/madmax/rtl/stdout.log:828)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '5', 'dispatch-commit': '1'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### mstress

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/timing/stderr.log:12)、[rtl:203](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/rtl/stdout.log:203)、[rtl:249](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/rtl/stdout.log:249)、[rtl:267](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/rtl/stdout.log:267)。

访存实例：launch 1 / CTA 0 / Warp 3 / 0x800000a8 LW 第 1 次，地址 0x10100。Timing 三段 [118, 100, 10]，RTL [32, 18, 24]；dispatch→commit 差 +154 周期。Timing 四点 903→1021→1121→1131；RTL 551→583→601→625。证据：[timing:607](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/timing/stderr.log:607)、[timing:722](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/timing/stderr.log:722)、[timing:824](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/timing/stderr.log:824)、[timing:835](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/timing/stderr.log:835)；[rtl:5915](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/rtl/stdout.log:5915)、[rtl:6537](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/rtl/stdout.log:6537)、[rtl:6942](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/rtl/stdout.log:6942)、[rtl:7366](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/mstress/rtl/stdout.log:7366)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### multikernel

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/timing/stderr.log:12)、[rtl:188](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/rtl/stdout.log:188)、[rtl:236](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/rtl/stdout.log:236)、[rtl:254](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/rtl/stdout.log:254)。

访存实例：launch 1 / CTA 1 / Warp 0 / 0x80000094 LW 第 1 次，地址 0x10040。Timing 三段 [10, 100, 11]，RTL [10, 16, 11]；dispatch→commit 差 +84 周期。Timing 四点 4421→4431→4531→4542；RTL 3031→3041→3057→3068。证据：[timing:3072](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/timing/stderr.log:3072)、[timing:3082](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/timing/stderr.log:3082)、[timing:3108](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/timing/stderr.log:3108)、[timing:3113](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/timing/stderr.log:3113)；[rtl:21638](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/rtl/stdout.log:21638)、[rtl:21827](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/rtl/stdout.log:21827)、[rtl:21995](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/rtl/stdout.log:21995)、[rtl:22107](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/rtl/stdout.log:22107)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### occupancy

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/timing/stderr.log:2)、[timing:10](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/timing/stderr.log:10)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/timing/stderr.log:11)、[rtl:190](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/rtl/stdout.log:190)、[rtl:223](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/rtl/stdout.log:223)、[rtl:235](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/rtl/stdout.log:235)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x8000003c LW 第 1 次，地址 0x11000。Timing 三段 [11, 100, 10]，RTL [11, 25, 10]；dispatch→commit 差 +75 周期。Timing 四点 321→332→432→442；RTL 196→207→232→242。证据：[timing:159](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/timing/stderr.log:159)、[timing:169](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/timing/stderr.log:169)、[timing:176](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/timing/stderr.log:176)、[timing:187](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/timing/stderr.log:187)；[rtl:731](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/rtl/stdout.log:731)、[rtl:767](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/rtl/stdout.log:767)、[rtl:840](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/rtl/stdout.log:840)、[rtl:894](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/occupancy/rtl/stdout.log:894)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### packld

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/timing/stderr.log:12)、[rtl:190](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/rtl/stdout.log:190)、[rtl:236](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/rtl/stdout.log:236)、[rtl:254](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/rtl/stdout.log:254)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x8000002c LW 第 1 次，地址 0x11000。Timing 三段 [103, 100, 11]，RTL [17, 75, 11]；dispatch→commit 差 +111 周期。Timing 四点 267→370→470→481；RTL 142→159→234→245。证据：[timing:110](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/timing/stderr.log:110)、[timing:261](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/timing/stderr.log:261)、[timing:291](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/timing/stderr.log:291)、[timing:300](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/timing/stderr.log:300)；[rtl:975](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/rtl/stdout.log:975)、[rtl:1321](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/rtl/stdout.log:1321)、[rtl:1904](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/rtl/stdout.log:1904)、[rtl:1982](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/packld/rtl/stdout.log:1982)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### pathfinder

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/timing/stderr.log:12)、[rtl:201](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/rtl/stdout.log:201)、[rtl:247](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/rtl/stdout.log:247)、[rtl:265](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/rtl/stdout.log:265)。

访存实例：launch 1 / CTA 0 / Warp 1 / 0x800000b4 LW 第 1 次，地址 0x10000。Timing 三段 [10, 100, 11]，RTL [10, 18, 11]；dispatch→commit 差 +82 周期。Timing 四点 852→862→962→973；RTL 577→587→605→616。证据：[timing:623](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/timing/stderr.log:623)、[timing:633](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/timing/stderr.log:633)、[timing:676](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/timing/stderr.log:676)、[timing:681](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/timing/stderr.log:681)；[rtl:3621](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/rtl/stdout.log:3621)、[rtl:3674](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/rtl/stdout.log:3674)、[rtl:3758](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/rtl/stdout.log:3758)、[rtl:3830](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/pathfinder/rtl/stdout.log:3830)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### raycast

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/timing/stderr.log:2)、[timing:16](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/timing/stderr.log:16)、[timing:17](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/timing/stderr.log:17)、[rtl:187](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/rtl/stdout.log:187)、[rtl:240](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/rtl/stdout.log:240)、[rtl:258](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/rtl/stdout.log:258)。

访存实例：launch 1 / CTA 2 / Warp 0 / 0x800002d8 FLW 第 4 次，地址 0x31080。Timing 三段 [233, 100, 10]，RTL [13, 18, 10]；dispatch→commit 差 +302 周期。Timing 四点 31368→31601→31701→31711；RTL 24391→24404→24422→24432。证据：[timing:29140](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/timing/stderr.log:29140)、[timing:29378](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/timing/stderr.log:29378)、[timing:29476](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/timing/stderr.log:29476)、[timing:29487](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/timing/stderr.log:29487)；[rtl:159623](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/rtl/stdout.log:159623)、[rtl:159724](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/rtl/stdout.log:159724)、[rtl:159846](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/rtl/stdout.log:159846)、[rtl:159921](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/raycast/rtl/stdout.log:159921)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### relu

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/timing/stderr.log:12)、[rtl:200](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/rtl/stdout.log:200)、[rtl:248](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/rtl/stdout.log:248)、[rtl:266](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/rtl/stdout.log:266)。

访存实例：launch 1 / CTA 1 / Warp 0 / 0x8000004c FLW 第 1 次，地址 0x10040。Timing 三段 [10, 100, 11]，RTL [10, 16, 11]；dispatch→commit 差 +84 周期。Timing 四点 931→941→1041→1052；RTL 586→596→612→623。证据：[timing:580](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/timing/stderr.log:580)、[timing:590](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/timing/stderr.log:590)、[timing:622](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/timing/stderr.log:622)、[timing:627](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/timing/stderr.log:627)；[rtl:4336](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/rtl/stdout.log:4336)、[rtl:4450](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/rtl/stdout.log:4450)、[rtl:4605](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/rtl/stdout.log:4605)、[rtl:4733](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/relu/rtl/stdout.log:4733)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### sgemm

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/timing/stderr.log:12)、[rtl:188](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/rtl/stdout.log:188)、[rtl:236](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/rtl/stdout.log:236)、[rtl:254](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/rtl/stdout.log:254)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x800000e4 FLW 第 1 次，地址 0x10180。Timing 三段 [110, 100, 11]，RTL [26, 18, 11]；dispatch→commit 差 +166 周期。Timing 四点 1008→1118→1218→1229；RTL 637→663→681→692。证据：[timing:610](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/timing/stderr.log:610)、[timing:676](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/timing/stderr.log:676)、[timing:778](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/timing/stderr.log:778)、[timing:790](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/timing/stderr.log:790)；[rtl:5615](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/rtl/stdout.log:5615)、[rtl:5983](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/rtl/stdout.log:5983)、[rtl:6181](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/rtl/stdout.log:6181)、[rtl:6313](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm/rtl/stdout.log:6313)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### sgemm2

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/timing/stderr.log:12)、[rtl:203](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/rtl/stdout.log:203)、[rtl:251](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/rtl/stdout.log:251)、[rtl:269](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/rtl/stdout.log:269)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x80000034 LW 第 1 次，地址 0x11000。Timing 三段 [184, 100, 11]，RTL [63, 48, 11]；dispatch→commit 差 +173 周期。Timing 四点 285→469→569→580；RTL 160→223→271→282。证据：[timing:144](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/timing/stderr.log:144)、[timing:352](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/timing/stderr.log:352)、[timing:438](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/timing/stderr.log:438)、[timing:443](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/timing/stderr.log:443)；[rtl:1316](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/rtl/stdout.log:1316)、[rtl:1941](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/rtl/stdout.log:1941)、[rtl:2278](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/rtl/stdout.log:2278)、[rtl:2408](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemm2/rtl/stdout.log:2408)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### sgemmx

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/timing/stderr.log:12)、[rtl:202](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/rtl/stdout.log:202)、[rtl:248](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/rtl/stdout.log:248)、[rtl:266](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/rtl/stdout.log:266)。

访存实例：launch 1 / CTA 0 / Warp 3 / 0x80000484 FLW 第 1 次，地址 0x10b00。Timing 三段 [129, 100, 20]，RTL [10, 18, 15]；dispatch→commit 差 +206 周期。Timing 四点 12968→13097→13197→13217；RTL 10684→10694→10712→10727。证据：[timing:12962](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/timing/stderr.log:12962)、[timing:13038](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/timing/stderr.log:13038)、[timing:13130](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/timing/stderr.log:13130)、[timing:13148](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/timing/stderr.log:13148)；[rtl:85689](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/rtl/stdout.log:85689)、[rtl:85779](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/rtl/stdout.log:85779)、[rtl:85996](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/rtl/stdout.log:85996)、[rtl:86210](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemmx/rtl/stdout.log:86210)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### sgemv

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/timing/stderr.log:12)、[rtl:195](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/rtl/stdout.log:195)、[rtl:241](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/rtl/stdout.log:241)、[rtl:259](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/rtl/stdout.log:259)。

访存实例：launch 1 / CTA 0 / Warp 1 / 0x800000a8 FLW 第 1 次，地址 0x10100。Timing 三段 [105, 100, 11]，RTL [47, 18, 13]；dispatch→commit 差 +138 周期。Timing 四点 867→972→1072→1083；RTL 567→614→632→645。证据：[timing:671](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/timing/stderr.log:671)、[timing:747](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/timing/stderr.log:747)、[timing:777](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/timing/stderr.log:777)、[timing:782](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/timing/stderr.log:782)；[rtl:4067](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/rtl/stdout.log:4067)、[rtl:4703](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/rtl/stdout.log:4703)、[rtl:4876](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/rtl/stdout.log:4876)、[rtl:4992](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sgemv/rtl/stdout.log:4992)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### softmax

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/timing/stderr.log:12)、[rtl:202](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/rtl/stdout.log:202)、[rtl:248](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/rtl/stdout.log:248)、[rtl:266](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/rtl/stdout.log:266)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x8000002c LW 第 1 次，地址 0x11000。Timing 三段 [103, 100, 11]，RTL [17, 75, 11]；dispatch→commit 差 +111 周期。Timing 四点 266→369→469→480；RTL 141→158→233→244。证据：[timing:109](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/timing/stderr.log:109)、[timing:261](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/timing/stderr.log:261)、[timing:291](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/timing/stderr.log:291)、[timing:300](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/timing/stderr.log:300)；[rtl:982](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/rtl/stdout.log:982)、[rtl:1328](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/rtl/stdout.log:1328)、[rtl:1916](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/rtl/stdout.log:1916)、[rtl:1994](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/softmax/rtl/stdout.log:1994)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### sort

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/timing/stderr.log:12)、[rtl:199](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/rtl/stdout.log:199)、[rtl:247](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/rtl/stdout.log:247)、[rtl:265](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/rtl/stdout.log:265)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x80000064 LW 第 17 次，地址 0x10040。Timing 三段 [10, 100, 11]，RTL [10, 16, 11]；dispatch→commit 差 +84 周期。Timing 四点 2385→2395→2495→2506；RTL 2096→2106→2122→2133。证据：[timing:2131](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/timing/stderr.log:2131)、[timing:2141](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/timing/stderr.log:2141)、[timing:2194](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/timing/stderr.log:2194)、[timing:2199](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/timing/stderr.log:2199)；[rtl:16855](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/rtl/stdout.log:16855)、[rtl:16968](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/rtl/stdout.log:16968)、[rtl:17124](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/rtl/stdout.log:17124)、[rtl:17256](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/sort/rtl/stdout.log:17256)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### stencil3d

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/timing/stderr.log:2)、[timing:13](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/timing/stderr.log:13)、[timing:14](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/timing/stderr.log:14)、[rtl:200](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/rtl/stdout.log:200)、[rtl:250](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/rtl/stdout.log:250)、[rtl:268](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/rtl/stdout.log:268)。

访存实例：launch 1 / CTA 0 / Warp 1 / 0x800002cc FLW 第 1 次，地址 0x10080。Timing 三段 [14, 100, 11]，RTL [12, 18, 15]；dispatch→commit 差 +80 周期。Timing 四点 3003→3017→3117→3128；RTL 2038→2050→2068→2083。证据：[timing:2069](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/timing/stderr.log:2069)、[timing:2083](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/timing/stderr.log:2083)、[timing:2184](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/timing/stderr.log:2184)、[timing:2196](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/timing/stderr.log:2196)；[rtl:17162](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/rtl/stdout.log:17162)、[rtl:17428](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/rtl/stdout.log:17428)、[rtl:17710](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/rtl/stdout.log:17710)、[rtl:17900](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/stencil3d/rtl/stdout.log:17900)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### vecadd

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/timing/stderr.log:12)、[rtl:188](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/rtl/stdout.log:188)、[rtl:236](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/rtl/stdout.log:236)、[rtl:254](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/rtl/stdout.log:254)。

访存实例：launch 1 / CTA 1 / Warp 0 / 0x80000054 FLW 第 1 次，地址 0x10040。Timing 三段 [10, 100, 11]，RTL [10, 16, 11]；dispatch→commit 差 +84 周期。Timing 四点 891→901→1001→1012；RTL 532→542→558→569。证据：[timing:558](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/timing/stderr.log:558)、[timing:568](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/timing/stderr.log:568)、[timing:613](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/timing/stderr.log:613)、[timing:619](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/timing/stderr.log:619)；[rtl:4531](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/rtl/stdout.log:4531)、[rtl:4644](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/rtl/stdout.log:4644)、[rtl:4843](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/rtl/stdout.log:4843)、[rtl:5010](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/rtl/stdout.log:5010)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### wgather

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/timing/stderr.log:2)、[timing:16](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/timing/stderr.log:16)、[timing:17](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/timing/stderr.log:17)、[rtl:187](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/rtl/stdout.log:187)、[rtl:240](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/rtl/stdout.log:240)、[rtl:258](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/rtl/stdout.log:258)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x8000002c LW 第 1 次，地址 0x11000。Timing 三段 [103, 100, 11]，RTL [17, 75, 11]；dispatch→commit 差 +111 周期。Timing 四点 267→370→470→481；RTL 142→159→234→245。证据：[timing:115](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/timing/stderr.log:115)、[timing:266](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/timing/stderr.log:266)、[timing:296](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/timing/stderr.log:296)、[timing:305](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/timing/stderr.log:305)；[rtl:979](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/rtl/stdout.log:979)、[rtl:1325](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/rtl/stdout.log:1325)、[rtl:1914](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/rtl/stdout.log:1914)、[rtl:1988](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wgather/rtl/stdout.log:1988)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### basic

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/timing/stderr.log:2)、[timing:6](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/timing/stderr.log:6)、[timing:7](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/timing/stderr.log:7)、[rtl:202](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/rtl/stdout.log:202)、[rtl:223](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/rtl/stdout.log:223)、[rtl:234](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/rtl/stdout.log:234)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x8000003c LW 第 17 次，地址 0x10040。Timing 三段 [10, 100, 11]，RTL [10, 16, 11]；dispatch→commit 差 +84 周期。Timing 四点 1359→1369→1469→1480；RTL 1077→1087→1103→1114。证据：[timing:960](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/timing/stderr.log:960)、[timing:970](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/timing/stderr.log:970)、[timing:988](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/timing/stderr.log:988)、[timing:993](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/timing/stderr.log:993)；[rtl:2960](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/rtl/stdout.log:2960)、[rtl:3001](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/rtl/stdout.log:3001)、[rtl:3037](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/rtl/stdout.log:3037)、[rtl:3070](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/basic/rtl/stdout.log:3070)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### wsync

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/timing/stderr.log:2)、[timing:6](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/timing/stderr.log:6)、[timing:7](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/timing/stderr.log:7)、[rtl:194](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/rtl/stdout.log:194)、[rtl:215](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/rtl/stdout.log:215)、[rtl:226](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/rtl/stdout.log:226)。

访存实例：launch 1 / CTA 0 / Warp 0 / 0x8000011c LW 第 8 次，地址 0x10fc0。Timing 三段 [10, 100, 11]，RTL [10, 16, 11]；dispatch→commit 差 +84 周期。Timing 四点 4689→4699→4799→4810；RTL 3506→3516→3532→3543。证据：[timing:3551](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/timing/stderr.log:3551)、[timing:3562](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/timing/stderr.log:3562)、[timing:3622](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/timing/stderr.log:3622)、[timing:3633](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/timing/stderr.log:3633)；[rtl:9256](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/rtl/stdout.log:9256)、[rtl:9335](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/rtl/stdout.log:9335)、[rtl:9389](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/rtl/stdout.log:9389)、[rtl:9431](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/wsync/rtl/stdout.log:9431)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

### bfs

[运行审计](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/result.json) · [全部阶段匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/aligned.csv) · [访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/evidence.json)

首条取指 schedule→decode：Timing 170，RTL 45；分段（schedule→mem accept / service / return→decode）Timing [66, 100, 4]，RTL [16, 25, 4]。证据：[timing:2](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/timing/stderr.log:2)、[timing:11](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/timing/stderr.log:11)、[timing:12](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/timing/stderr.log:12)、[rtl:192](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/rtl/stdout.log:192)、[rtl:238](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/rtl/stdout.log:238)、[rtl:256](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/rtl/stdout.log:256)。

访存实例：launch 2 / CTA 0 / Warp 0 / 0x80000038 LW 第 1 次，地址 0x11000。Timing 三段 [77, 100, 11]，RTL [10, 46, 11]；dispatch→commit 差 +121 周期。Timing 四点 234→311→411→422；RTL 166→176→222→233。证据：[timing:1835](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/timing/stderr.log:1835)、[timing:1922](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/timing/stderr.log:1922)、[timing:1952](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/timing/stderr.log:1952)、[timing:1961](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/timing/stderr.log:1961)；[rtl:7039](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/rtl/stdout.log:7039)、[rtl:7181](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/rtl/stdout.log:7181)、[rtl:7505](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/rtl/stdout.log:7505)、[rtl:7565](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/bfs/rtl/stdout.log:7565)。

各阶段差（Timing−RTL）众数：{'schedule-decode': '0', 'decode-dispatch': '0', 'dispatch-commit': '0'}。这是局部 interval，不可将跨 Warp 重叠区间相加为总误差。

## 5. 运行记录、边界与复核

最初提交正式 28 项加 basic/wsync，发现旧通过集另含 BFS 后追加，不遗漏此前已通过项目。其余规模按 manifest 固定，不因执行结果调整或删除子测试。解析器初版误将 Epoch 关联到 WarpGeneration，导致 multikernel 虚假序列不一致；核对显式 Bindings 与 Token.ID 后修正分析器，未修改模拟器。所有双侧原始日志完整保留。

无 trace 控制实验：basic、packld、multikernel 使用独立、当前源码编译的未插桩库，对比 launch/flush 的执行边与退休数；结果见 [control result](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/observer-control/result.json)。

结论与补充诊断见下节。本轮没有执行 commit，也没有修复任何正式模型逻辑。

## 6. 本轮实际确认了哪些修复，哪些差异仍在

### 6.1 已确认：D-cache port 0 的注册缓冲不再缺失

vecadd 同一个 Token 33、地址 `0x11008`，Timing global-adapter 在设备周期 264 接受两路请求：port 1 同周期进入 Cache，port 0 到 265 才进入 Cache。
见 [Timing 周期 264](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/timing/stderr.log:105)、[周期 265](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/timing/stderr.log:106)。RTL 对应地址首次 port 1 请求为 raw 395、port 0 为 raw 397，即相差一周期，见 [RTL port 1](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/rtl/stdout.log:919)、[RTL port 0](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/rtl/stdout.log:928)。

这与当前 [dcache_port_buffer.go](../../timing/memsys/dcache_port_buffer.go) 的两个注册槽、port 1 直通一致。能确认的是该样例的结构延时已存在；不能据此宣布所有满队列、同周期出入队、flush 仲裁均逐周期一致。

### 6.2 已确认：跨 launch 不再重新创建冷 Cache

multikernel 的第 2、3 次 launch，首条 fetch 的 schedule→external accept 均为 **Timing 7 / RTL 7**；外部服务为 **100 / 32**；返回→decode 均为 **4 / 4**。见 [第二次 Timing 接受](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/timing/stderr.log:3241)、[第二次 RTL 接受](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/rtl/stdout.log:23418)、[完整三次分段](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/multikernel/evidence.json)。

此处剩余 68 周期差可以直接定位到外部 backend 服务时间；不再是每次 launch 重跑 reset scan。对应实现为 [Kernel.NextLaunch](../../timing/runner/kernel.go:387) 的 memory hierarchy/clock 所有权转移。host 显式 flush 仍发生；保持 hierarchy 生命周期不等于所有 line 永远保持热状态。

### 6.3 已确认：CTA 可以逐 Warp 分配，不再要求完整四槽同时空闲

dotproduct 第二个 CTA 的物理 Warp 顺序，Timing 与 RTL 均为 **1、2、3、0**。Timing 分配周期为 2131、2132、2133、2327；RTL raw 为 3291、3293、3295、3669。前三个成员可先开始，最后一个等待旧资源释放。
证据：[Timing 前三个成员](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/timing/stderr.log:1587)、[最后一个成员](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/timing/stderr.log:1807)、[RTL 前三个成员](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/rtl/stdout.log:8896)、[RTL 最后成员](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/rtl/stdout.log:10843)。

这证明旧的整 CTA 预留限制在该样例中已消除；实际间隔仍相差 7 周期（196 对 189），含旧 Warp 完成/访存尾部差异，不应直接将这 7 周期归为 dispatcher 内部流水错误。

### 6.4 仍在：首个 launch 的 reset/启动测量边界不同

本轮首条 fetch 的 schedule→decode 为 **170 / 45**，分段为 **66+100+4 / 16+25+4**。其中 75 周期来自 backend；另外 50 周期发生在 external accept 以前。
RTL I-cache tags-init 从 raw 19 开始，首个 scheduler dispatch 在 raw 121，此时 reset scan 已推进 51 个周期；最后一个 set 的 init 在 raw 145。见 [init 起点](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/rtl/stdout.log:25)、[init 末尾](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/vecadd/rtl/stdout.log:219)。Timing 首次 NewKernel 将新 hierarchy 与执行一起启动，因此模型的首条 fetch 承担了更多 reset 尾部等待。

这是设备生命周期/启动边界不一致的证据，不应把 125 周期全部算作 I-cache hit latency 建错；也不能从每个 benchmark 总周期机械减去 125，因为多 Warp 重叠、busy-qualified 计数和后续阻塞会改变总量。下一轮应将 reset/初始化与 kernel busy 测量分离，保留未校准和校准两套结果。

### 6.5 仍在：固定 100 周期与 RTL backend 不等价，且会传播为排队差

vecadd，第二 CTA 的 `0x80000054 FLW`：双方 dispatch→external accept 都是 10，return→commit 都是 11，服务时间是 100 对 16，整条 load 因而差 **84** 周期。这个实例已把差明确定位到 backend，而非 LSU 返回流水。
dotproduct 第一 CTA 的 `0x8000002c LW`：Timing 三段 **103/100/11**，RTL **17/75/11**。Timing external offer 比 actual accept 早 86 周期；多出来的等待发生在后端接受以前，不能把整段 111 周期差全部解释为 100−75。分别见第 4 节对应条目的原始日志和 [dotproduct 访存证据](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/results/dotproduct/evidence.json)。

所有 **9,341 次**已完成 backend 请求均满足 `CompletedCycle−AcceptedCycle=100`，包括本轮显式 flush 的请求。配置实现仍是 [runtime config.Latency=100](../../integration/vortexruntime/device.go:359)。请求 offer 等待、return 带宽等待不属于这 100 周期的服务定义。

冻结 backend 接受能力为每周期 1 个请求、最大在途 16、每周期返回 1 个；全局接受顺序返回并允许 head-of-line blocking，见 [mc-backend 参数](../../timing/ir.yaml:6457)。因此“固定延迟 100”不代表所有 miss 都从检测时刻起恰好 100 周期完成。

要进一步定位 Cache/MSHR/仲裁的纯建模误差，需要双方使用一致的 memory request/response 驱动（同后端或确定性响应回放），再比较 accepted/ready/valid，而不是调整经验 latency 去拟合一个 benchmark 的总周期。本轮未改变任何 backend 参数去追平结果。

### 6.6 仍需验证：队列与并发时序不能由“总周期更接近”代替

本轮 `mstress` 执行边反而增加 109、`occupancy` 增加 4；`packld` 减少 499、`sgemm2` 减少 398。多项修复同时进入当前版本，且内存响应时序改变资源竞争，不能由前后总数把变化单独归因到某一个 patch。

全量对齐中 schedule→decode 有 203,175 / 206,278 条局部区间完全相同，decode→dispatch 为 167,181 / 206,278，dispatch→commit 为 174,880 / 206,278。剩余区间差异不是独立额外周期，也不全是模型缺陷；它们可能来自共享资源竞争和不同到达时刻。所有原始分布、最大差异实例均保存在 `analysis.json`，避免仅挑相同的阶段展示。

下一步应在相同 memory 响应条件下，依次核查 port 缓冲满/释放、MSHR 合并与 replay、flush 与 store 的顺序、CTA 复用尾部；对于第一个握手分歧，记录旧状态、ready/valid、仲裁选择及新状态，再将最小反例固化成逐边回归。**本轮证据支持功能与若干关键结构修复有效，不支持宣称已实现逐周期 RTL 等价。**

## 7. 最终复核与复现入口

- 31 项双侧作业与 trace 分析均 COMPLETED / 0；无超时、无 simulator panic、无 host oracle 失败。最长单后端是 occupancy Timing **215.3 秒**，trace 约 1.76 GB；这是含观测输出和共享存储 I/O 的实测耗时，不是纯模拟器吞吐基准。
- 已监控至仿真、分析及重试控制实验全部结束；[Slurm 最终账单](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/slurm-final.psv) 保存每个作业的 Start/End、退出码和时长。全文 548 个原始证据/文件链接在首次生成时检查均有效；后续追加链接也指向本轮留存产物。
- 控制作业 `12770656` 因缺少正确 `libstdc++.so.6` 搜索路径，在进入模拟器前失败（缺 GLIBCXX_3.4.29/30）；保留于 `observer-control-env-failure/`。按主作业同样设置 GCC runtime 路径后，`12770671` 的 basic、packld、multikernel 全通过，所有 launch/flush 的执行边和退休数与有 trace 版本完全相同。
- raycast 双侧 `output.ppm` 字节完全一致，SHA256 为 `934c95e672fd3186135eae938c6434031ba7e78012381cb43fcdcf62adaec730`；这增强了两侧一致性证据，但不是独立的完整图像算法 oracle。
- 310 个冻结输入/库哈希重新校验通过；旧 20 项的参数及输入哈希与前次一致；31 项均完整匹配 schedule/decode/dispatch/commit，macro/uop/Instret 核对通过。机器可读汇总见 [verification.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/trace-suite-small-20260917/verification.json)。

脚本和大日志均放在本轮独立 `.cache/trace-suite-small-20260917/`，不会覆盖 20260914 的记录。可以只读复查当前结果：

```bash
python3 Simulator_timing/.cache/trace-suite-small-20260917/run.py status
python3 Simulator_timing/.cache/trace-suite-small-20260917/verify.py
```

复现实验时应先建立新日期的隔离目录并重新冻结 manifest，不直接再次提交当前 `run.sbatch` 覆盖已留存证据。观测 overlay 只增加日志：`overlay.json` 将当前源码的 device/backend 映射到隔离副本，未替换为上一轮旧模型；`control-lib` 是当前源码无插桩构建。

## 8. 排除 DRAM 与统一边界的追加微测试

### 8.1 方法：不能从总周期直接扣 DRAM 延迟

多 Warp/miss 的延迟会重叠，响应变化又会改变 Scoreboard、MSHR、端口排队与指令发射。因此 `原总周期 − miss数 × 延迟差` 不是非 DRAM 周期；把每条指令的阶段误差求和也不是整个 kernel 的误差。

分三层验收更可靠：

| 层次 | 起止事件 | 控制条件 | 应报指标 |
| --- | --- | --- | --- |
| 局部流水线/组件 | 相同 token 的 schedule/decode/dispatch/commit，组件 valid&&ready | 相同指令、mask、输入依赖及竞争条件 | 完全相同区间占比、平均绝对周期差、最大差及首个分歧 |
| 去启动的 kernel ROI | 首条真实 decode → 最后一条 EOP commit；或两个同 PC 同逻辑 Warp 的循环边界 | Cache 初态相同；无外部请求窗口或统一响应驱动；统一执行 profile | ROI 相对误差、局部事件对齐；明确此项不覆盖首次 fetch/CTA 接纳 |
| 完整设备/Kernel 生命周期 | reset 释放→初始化完成；KMU 接受→最后 commit；CTA 回收；flush 接受→完成分别计量 | 同 reset warmup、host 操作、Cache 状态、memory 端口/队列/响应顺序 | 各段真实 clock 边数及 busy-qualified PERF，不能混成一项 |

多 launch 使用设备持续时钟，RTL raw timestamp 每 2 个单位为 1 周期；按同一逻辑事件归零，不使用 host wall time。结束事件取**全部指令中的最后 commit**，不是最后一条 TMC 的 commit——load 可以比停止取指更晚完成。最后 commit 也不自动等于 dirty 数据已经对 host 可见。

如要给完整支持集一个“非 DRAM MAPE”，还需先统一 cache 外部接口的位置、请求接受带宽、在途上限、返回带宽、响应顺序和 backpressure；只把两侧 latency 都设为 100 不够。Timing 在 L1 memory 端口接 backend，而 RTL 的 Ramulator 在顶层 memory bus，之间仍有 socket/旁路互连。必须在同一边界接驱动，或把这些互连显式保留在误差归属中。

### 8.2 实际测试内容与隔离方式

新增 [微测试目录](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917)，没有修改正式模拟器或冻结 RTL：

- [probe.S](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/probe.S)：裸汇编，无 C runtime、构造函数、栈初始化；程序最长 28 字节，在一条 I-cache line 内。
- [host.cpp](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/host.cpp)：同设备执行两次 launch，默认 1 Warp × 4 lane，另有 4 Warp 对照。load 读取 runtime 参数中的 42。host PASS 只判定完成；这些测试专门测时序，不冒充独立算术结果 oracle。
- 11 个程序：仅 TMC 结束、1 条 ADDI、2 条依赖 ADDI、64 次 ADD/MUL/DIV 循环、单次 load、连续同址两次 load、64 次同址 load 循环、4 Warp ADD/load 循环。反汇编留在 `inputs/<case>/kernel.dump`。
- 原配置作业 `12770956`，11 对 Timing/RTL、每对各 2 次 launch；47 秒完成。全部完成审计通过，PC/mask/uop 对齐。
- 回放作业 `12770975`，再次执行相同 11 对程序；49 秒完成。仅诊断库中的 external backend 使用 [replay-backend.go](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/replay-backend.go)，为对应 cache/地址/出现次序的 program read 回放 RTL 从接受到响应的周期数。相对实际 accept 计时，未强行重排或指定绝对响应时刻，未修改 Cache、Core、MSHR、端口逻辑；未匹配请求直接失败。
- 回放计划含 host flush 的 `addr=0` dummy reads，Timing 对应使用 Visibility 操作而非同址读，因此这些项不消费、不作为 program read 精度证据。机器汇总显式记录排除项。RTL 参考两次运行的 memory trace 完全一致；实际 program read 的回放延迟逐项核对通过。

此回放是**受限微测试的定位实验**，不是已统一全 benchmark 后端。复杂 workload 的 miss 顺序、合并次数变化可能导致回放无法匹配，不能静默套用地址平均延迟。也没有把匹配首条 decode 的做法当作首次 I-cache 路径已被验证。

### 8.3 实测：统一首条 decode → 最后 commit

下表均取第 2 次 launch；单位为周期，左 Timing、右 RTL。机器汇总、所有起止行号、无在途请求断言见 [measurements.json](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/measurements.json)。

| 程序 | 原 100-cycle backend，T / R | 回放同接口服务延迟，T / R | 结论 |
| --- | --- | --- | --- |
| empty：仅 TMC 结束 | 11 / 11 | 11 / 11 | 结束指令局部路径一致 |
| add：1 次 ADDI | 20 / 20 | 20 / 20 | 相对 empty 增量 9 / 9 |
| dependent_add：2 次依赖 ADDI | 29 / 29 | 29 / 29 | 再增加 9 / 9 |
| alu_loop | 2278 / 2278 | 2278 / 2278 | 热 I-cache 算术/分支一致 |
| mul_loop | 2278 / 2278 | 2278 / 2278 | MUL 局部延时一致 |
| div_loop | **2715 / 2278** | **2715 / 2278** | **残差 437，非 DRAM；执行 profile 不同** |
| load | 140 / 58 | 58 / 58 | 82 周期差来自该次 external service |
| load_twice | 142 / 60 | 60 / 60 | 第二次 load 可与首个 miss 重叠，不是两个串行 100 |
| load_loop | 2354 / 2272 | 2272 / 2272 | 首次缺失之外的热循环一致 |
| alu_4warp | 2281 / 2281 | 2281 / 2281 | 所测四 Warp 算术竞争一致 |
| load_4warp | 2382 / 2300 | 2300 / 2300 | 所测同址四 Warp LSU/Cache 竞争一致 |

所以本次可说“**10/11 个受限程序在该 ROI 完全对齐，DIV 的 profile 差异仍在**”，不能将其换算成“整个模拟器准确率 90.9%”，更不能据此宣布原 31 项非 DRAM MAPE 为零。

### 8.4 不使用回放也成立：无外部请求在途的热窗口

选第 2 次 launch 的第 3 次循环头 schedule 到第 64 次同一 Warp/PC schedule，跨 61 个循环间隔；检查双方读请求接受→响应区间均不与窗口相交，且程序没有 store，排除了外部 memory 对该窗口的直接占用。

| 热窗口 | Timing / RTL 周期 | 对齐指令数 | 三段局部区间完全一致 |
| --- | --- | --- | --- |
| ADD loop | 2135 / 2135 | 183 | 183 / 183，每段均一致 |
| MUL loop | 2135 / 2135 | 183 | 183 / 183，每段均一致 |
| load hit loop | 2135 / 2135 | 183 | 183 / 183，每段均一致 |
| 四 Warp ADD | 2135 / 2135 | 732 | 732 / 732，每段均一致 |
| 四 Warp load hit | 2155 / 2155 | 732 | 732 / 732，每段均一致 |
| DIV loop | **2561 / 2135** | 183 | schedule→decode 183/183；decode→dispatch 0/183；dispatch→commit 122/183 |

前五项共 **2013 条指令 / 6039 个局部阶段区间**完全一致，热窗口周期误差为 0；这是有明确覆盖范围的局部精度结果，不是全模型精度。DIV 窗口慢约 **19.95%**，回放后保持不变。

局部 dispatch→commit：ADDI/BNE **2 / 2**，MUL **5 / 5**，单 Warp 热 LW **15 / 15**。四 Warp 热 LW 会变为 15、16、17、19、20 周期，两侧逐项一致，说明本组竞争不是靠一个固定 LW 延迟碰巧拟合。
例如 load_4warp 样例见 [Timing dispatch](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/results/load_4warp/timing/stderr.log:2462)、[Timing commit](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/results/load_4warp/timing/stderr.log:2477)、[RTL dispatch](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/results/load_4warp/rtl/stdout.log:24274)、[RTL commit](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/results/load_4warp/rtl/stdout.log:24481)。

### 8.5 确认的非 DRAM 差异一：除法编译路径不同

`div_loop` 第 2 次 launch、第 3 次 `PC=0x8000000c DIVU`：

- Timing dispatch=229、commit=264，**35 周期**：[dispatch 行 2403](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/results/div_loop/timing/stderr.log:2403)、[commit 行 2431](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/results/div_loop/timing/stderr.log:2431)。
- RTL dispatch=147、commit=152，**5 周期**，raw 5729→5739：[dispatch 行 4758](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/results/div_loop/rtl/stdout.log:4758)、[commit 行 4768](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/results/div_loop/rtl/stdout.log:4768)。
- 差异还通过资源/依赖等待传到后续 decode→dispatch，不能把每条 DIV 的 30 周期差简单相加。

原因链已由代码与二进制确认：

1. [VX_define.vh:108](../../Vortex_rtl/hw/rtl/VX_define.vh:108)：`!SYNTHESIS && SV_DPI` 自动定义 `IDIV_DPI`。
2. [VX_alu_muldiv.sv:223](../../Vortex_rtl/hw/rtl/core/VX_alu_muldiv.sv:223)：该分支使用 `LATENCY_IMUL=3` 的 shift pipeline，不是 serial divider。所运行的 `librtlsim.so` 导出有 muldiv 路径的 `dpi_idiv` Verilator wrapper。
3. [Timing NewDivider](../../timing/model/execute.go:146) 读取 `tm-idiv-result=33`、`capacity=1`；[IR](../../timing/ir.yaml:5547) 明确条件为 `!IDIV_DPI`。两侧再加对应 merge/commit 边界，就成为 35 对 5。

因此这是**比较对象的执行 profile 没有闭合**，不是已证明硬件 serial divider 的 33 周期算错，也不是除数数据触发 RTL early exit。IR 已分别记录 serial/DPI 条件，但实验基线没有把有效编译条件当成必须一致的门禁。

下一轮须先选择目标：若验证可综合硬件路径，就建立专用非 IDIV_DPI RTL 参考并检查预处理结果；若验证当前默认 RTLSIM，就为模型显式选择对应 DPI 时序 profile。不能悄悄把正式 Divider 的 33 改成 3，也不应全局打开 SYNTHESIS 来掩盖问题——它可能同时改变其他路径。

### 8.6 确认的非 DRAM 差异二：启动边界和 PERF 首尾计数

回放后的 empty：第 1 次 launch 首条 decode 是 Timing 95 / RTL 45（相对双方首 schedule），仍差 **50**；第 2 次 launch 的 **schedule/decode/dispatch/commit 全部为 0/43/50/54，两侧完全相同**。证据见 [empty 全部匹配](/hpc2hdd/home/zekaiwang/vortex-work/Simulator_timing/.cache/timing-boundary-micro-20260917/replay/results/empty/aligned.csv)。这进一步确认首个 launch 包含不同 reset scan 等待，而非 TMC 流水本身错误。

但回放 empty 的 PERF 累计值为：Timing **112、172**；RTL **63、124**。第 2 次增量仍是 **60 对 61**，即使指令流水事件已对齐，busy-qualified 计数仍少 1 周期。该差异在这些非 DIV 程序的回放结果中同样出现。

目前可定位为 **首 schedule 之外的启动/dispatcher/busy 尾部计数边界**，尚不能仅凭现有日志区分是哪一个寄存边沿导致。对应应继续增加 `cta_dispatcher_busy`、`busy_buf`、active mask、pending empty 和 PERF increment 的逐边观测，首个分歧处对照 [RTL scheduler](../../Vortex_rtl/hw/rtl/core/VX_scheduler.sv:580) 与 [模型 accounting](../../timing/model/accounting.go)。不要先在总计数上补 1。

### 8.7 剩余工作按证据分级，不把未验证当已知缺陷

| 部分 | 本轮结论 | 后续必要验证 |
| --- | --- | --- |
| 常规整数/乘法、热 I-cache、分支、同址热 D-cache、所测四 Warp 竞争 | 本组局部周期一致 | 扩展不同依赖距离、端口地址映射、packed uop，不能由本组外推全部情况 |
| 除法 | 确认 serial vs DPI profile 不同 | 统一参考 profile 后重测，不直接改经验延迟 |
| 首次启动 | 确认 reset/初始化开始时刻不同 | reset-ready 与 launch-ready 分离，测完整生命周期时仍报告初始化成本 |
| PERF 首尾 | 确认统一流水事件后有每 launch 1 周期残差 | 比较真实 busy/dispatch/pending 寄存边沿 |
| Cache flush/host 可见性路径 | RTL 有 addr=0 dummy-read；模型为 Visibility 抽象，不是相同微事件序列 | 独立测 flush accept→done、写回/drain；不混入执行 ROI 的精度 |
| MSHR 满、dirty replacement、多 miss 返回重排、LMEM bank、BAR/WSYNC/FENCE、CTA tail | 前轮功能 PASS；本轮未完成统一后端下的逐边证明 | 用各自最小压力程序与握手轨迹定位，当前不能无证据宣称这些组件仍有 bug 或已经精确 |

这里最优先的不是继续跑更多大 benchmark，而是把**执行 profile、Cache 初态、接口边界和计数事件**写入实验 manifest，并增加自动门禁。随后在统一 memory 驱动下从单请求、双请求、队列满到混合背压逐级展开；一旦出现请求集合/身份不匹配，停止计算精度，保留首个分歧，不能静默回退为固定平均延迟。

两组 Slurm 实验均已监控至 COMPLETED / 0，未修改生产模型、RTL、IR，未 commit。独立回放库及原始 100-cycle 结果分别留存，避免把诊断后端误当作正常模型。

## 9. 扩展压力 microbenchmark：发现真实差异，而非用总周期猜测（2026-09-17）

### 9.1 实验覆盖与可信边界

新增实验根目录：[timing-stress-micro-20260917](../../.cache/timing-stress-micro-20260917)。仍只修改诊断程序、观测 overlay 和本报告；没有修改生产模型、RTL、Timing IR，也没有 commit。

| 组别 | 双后端配置数 | 双侧通过 | 用途 |
| --- | ---: | ---: | --- |
| 基线压力与 CTA ABI 修订 | 54 | 49 | 1–4 Warp、部分 mask、连续 CTA、ALU/MUL/FPU/CSR/JAL、BAR/WSYNC/FENCE、不同 lane 地址映射、store/load、连续 miss |
| 显式 STD 浮点参考 | 8 | 8 | 排除 FPU DPI/STD 编译差异 |
| 外部读服务时间回放 | 22 | 22 | 区分外部服务时间与内部请求/返回路径 |
| LMEM 内部逐边观测 | 3 | 3 | 请求接受、bank 服务、各 port 返回；检查观测没有改变结果 |
| CTA 几何、CSR、生命周期 | 12 | 10 | 第 10 节逐项核查用户指定的 12 个角度 |

合计 **99 组双后端配置对照，92 组双侧通过**；这是含控制组的运行数，不是 99 个独立 benchmark。每个正常程序执行两次 launch。5 个失败是最初空程序违反 reentry ABI，另 2 个是双方共同出现的坐标边界行为，不能混称模型差异。详见 [audit.json](../../.cache/timing-stress-micro-20260917/audit.json)；各 manifest 的输入/库 SHA-256 全部复核一致。

测试能力也有边界：memory 程序检查返回值 42/43，CTA 程序逐线程检查坐标与上下文；纯算术程序是完成/指令轨迹 oracle，不是新增完备算术值验证。PC/mask 和 issue uop 对齐不能证明 response fragment 分组一致——本轮恰好发现了这一盲点。

所有计算作业均已结束，最终状态见 [Slurm 清单](../../.cache/timing-stress-micro-20260917/slurm-final.txt)。最终 CTA 作业是 `12771882`。实验工程失误也保留记录：`12771875` 在观测库尚未成功构建时启动，Timing 未加载成功，结果保留在 `shape-ref`，不纳入上述 99 组；补齐仓库 `env/env.sh` 构建环境后，在独立 `shape-audit-ref` 重跑。不能用作业 COMPLETED 代替逐 case 的 PASS。

### 9.2 新确认：CTA 复用被软件 quiescence 条件推迟

用四条 NOP + TMC0 组成合法 20-byte dispatch window，执行 8 个 CTA；第 2 次 launch、统一外部读服务时间后：

| 每 CTA Warp 数 | first decode → last commit：Timing / RTL |
| --- | --- |
| 1（1 lane 或 4 lanes） | 118 / 119 |
| 2 | 228 / 220 |
| 3 | 347 / 334 |
| 4 | **456 / 435** |

四 Warp 的证据最干净：首 CTA 的 schedule/commit 完全相同，随后 **7 次复用每次增加 3 周期，累计 21 周期（ROI +4.83%）**。没有 D-cache 数据访问；首条 cache line 填充后不再靠外部访存解释差异。

以下使用第 2 次 launch 首 schedule 为零点；commit 指的是 RTL trace 的 commit 输入握手，对应模型 `n-commit enter`，不是 writeback：

| Warp0 事件 | Timing | RTL |
| --- | ---: | ---: |
| CTA0 的 TMC commit | 90 | 90 |
| CTA1 首 schedule | 94 | 91 |
| CTA2 首 schedule | 152 | 146 |
| CTA3 首 schedule | 210 | 201 |
| CTA7 首 schedule | 442 | 421 |

完整逐指令证据：[aligned.csv](../../.cache/timing-stress-micro-20260917/replay-ref/results/cta_w4_l16_g8_abi/aligned.csv)。模型日志以 device cycle 968 为该 launch 的首 schedule：TMC control feedback=88、commit=90、writeback=91、pending release=92、新 CTA schedule=94，见 [Timing 行 533](../../.cache/timing-stress-micro-20260917/replay-ref/results/cta_w4_l16_g8_abi/timing/stderr.log:533)；RTL 下一 CTA 首 schedule 见 [RTL 行 4205](../../.cache/timing-stress-micro-20260917/replay-ref/results/cta_w4_l16_g8_abi/rtl/stdout.log:4205)。

代码上的差别不是“少建一个固定 CTA 延迟”：

- RTL [VX_cta_dispatch.sv](../../Vortex_rtl/hw/rtl/core/VX_cta_dispatch.sv:147) 用 `~(active_warps | dispatched_warps)` 选 physical wid；TMC 控制信号使 active 清零，随后寄存 `warp_fire_r`。它没有把整个旧 Warp 的 commit/pending/software receipt 全部排空作为该选择器条件。
- 模型 [kernel_dispatch.go](../../timing/runner/kernel_dispatch.go:49) 用 `runner.WarpQuiescent` 筛选；[model/cta.go](../../timing/model/cta.go:7) 还要求 commit feedback、pending ledger、所有 token 资源均清空；runner 又叠加 effect/transport 尾部。

这些保护有防止迟到事件污染新 residency 的功能价值，但被直接串进硬件分派 ready 后，就改变了时序。应把 **硬件 wid 可再次分派** 与 **旧 generation 资源仍需保留** 分开建模；后者以 identity/generation 路由，不应自动等价为禁止下一 CTA 进入。不能简单删掉保护，或把 dispatch 统一减 3 周期。

最初 5 个空程序仅有一条 TMC，复用会执行 `0x80000004 - 20 = 0x7ffffff0`，两侧都失败。已保留原失败，并改用合法 20-byte 窗口另跑。这是 test ABI 错误，不是上述模型问题的证据。

### 9.3 新确认：背压下 response fragment 分组与 RTL 不同

这里先纠正定位层次：**“LMEM 同 bank 测试出现误差”不等于“LMEM bank 延迟错了”**。沿返回路径观察，实际证据是 fragment 的接受/汇聚时机不同，进而改变 LSU/commit 竞争。

#### A. LMEM 首个宏指令完成分歧

`local_hit_stride0`，第 2 次 launch，Warp3，PC=`0x80000044`，occurrence=1，模型 token=94：

| 事件 | Timing | RTL |
| --- | --- | --- |
| dispatch | 295 | 295 |
| LMEM 向上游交付各 lane | cycle308：port0；313：port1/2/3 | cycle312：port0/1/2/3 一起 |
| LSU load-result / LSU Rsp 接受 | 313 mask=`0001`；314 mask=`1110` | 313 mask=`1111` |
| commit 接受 | 319 mask=`0001`；320 mask=`1110` | 319 mask=`1111` |

模型证据：[LMEM 行 4846](../../.cache/timing-stress-micro-20260917/local-ref/results/local_hit_stride0/timing/stderr.log:4846)、[LSU 行 4858](../../.cache/timing-stress-micro-20260917/local-ref/results/local_hit_stride0/timing/stderr.log:4858)、[commit 行 4864](../../.cache/timing-stress-micro-20260917/local-ref/results/local_hit_stride0/timing/stderr.log:4864)。RTL 对应：[四 port 返回行 25159 附近](../../.cache/timing-stress-micro-20260917/local-ref/results/local_hit_stride0/rtl/stdout.log:25159)、[合并响应行 25178](../../.cache/timing-stress-micro-20260917/local-ref/results/local_hit_stride0/rtl/stdout.log:25178)、[commit 行 25210](../../.cache/timing-stress-micro-20260917/local-ref/results/local_hit_stride0/rtl/stdout.log:25210)。双方最后数据相同，但模型多提交一拍 fragment。

模型 [memory_system.go:receive](../../timing/runner/memory_system.go:113) 只要 `r.response` 空，就先从存储系统取走并保存一个响应，设置 `dataPop`；是否被 LSU 真正接受，要到稍后的 Core Evaluate 才确定。这使响应可提前被软件 holding slot 固定成较小 mask，释放上游 ready，和 RTL 的组合/寄存 ready 传播不同。以上日志已证实接口行为差异；要把全部残差归结到某一行，仍应补该 holding slot 与 split 输出的容量/ready 对照或隔离 A/B，不能声称已经证明 bank 算法本身错误。

之后的 token124 是另一种表象：双方 bank 接受=386/387/388/389、bank 服务=387/388/389/390、port 返回=389/390/391/392，均一致；LSU 也在390–393收到相同 mask 序列。然而模型 commit=394–397，RTL=393–396。继续回溯发现模型另一条 BNE 已被此前差异推迟，正好在393抢占 commit；这不是该笔 LMEM 请求自身多一个 SRAM 周期。证据：[详细 Timing](../../.cache/timing-stress-micro-20260917/local-ref/results/local_hit_stride0/timing/stderr.log:5004)、[RTL](../../.cache/timing-stress-micro-20260917/local-ref/results/local_hit_stride0/rtl/stdout.log:25897)。

#### B. 多 line global load 出现同类分组症状

`global_hit_stride64`，第 2 次 launch，Warp1，PC=`0x80000044`，occurrence=1，token90，双方 dispatch 都是329：

- Timing LSU 接受：354/355/356，mask=`0101 / 1000 / 0010`；commit：360/361/366。
- RTL LSU 接受：354/355，mask=`0101 / 1010`；commit：360/361。

证据：[Timing 行 2374](../../.cache/timing-stress-micro-20260917/replay-ref/results/global_hit_stride64/timing/stderr.log:2374)、[RTL 行 32617](../../.cache/timing-stress-micro-20260917/replay-ref/results/global_hit_stride64/rtl/stdout.log:32617)。最后 mask 的分裂多消耗提交机会，结束时间晚 5 周期；这不是把 Cache miss latency 减 5 就能修复的事情。

目前将 LMEM/global 两者归为 **返回握手、分组与背压的已复现差异家族**，不包装成两个已独立证明的 bank/MSHR bug。Global adapter/coalescer 也可能贡献 ready 相位，尚需内部接口观测才能完全排除。

#### C. 为什么只看总周期/最终 mask 会漏检

热窗口取第 2 次 launch 的 Warp0 循环头 occurrence3→31，28 个循环间隔；下表各窗口双方均没有外部请求区间相交，且回放后结论仍成立：

| 程序 | 窗口周期 Timing / RTL | 560 条指令中 decode→dispatch 相同 | dispatch→commit 相同 |
| --- | --- | ---: | ---: |
| LMEM stride0 / stride16（同 bank） | **1568 / 1568** | 434 | 462 |
| LMEM stride4（不同 bank） | 1484 / 1484 | 560 | 560 |
| global stride0 / stride4 | 1568 / 1568 | 560 | 560 |
| global stride64 / stride1024 | **1511 / 1498** | 410 | 493 |

所有行的 schedule→decode 都是560/560。LMEM 总周期误差为零，却有126个 decode→dispatch、98个 dispatch→commit 局部区间不一致。Global 多 line 热窗口误差为 **+0.868%**。这些是实际 kernel 内部问题，不是仅发生在 kernel 前，也不是单纯统计起点不同。

详细统计：[replay measurements](../../.cache/timing-stress-micro-20260917/replay-ref/measurements.json)、[local measurements](../../.cache/timing-stress-micro-20260917/local-ref/measurements.json)。三组详细 LMEM 观测与未加内部日志的回放组逐指令事件完全一致（剔除日志行号），见 `audit.json.observation_control`。

### 9.4 本轮排除项与未下结论项

- **浮点 profile**：旧 RTL 参考构建定义了 `SV_DPI`，默认选择 FPU DPI；模型明确选 STD。显式加 `VX_CFG_FPU_TYPE_STD` 重建独立参考库后，FADD/FMUL/FDIV/FSQRT × 1/4 Warp 的8个热窗口全部对齐。FADD/FMUL 为1708/1848周期，FDIV/FSQRT为980/1108周期，各自三段局部区间也全部相同。第8节提及的 profile 风险应扩展为 **整数除法及浮点**，不能只核对配置 TOML。构建参数见 [std-build.sbatch](../../.cache/timing-stress-micro-20260917/build-std.sbatch)，结果见 [std measurements](../../.cache/timing-stress-micro-20260917/std-ref/measurements.json)。
- **BAR/WSYNC**：所测热循环完全对齐。BAR1/4 Warp 为1232/1316，WSYNC为1204/1204；各自局部三段区间也相同。不能据冷路径总差声称 barrier 延迟有误。
- **store→load 四 Warp**：基线部分 commit 区间不同，回放后全部相同；不能据基线判定存储顺序模型错。
- **miss burst**：回放后热窗口总周期相同，但部分 load commit 区间仍不同；尚未证明 MSHR 分配/释放本身有误。
- **FENCE、store+FENCE、streaming miss**：窗口仍含外部请求/flush/可见性事件，本轮没有完成严格接口等价归因，不报告“去 DRAM 后精度”。

## 10. 按 RTL 核查 KMU / CTA / TID / 首次 Fetch

### 10.1 新增测试与测量方式

[shape.S](../../.cache/timing-stress-micro-20260917/shape.S) 从第一条指令开始读取 thread X/Y/Z，随后读取 block X/Y/Z、rank、lane、physical wid、CTA slot、mscratch、entry、LMEM base；每线程写独立64-byte记录，host逐项校验。特意不先跑长 runtime prologue，以尽可能早地读取 CTA context。

包含单线程、1/2/3/4 Warp、部分末 Warp、block=`3×2×2 / 1×3×3 / 2×2×3 / 4×4×1`、多维 grid、多 CTA、LMEM=0/8192/16384，以及同进程两次 launch。数据和 trace 汇总：[dispatch-audit.json](../../.cache/timing-stress-micro-20260917/shape-audit-ref/dispatch-audit.json)。Kernel 事件仅在原来的 `TakeEvents()` 处打印，不额外消费事件、不改变执行。

这些带写回的 shape 程序使用原始100-cycle后端，而非回放，故 **不使用其全程周期差证明 wid 选择优先级错误**。initial admission/fire 的早期边界、跨 launch slot 状态、CSR值可以独立核查；纯复用时序归因使用第9.2节无数据访存的回放实验。

### 10.2 用户指定的 12 项逐项结论

| 核查角度 | RTL 实际依据与本轮证据 | 模型结论 |
| --- | --- | --- |
| KMU start → CTA fire | 首次 raw113=start、115=accept、119=fire，即3个RTL周期 | 模型 Kernel 从generated/admitted同cycle开始，没有独立硬件KMU start/valid传播阶段；不能把两个API起点当同一事件 |
| CTA admission timing | accept→首fire=2周期；单Warp多CTA在资源未满时accept间隔3周期 | 模型admit0→select1→fire2，下一CTA admit3；此局部节拍一致，但满资源后的ready条件不同 |
| one-warp-per-cycle dispatch | 4 Warp首次fire在raw119/121/123/125 | 模型fire2/3/4/5；无背压吞吐一致，不意味着复用停顿一致 |
| physical wid 分配 | RTL最低可用wid，由active和dispatched mask决定 | 首次0/1/2/3一致；模型增加quiescent条件，复用可用时刻不同。shape实测后续wid序列不同，但不全部归因于priority算法 |
| CTA slot 分配 | tail轮转、受usable slots与cluster窗口约束；tail只在reset清零 | 同一launch初始slot轮转匹配；**跨launch tail模型被重置，RTL保留，已实测不同** |
| thread XYZ生成 | 按warp base和lane ripple、有限位宽及wrap规则产生 | 十组正常形状host逐线程检查通过；16×1×1边界双方共同偏离普通笛卡尔坐标，见10.4 |
| TID pipeline | `TID_STEP=2`，4 lanes有2级展开；最终valid才写warp RAM | 模型直接计算并绑定，无逐级TID有效性状态；本组首CSR读取远晚于2级写入，未观察到该简化造成read timing差异，不能宣称内部pipeline已等价 |
| wid → CTA table | dispatcher选择时更新retirement map；context/warp RAM分别按slot/wid寻址，有寄存读 | 模型BindWarp/identity视图实现映射，非逐级RAM。CSR输出和本组归属正确；时序有效窗口不能仅由最终值PASS证明 |
| StartupPC / reentry PC | 首用StartupPC，复用当前warp PC回退20 bytes | 合法dispatch window双方相同；单TMC的非法测试双方跳至0x7ffffff0。不是任意程序末尾都可直接复用 |
| parameter → mscratch | scheduler在cta_fire写mscratch[wid] | 模型DispatchWarp安装参数；本组读出值与双方launch参数一致，无错误wid串值证据。安装时点受下述fire/visibility差异影响 |
| first scheduler visibility | RTL cta_fire→scheduler dispatch为1周期 | **模型warp-dispatched与b-schedule enter同cycle，早一拍可见**；并非只有日志总计数的零点不同 |
| first fetch timing | RTL scheduler dispatch→fetch req为1周期 | 模型b-schedule enter→b-fetch-request enter为1周期，一致；但fire→fetch整体为模型1 / RTL2。Cache返回还需另分冷/热与外部服务时间 |

TID/表结构代码依据：[RTL CTA context和TID pipeline](../../Vortex_rtl/hw/rtl/core/VX_cta_dispatch.sv:500)、[模型BindWarp所在分派流程](../../timing/runner/kernel_dispatch.go:103)、[模型坐标生成](../../emu/core/cta.go:438)。它们是“可见效果已验证但内部实现仍抽象”的项目，不等于全部发现bug。

### 10.3 两个独立于 DRAM 的启动/生命周期差别

**A. fire 到首次 scheduler 可见性少一拍。**

`shape_g111_b441_l0`首launch：RTL fire raw119、scheduler raw121、fetch raw123；模型 fire=2、schedule=2、fetch=3。分别以fire为0，就是RTL `0→1→2`，模型 `0→0→1`。RTL见 [日志行183](../../.cache/timing-stress-micro-20260917/shape-audit-ref/results/shape_g111_b441_l0/rtl/stdout.log:183)；模型见 [首个TIMING事件](../../.cache/timing-stress-micro-20260917/shape-audit-ref/results/shape_g111_b441_l0/timing/stderr.log:2) 和 `dispatch-audit.json` 中 KernelEvent。

源代码吻合：[Kernel.Run](../../timing/runner/kernel.go:252) 先执行residency，`DispatchWarp`直接CommitEdge修改scheduler state，随后才运行当前Core step；因此当前step已看到新active。RTL则在cta_fire边沿更新active寄存器，下一拍scheduler才可选到新Warp。这是明确的相位建模差异。它可能参与第8节PERF首尾残差，但本轮仍不把“每launch少1周期”完整归因于此，busy尾部需独立观测。

**B. CTA slot tail跨launch生命周期不同。**

同一`shape_g111_b441_l0`，第一launch双方slot=0；第二launch RTL接受slot=1，模型又从slot=0开始。RTL第二launch证据：[raw1935，日志6488](../../.cache/timing-stress-micro-20260917/shape-audit-ref/results/shape_g111_b441_l0/rtl/stdout.log:6488)；模型第二launch admitted Cycle1245/Slot0见 [日志1041](../../.cache/timing-stress-micro-20260917/shape-audit-ref/results/shape_g111_b441_l0/timing/stderr.log:1041)。host实际读到的CTA_ID也不同，16个线程均复现，不只是trace标注。

RTL `tail_r`仅reset清零；新ctx只清warp init mask，不清tail。模型每launch新建Kernel，`tail`自然为零。这个问题涉及硬件状态应该归device还是launch所有，不能通过把CTA_ID当逻辑grid index来掩盖。当前LMEM=0反例不证明最终程序输出会坏；若要支持严格slot/LMEM放置等价，后续应增加非零LMEM的跨launch轮转回归。

### 10.4 RTL自身坐标边界：忠实复制不等于抽象语义正确

冻结配置4 Warp×4 lanes，`CTA_TID_WIDTH=4`。block=`16×1×1`虽可传入5-bit block_dim，但RTL ripple及warp base取`block_dim[3:0]`，X维16变为0。最小程序读取Warp0的thread Z得到lane0..3=`0,1,2,3`，而通常笛卡尔语义期待全0；第一个host失败是thread1/Z=1。

证据：[RTL第一组CSR Z commit](../../.cache/timing-stress-micro-20260917/shape-audit-ref/results/shape_g111_b1611_l0/rtl/stdout.log:492)、[RTL host错误](../../.cache/timing-stress-micro-20260917/shape-audit-ref/results/shape_g111_b1611_l0/rtl/stderr.log:1)、[模型相同错误](../../.cache/timing-stress-micro-20260917/shape-audit-ref/results/shape_g111_b1611_l0/timing/stderr.log:529)。单CTA和8CTA两组都复现。模型`advanceLaneCoordinate`也显式`dimensions[0]&15`，所以相对冻结RTL是一致的。

因此保留两种评价：**RTL一致性方面不是模型缺陷；高层坐标语义方面是配置/RTL边界风险**。本轮不自行改RTL，也不把host oracle改成该异常值以制造PASS；另测总线程数相同的`4×4×1`作为合法坐标对照，两侧通过。

### 10.5 下一次构建/验收需要的门禁

1. 从有效预处理配置生成执行profile清单，尤其IDIV_DPI、FPU_DPI/STD、各buffer OUT_BUF；不仅比较TOML。
2. 对每条接口保存valid/ready/fire、identity、mask、sop/eop；**issue uop一致之外，还必须比较response beat分组和commit beat数**。
3. 用同一输入序列测试response背压0/1/多周期，明确每个holding slot对应哪个RTL寄存器，避免software queue悄悄增加硬件容量。
4. CTA门禁同时覆盖admit/select/fire/首schedule/首fetch，验证满资源后的复用，而不只检验one-warp-per-cycle无阻塞吞吐。
5. 分离physical wid可分派条件与旧generation存储；多launch测试slot tail、context、cache/reset状态的所有权。
6. TID pipeline、context RAM等若保留抽象，写清“为什么读取前已稳定”的时序前提；只有加了逐边valid/write/read观测后才标记内部等价。
7. 失败分开标注模型误差、RTL自身行为、测试ABI错误、profile不一致和实验基础设施错误；不只输出一个总MAPE或PASS。

目前最有证据支撑的修正方向是 **CTA复用ready条件、fire→schedule相位、跨launch slot状态所有权、响应fragment/背压传播**。本轮只定位和记录，未实施生产修复。
