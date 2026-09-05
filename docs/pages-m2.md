# M2：Pages 发布体验

M2 保留单实例 SQLite + 本地磁盘架构，不引入 Caddy。已有 API 域名、旧子域名路径和已验证自定义域名继续使用。升级前停止服务，备份整个数据目录（数据库、sites、证书和签名密钥）；参考 [M1 迁移说明](migration-m1.md)。数据库首次打开时自动补齐项目主机标识及预览、发布事件、token 表。回退旧程序前应恢复升级前的完整备份。

每个项目分配稳定的单层 `p-<随机标识>.example.com` 主机名；每个预览分配 `d-<随机标识>.example.com`；分支别名使用项目 ID 与原始分支名的哈希生成 `b-<哈希>.example.com`。它们均在 `*.example.com` 范围内，无需双层通配符。随机标识与分支哈希不充当访问凭证。项目主机名与旧用户子域名分开，不允许用户通过命名占用这些主机。

继续使用现有 DNS 通配符和自动证书授权；手工证书模式需覆盖新增主机。API 仍由独立的 `api.example.com` 入口提供。新增项目主机在列出的项目 `host_label` 字段和部署响应中可见，部署返回的 `url` 指向项目根地址或预览地址。

```sh
# 生产发布
droply deploy dist --sub alice --project blog --json
# 预览；branch/commit 可选
droply deploy dist --sub alice --project blog --preview --branch feature/docs --commit abc123 --json
# 提升预览版本，不重新上传
droply deployment promote 42 --sub alice --project blog --json
# 按时间顺序查看提升记录
droply deployment events --sub alice --project blog
# 回到已发布且仍保留的版本
droply deployment rollback 41 --sub alice --project blog --json
```

预览成功只更新其分支别名；失败上传不会移动别名或生产指针。省略分支名只创建不可变预览地址。不同项目、大小写及包含斜杠的原始分支名互不混用。提升时验证归属、产物可用性及完整性，原子切换生产并记录操作者、源版本及时间。部署、提升和回滚按服务端实际提交顺序生效；CLI 输出表示该事务已提交，不能保证后续没有其他人再次发布。

尚未提升的预览不能直接作为回滚目标。提升后，其原预览地址继续提供相同产物，也可以正常参与生产回滚。版本清理保护生产和分支别名正在引用的产物；不再被引用的旧预览遵循 M1 保留策略，清理后对应地址不可用。不可变表示地址不指向其他版本，并非永久保留。

生产、预览、分支和自定义域名都逐请求检查当前项目/子域名访问规则。规则变更对旧预览同样生效，项目级规则优先。密码会话受项目绑定限制；预览响应包含 `X-Robots-Tag: noindex`，这不代替访问控制。受保护响应使用私有、禁止存储的缓存策略。

`_droply.toml`、`_headers`、`_redirects` 位于待上传目录根部，并随产物版本保存。非法或不支持的规则在发布前拒绝。静态模式、SPA、错误页、清理后的 HTML 路径、头规则和跳转的准确子集见 [静态规则](static-rules-m2.md)。这三个配置文件不作为静态资源公开。

自动化使用 [项目 token](project-tokens-m2.md)，默认仅允许预览，生产权限单独授予；[CI 文档](ci-m2.md) 给出环境变量、JSON、退出码和明确无自动重试的契约。M2 不包含服务端构建、Functions/Workers、多节点、边缘 CDN 或完整 Cloudflare Pages 配置兼容。
