---
title: Eino 文档管道示例
---

# 文档向量化

文档先按扩展名进入 Markdown、DOC 或 DOCX 专用加载管道，然后转换成统一的结构化文本块。

## Markdown 管道

Markdown 管道保留标题层级和代码块，使分块后的向量仍然带有章节上下文。

```go
result, err := service.IngestFile(ctx, request)
```

## 混合检索

共享的 Eino 工作流生成 dense embedding 和 sparse terms，并写入 Qdrant 或 pgvector，最终使用 RRF 融合两路召回。
