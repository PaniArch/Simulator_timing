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

## 11. 授权后的 RTL 源码驱动修复迭代（进行中）

本节接续前述只读诊断；用户已授权直接修复并持续测试。修复基线为 `33bc1be`。
所有行为依据仓库 `Vortex_rtl`，未借用 SimX/其他模拟器实现，也未修改冻结 RTL。
下面局部对齐不等于全系统精度验收，特别不能用功能 PASS 替代周期证据。

### 11.1 已修复的因果链

| 问题 | RTL 依据 | 实施与验证 |
| --- | --- | --- |
| Runner 在 LSU 未 ready 时提前收取 response，增加不存在的 holding slot | `VX_lsu_slice` / `VX_lsu_scheduler` / `VX_lmem_switch` 的既有返回缓冲与 `valid && ready` | `runnerMemory.receive` 仅呈现组合输出；实际握手时才 pop 和记录功能 receipt；没有改变 Cache/LMEM 容量或添加延迟常数 |
| physical wid 复用等待软件尾部和 pending 全部排空 | `VX_cta_dispatch.priority_enc.data_in = ~(active_warps \| dispatched_warps)` | 增加硬件 `WarpDispatchable`，保留独立 `WarpQuiescent` 生命周期检查；旧 token 保留原 CTA/warp generation，LMEM 路由固定原物理地址，不随 wid 重绑 |
| CTA fire 当边沿立即可被 scheduler 选择 | `VX_scheduler` 在 fire 边沿写 active/PC/mask 寄存器 | fire 成为暂存输入，选择仍读取旧状态，下一边沿才可 schedule |
| NextLaunch 重置 slot tail | `VX_cta_dispatch.tail_r` 仅 reset 清零 | 跨 launch 保留 tail；新的 usable capacity 缩小时按 `base_tail` 回绕 |
| split 返回仲裁错误采用默认 R | `VX_mem_unit.g_lmem_switches` 明确 `.ARBITER("P")`；`VX_lmem_switch.rsp_arb` global 为输入 0 | IR 与 `SIMDSplit` 同步改成 global 固定优先；新增连续冲突及背压单测，另直接运行冻结 RTL 仲裁器微测试 |

新增 `MultiRecord.TokenBindings`：当前 physical wid 绑定与在途 token 原绑定分开，避免 trace 将旧 CTA commit 归到新 CTA。对外观察发生在内部生命周期清理之后；观察者修改 detached 数据不能改变执行。原 JSON trace fingerprint 因新字段和真实相位修复而更新，observer 开关一致性测试仍保留，不豁免。

### 11.2 已完成的 22 组局部修复对照

记录目录：[repair3-ref](../../.cache/timing-stress-micro-20260917/repair3-ref/measurements.json)，Slurm `12772150`。
每组都重新执行 RTL 和模型，两个后端 host PASS，PC/mask 对齐。该版本包含 response 与 CTA 修复，尚不包含随后发现的 split P 修复。
外部 read 服务时间按 cache/address/occurrence 重放，只作诊断；下面 17 组热窗口已经核实无外部请求重叠。

| 第二次 launch 的热窗口 | Timing / RTL cycles | 三段局部时序 |
| --- | --- | --- |
| global hit stride 0 / 4 | 1568 / 1568 | 每组 560 条全部对齐 |
| global hit stride 64 / 1024 | 1498 / 1498 | 每组 560 条全部对齐 |
| local hit stride 0 / 16 | 1568 / 1568 | 每组 560 条全部对齐 |
| local hit stride 4 | 1484 / 1484 | 560 条全部对齐 |
| miss burst stride 0 / 4 | 1736 / 1736 | 每组 672 条全部对齐 |
| miss burst stride 64 / 1024 | 1792 / 1792 | 每组 672 条全部对齐 |
| BAR 1 / 4 Warp | 1232 / 1232；1316 / 1316 | 84 / 336 条全部对齐 |
| WSYNC 1 / 4 Warp | 1204 / 1204 | 84 / 336 条全部对齐 |
| store→load 1 / 4 Warp | 1484 / 1484 | 140 / 560 条全部对齐 |

三段分别是 schedule→decode、decode→dispatch、dispatch→最后 commit；不只比较总数。
CTA 8 次复用的第二次 launch，以首 decode→末 commit 测量：1 lane/1 Warp 为 119/119、4 lanes/1 Warp 为 119/119、2 Warp 为 220/220、3 Warp 为 334/334、4 Warp 为 **435/435**（原 **456/435**）。

冷启动仍有独立的前置状态差异：例如 `cta_w4_l16_g8_abi` 的累计 PERF 是首次 536/487，第二次累计 1021/972，第二次增量都为 485。首 schedule→decode 多出的 49 周期集中在首次 I-cache 请求开始服务前；不能将此差值归到 DRAM，也不能在每个 kernel 减去固定 49。
当前模型在 NewKernel 才创建 Cache，RTL 在 KMU start 前已经进行 reset 后初始化；需要分离 reset→launch 的环境时钟和 kernel 内部延迟，或采用双方 Cache-ready 后统一发起 launch 的受控边界，不能跳过 Cache reset scan。

### 11.3 扩大回归与未关闭边界

全部 31 个小规模 benchmark 的新鲜 STD-FPU RTL / 固定 100-cycle 模型测试位于 [repair-full-ref](../../.cache/timing-stress-micro-20260917/repair-full-ref/manifest.json)，作业 `12772182`；随后将用独立 `repair-full-replay-ref` 保存服务时间受控的诊断，不覆盖固定延迟结果。
RTL 当前仍启用 IDIV_DPI，不能把 DIV 差值认作串行除法器模型错误；须与使用同一 RTL 硬件分支的参考另比。
混合子路径请求的 exactly-once 保护与 RTL 未设置 sent 标记的问题仍是 `u-mixed-split`，本次返回 P 修复不代表请求侧已等价。
TID 内部 pipeline、首启动环境时钟、Cache/外部边界排序也需要继续定向核查。

### 11.4 后续发现与修复：Fetch 握手、slot 释放、实验边界

Fetch 存在与 LSU 同类的额外 holding slot：`VX_fetch.sv:207` 直接将 icache `rsp_ready` 接到 `fetch_if.ready`，旧 runner 在 decode 不 ready 时也先 pop Cache。现改为仅实际 fetch 握手时移除 identity；`TestRTLFetchBackpressureRetainsCacheResponse` 用 4 Warp 连续 WAW 串行 DIV 制造 frontend 背压，确认确实覆盖 blocked 状态且 Cache 所有权未提前丢失。

CTA slot 的 `slot_valid` 生命周期也已分开：`VX_cta_dispatch.sv:371–376` 在最后一个 delayed warp_done 清位，不等待最后的 commit/pending receipt。原模型只修 wid 选择仍会让单 slot 每次复用晚 1 周期。现在物理 slot 在退休表边沿释放，旧 token/LMEM 地址继续保留，最终 Kernel 完成另检查 runner 尾部；没有删除内存身份检查，也没有全局 drain 才允许 admission。
`TestRTLCTASlotReleaseDoesNotWaitForPendingReceipt` 验证 8 次单 slot 复用：TMC 后第 3 边沿释放、第 4 边沿重新 admission，结束时 token binding 账本为空。

Slurm `12772306`、[repair-slot-ref](../../.cache/timing-stress-micro-20260917/repair-slot-ref/manifest.json) 的 12 个形状测试：10 个有效用例双 PASS；2 个 `16×1×1` 仍双失败，保留原 RTL 坐标缺陷，不改 oracle。9 个有效用例只剩首次启动的累计 +49；例如：

| 用例 | 修 slot 前 Timing/RTL 累计 PERF（两次 launch） | 修后 |
| --- | --- | --- |
| `shape_g221_b311_l16384`，仅 1 slot | 1787/1735；3523/3468 | 1784/1735；3517/3468 |
| `shape_g811_b411_l8192`，2 slots | 1815/1764；3572/3519 | 1813/1764；3568/3519 |
| `shape_g811_b411_l0`，4 slots | 1171/1103；2266/2197 | 1152/1103；2246/2197 |

