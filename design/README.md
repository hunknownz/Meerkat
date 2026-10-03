# Meerkat 完整设计

- [完整方案](complete-design-20261002.md)：术语、Issue、共享上下文、多模型分工、工作区、交付检查与精修、UI、架构、状态、指标和整体验收。
- [Go / SQLite / React 技术栈方案](architecture-go-sqlite-react-20261002.md)：当前实现核对、Magpie 源码证据、目标架构与迁移验收；保留当时的迁移目标与依据。
- [交互原型](ui/README.md)：Magpie 风格，Agents / Tasks / Usage，样例数据。
- [核验与本次执行指标](verification-20261002.md)：实际完成范围、检查和未知项。

真实任务编排、Issue 适配和共享 UI 已有本地实现及测试，见 [实现记录](implementation-20261002.md) 和 [当前使用指南](../README.md)。当前运行栈为 Go / SQLite / React；标准 MCP Apps 面板已取得 [真实 Codex 证据](../docs/codex-ui-acceptance.md)，新增版本详情仍单独核验。已安装插件、源码和历史设计的状态分开记录。

后续重构见 [会话、预算与协作完整计划](session-budget-coordination-20261003.md)。阶段实施分别见持久会话、请求预算、检查点、追加预算与 [已完成步骤的中断恢复](session-recovery-20261003.md)；该完整计划的全部工作包尚未完成。
