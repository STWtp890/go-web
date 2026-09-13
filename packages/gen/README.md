# RPC 生成代码

此目录集中保存由 `packages/proto` 中协议生成的共享 Go 代码，并作为独立 Go module 参与根 `go.work`。

共享模块路径为 `packages/gen`。

- 生成代码由协议源统一生成，不直接手工修改；
- 应用通过模块导入使用生成类型；
- 生成代码提升到此目录不改变 RPC 服务语义或应用运行时边界。

提交前运行 packages/proto/verify-generated.ps1，确保生成文件与协议源完全一致。