这些组第二次 launch 增量完全相同。`shape_g221_b133_l0` 尚有独立差异，不计作闭合。

扩大回归方面，`12772182` 全部 31 项固定100后端与 STD-FPU RTL 双 PASS。后续每批重新保存二进制 hash、结果与逐指令记录，不覆盖失败批次。

重放工具的两项干扰已由实际记录确认，不能归到 L1 建模：

- `repair-full-replay-ref/sgemm2/evidence.json`：同一 read 指定服务 48 cycles，但生产 FIFO 将返回拖到 94；后续实验独立逐端口返回以免叠加全局 head-of-line blocking。
- `repair-independent-ref/dotproduct/memory.json`：16 个 stack store miss 后，参数 read 在 208 已 offer，软件 external backend 到 216 才接受；这正是预设 `max_inflight=16` 的外部资源限制，不是 coalescer/Cache 凭空多 8 周期。诊断版本把外部容量及接受带宽解除，正式模型的固定100、16在途、1接受/周期保持不变。
- 变延迟重放中 write 仍固定100会使更短 read 超过写入并读到旧 backing。新的诊断边界明确采用一边沿 write/visibility 服务；它不是生产后端，也不能作为真实 DRAM 精度结论。

上述实验失败（包括 unmatched replay address/occurrence）保留，不允许回退默认延迟冒充匹配。当前必须逐批核对实际 read 返回时间、地址/次数、功能状态、PC/mask 以及局部区间；重放不满足这些条件的行不得纳入准确率。

另构建了 STD FPU + 原有串行 DIV RTL 参考（`12772261`）。`serial-profile.sv` 只在解析既有 header 后 `undef IDIV_DPI`，选中原 `VX_alu_muldiv.serial_div`，不改 datapath；生成的 Verilator 层次已确认包含 serial_div。首次全量运行有10项因节点 GCC运行库版本失败，保留在 `repair-serial-ref`，不算模型失败；统一携带 GCC12 runtime 后重跑到 `repair-serial2-ref`（`12772408`）。

### 11.5 I/O 属性漏传：真实访存路径问题，而非延迟参数

`io_addr` 暴露了独立缺陷：RTL 对 I/O 地址连续发出非缓存请求，模型只做首次 refill，后续误走 cache hit。
依据冻结 `VX_lsu_slice.sv:83–86` 的属性生成和 `VX_types.toml` 的 memmap，I/O 区间是
`[0x40, 0x10000)`。该属性此前没有从 runner 传到 `LaneRequest.NonCacheable`。
现把地址边界纳入 IR，按地址设置 NonCacheable，继续走现有 coalescer/NC bypass；不把 I/O
错误地标为 NoMerge（`VX_mem_unit` 的 no_merge 使用 AMO 属性）。没有改变 Cache 结构或容量。

`TestRTLIOApertureReachesNoncachedPath` 检查区间两端及 LMEM 地址，并验证两次四-lane I/O load
都产生真实 NC 请求。`repair-io-ref`（`12772486`）的 `io_addr` 首 decode→末 commit 从
1266/1398 改为 **1398/1398**。368 条指令的 decode→dispatch、dispatch→commit 全部相同；
schedule→decode 仅首次冷启动的4条 Warp fetch 各保留 +49，其余364条相同。

完整单元回归随后暴露旧夹具问题：若干 dirty-cache、self-modifying code、flush、refill-fault
测试把应缓存的数据放在 `0x100`、`0x800` 等 I/O 地址。现搬到 `0x10000` 以上并同步编码、
backing 容量和结果断言；不删除 dirty 可见性/flush/fault 断言，不放宽周期预算。
共享夹具的 trace 指纹相应更新，分块执行/诊断开关等独立等价性检查仍保留。

### 11.6 当前修复版的全量小规模结果与准确率边界

两批均重新运行全部31个受支持小规模 benchmark 的模型及 RTL，不复用旧 PASS：

| 批次 | Slurm | 结果 | 外部边界 |
| --- | --- | --- | --- |
| [repair-io-fixed-ref](../../.cache/timing-stress-micro-20260917/repair-io-fixed-ref/manifest.json) | 12772552 | 31/31 双 PASS | 正式模型固定100 cycles，16在途，1接受/周期；RTL原外部服务 |
| [repair-serial-boundary-ref](../../.cache/timing-stress-micro-20260917/repair-serial-boundary-ref/repair-audit.json) | 12772488 | 31/31 双 PASS | STD FPU/串行 DIV 同配置；逐缓存、地址、出现次数重放读服务时间 |

第二批有 **67次 launch、206278条对齐指令**，PC/mask 检查全通过，uop 对齐问题为0。
以每次 launch 的首 decode→最后 commit 为窗口，**64/67 个窗口完全相等**，
28/31 benchmark 的所有窗口都相等。非零项如下：

| benchmark / launch | Timing | RTL | 差值 |
| --- | ---: | ---: | ---: |
| dogfood / 20 | 1186 | 1188 | -2 |
| raycast / 1 | 171372 | 171373 | -1 |
| sgemmx / 1 | 12298 | 12294 | +4 |

例如 occupancy 为647981/647981、sgemm2为10091/10091、softmax为95168/95168。
窗口净误差最大为0.1684%，但这**不是全模拟器“99.83%周期精度”的证明**：

- 总周期抵消会掩盖局部差异。dogfood/sgemmx 仍有局部 dispatch/commit 区间相差十余周期，
  不能只看净差1–4周期就宣告所有组件等价。
- 诊断后端不是原 DRAM：独立端口返回、解除软件外部容量限制、write/visibility一边沿服务；
  正式生产后端仍为固定100。该结果仅用于定位内部时序，不能替代真实DRAM验证。
- multikernel 有2次、raycast有1次 read 实际交付晚于计划完成边沿，需要连同接收侧背压解释；
  其它用例未出现这种交付延后。不能把指定 service latency 当成实际响应时刻。
- 冷启动初始化先后关系仍不同。首 decode 窗口明确排除了前置边界，并非在每个 Kernel
  总周期中减49；实际累计 PERF 仍应原样保留。

### 11.7 剩余差异追到哪里：先区分外部接受与内部执行

`repair-serial-boundary-ref/sgemmx` 中首个局部 commit 差异是 Warp1、token174、
PC `0x800000b4` 的 store：归一化 dispatch 为692/643，commit 为765/717。
前者保持冷启动偏移49，后者变为48，即该 store 的局部路径短1周期。
模型 `timing/stderr.log:845`、RTL `rtl/stdout.log:7259` 可定位原事件。

进一步向前查，差异已经出现在外部请求接受，而不是从这条 store 才开始：

| D-cache 外部 read 地址（该次出现） | 模型 accept（首 schedule归一化） | RTL accept（同边界） | 偏移 |
| --- | ---: | ---: | ---: |
| `0xfffdbfc0` | 639 | 590 | 49 |
| `0xfffdffc0` | 640 | 591 | 49 |
| `0xfffd9fc0` | 641 | 593 | 48 |
| `0xfffddfc0` | 642 | 594 | 48 |

RTL原始时刻1303接受 `0xfffdffc0`，同周期还有 I-cache miss 请求；1305没有该 D-cache
read 接受，1307才接受 `0xfffd9fc0`。模型诊断边界连续接受，没有对应空拍。
证据：RTL日志6363–6397行，模型 BACKEND 日志720、723行及 `memory.json`。
目前日志证明了**外部接受边界先分歧**，尚不能仅凭握手日志判定该空拍具体由下游 ready
还是内部 output valid 引起，需要 valid/ready 独立波形；不据此给 Cache 添加经验气泡。
同地址的后续D-cache core store 接受（例如 `0xfffebfc8`）也变为756/708，
随后才传播到 LSU/store commit 和 Warp 竞争。

