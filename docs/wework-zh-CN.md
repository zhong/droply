# 企业微信扫码登录集成

本文档说明如何在 droply 部署的站点上启用企业微信扫码登录作为访问控制方式。

> 📖 English: [wework.md](wework.md)

## 概述

企业微信扫码登录是 droply 三种访问控制方式之一（另两种是 IP 白名单和密码）。三种方式可以叠加 —— **任一通过即可访问**。

```
                    ┌─────────────────┐
   访问站点 →       │ IP 白名单匹配？ │ ─是→ 放行
                    └────────┬────────┘
                             │ 否
                    ┌────────▼────────┐
                    │ 有效 Cookie？   │ ─是→ 放行
                    │ （密码或扫码）  │
                    └────────┬────────┘
                             │ 否
                    ┌────────▼────────┐
                    │  显示登录页面   │
                    │  密码输入框     │
                    │   + 扫码按钮    │
                    └─────────────────┘
```

## 架构

```
                                                  ┌─────────────────┐
                                                  │  企业微信后台   │
                                                  └────────┬────────┘
                                                           │
                                                           │ 4. OAuth 回调带 code
                                                           ▼
   ┌──────────┐  1. 点击"扫码登录"  ┌──────────────────────────────────┐
   │  访客    │ ──────────────────▶ │ droply-server :8081 (站点端口)   │
   │（浏览器）│ ◀──────────────────│ /_droply/wework/auth → 跳转      │
   └────┬─────┘  2. 跳转到企业微信  │ /_droply/wework/callback ← 回调  │
        │           OAuth 授权页    └──────────────┬───────────────────┘
        │                                          │
        │ 3. 手机扫码授权                          │ 5. 用 code 换 user_id
        ▼                                          ▼
   ┌─────────────┐                       ┌──────────────────┐
   │ 企业微信 App│                       │ 企业微信 API     │
   │ （手机端）  │                       │ qyapi.weixin...  │
   └─────────────┘                       └──────────────────┘
```

## 前置条件

- 一个企业微信组织，且你有管理员权限创建自建应用
- droply-server **v0.4.0 或更高版本**，且启动时已配置 WeCom 参数
- droply 的 base domain 能被企业微信服务器访问（OAuth 回调需要）

---

## 步骤 1：创建企业微信自建应用

