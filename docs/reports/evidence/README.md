# 阶段证据快照

本目录保存阶段报告引用的不可变运行产物。它与 `deployments/test-results/` 的职责不同：

| 目录 | 职责 | 保留策略 |
| --- | --- | --- |
| `deployments/test-results/` | 验收脚本按运行写入的操作性产物 | 每个报告族只保留最近 3 次运行 |
| `docs/reports/evidence/` | 阶段报告引用的证据快照 | 不清理、不改写 |

## 为什么需要快照

`deployments/prune-test-results.ps1` 按保留策略删除较早的运行产物。阶段报告引用这些产物时，引用会随清理失效——阶段 1 和阶段 2 报告曾因此累计 23 处断链。

正确处理方式是恢复或归档阶段报告引用的不可变证据，而不是把链接改指到最新报告（那会改变历史断言的含义）。因此：

- 阶段报告只链接本目录下的快照；
- `docs/check-doc-links.ps1` 强制 `docs/` 下不存在指向 `deployments/test-results/` 的链接；
- `deployments/prune-test-results.ps1` 不会删除仍被受版本控制 Markdown 引用的产物。

## 来源

| 目录 | 内容 | 恢复来源 |
| --- | --- | --- |
| `phase1/` | 阶段 1（P1.4、P1.5）运行时 API 回归报告 | 提交 `ac01d7e` |
| `phase2/` | 阶段 2（P2.0-P2.5）运行时 API 回归与检索质量评估报告 | 提交 `ac01d7e` |

`ac01d7e` 是清理提交 `a86f6f3` 的父提交，即这些产物最后一次被完整跟踪的状态。文件内容按原字节恢复，不做编辑。