因此下一阶段要用 L1 外侧的逐端口 valid/ready/response 联合轨迹，或在双方接入同一个
明确固定延迟协议边界；仅逐read重放 latency 不足以消除所有外部因素。
这轮没有通过调大固定延迟、给 store 加常数、全局 drain 或修改 RTL 来对齐总数。
混合 local/global 请求的 `u-mixed-split`、内部 TID/Barrier RAM 抽象与首启动环境时钟
仍明确未关闭；以上小规模通过不能推导为任意参数、任意程序的RTL逐周期等价。

### 11.8 修复版仓库回归

- `go test -mod=vendor ./...`：全部通过，记录 `final-go-tests-v3.log`。
- `scripts/verify-timing.sh`：IR一致性通过，记录 `final-verify-timing-v2.log`。
- `scripts/verify-all.sh`：冻结RTL manifest、功能回归、空缓存/禁网络/vendor-only离线构建、测试和vet通过，记录 `final-verify-all-v2.log`。
- 新增 per-token binding map 的 observer 篡改隔离测试；诊断状态比较和恢复回归改为检查完整 backing 容量，避免夹具搬址后只检查低4KiB。增强后的 diagnostic/state-cost 测试通过，记录 `diagnostic-owner-final.log`。
- `git diff --check` 通过；没有修改冻结 `Vortex_rtl`。本轮没有参考其它模拟器的时序语义，也没有修改正式 external backend 的固定100周期配置。

以上日志位于 `.cache/timing-stress-micro-20260917/`；批次目录各自保留 manifest、二进制hash、host结果、逐指令对齐与memory证据。

## 12. 后续修复：启动与完成边界，保持 DRAM 不变

本节是第11节之后的版本，不覆盖前面的实验记录。不修改 external backend 的服务延迟、带宽、容量或返回策略，不采用延迟重放；正式模型仍为固定100 cycles。

### 12.1 已实现的修改

1. **设备生命周期与 Kernel 分离。** `runner.PowerOn` 创建初始存储层级和持续时钟，`Initialize(budget)` 逐边运行真实 Cache init 扫描及其流水尾部，`Start` 移交同一层级/时钟给首个 Kernel。冻结配置完全 settle 实测66边，来自64项扫描和两级 bank pipeline，不是拟合出来的启动延迟。允许尚未完成扫描就 Start，剩余初始化仍真实阻塞访问；零budget、分块budget、非法launch不消耗所有权、禁止重复移交都有测试。
2. **runtime 在创建设备时初始化，不在首次 Kernel 中重建 Cache。** 增加 `initialization_cycles` 独立统计。Kernel 的 `startCycle` 从真实移交时钟取值，busy-qualified PERF 不减常数。`NewKernel` 仍保留“从冷设备直接启动”的显式API。
3. **KMU start 的寄存阶段。** 根据 `VX_kmu.sv:204–216,299`，start边只更新running，下一边才可向CTA握手。测试验证 start E0→accept E1→select E2→fire E3→schedule E4，4 Warp逐周期fire；不是增加固定memory service延迟。
4. **TID/context 可见性。** 按 `VX_cta_dispatch.sv:558–642`，冻结4-lane的两级TID流水后才写warp RAM。保留已有有限位宽坐标算法，但不再在wid选择时就向执行上下文暴露最终值；fire E3→RAM写E5→写边后可见。尚不宣称建立了通用BRAM模型。
5. **硬件结束与软件排空分别观测。** `HardwareComplete/HardwareEndCycle/HardwareCycles` 根据KMU耗尽、scheduler busy、LSU scheduler empty、coalescer empty判断；`Complete`仍要求软件receipt、存储尾部、退休路径安全完成。`TestHardwareEndDoesNotWaitForStoreRefill` 用真实store miss验证硬件先结束、尾部继续存活。runtime新增 `hardware_execution_cycles/hardware_complete`；例如vecadd硬件区间979边、软件安全区间1040边，不能混用。

这里仍有明确的**环境边界限制**：runtime现在选择reset-settled后开始host launch，并未逐拍模拟原生CP在reset之后的所有DCR/host操作。旧RTLSim原生启动可能与Cache初始化重叠，因此“启动结构已支持真实进度”不等于“原生CP的首次启动环境已逐边重现”。没有通过强制减49去宣称两者完全对齐。显式早启动API使这个差别可输入、可测试，而不再与Kernel构造时间混在一起。

### 12.2 mixed 背压：冻结RTL组件反例与防止误报等价

Slurm `12773795`，直接实例化冻结 `VX_lmem_switch`，参数与 `VX_mem_unit` 一致：GLOBAL_OUT_BUF=1、LOCAL_OUT_BUF=1、RSP_OUT_BUF=1、ARBITER=P。
先用一个local请求占满local buffer并保持下游不ready，再持续呈现合法稳定的mixed父请求，global下游保持ready。
原RTL记录显示：父请求ready始终0，同一个tag的global子请求握手3次。见
`.cache/timing-stress-micro-20260917/mixed-rtl-12773795.log` 和 `mixed_rtl_tb.sv`。
前两次构建因缺少生成头文件路径/RV32宏而失败，日志12773720、12773749保留，不算仿真结论。

这证实了**组件级**重复握手，不等于已经证明所有上游约束下的完整Kernel必然可触发。
不能通过给模型添加sent位就称作RTL修复，也不能修改冻结RTL让它迁就模型。
现有软件exactly-once行为仍保留，但新增sticky `RTLTimingIssue`：第一次mixed两侧缓冲接受能力不对称即标记
`mixed-local-global-asymmetric-acceptance`，经System、KernelStatus一直传到runtime JSONL。
对应执行即使功能PASS也不得纳入RTL时序等价验收；对称mixed正常接受不误标。
这关闭的是“静默把软件保护当RTL等价”的缺口，**没有声称已经仿真RTL重复请求及其最终错误行为**。

### 12.3 验证记录

- 启动/完成修复版31项小规模benchmark重新双端执行，Slurm `12773753`，全部31/31双PASS；记录 `startup-fixed-ref`，外部memory仍是原固定100，无重放。该批次在新增mixed诊断标记之前构建；标记不改变请求或周期行为，另外由组件/集成回归覆盖。
- 5组CTA ABI微测试（8 CTA，1/2/3/4 Warp、1/4 lanes）两次launch全部双PASS，PC/mask全部匹配。首decode→末commit为119、119、220、334、435，两个launch均与RTL完全相同。窗口内RTL没有新的外部read请求；没有利用DRAM重放对齐。
- 上述微测试的native CP会在launch之间flush，故第二次launch的首次fetch并非天然热命中。这里只把**首次decode之后的同line执行窗口**称为热窗口，不能把整个第二次launch称为零DRAM测试。首fetch与末尾flush仍保留各自实际费用。
- 微测试记录 `startup-micro-ref`，作业12773840和12773852。首次3个子任务早于manifest准备完成，未执行仿真；准备完成后只补跑这3项，原错误日志保留。
- 完整Go回归 `startup-full-tests-v2.log` 通过；启动/硬件完成/mixed标记及runtime定向回归 `startup-final-focus.log` 通过；IR检查 `startup-verify-ir.log` 通过。新增测试 `timing/runner/power_test.go`。
- 最终代码（包含mixed诊断标记及runtime配置清理）再次运行 `go test -mod=vendor ./...` 全部通过，记录 `startup-full-final.log`，runner耗时191.652秒；`scripts/verify-timing.sh` 与 `go vet -mod=vendor ./...` 通过，记录 `startup-ir-final.log`、`startup-vet-final.log`。最后的IR说明文字修正另经 `startup-ir-latest.log` 验证通过。
- 本轮 `scripts/verify-all.sh` 通过冻结RTL manifest、功能检查和空缓存/禁网络离线构建、测试、vet，记录 `startup-verify-all.log`；最终 `git diff --check` 通过。冻结 `Vortex_rtl` 与 `timing/memsys/backend.go` 无改动。

仍未关闭：完整native CP/DCR启动环境的逐拍输入、通用context/Barrier RAM等价证明、mixed反例的完整Core可达性及重复响应语义。DRAM差异按本次要求不处理，不据本轮固定100对原RTL总周期宣称≤5%。