1. 登录 [企业微信管理后台](https://work.weixin.qq.com/)
2. 进入 **应用管理** → **自建** → **创建应用**
3. 填写：
   - **应用名称**：例如 "Droply 文档访问"
   - **应用 Logo**：任意图标
   - **可见范围**：选择允许扫码登录的部门 / 成员
4. 创建完成后，从应用详情页记下三个值：
   - **CorpID**：在 **我的企业** → **企业信息** → 页面底部
   - **AgentID**：应用详情页
   - **Secret**：应用详情页（可能需要点 **查看** 才能显示）

## 步骤 2：配置可信域名

在应用详情页 → **网页授权及 JS-SDK** → **设置可信域名**：

```
可信域名：docs.paratera.co   ← 你 droply 的 base domain（不带前导点）
```

企业微信会要求你下载一个校验文件（如 `WW_verify_AbCdEf123.txt`），放到该域名根路径下来证明你拥有该域名。

> ⚠️ **droply 暂时没有内置方式服务这个文件**，有两种变通办法：
>
> **方案 A — 用 Caddy 静态托管**：在 `/etc/caddy/Caddyfile` 中临时加一条路由：
> ```caddyfile
> docs.paratera.co {
>     handle /WW_verify_AbCdEf123.txt {
>         respond "AbCdEf123" 200
>     }
>     # ... 其他配置
> }
> ```
> 重新加载 Caddy：`sudo systemctl reload caddy`
>
> **方案 B — 作为项目部署**：如果你恰好有一个子域名与可信域名同名，可以用 droply 部署一个包含校验文件的目录。
>
> 校验通过后即可移除上述静态路由。

## 步骤 3：配置 droply-server

在 `/etc/systemd/system/droply.service` 中添加环境变量：

```ini
[Service]
Environment="DROPLY_WEWORK_CORP_ID=ww1234567890abcdef"
Environment="DROPLY_WEWORK_AGENT_ID=1000002"
Environment="DROPLY_WEWORK_SECRET=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
Environment="DROPLY_WEWORK_REDIRECT_URI=https://login.docs.paratera.co/_droply/wework/callback"
ExecStart=/usr/local/bin/droply-server \
  --addr :8080 \
  --site-addr :8081 \
  --data-dir /data/droply \
  --domain docs.paratera.co \
  --caddy-admin http://localhost:2019
Restart=always
User=www-data
```

也可以用命令行参数（等效）：

```ini
ExecStart=/usr/local/bin/droply-server \
  --wework-corp-id ww1234567890abcdef \
  --wework-agent-id 1000002 \
  --wework-secret xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
  --wework-redirect-uri https://login.docs.paratera.co/_droply/wework/callback \
  --domain docs.paratera.co \
  ...
```

**`DROPLY_WEWORK_REDIRECT_URI` 的关键约束：**

1. host **必须**是 droply 的某个站点子域（不能是 `api.`），因为回调由站点服务（:8081）处理
2. 企业微信每个应用只允许配置**一个**回调 URL。如果你想让多个子域（`alice.docs.paratera.co`、`bob.docs.paratera.co`）都用扫码登录，必须用一个"登录中转"子域（如 `login.docs.paratera.co`）作为回调
3. 回调成功后 cookie 的作用域是回调 host。如果你访问的是 `alice.docs.paratera.co`，需要确保 cookie 能在那边读到 —— 详见 **已知限制**

重新加载 systemd 并重启：

```bash
sudo systemctl daemon-reload
sudo systemctl restart droply
sudo journalctl -u droply -f | grep -i wework
# 应看到：WeWork OAuth enabled (corp=..., agent=...)
```

## 步骤 4：为项目启用扫码登录

使用 droply CLI 设置一条带 WeCom 的访问规则。

### 允许任何企业成员

```bash
droply access set --subdomain alice --project docs --wework
```

企业内任何成员都可扫码登录。

### 限定指定用户

```bash
droply access set --subdomain alice --project docs \
  --wework-user zhangsan --wework-user lisi
```

`--wework-user` 接收的是**企业微信 user_id**（在管理后台 **通讯录 → 成员详情 → 账号** 可以看到），**不是**显示名或手机号。

`--wework-user` 隐式启用 `--wework`，所以提供了白名单时可省略后者。

### 与密码、IP 白名单组合

三种方式以 **OR** 方式叠加 —— 任一通过即可访问：

```bash
droply access set --subdomain alice --project docs \
  --ip 10.0.0.0/8 \
  --password "myteamsecret" \
  --wework-user zhangsan
```

逻辑：
- 来自 `10.0.0.0/8` 的访客 → 直接放行
- 其他访客 → 看到登录页，包含密码输入框和企业微信扫码按钮
- 密码或扫码任一通过即获得会话 cookie

### 子域级规则

省略 `--project` 即可应用到该子域下的所有项目：

```bash
droply access set --subdomain alice --wework
```

项目级规则会完全覆盖子域级规则。

## 步骤 5：验证

访问受保护的站点：

```
https://alice.docs.paratera.co/docs/
```

应该能看到一个登录页，上面有 **"使用企业微信登录"** 按钮。点击后跳转到企业微信扫码授权页，扫码成功后跳回原 URL。

检查访问规则是否生效：

```bash
droply access get --subdomain alice --project docs
# 预期输出包含：
#   WeCom login: enabled (any corp member)
# 或：
#   WeCom login: enabled (allow-list: [zhangsan lisi])
```

---

## 参考

### 客户端参数

| 参数 | 说明 |
|------|------|
| `--wework` | 启用企业微信扫码登录（任何企业成员） |
| `--wework-user <user_id>` | 允许指定 user_id；可重复；隐式启用 `--wework` |

### 服务端参数 / 环境变量

| 参数 | 环境变量 | 说明 |
|------|----------|------|
| `--wework-corp-id` | `DROPLY_WEWORK_CORP_ID` | 企业微信 CorpID |
| `--wework-agent-id` | `DROPLY_WEWORK_AGENT_ID` | 自建应用 AgentID |
| `--wework-secret` | `DROPLY_WEWORK_SECRET` | 应用 Secret |
| `--wework-redirect-uri` | `DROPLY_WEWORK_REDIRECT_URI` | OAuth 回调 URL（必须是某个站点子域） |

四个参数都必须设置才会启用 WeCom 功能；缺一不可。缺失时服务器会打印警告并跳过此功能（已有的 IP / 密码规则照常工作）。

### 公开端点

WeCom 启用后，站点服务器会暴露两个路由：

| 路径 | 方法 | 用途 |
|------|------|------|
| `/_droply/wework/auth` | GET | 生成 state token 并跳转到企业微信 OAuth |
| `/_droply/wework/callback` | GET | 接收 OAuth code，换取 user_id，签发会话 cookie |

### Cookie 格式

扫码登录成功后设置的 cookie `_droply_access` 格式：

```
v2:{subdomain}:{project}:wework:{userid}:{expiry}:{hmac}
```

HMAC payload 包含 `sha256(allowed_users_json + userid)`，所以：
- 修改白名单会使所有存活的 cookie 失效
- 被移出白名单的用户在下一次请求时立刻失去访问权

### 安全说明

- **CSRF 防护**：OAuth state token 一次性使用，10 分钟 TTL，存于内存
- **无外部会话存储**：state token 只存在 droply-server 进程内存中；服务器重启会让正在进行中的 OAuth 流程失效（用户重试即可）
- **Secret 隔离**：WeCom Secret 仅在启动时读取一次；轮换需要重启服务器
- **HTTPS only**：cookie 设置为 `Secure; HttpOnly; SameSite=Lax`

---

## 已知限制

### 1. 每个企业微信应用只允许一个回调 URL

企业微信限制每个应用只能配置一个 OAuth 回调地址。droply 在启动时固定 `--wework-redirect-uri`。如果有多个子域都需要扫码登录，必须用一个"中转"子域作为回调。中转子域的 cookie 不会自动共享给其他子域。

**当前变通方案**：

- **每个子域用单独应用**：为每个子域注册一个独立的企业微信应用，运行多个 droply-server 实例
- **父域 cookie**（计划中）：把 cookie 作用域设为 `.docs.paratera.co`，让所有子域共享会话（代价：子域之间的隔离变弱）

### 2. 校验文件托管

droply 没有内置 handler 来服务企业微信的域名校验文件，需要在 Caddy 中手动配置（见步骤 2）。未来版本可能加 `--wework-verify-file` 参数。

### 3. 必须用 user_id

`--wework-user` 白名单使用的是企业微信内部 `user_id`，不是显示名或邮箱。管理员需要在企业微信后台手动查找 user_id。

### 4. 外部联系人

OAuth 使用 `snsapi_base` 作用域，只对企业内部成员返回 user_id。外部联系人扫码会报错。这是设计预期 —— droply 面向的是内部访问控制场景。

---

## 故障排查

### "WeWork login is not configured" (503)

服务器启动时缺少四个 `--wework-*` 参数。检查 `journalctl -u droply` 启动日志：

```
WeWork OAuth enabled (corp=ww1234..., agent=1000002)
```

如果看到 `WeWork OAuth NOT enabled: all of corp-id, agent-id, secret, redirect-uri are required` —— 补全 systemd unit 中的缺失变量。

### "Access denied: user not in allow-list" (403)

WeCom 用户扫码成功了，但 user_id 不在 `allowed_wework_users` 中。检查：

```bash
droply access get --subdomain alice --project docs
# WeCom login: enabled (allow-list: [zhangsan lisi])
```

在企业微信管理后台核对该用户的真实 `user_id`（不是姓名也不是手机号）。

### "invalid or expired state" (400)

OAuth state token 10 分钟过期。如果用户扫码花了过长时间，让他刷新登录页重新开始。如果经常出现，检查服务器时钟是否同步（`timedatectl status`）。

### 企业微信跳转报 `redirect_uri_mismatch`

`DROPLY_WEWORK_REDIRECT_URI` 与企业微信后台的**可信域名**不匹配。两者必须是同一根域。回到步骤 2 重新配置。

### 登录页不显示扫码按钮

访问规则 `wework_enabled=false`（默认）或服务器未配置 WeCom。检查：

```bash
droply access get --subdomain alice --project docs
# 应包含："WeCom login: enabled"
```

如果规则正确但按钮仍不显示：登录页只在 `s.wework != nil` 时才渲染按钮 —— 检查服务器启动日志确认 WeCom 已启用。
