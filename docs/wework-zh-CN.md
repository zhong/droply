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
   │  访客    │ ──────────────────▶ │ droply-server HTTPS 统一入口   │
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
- 当前版本的 droply-server，已配置 WeCom 参数
- 配置的回调地址和受保护站点均可通过 HTTPS 访问

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

在应用的 **网页授权及 JS-SDK** 设置中，按实际 OAuth 配置填写可信域名。如果后台要求提供校验文件（如 `WW_verify_AbCdEf123.txt`），文件必须位于待校验的准确主机名的根路径。

Droply 可以把它作为普通静态文件，托管在**公开项目的根路径域名**（部署返回的项目 URL），或绑定到该项目的**已验证自定义域名**上。将后台提供的文件原样放入项目部署目录，确认无需登录 Cookie 即可访问 `https://<该主机名>/WW_verify_AbCdEf123.txt`。访问规则不能拦截校验请求；旧式 `/docs/WW_verify_AbCdEf123.txt` 路径不是域名根路径。

`api.<domain>` 和裸基域名没有专用校验文件 handler。在另一个主机名上部署文件，不能完成对它们的校验。先核对企业微信后台要求校验的主机名，再选择文件托管位置；Droply 不会自动配置该文件。

## 步骤 3：配置 droply-server

API 和站点共用配置的 HTTP/HTTPS 入口，按 Host 分流，不需要独立站点端口或 Caddy。先完成[部署与 TLS 配置](operations-m3.md)：裸二进制默认只在 `:8080` 提供 HTTP，不会自动启用 HTTPS。OAuth 流程应使用已有的 HTTPS 监听器或受信 TLS 网关。

使用仓库安装器部署时，在现有环境文件（默认 `/etc/droply/env`）中添加或更新以下四个值。保留其他配置、文件权限、服务用户和 `ExecStart`：

```sh
DROPLY_WEWORK_CORP_ID=ww1234567890abcdef
DROPLY_WEWORK_AGENT_ID=1000002
DROPLY_WEWORK_SECRET=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
DROPLY_WEWORK_REDIRECT_URI=https://api.example.com/_droply/wework/callback
```

等效命令行参数见下方参考表。自定义服务使用已有的环境变量或配置方式，无需用本示例替换 service unit。

回调路径为 `/_droply/wework/callback`。`api.<domain>` 明确支持中央回调；已登记的站点主机名或已验证自定义域名也可处理回调。配置的 URL 必须指向可访问的回调入口，并与 OAuth 设置一致；未知 Host 会被拒绝。启动时只检查 URL 语法，不验证域名归属或远端应用配置。

每个 Droply 服务使用一个配置的回调 URL。回调与原站点是所配置基域名下的不同子域时，会设置父域 Cookie，使跳转回原站点后仍保留会话；同一主机名使用 host-only Cookie。`api.example.com` 的回调不能把 Cookie 共享给无共同基域的自定义域名，详见已知限制。

重启已有服务并检查状态和日志：

```bash
sudo systemctl restart droply
sudo systemctl status droply
sudo journalctl -u droply -n 50 --no-pager
```

四项均未设置时，WeCom 功能关闭。仅设置部分值会以 `all four WeCom options must be configured` 拒绝启动；URL 无效会报 `invalid WeCom callback URL`。这些检查发生在打开安装数据资源之前，不能依赖“只打印警告后继续运行”或 OAuth 启用日志横幅。

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
https://alice.example.com/docs/
```

实际行为取决于访问规则：

- **仅启用 WeCom 的规则**（无密码）：浏览器自动跳转进入企业微信 OAuth 流程，无需手动点按钮
  - 桌面浏览器 → 显示二维码页
  - 企业微信 App 内嵌浏览器 → 静默授权
- **同时启用 WeCom 和密码的规则**：显示登录页，包含密码输入框和 **"使用企业微信登录"** 按钮，由访客选择

授权成功后跳回原 URL 并设置会话 cookie。

如果 OAuth 流程失败（state 过期、白名单未命中等），用户会看到登录页（含 WeCom 按钮）而不是被反复跳回 OAuth —— 一个短期 cookie 标记会在约 60 秒内防止跳转循环。

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
| `--wework-redirect-uri` | `DROPLY_WEWORK_REDIRECT_URI` | OAuth 回调 URL（`api.<domain>` 或获允许的站点主机名） |

四项均未设置时关闭 WeCom；只要设置其中一项，就必须补齐全部四项，且回调 URL 须通过启动校验。配置不完整会阻止启动。

### 公开端点

WeCom 启用后，统一入口在站点 Host 上提供以下路由。API Host 也处理回调路由；授权流程应从受保护站点发起：

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

### 1. 单个回调配置与 Cookie 边界

每个 Droply 服务使用一个 `--wework-redirect-uri`。配置基域名下的不同子域可以使用 `api.example.com` 等中央回调；父域 Cookie 已实现。签名 Cookie 仍包含子域/项目身份，并按当前访问规则校验；Cookie 能发送到同级主机名，不等于获准访问全部项目。

回调和原站点没有共同的配置基域名后缀时，不会获得共享 Cookie，不支持跨无关自定义域名的中央登录。同一主机名的回调使用 host-only Cookie。

### 2. 校验文件托管

按步骤 2，将文件发布到公开项目的根路径域名或已验证自定义域名。API Host 和裸基域名没有自动校验文件路由，也没有 `--wework-verify-file` 参数。

### 3. 必须用 user_id

`--wework-user` 白名单使用的是企业微信内部 `user_id`，不是显示名或邮箱。管理员需要在企业微信后台手动查找 user_id。

### 4. 外部联系人

OAuth 流程会根据访问者的 User-Agent 自动选择端点：

- **在企业微信 App 内**（User-Agent 包含 `wxwork/`）：使用 `snsapi_base` 作用域静默授权 —— 用户无需扫码即可登录，因为企业微信内嵌浏览器已有会话
- **其他浏览器**（桌面 Chrome/Safari、手机 Safari 等）：使用 SSO 登录端点显示二维码页，用户用企业微信 App 扫码登录

两种流程最终都调用同一个 `auth/getuserinfo` API 用 code 换 user_id，所以后续登录逻辑完全一致。

外部联系人扫码 / 访问会报错。这是设计预期 —— droply 面向的是内部访问控制场景。

---

## 故障排查

### "WeWork login is not configured" (503)

运行中的服务未启用 WeCom，例如四项配置均未设置。检查实际服务的环境变量和 `journalctl -u droply`。当前版本遇到不完整配置会拒绝启动，补齐四项后再重启；重启失败不能理解为“仅警告并关闭功能后继续运行”。

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

核对 `DROPLY_WEWORK_REDIRECT_URI` 与企业微信后台应用配置，包括协议、主机名和回调路径，并重新检查步骤 2。Droply 启动时不会验证远端配置。

### 登录页不显示扫码按钮

访问规则 `wework_enabled=false`（默认）或服务器未配置 WeCom。检查：

```bash
droply access get --subdomain alice --project docs
# 应包含："WeCom login: enabled"
```

如果规则正确但按钮仍不显示，核对运行中的服务是否收到全部四项 WeCom 配置，并检查服务状态和启动失败日志；只有服务已配置 WeCom 客户端时，登录页才显示按钮。