## 13. 后续授权：直接复用 RTLSim DramSim 并跑通

用户随后明确要求直接接入已有 `dram_sim`。本节是新的可选后端，不改写第12节固定100
实验结论；原 `fixed` 后端仍保留且默认选择，不要求安装Ramulator。

### 13.1 实施边界

- `integration/dramsim/Makefile` 直接编译工作区 `vortex/sim/common/dram_sim.cpp`，链接
  `vortex/third_party/ramulator/libramulator.so`；没有复制或改动原DramSim、Ramulator源码。
  新增的C++代码只负责C ABI、回调ID及生命周期，Go动态加载桥接库。
- 新增 `ExternalBackend/BackendFactory` 与 `AsyncBackend`，将原有具体后端依赖改为接口。
  原Cache、LMEM、coalescer、replacement、MSHR、Core流水线和冻结RTL不改。
- `SIMTIMING_MEMORY_BACKEND=rtlsim-dram` 选择该路径，`SIMTIMING_DRAM_LIBRARY` 为绝对库路径。
  缺失/非法选择明确失败，不静默降级；functional模式不加载DRAM。
  每次launch汇总记录 `memory_backend`，跨launch复用同一个DramSim实例。
- 冻结平台2通道、64字节、默认MEM_CLOCK_RATIO=1；HBM2/FRFCFS/刷新、地址转换和子请求
  拆分全部来自原DramSim。桥接库编译时验证冻结平台bank数量与总线宽度。
- 新路径没有叠加100周期。保持明确的桥接容量/带宽（16在途、1接受/周期、1返回/周期），
  不是声称复刻了RTLSim外围per-bank队列或RTL socket。preview无副作用，背压中的响应保持
  stable；不同client可按实际完成情况选择返回，同client仍保留接受顺序。
- 数据在外部接受时读快照/按byte enable写入，沿用RTLSim外部memory harness约定；
  不改变Cache writeback策略，不在load completion时重新绕过Cache读取backing。
- 特别注意：原DramSim的write callback是Ramulator接受写请求的应答，不是物理DRAM排空；
  原64→16拆分只给首子请求挂回调的行为也原样保留。Visibility等待较早桥接应答，
  不能把它或device Close解释为“所有物理DRAM命令执行结束”。

使用方法见 [DramSim接入说明](../../integration/dramsim/README.md)。

### 13.2 实际测试与周期数

记录根目录 `.cache/dramsim-integration/`。初次作业12774741全部通过；随后增加
“已阻塞响应不得被另一端口新完成请求抢走”的保持逻辑及回归，用最终库重新执行
作业 **12774824**，结果位于 `final/`。两批结果分别保留，不覆盖初始证据。
manifest记录DramSim源码、Ramulator和模拟器库hash、配置、输入参数和job ID。

真实库测试：8个读写请求全部恰好返回一次，完成边包括5、13、19、20、21、24、26、29，
随后同一实例上的第9个读请求通过。benchmark端也产生实际Ramulator读写计数，
不是换了后端名字而继续使用固定100。

最终7项全部双端host PASS，指令总数一致；模型的每个launch JSONL均确认 `rtlsim-dram`。
以下为原样累计PERF，不减启动常数，不重放DRAM延迟：

| benchmark | Timing + 原DramSim | RTLSim | 相对差 |
| --- | ---: | ---: | ---: |
| demo | 857 | 873 | -1.83% |
| fence | 1449 | 1482 | -2.23% |
| io_addr | 1328 | 1450 | -8.41% |
| multikernel（3次launch累计） | 12054 | 12439 | -3.10% |
| sgemm2 | 10082 | 10143 | -0.60% |
| vecadd | 649 | 672 | -3.42% |
| wsync | 7148 | 7236 | -1.22% |

这7项等权MAPE约 **2.97%**，6/7在5%以内。仅是小规模接入冒烟样本，**不是全部支持
benchmark的最终精度验收**。`io_addr`尚有明显偏差，尚未做新版本逐事件归因，不把差值
直接归为Core、DRAM或启动中的任何一方。复用DramSim并不自动统一外围请求队列、返回
注册级与启动环境；本轮证明“接通并正确运行”，不声称完整边界等价。

### 13.3 回归

- 完整 `go test ./...` 通过，`full-tests.log`，runner 193.031秒（响应保持强化前）。
- 响应保持强化后，memsys/runtime定向全包回归通过，`final-focused.log`；另外新增
  后端选择失败关闭、functional不依赖DRAM测试，`runtime-final-tests.log`通过。
- `go vet`覆盖改动的memsys、runner、dramsim、runtime包，`vet.log`通过；IR检查`ir.log`通过。
- 最终原生库在计算节点复跑7项和真实DramSim单测通过，Slurm12774824退出0。
- 固定后端 `timing/memsys/backend.go`、Cache实现及冻结RTL未修改；`git diff --check`通过。

## 14. DramSim 扩大测试集与规模：61组双端回归

### 14.1 范围、产物与验收口径

本轮只扩大测试并新增统计，没有修改模拟器实现或调参改善误差。
从第13节7个benchmark扩到当前全部31个受支持benchmark；每项重跑原小规模，另外对30项
支持调参的benchmark扩大输入，合计 **61组 / 122次仿真**。`packld`没有规模参数，
不重复同一输入冒充扩大测试。扩大组是中等规模覆盖，不代表每个benchmark的默认/最大规模。

- Slurm数组 **12775578**，CPU `long_cpu`，最多6组并发，每组2核/4GiB。
  61个任务全部COMPLETED、退出0；每后端1200秒异常保护，**实际无超时、无取消**。
- 原始记录目录：`.cache/dram-expanded/20260917T104616Z-gz87hsum/`。
  [完整JSON统计](../../.cache/dram-expanded/20260917T104616Z-gz87hsum/summary.json)、
  [完整CSV统计](../../.cache/dram-expanded/20260917T104616Z-gz87hsum/summary.csv)、
  [输入与库manifest](../../.cache/dram-expanded/20260917T104616Z-gz87hsum/manifest.json)。
  `results/<index>/<rtlsim|simtiming>/`分别保存stdout.log.gz、stderr、Ramulator统计、模型事件；
  每个case另有status/result JSON。日志压缩只节省存储，不过滤RTL trace。
- 两端使用相同benchmark/kernel二进制和参数，库与输入快照共74项文件有SHA256。
  模型使用第13节已验证的最终库；RTL仍是相同冻结配置、STD FPU/串行DIV参考。
  模型和RTL均加载快照中的同一个Ramulator库。bridge仍为16在途、1接受/周期、1返回/周期。
- 周期取host原样输出的**最终累计PERF**，不把多个累计快照相加，不扣启动常数，
  不使用read latency replay。误差为 `(Timing / RTL - 1) × 100%`。
  软件执行、安全排空和flush周期在CSV中单列，不混入PERF。
- 每组要求双端host自检通过、退出0、模型launch/finish/flush审计链完整且backing可见、
  后端确认为`rtlsim-dram`，然后核对最终指令总数。准确率统计还要求无`rtl_timing_issue`。
  本轮61组全部满足，没有隐藏排除失败或高误差样本。指令总数一致不等于逐指令trace一致；
  本轮没有重新做全部模型/RTL逐指令对齐。

新增可复用工具 `scripts/vortex-dram-suite.py`，支持prepare/run/aggregate；
`integration/dramsim/run-suite.sbatch`提交数组。汇总器测试3项、既有审计器测试6项均通过。

### 14.2 汇总结果

| 统计 | 原小规模 | 扩大规模 | 合计 |
| --- | ---: | ---: | ---: |
| 双端通过 / 计划 | 31/31 | 30/30 | **61/61** |
| 可纳入周期统计 | 31 | 30 | 61 |
| 等权平均绝对误差（MAPE） | 1.627% | 1.154% | **1.394%** |
| 绝对误差中位数 | 1.167% | 0.413% | 0.732% |
| 绝对误差P95（nearest rank） | 3.689% | 3.110% | 3.423% |
| 最大绝对误差 | 8.414% | 14.037% | **14.037%** |
| 绝对误差≤5% | 30/31 | 29/30 | **59/61（96.72%）** |
| 绝对误差≤10% | 31/31 | 29/30 | 60/61 |
| 按RTL周期加权的绝对误差 | 0.267% | 0.207% | 0.219% |
| 按RTL周期加权的有符号差 | -0.266% | -0.113% | -0.145% |

