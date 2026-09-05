# Droply 控制台开发规范

## Kumo
- UI 优先使用 @cloudflare/kumo 的现有组件。
- 编码前读取已安装 Kumo 包中的 ai/USAGE.md；
  按需查询 ai/component-registry.json，并核对当前 TypeScript 类型。
- 不凭其他组件库的经验猜测 props、variants 或回调名称。
- 颜色使用 Kumo 语义 token，保持深浅主题一致。
- Blocks 通过 Kumo CLI 引入，之后作为项目源码维护。
- 不修改 node_modules 或 Kumo 自动生成文件。
- 不在本项目执行 Kumo 仓库专用的构建、代码生成和发布命令。

## Droply
- API 路径、请求和响应从现有服务端实现确认，不编造接口。
- 页面覆盖加载、空数据、失败、权限不足和成功状态。
- 回滚、删除等操作提供确认，并防止重复提交。
- 完成后运行项目已有检查，并验证中文布局、键盘操作和主题。
