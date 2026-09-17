# 支持集 Slurm 扩展测试

## 本轮提交

- 日期：2026-09-10。
- 清单：与 Simulator_dev 的 run-supported.sbatch 相同的 28 项及中等规模参数。
- 模式：timing、functional 各一次，共 56 项；详见 scripts/vortex-supported.py 的 CASES。
- debug 每项 4 CPU、8 GiB，作业时限 30 分钟，benchmark 超时 1500 秒。
- 外部 memory latency 仍为 100 cycles；未改动模拟器主体及 Cache 结构。
- 当前入口 Job ID：12702531、12702532。
- 当前结果目录：`.cache/runtime-supported/20260910T123839Z-87khjjzf/`。
- 提交后初查：两项均 PENDING，原因为 MaxJobsPerAccount；56 项尚无正式结果。

debug 的默认 QoS 最多接收 8 项待运行/运行作业，因此不一次提交 56 元素数组。
改为两条作业链，每条 28 项、混合两种模式。每项结束时提交一个 afterany 后继，
同一链不会同时执行两项，总共最多并行两项。不需要管理节点持续驻留的轮询进程。
每个后继的 Job ID 保存在 `chain-<chain>-<position>.submission.json` 中。

提交前重新 configure、更新了原生 runtime 和 28 项 benchmark；LLVM 通过已有
Singularity 包装器运行，规避宿主 glibc 版本不足。冻结输入包括 host 程序、kernel.vxbin、
原生 runtime、Go/backend 库、GCC 12 的 libstdc++/libgcc 和测试驱动脚本。
manifest.json 记录 SHA-256，各项执行前后核对相关二进制，不读取后续修改的构建产物。
每项使用独立工作目录，raycast 输出等不会互相覆盖。

## 使用

从 Simulator_timing 根目录提交新的完整一轮（会启动真实作业，请勿用来查询已有轮次）：

```bash
./scripts/submit-vortex-supported.sh
```

查看本轮累计结果（未结束或存在失败时返回非零，不代表汇总工具失败）：

```bash
python3 .cache/runtime-supported/20260910T123839Z-87khjjzf/runner.py aggregate \
  --root .cache/runtime-supported/20260910T123839Z-87khjjzf
```

每项的 stdout.log、stderr.log、events.jsonl、result.json 位于 `results/<index>/`。
summary.json/summary.tsv 总是列出完整 56 项；未报告结果是 NOT_REPORTED，不能算作通过。
NOT_REPORTED 可能表示排队、运行中、作业被外部终止或链暂停，需结合 Slurm 状态判断。

PASS 要求 host 退出 0，且至少有一次完整 launch。按连续 sequence 验证 start → finish，
模式与 launch 描述逐项匹配，finish 为 complete 且 generated/admitted/completed 一致。
周期模式还要求同 sequence 的成功 cache-flush（cycle/counter 与 finish 一致）之后才能
开始下一次 launch；功能模式 finish 本身必须 backing-visible。重复、错序、缺失、
畸形 JSON（含重复 key）、错误事件或未显式记录周期都不能判 PASS。
aggregate 重新读取原始 events.jsonl 并核对 result 与 manifest 的 case 身份，
不会仅相信 result.json 中预存的 PASS。缺失或不完整证据的周期在 JSON 中为 null、
TSV 中为 unknown；功能模式的周期也为 null/unknown。旧日志省略的零周期不补零。
其他类别为 SIMULATOR_INTERNAL、EXTERNAL_CONNECTION、TIMEOUT、HOST_OR_UNKNOWN、
INCOMPLETE_EVIDENCE。超时不自动归因于模拟器设计。模型错误原样记录，不自动修主体。
运行库/驱动脚本等全局基础设施失败会暂停对应链，避免余下项目全部无效失败。
需要阻止后续提交时，可在本轮目录创建 STOP 文件；当前任务不会因此被强制终止。

## 提交调试记录

1. 56 项数组在 debug 被 QOSMaxSubmitJobPerUserLimit 拒绝，没有运行。
2. 旧目录 `20260910T122120Z-lv9oc5wa` 的首批节点任务暴露继承 module 函数的路径不兼容，
   继而缺少 GLIBCXX_3.4.29/3.4.30；这些启动前失败属于环境问题，不是模型结论。
3. 上述旧链及 `20260910T123258Z-1xbl_7bt` 的替换链均已停止，查询用户队列确认清空后，
   才提交本轮。旧证据保留，不混入本轮汇总。
4. 最终快照自带 GCC 12 运行库，节点不再调用继承的 module 函数；子作业参数通过明确的
   提交环境传递，避免 --export=ALL 继承旧 case/position。其计算节点执行结果仍待排队完成。
5. 测试基础设施的 3 项单测通过：28 项 allowlist 完整性、缺失/失败不能汇总 PASS、
   双链完整覆盖与 afterany 提交/重复提交拒绝。shell 语法、git diff --check 通过。

本文件中的初查状态是提交时快照；后续实际结果以本轮 Slurm 日志与汇总为准。