加权绝对误差为 `Σ|Timing−RTL| / ΣRTL`，不会发生正负抵消，但仍会被长程序主导；
不能用它替代等权MAPE或最大误差。合计61组是31种程序的两档输入，不是61种独立benchmark。
相对于第13节7项MAPE下降来自样本集合变化，不是本轮又修复了模型。

模型共执行 **159次launch、1,384,009条PERF指令**，两端每case指令总数全部一致，
未出现mixed时序限制标记。首任务开始至末任务结束约 **374秒**，计入并发和调度间隔。
两端子进程运行耗时累计：Timing **1162.38秒**，RTL **308.04秒**；这不是公平速度评测，
因为RTL库持续输出详细trace并压缩，模型只记录runtime事件，而且受到节点和并发影响。
原始执行与flush时间、每case wall time均保留在CSV/JSON。

### 14.3 全部参数与周期结果

表内均为 `Timing / RTL（相对差）`。每一行的两个规模都重新执行，没有复用上轮PASS。

| benchmark | 原小规模参数 | 原小规模周期 | 扩大参数 | 扩大周期 |
| --- | --- | ---: | --- | ---: |
| async_barrier | `-n8 -t4` | 18840 / 18866 (-0.14%) | `-n32 -t4` | 730246 / 729822 (+0.06%) |
| conv3 | `-n4 -l` | 1394 / 1432 (-2.65%) | `-n8 -l` | 5039 / 5060 (-0.42%) |
| demo | `-n4 -x4 -y4` | 857 / 873 (-1.83%) | `-n32 -x4 -y4` | 3664 / 3691 (-0.73%) |
| diverge | `-n1 -d4` | 5730 / 5725 (+0.09%) | `-n4 -d4` | 59051 / 59295 (-0.41%) |
| dogfood | `-n4 -s0 -e21 -c` | 160885 / 161569 (-0.42%) | `-n16 -s0 -e21 -c` | 593200 / 593925 (-0.12%) |
| dotproduct | `-n32` | 3288 / 3320 (-0.96%) | `-n1024` | 98675 / 98707 (-0.03%) |
| dotproduct2 | `-n32` | 2491 / 2526 (-1.39%) | `-n1024` | 72673 / 72677 (-0.01%) |
| dropout | `-n32` | 1292 / 1321 (-2.20%) | `-n1024` | 37197 / 37306 (-0.29%) |
| fence | `-n4` | 1449 / 1482 (-2.23%) | `-n32` | 4665 / 4554 (+2.44%) |
| io_addr | `-n4` | 1328 / 1450 (-8.41%) | `-n32` | 11081 / 9717 (+14.04%) |
| jacobi | `-n4` | 15036 / 15211 (-1.15%) | `-n16` | 42141 / 42400 (-0.61%) |
| madmax | `-n2` | 70904 / 70929 (-0.04%) | `-n4` | 91226 / 91247 (-0.02%) |
| mstress | `-n4` | 2421 / 2492 (-2.85%) | `-n32` | 16275 / 16533 (-1.56%) |
| multikernel | `-n32` | 12054 / 12439 (-3.10%) | `-n256` | 46738 / 47303 (-1.19%) |
| occupancy | `-c3` | 648012 / 648033 (-0.003%) | `-c8` | 1295923 / 1295944 (-0.002%) |
| packld | 默认 | 3087 / 3088 (-0.03%) | — | 无规模参数 |
| pathfinder | `-n8` | 4810 / 4958 (-2.99%) | `-n32` | 39409 / 40674 (-3.11%) |
| raycast | `-n1 -w4 -h4 -s1 -d1` | 170770 / 171425 (-0.38%) | `-n2 -w8 -h8 -s1 -d1` | 790693 / 792845 (-0.27%) |
| relu | `-n32` | 779 / 802 (-2.87%) | `-n1024` | 21983 / 22192 (-0.94%) |
| sgemm | `-n8` | 3477 / 3514 (-1.05%) | `-n32` | 115172 / 115119 (+0.05%) |
| sgemm2 | `-n8 -t4 -c4` | 10082 / 10143 (-0.60%) | `-n32 -t4 -c8` | 381837 / 382156 (-0.08%) |
| sgemmx | `-n16` | 12299 / 12346 (-0.38%) | `-n32` | 80122 / 80248 (-0.16%) |
| sgemv | `-m8 -n8` | 1046 / 1073 (-2.52%) | `-m32 -n32` | 5054 / 5106 (-1.02%) |
| softmax | `-n4` | 94960 / 95220 (-0.27%) | `-n8` | 168205 / 168404 (-0.12%) |
| sort | `-n2` | 7781 / 7804 (-0.29%) | `-n4` | 29624 / 29653 (-0.10%) |
| stencil3d | `-n4` | 9087 / 9092 (-0.05%) | `-n8` | 68914 / 68538 (+0.55%) |
| vecadd | `-n32` | 649 / 672 (-3.42%) | `-n1024` | 17451 / 17660 (-1.18%) |
| wgather | `-n4 -t4` | 705 / 732 (-3.69%) | `-n8 -t4` | 1223 / 1247 (-1.92%) |
| basic | `-n32` | 1948 / 1971 (-1.17%) | `-n1024` | 54958 / 55167 (-0.38%) |
| wsync | `-i16` | 7148 / 7236 (-1.22%) | `-i128` | 51483 / 51892 (-0.79%) |
| bfs | `-n32` | 14519 / 14821 (-2.04%) | `-n256` | 21477 / 21922 (-2.03%) |

### 14.4 新暴露的精度异常与结论边界

唯一超过5%的benchmark是 **io_addr**，两个规模分别为 -8.41% 和 +14.04%。
小规模绝对周期差 **-122**，扩大后变为 **+1364**，且两端分别执行相同的368/2832条指令。
这个变化不能由单一固定启动偏移解释；但本轮未做新的逐事件定位，不能仅凭总数判定
是NC路径、桥接排队、返回端口或某个Core阶段的错误。后续应优先对该case做NC请求接受、
DramSim完成、Cache返回和LSU commit的联合trace，而不是给模型补常数。

两边Ramulator统计另有如下事实（`results/9`和`results/40`下各自的`ramulator.stats.log`）：

| io_addr | Timing read / write请求 | RTL read / write请求 | Timing / RTL memory_system_cycles |
| --- | ---: | ---: | ---: |
| `-n4` | 444 / 16 | 452 / 16 | 1607 / 1763 |
| `-n32` | 5260 / 128 | 5268 / 128 | 11360 / 10136 |

这是整个DRAM实例的统计，包含Kernel之外的阶段，而且计数单位是原DramSim拆分后的请求，
不能直接当作ISA load/store数或Kernel PERF；固定的8个read差异本身也不能定位+1364周期。
它再次说明“同一Ramulator库”不意味着两边输入请求序列和外部运行边界已经完全相同。

结论：扩大测试支持当前模型在这套冻结配置和输入集合下达到较好的周期近似，
**59/61组误差在5%内，但不能承诺每种访问模式均在5%内**。本轮没有证明任意更大规模、
不同DRAM配置或此前未支持路径的精度，也没有以功能PASS替代完整时序等价证明。

## 15. io_addr 异常定位与修复（2026-09-17）

### 15.1 证据范围与测量边界

诊断目录：`.cache/io-dram-diagnosis/`。原始四组联合 trace 在
`results/n{4,8,16,32}/{rtl,timing}/stdout.log`，同级保留 `aligned.csv`、
`analysis.json`、`memory.json`、`cta-gaps.json`。观察版只增加 trace，未改变生产参数；
`n4/n32` 的 PERF 与第14节完全一致。

