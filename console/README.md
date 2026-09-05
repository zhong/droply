# Droply Console

独立前端：React、TypeScript、Vite、Tailwind CSS v4 和 `@cloudflare/kumo`。修改前先阅读 [AGENTS.md](AGENTS.md)，组件用法以安装包 `ai/USAGE.md`、组件注册表及 TypeScript 类型为准。

## 构建与开发

使用 Node.js 22.12+，在项目根目录运行：

```sh
make console
make server
```

`make console` 使用锁文件安装依赖，检查类型与格式，再将产物写入 `internal/server/console_assets/`。该目录只保存构建产物，和源码一起提交，CI 会检查两者是否同步。Go 的内嵌资源边界保持不变，因此普通 `go build`、CLI 构建和生产运行不依赖 Node.js。

```sh
npm --prefix console run dev     # 前端布局预览
npm --prefix console run watch   # 改动后持续生成内嵌资源
npm --prefix console run check   # TypeScript 与格式检查
npm --prefix console run format
```

Vite 开发预览地址为 `http://localhost:5173/console/`。登录和真实 API 操作需在 Go 服务的 `https://api.<base-domain>/console/` 验证；每次产物变更后重新编译并启动 Go 服务。不要为本地预览削弱 HTTPS、Origin、HttpOnly Cookie 或 CSRF 校验。

## 源码

- `src/App.tsx`：登录、会话、主题、项目导航。
- `src/ProjectDetail.tsx`：项目详情、发布/回滚、访问规则。
- `src/api.ts`：现有 Go 服务接口类型和同源请求。
- `src/ui.tsx`：共用的 Kumo 表格、加载、错误和链接。
- `src/style.css`：Kumo 语义 token 与响应式布局。

保持各详情区域的失败隔离；切换项目或退出时取消旧请求。写操作确认后锁定表单和导航，完成后重新获取详情。凭证仅保留于现有 HttpOnly Cookie，会话中的 CSRF 值仅存在内存；主题也不写入浏览器存储。

## 验收

真实 Go HTTPS fixture 的浏览器验收入口仍为 `scripts/console_test.mjs`，覆盖登录、权限、Cookie 隔离、发布、回滚、规则变更与过期；它同时调用 `console/tests/acceptance.mjs` 验证主题、中文窄屏、键盘、失败状态和重复提交。运行方法见 [控制台文档](../docs/console-m3.md)。
