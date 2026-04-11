const translations = {
  zh: {
    // Nav
    "nav.features": "特性",
    "nav.quickstart": "快速开始",
    "nav.architecture": "架构",

    // Hero
    "hero.badge": "开源 · 自托管 · Go",
    "hero.title": "静态网站，<br>秒级发布。",
    "hero.sub": "多用户静态内容发布平台，自动分配子域名，<br>通配符 HTTPS，支持自定义域名。一条命令上线。",
    "hero.cta": "快速开始",
    "hero.github": "GitHub 仓库",

    // Features
    "features.title": "你需要的都有，不需要的都没有。",
    "features.sub": "专注静态内容发布的工具集。快速、安全、自托管。",
    "features.deploy.title": "一键部署",
    "features.deploy.desc": "一条 CLI 命令即可打包发布任意目录。自动版本管理，每次部署都有记录。",
    "features.subdomain.title": "自动子域名",
    "features.subdomain.desc": "每个用户拥有独立子域名。即时创建 <code>alice.droplydoc.com</code>，在其下托管多个项目。",
    "features.https.title": "全站 HTTPS",
    "features.https.desc": "通过 Caddy 提供通配符 TLS 证书。每个子域名和自定义域名都自动启用 HTTPS。",
    "features.selfhost.title": "自托管部署",
    "features.selfhost.desc": "在自己的 VPS 上运行。数据完全由你掌控。只需一个二进制文件 + Caddy。",
    "features.domain.title": "自定义域名",
    "features.domain.desc": "绑定自己的域名。添加 CNAME 记录，Caddy 自动申请和续期证书。",
    "features.access.title": "访问控制",
    "features.access.desc": "通过 IP 白名单和密码保护站点。支持子域名级和项目级规则，可配置会话过期时间。",
    "features.zero.title": "零依赖",
    "features.zero.desc": "纯 Go 编译的二进制文件，内嵌 SQLite。无需 Docker、Node.js 或外部数据库。",

    // How it works
    "how.title": "工作流程",
    "how.sub": "从代码到上线，只需三步。",
    "how.step1.title": "创建子域名",
    "how.step1.desc": "申请你的命名空间。可以创建多个子域名，每个托管不同的项目。",
    "how.step2.title": "部署网站",
    "how.step2.desc": "文件自动打包、上传并解压。首次部署时项目自动创建。",
    "how.step3.title": "已经上线",
    "how.step3.desc": "Caddy 立即接管新路由。HTTPS 已就绪。分享链接即可。",

    // Architecture
    "arch.title": "系统架构",
    "arch.sub": "简洁、可靠、易于运维。",
    "arch.cli": "CLI 客户端",
    "arch.caddy_desc": "TLS + 反向代理 + 静态文件服务",
    "arch.browser": "浏览器",
    "arch.api_desc": "认证 · 上传 · 路由",
    "arch.db_desc": "用户 · 项目 · 部署记录",
    "arch.tech": "技术栈",

    // Quick Start
    "qs.title": "快速开始",
    "qs.sub": "几分钟即可上手。",
    "qs.install_cli": "安装 CLI",
    "qs.install_cli_desc": "一条命令安装。自动检测操作系统和架构。支持 <strong>macOS</strong>（ARM/x64）、<strong>Linux</strong>（x64）和 <strong>Windows</strong>（x64）。",
    "qs.setup_server": "部署服务端",
    "qs.setup_server_desc": "在全新 VPS 上一键部署完整的 droply 服务端。安装 droply-server、Caddy（含 Cloudflare DNS 模块）、配置 systemd 并启动所有服务。过程中会提示输入域名和 Cloudflare API Token。",
    "qs.build": "从源码编译",
    "qs.build_desc": "生成两个二进制文件：<code>bin/droply-server</code> 和 <code>bin/droply</code>",
    "qs.install": "或使用 Go 安装",
    "qs.install_desc": "CLI 客户端将安装到 <code>$GOPATH/bin</code>",
    "qs.server": "服务端参数一览",
    "qs.th_flag": "参数",
    "qs.th_default": "默认值",
    "qs.th_desc": "说明",
    "qs.flag_addr": "API 监听地址",
    "qs.flag_site": "站点服务地址（受保护站点）",
    "qs.flag_data": "数据目录（数据库 + 文件）",
    "qs.flag_domain": "基础域名",
    "qs.flag_hmac": "Cookie 签名密钥（留空自动生成）",

    // CLI
    "cli.title": "CLI 参考",
    "cli.sub": "简洁的命令，可预期的行为。",
    "cli.auth": "用户认证",
    "cli.auth_register": "注册账号",
    "cli.auth_login": "登录",
    "cli.auth_whoami": "当前用户",
    "cli.auth_logout": "登出",
    "cli.subdomains": "子域名管理",
    "cli.projects": "项目管理",
    "cli.deploying": "部署",
    "cli.deploy_flags": "使用参数部署",
    "cli.deploy_config": "或使用 .droply.toml 配置文件",
    "cli.access": "访问控制",
    "cli.access_set": "设置规则（IP + 密码）",
    "cli.access_never": "永不过期密码",
    "cli.access_get": "查看规则",
    "cli.access_remove": "移除规则",
    "cli.domains": "自定义域名",
    "cli.config": "项目配置文件 <code>.droply.toml</code>",
    "cli.config_desc": "放在项目根目录，之后 <code>droply deploy</code> 无需传参，也可以配置不发布的精确路径和文件名。",

    // API
    "api.sub": "RESTful JSON API，地址 <code>api.droplydoc.com</code>，使用 <code>Bearer</code> token 认证。",
    "api.th_method": "方法",
    "api.th_endpoint": "端点",
    "api.th_desc": "说明",
    "api.register": "注册",
    "api.login": "登录",
    "api.create_sub": "创建子域名",
    "api.list_sub": "列出子域名",
    "api.delete_sub": "删除子域名",
    "api.deploy": "部署（multipart）",
    "api.list_proj": "列出项目",
    "api.delete_proj": "删除项目",
    "api.history": "部署历史",
    "api.add_domain": "添加自定义域名",
    "api.remove_domain": "删除自定义域名",
    "api.set_access": "设置访问控制",
    "api.get_access": "查看访问控制",
    "api.del_access": "移除访问控制",
  },
};