| 参数 | Timing PERF | RTL PERF | 指令数（两端一致） |
| --- | ---: | ---: | ---: |
| `-n4` | 1328 | 1450 | 368 |
| `-n8` | 2500 | 2674 | 720 |
| `-n16` | 4844 | 5122 | 1424 |
| `-n32` | 11081 | 9717 | 2832 |

按逻辑 warp 对齐的 PC/mask 序列全部一致。以各端 first-schedule 为0，
first-decode 为 Timing 32、RTL 45；n4 last-commit 为1321/1443，n32为11074/9710。
两端 PERF 均比 last-commit 多7周期。因此本次 -122/+1364 周期不是 commit tail
统计口径造成的；固定的启动差13周期也无法解释规模增大后误差反向。

### 15.2 外部返回排序域错误：Cache client 不等于物理 memory bank

原 `timing/memsys/external.go` 在 `PreviewResponses` 中按 `seen[e.client]`
限制返回，错误地把每个 Cache 客户端视为独立 FIFO。
实际依据是：

- `Vortex_rtl/hw/rtl/libs/VX_mem_bank_adapter.sv:91`：interleave 模式以 line address
  低位选择物理 bus bank；冻结配置2 banks、64B，即 `(byte_address / 64) % 2`。
- Vortex 源码 `sim/rtlsim/processor.cpp:339` 起：按物理 bank 遍历
  `pending_mem_reqs_[b]`，只取队首 ready 请求；I/D 客户端共享该 bank 的排序域。

不是 L1 bank，也不是 Ramulator 内部 DRAM bank。它产生两种相反偏差：
同一 Cache client 的跨 bank 请求不该互相阻塞；不同 client 的同 bank 请求却不能随意越过。

**RTL 的实际跨 bank 超越证据**（`results/n32/rtl/stdout.log`，时间为原始 RTL
timestamp，2 timestamp units = 1 core cycle，不直接与归一化 cycle 混用）：

| 事件 | timestamp | 日志行 | D-cache port[1] tag / 地址 |
| --- | ---: | ---: | --- |
| request | 811 | 2574 | `0x56 / 0x1080`，bus bank0 |
| request | 815 | 2602 | `0x6c / 0x1080`，bus bank0 |
| request | 819 | 2635 | `0x7e / 0x1080`，bus bank0 |
| request | 921 | 2889 | `0x2 / 0x40`，bus bank1 |
| response | 983 | 2966 | `0x2`，先于前三个更早请求返回 |
| response | 997 / 1029 / 1061 | 2990 / 3052 / 3144 | `0x56 / 0x6c / 0x7e` |

Timing n32 的对应机制证据：client2、token245、地址`0x40`在归一化1078接受，
1100已收到 DramSim completion，却到1137才交付，多等37周期。
同 client 更早的 `0x1180` 请求直到1104、1120、1136完成；它们属 bank0，
不应挡住 bank1 的返回。接受/交付日志分别在 Timing stdout 第1221/1290行。

n4 的相反证据：I-cache line `0x80000040`在模型166接受、203完成并返回，
而同一 bus bank1 的更早 D-cache parameter 请求仍在210～266陆续返回。
RTL I-cache 同一 line 的请求/返回为原始479→687（104周期，日志1466/1645行），
模型是37周期。模型允许跨 I/D client 绕过同 bank 队列，造成低估。

一个 Core 局部例子也把差异定位到访存而非普通流水级：n32 CTA2、token213、
PC`0x8000002c`，schedule→decode 两端均7周期，decode→dispatch 均7周期，
dispatch→commit 却是96/50周期。Timing dispatch/commit 在1036/1159行，
RTL在6873/7447行。不能把各条 load 的差值简单相加，因为等待会重叠。

### 15.3 反事实实验：不是容量不足，也不能直接取消所有顺序

实验保留旧 runtime 地址布局，仅临时修改外部桥接副本；正式修复前结果如下。
完整数据在 `controls.json` 与 `controls-bank.json`。

| 变体 | n4 Timing（RTL1450） | n32 Timing（RTL9717） |
| --- | ---: | ---: |
| 原桥接 | 1328（−8.41%） | 11081（+14.04%） |
| inflight 16→128 | — | 11081 |
| accept 1→3 / return 1→3 / 两者同时 | — | 均11081 |
| 取消全部 FIFO 限制 | 1262（−12.97%） | 9775（+0.60%） |
| 按物理 bus bank FIFO | 1411（−2.69%） | 9819（+1.05%） |

均功能PASS。容量/吞吐实验排除了当前桥接上限作为主要原因；无限制乱序虽然改善n32，
却进一步破坏n4。正确修复是排序域，不是放宽门禁或拟合延迟。

### 15.4 独立 runtime bug：低地址 reserve 导致自动分配低于用户基址

Vortex 源码 `sw/common/mem_alloc.h` 的 `findNextAddress` 从 `baseAddress_` 开始寻找
空隙，却无条件把游标改为当前 page end。显式保留低地址 I/O page 后，游标会倒退。
既有 page 放不下大分配时，普通 buffer 因而可能落入 I/O aperture。

最小复现源文件 `allocator-probe.cpp`，旧结果见 `allocator-probe.log`：
基址`0x10000`，先 allocate64，再 reserve(`0x40`,64)，allocate512得到`0x10040`，
allocate4096却得到`0x1040`。这是分配器错误，不是模拟器错误识别缓存属性。

原 benchmark 两端地址相同：n4/8/16 的 source 为`0x10040`（cached），
n32为`0x1040`（NC）；parameter 分别为`0x1040`/`0x2040`（NC）。
n32 的1280个 NC read由512 parameter、512 source、256 I/O target组成，
所以规模增长也改变了访问类型，不能按纯数据量缩放解释。

host-only 实验保持旧桥接：显式把n32 source保留到`0x20000`，Timing/RTL变为
10218/10098（+1.19%）；把n4 source移到`0x4000`得到1581/1632（−3.13%）。
该操作也可能影响后续参数分配位置，不能将全部周期差归为单个 buffer 的 cacheability。
更不能通过移动地址掩盖15.2的桥接错误。

### 15.5 正式修复与验证边界

用户授权后，正式源码修复：

- `AsyncBackend` 显式接收物理 bus bank 数，按64B交错地址执行每-bank FIFO；
  返回保留原 client/tag/identity，继续保持 valid 在背压时稳定。固定100周期后端不变。
- native DramSim 插件增加 interleave 配置校验，避免误把非交错平台当成交错。
- runtime allocator 游标只前进不倒退，普通自动分配不低于基址；显式低地址 I/O reserve仍合法。
- runtime共享库构建目标增加 allocator header依赖，防止只改头文件却复用旧库。
- 新增同/异 client × 同/异 bank、返回背压与非法 bank 配置测试；allocator新增低地址
  reserve、大/小分配、释放复用及耗尽边界回归。

不改 Cache/MSHR、RTL、Ramulator、DRAM延迟常数或 Kernel；目前仍是有限队列的近似桥接，
并未声称完整复制 RTL socket arbiter/elastic buffer 的每拍行为。

正式构建/回归记录：`build-fix-v2.log`、`fix-go-tests.log`、`fixed-suite/`；
`bank-fix-only/`专门保留旧runtime，独立验证正式桥接修复，避免地址布局掩盖效果。
最终数值以这些目录的已完成结果为准，不将上面的临时实验当作正式全量验收。

回归有效性检查：同一份新增 allocator 测试链接旧 header 时返回255，明确报告
`low reservation moved automatic allocation: 0x1040`（`allocator-before.log`）；
修复版 `vx_malloc` PASS。旧 per-client 返回策略的 Go overlay 在“异 client 同 bank”与
“同 client 异 bank”两项失败（`bank-test-before.log`），正式实现均通过。
修复后最小分配复现 `allocator-after.log`：512B仍为`0x10040`，4096B变为`0x11000`。

