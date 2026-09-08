# 初版静态 Timing IR

本目录按 [Task/T8.md](../Task/T8.md) 建立冻结 Vortex 配置的静态时序基线。

- [architecture.md](architecture.md)：抽象层级、维护约定、结构解释、功能衔接与未知项。
- [rules.md](rules.md)：周期计量边界、复杂阻塞/反馈与可追溯示例。
- [ir.yaml](ir.yaml)：配置、来源、节点、资源、端口、通道、缓冲边界与规则索引，结构化数值的唯一维护位置。
- [功能契约](../emu/docs/architecture.md)：指令结果、canonical state ownership、effect 与已有 `RESOLVED` 审计的唯一契约。

当前交付覆盖 `timing-baseline`、`timing-rules` 与 `timing-integration-validation`：可审查的静态结构和局部周期规则，不包含周期执行器、Scheduler、cache 实现或 RTLSIM 对齐。定量条目均限定起止事件；剩余外部后端、变量等待与完整跨组件同时事件验证仍为未决；YAML 中未定值显式为 `null` 并关联未知项。离线 Akita 依赖可供后续实现使用，本阶段不固定其调用组织。

修改结构化事实时，在 YAML 更新值、状态与证据，再更新 Markdown 中对应稳定 ID 的解释。不得把未启用 RTL 分支或功能 Step 次序当作时序事实。

阅读顺序：先完整阅读功能契约，再读 architecture → ir.yaml 的来源/结构 → rules → [integration.md](integration.md) 的职责、复用评估与未决索引。三个静态 IR 里程碑均已形成交付；后续实现仍须按 unknowns 的 deadline 闭合相关缺口。

在仓库根运行 `bash scripts/verify-timing.sh`：脚本加载 `env/env.sh`，使用固定 Go 工具链及 vendor 中的 `go.yaml.in/yaml/v3`，运行 [专用检查器](check/main.go) 和 [无效样例测试](check/main_test.go)。检查 YAML 语法/重复键、重复 ID、类型化引用、来源文件、状态、定量单位与起止事件、null 未知项及功能问题引用；失败返回非零。样例从真实 IR 内存副本分别破坏单一约束，不修改冻结输入、不下载依赖。

完整回归另运行 `bash scripts/verify-all.sh`，最后运行 `git diff --check`。现有完整门禁保持原范围，Timing 专用门禁单独执行；结构检查与功能回归都不能证明动态时序等价或所有 RTL 结论正确。

本次交付验证记录：`test -s timing/integration.md`、`bash scripts/verify-timing.sh`（含 15 个无效样例）、`bash scripts/verify-all.sh` 和 `git diff --check` 全部通过。完整门禁涵盖 RTL manifest、固定环境、`go mod verify`、功能 build/test/vet，以及独立空缓存 vendor build/test/vet。环境提供方补齐固定模块缓存后，原先的离线元数据缺失阻塞已解除。按授权仅调整 `scripts/verify-offline.sh` 的临时目录创建与位置，使用 `SIMULATOR_RUNTIME_ROOT`；保留空缓存、网络限制、断言和失败退出行为。未下载依赖或修改冻结 RTL、现有功能语义。