(function () {
  const btn = document.getElementById("langToggle");
  let lang = localStorage.getItem("droply-lang") || "en";

  function apply(lang) {
    document.documentElement.lang = lang === "zh" ? "zh-CN" : "en";
    btn.textContent = lang === "zh" ? "EN" : "中文";

    document.querySelectorAll("[data-i18n]").forEach(function (el) {
      var key = el.getAttribute("data-i18n");
      var useHtml = el.hasAttribute("data-i18n-html");

      if (lang === "zh" && translations.zh[key]) {
        if (useHtml) {
          el.innerHTML = translations.zh[key];
        } else {
          el.textContent = translations.zh[key];
        }
      } else if (lang === "en") {
        // English is the default in HTML; restore from saved originals
        if (useHtml) {
          el.innerHTML = originals[key] || el.innerHTML;
        } else {
          el.textContent = originals[key] || el.textContent;
        }
      }
    });
  }

  // Save original English content on first load
  var originals = {};
  document.querySelectorAll("[data-i18n]").forEach(function (el) {
    var key = el.getAttribute("data-i18n");
    var useHtml = el.hasAttribute("data-i18n-html");
    originals[key] = useHtml ? el.innerHTML : el.textContent;
  });

  btn.addEventListener("click", function () {
    lang = lang === "en" ? "zh" : "en";
    localStorage.setItem("droply-lang", lang);
    apply(lang);
  });

  // Apply saved preference on load
  if (lang === "zh") {
    apply("zh");
  }
})();