Slurm 正式 bridge-only 作业12778585完成，两端功能PASS、指令数一致，正式数值
1411/1450与9819/9717，分别−2.689655%和+1.049707%，复现临时诊断结论。
同时修复两处的作业为12778587（4个worker分担63组，不减少测试集）。
原提交12778028/12778030因45分钟申请超过debug分区30分钟限制而未启动；
仅取消这两组未启动作业后，以20分钟申请重提，未终止任何 benchmark 进程。

`go test ./...`、`verify-timing.sh`、`verify-all.sh`（含空缓存、禁网的 offline 检查）
最终均PASS，日志为 `fix-go-tests.log`、`fix-verify-timing.log`、`fix-verify-all-v2.log`。
首轮 verify-all 被本节文档的外部相对路径拼写触发环境审计；改为“Vortex源码 + 源码内路径”
后重跑通过，未修改或放宽审计脚本。functional模式 `io_addr -n4/-n32` 也均PASS
（作业12778654，`functional-fixed/summary.json`）。

修复后的可运行库快照在 `fixed-suite/lib/`，同时包含新的 `libvortex.so`（公共runtime）
和 `libsimtiminggo.so`（Timing），以及配套 native/backend 库。只替换 Go 库不会修复
allocator；重新运行时必须同时使用新的公共runtime。旧实验目录、旧库快照和原始日志
保持不变，不能把第14节结果当作修复版结果。修复涉及 Simulator_timing 与 Vortex 两个
仓库；本次未执行 git commit。

### 15.6 最终回归结果

作业12778587四个分片全部 `COMPLETED / 0:0`，最慢分片10分50秒。
原61组全部重跑，并增加io_addr n8/n16，共63组Timing/RTL配对、126次模拟器执行；
全部功能PASS、指令数匹配、原生执行审计完整，无记录的RTL timing issue。

| 集合 | 通过 / 总数 | MAPE | 最大绝对误差 | ≤5% |
| --- | ---: | ---: | ---: | ---: |
| 原小规模集 | 31/31 | 1.473769% | 3.688525% | 31/31 |
| 原扩大规模集 | 30/30 | 0.693827% | 3.630145% | 30/30 |
| 含2组新增诊断的全部集 | 63/63 | 1.167612% | 3.688525% | 63/63 |

同时修复桥接与runtime后的io_addr结果（raw累计PERF，不减启动、不拟合）：

| 参数 | Timing | RTL | 误差 | 指令数（两端） |
| --- | ---: | ---: | ---: | ---: |
| `-n4` | 1229 | 1276 | −3.683386% | 368 |
| `-n8` | 2301 | 2386 | −3.562448% | 720 |
| `-n16` | 4445 | 4606 | −3.495441% | 1424 |
| `-n32` | 8734 | 9063 | −3.630145% | 2832 |

n32实际 source 两端均由`0x1040`变为`0x11000`；n4 source仍为`0x10040`。
普通参数分配也不再掉入低地址IO区域，因此正式修复后的RTL基线周期亦发生变化，
不能把最终表直接与第14节相减来声称都是Timing模型改进。
单独桥接修复的因果效果应使用15.5的旧runtime对照。

完整逐案例周期、误差和耗时表：`fixed-suite/summary.csv`、`fixed-suite/summary.json`；
独立桥接对照：`bank-fix-only/summary.json`。加上独立桥接的4次模拟与functional的2次，
本轮正式benchmark共132次执行，全部通过。上述5%结论只覆盖当前冻结配置和此测试集合，
不等于任意工作负载、任意平台配置的误差保证，也不宣称RTL逐周期等价。

基础设施说明：trace作业12776175完成；重复提交12776307因目录已存在保护退出，未覆盖证据。
控制作业12776346的前8组完成，随后因host控制程序尚未成功链接退出；改用匹配C++工具链后，
12776394完成其余bank/layout实验。这两次退出不计为benchmark功能失败。

### 15.7 最终逐 benchmark 周期误差表

数据来源：`.cache/io-dram-diagnosis/fixed-suite/summary.json`，对应桥接返回排序与公共 runtime 分配器均修复后的作业12778587。以下为最终63组结果，不混入第14节旧结果或仅修桥接的控制实验。

误差定义：`(Timing cycles − RTLSim cycles) / RTLSim cycles × 100%`。正值表示模型周期偏高，负值表示偏低；采用原始最终累计 PERF，不扣除启动周期或 DRAM 周期。误差显示到小数点后3位，汇总使用未舍入值。所有行均功能PASS、指令数一致，且绝对误差≤5%。

#### 小规模：31组

| Benchmark | 参数 | Timing 周期 | RTLSim 周期 | 周期差（Timing−RTL） | 误差 |
| --- | --- | ---: | ---: | ---: | ---: |
| async_barrier | `-n8 -t4` | 18840 | 18866 | -26 | -0.138% |
| conv3 | `-n4 -l` | 1394 | 1432 | -38 | -2.654% |
| demo | `-n4 -x4 -y4` | 857 | 873 | -16 | -1.833% |
| diverge | `-n1 -d4` | 5730 | 5725 | +5 | +0.087% |
| dogfood | `-n4 -s0 -e21 -c` | 160879 | 161569 | -690 | -0.427% |
| dotproduct | `-n32` | 3288 | 3320 | -32 | -0.964% |
| dotproduct2 | `-n32` | 2491 | 2526 | -35 | -1.386% |
| dropout | `-n32` | 1292 | 1321 | -29 | -2.195% |
| fence | `-n4` | 1449 | 1482 | -33 | -2.227% |
| io_addr | `-n4` | 1229 | 1276 | -47 | -3.683% |
| jacobi | `-n4` | 15036 | 15211 | -175 | -1.150% |
| madmax | `-n2` | 70904 | 70929 | -25 | -0.035% |
| mstress | `-n4` | 2421 | 2492 | -71 | -2.849% |
| multikernel | `-n32` | 12054 | 12439 | -385 | -3.095% |
| occupancy | `-c3` | 648012 | 648033 | -21 | -0.003% |
| packld | 无 | 3087 | 3088 | -1 | -0.032% |
| pathfinder | `-n8` | 4810 | 4958 | -148 | -2.985% |
| raycast | `-n1 -w4 -h4 -s1 -d1` | 170737 | 171425 | -688 | -0.401% |
| relu | `-n32` | 779 | 802 | -23 | -2.868% |
| sgemm | `-n8` | 3477 | 3514 | -37 | -1.053% |
| sgemm2 | `-n8 -t4 -c4` | 10082 | 10143 | -61 | -0.601% |
| sgemmx | `-n16` | 12303 | 12346 | -43 | -0.348% |
| sgemv | `-m8 -n8` | 1046 | 1073 | -27 | -2.516% |
| softmax | `-n4` | 94960 | 95220 | -260 | -0.273% |
| sort | `-n2` | 7781 | 7804 | -23 | -0.295% |
| stencil3d | `-n4` | 9087 | 9092 | -5 | -0.055% |
| vecadd | `-n32` | 649 | 672 | -23 | -3.423% |
| wgather | `-n4 -t4` | 705 | 732 | -27 | -3.689% |
| basic | `-n32` | 1948 | 1971 | -23 | -1.167% |
| wsync | `-i16` | 7148 | 7236 | -88 | -1.216% |
| bfs | `-n32` | 14519 | 14821 | -302 | -2.038% |

#### 扩大规模：30组

| Benchmark | 参数 | Timing 周期 | RTLSim 周期 | 周期差（Timing−RTL） | 误差 |
| --- | --- | ---: | ---: | ---: | ---: |
| async_barrier | `-n32 -t4` | 730246 | 729822 | +424 | +0.058% |
| conv3 | `-n8 -l` | 5039 | 5060 | -21 | -0.415% |
| demo | `-n32 -x4 -y4` | 3664 | 3691 | -27 | -0.732% |
| diverge | `-n4 -d4` | 59051 | 59295 | -244 | -0.412% |
| dogfood | `-n16 -s0 -e21 -c` | 593200 | 593925 | -725 | -0.122% |
| dotproduct | `-n1024` | 98675 | 98707 | -32 | -0.032% |
| dotproduct2 | `-n1024` | 72673 | 72677 | -4 | -0.006% |
| dropout | `-n1024` | 37197 | 37306 | -109 | -0.292% |
| fence | `-n32` | 4507 | 4554 | -47 | -1.032% |
| io_addr | `-n32` | 8734 | 9063 | -329 | -3.630% |
| jacobi | `-n16` | 42481 | 42400 | +81 | +0.191% |
| madmax | `-n4` | 91226 | 91247 | -21 | -0.023% |
| mstress | `-n32` | 16521 | 16533 | -12 | -0.073% |
| multikernel | `-n256` | 46738 | 47303 | -565 | -1.194% |
| occupancy | `-c8` | 1295923 | 1295944 | -21 | -0.002% |
| pathfinder | `-n32` | 39409 | 40674 | -1265 | -3.110% |
| raycast | `-n2 -w8 -h8 -s1 -d1` | 790890 | 792845 | -1955 | -0.247% |
| relu | `-n1024` | 21983 | 22192 | -209 | -0.942% |
| sgemm | `-n32` | 115172 | 115119 | +53 | +0.046% |
| sgemm2 | `-n32 -t4 -c8` | 381837 | 382156 | -319 | -0.083% |
| sgemmx | `-n32` | 80139 | 80248 | -109 | -0.136% |
| sgemv | `-m32 -n32` | 5054 | 5106 | -52 | -1.018% |
| softmax | `-n8` | 168205 | 168404 | -199 | -0.118% |
| sort | `-n4` | 29624 | 29653 | -29 | -0.098% |
| stencil3d | `-n8` | 68914 | 68538 | +376 | +0.549% |
| vecadd | `-n1024` | 17451 | 17660 | -209 | -1.183% |
| wgather | `-n8 -t4` | 1223 | 1247 | -24 | -1.925% |
| basic | `-n1024` | 54958 | 55167 | -209 | -0.379% |
| wsync | `-i128` | 51483 | 51892 | -409 | -0.788% |
| bfs | `-n256` | 21488 | 21922 | -434 | -1.980% |

#### 新增 io_addr 诊断：2组

| Benchmark | 参数 | Timing 周期 | RTLSim 周期 | 周期差（Timing−RTL） | 误差 |
| --- | --- | ---: | ---: | ---: | ---: |
| io_addr | `-n8` | 2301 | 2386 | -85 | -3.562% |
| io_addr | `-n16` | 4445 | 4606 | -161 | -3.495% |

`packld` 没有扩大规模参数，因此仅在小规模表出现；新增诊断两行不重复计入前61组。总体63/63组在5%内，MAPE为1.167612%，最大绝对误差为3.688525%（小规模 `wgather -n4 -t4`）。

## 16. 三方简易测速与周期误差（2026-09-17）

### 16.1 测试口径

仅选3个benchmark：简单访存 `vecadd -n1024`、计算/访存混合 `sgemm2 -n16 -t4 -c8`、
I/O/NC路径 `io_addr -n32`。每项每后端运行3次，共27次进程执行；全部功能PASS，
各项三方退休指令数一致，3次重复的各自PERF周期数完全一致。未扩展为全量测速。

- 构建作业12778973，3分05秒，COMPLETED/0:0；测试作业12778977，1分05秒，COMPLETED/0:0。
- 测试都在 `gpu3-9` 同一作业内串行执行，分配2个CPU，记录CPU affinity `[28,29]`，Go `GOMAXPROCS=2`。
- 每轮轮换后端顺序：RTL→SimX→Timing、SimX→Timing→RTL、Timing→RTL→SimX。
- 新建独立release构建：C++ `-O2 -DNDEBUG`；SimX与RTLSim关闭详细trace/VCD，保留PERF计数。
  RTL使用冻结源码、STD FPU和现有serial DIV分支，与此前周期验证profile一致。
- 当前Vortex与冻结快照的配置TOML、types TOML逐字一致；重新configure生成头文件，未手工篡改配置。
- 三方使用相同host程序/kernel镜像、同一个修复后公共runtime、同一DramSim源码实现和Ramulator库；
  各自的Cache/互连及请求次序保留原模型行为，没有回放或强制匹配DRAM响应。
- Timing复用第15节修复后的正式库，无逐拍trace，仅保留原生执行审计；27份stdout均无TRACE/DEBUG行，
  输出大小84～9918字节，不启用在线日志压缩。
- wall time为 `perf_counter` 测得的进程启动至退出，使用阻塞wait，不以轮询周期量化耗时；
  不包含编译、Slurm排队、结果解析，包含进程初始化、runtime、仿真和普通输出。三次均计入，中位数汇总。
- 周期误差为最终累计原始PERF相对RTLSim的差值百分比；不减启动、DRAM或commit tail，
  不声称各后端内部统计边界逐事件完全相同。本轮没有新增trace定位或修改模拟器。

### 16.2 运行速度（秒，中位数）

| Benchmark / 参数 | RTLSim | SimX | Timing | SimX相对RTL加速比 | Timing/RTL耗时 | Timing/SimX耗时 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| vecadd `-n1024` | 0.317862 | 0.054057 | 4.194949 | 5.88× | 13.20× | 77.60× |
| sgemm2 `-n16 -t4 -c8` | 0.822244 | 0.167850 | 11.969464 | 4.90× | 14.56× | 71.31× |
| io_addr `-n32` | 0.191312 | 0.039189 | 2.345389 | 4.88× | 12.26× | 59.85× |

三次耗时范围如下；小于1秒的样本易受启动和主机调度抖动影响，不能用小数位数代表统计置信度。

| Benchmark | RTLSim min–max（秒） | SimX min–max（秒） | Timing min–max（秒） |
| --- | ---: | ---: | ---: |
| vecadd | 0.267616–0.355058 | 0.046210–0.057802 | 4.099036–4.329358 |
| sgemm2 | 0.811279–0.862817 | 0.140817–0.184420 | 11.955571–12.236785 |
| io_addr | 0.145730–0.204798 | 0.037095–0.040711 | 2.243232–2.407617 |

### 16.3 周期误差

| Benchmark / 参数 | 指令数（三方一致） | RTLSim周期 | SimX周期 | SimX误差 | Timing周期 | Timing误差 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| vecadd `-n1024` | 6160 | 17660 | 17260 | -2.265% | 17451 | -1.183% |
| sgemm2 `-n16 -t4 -c8` | 19408 | 54630 | 55146 | +0.945% | 54644 | +0.026% |
| io_addr `-n32` | 2832 | 9063 | 8832 | -2.549% | 8734 | -3.630% |

vecadd与io_addr关闭RTL trace后的周期分别仍为17660、9063，与第15节相同，
因此本次速度差异不是靠改变这两项的RTL执行周期取得的。

### 16.4 结论与适用范围

这三个样本中，SimX比无详细trace的RTLSim快4.88～5.88倍；当前Timing比RTLSim慢
12.26～14.56倍，比SimX慢59.85～77.60倍。两种软件模型本轮周期误差均在5%内，
Timing在vecadd/sgemm2上更接近RTL，SimX在io_addr上更接近；3个样本不足以给出全局精度排名。

此前63组的“Timing慢约3.94倍”是相对于开启详细trace的RTL构建；本轮移除该开销后，
差距明显扩大。两轮输入集合与测量方式也并不完全相同，不应把总倍数直接相除来量化trace成本。
本轮可以确认当前Timing性能显著落后，而不能据此认定某个Go函数或GC就是主因，仍需profiling。

原始记录：`.cache/three-way-speed-20260917/` 中的 `manifest.json`（输入/库哈希、节点与配置）、
`results.json`（27次原始耗时、CPU时间、PERF及审计）、`summary.json`（中位数与误差）、
`results/<benchmark>/<repeat>/<backend>/`（stdout/stderr/结果）。脚本为 `prepare.py`、
`build.sbatch`、`run.py`、`run.sbatch`，未修改生产模拟器源码。
