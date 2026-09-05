"use strict";
const $ = (id) => document.getElementById(id);
let session = null,
  projects = [],
  selected = null;
function element(tag, text, cls) {
  const n = document.createElement(tag);
  if (text !== undefined) n.textContent = text;
  if (cls) n.className = cls;
  return n;
}
function showLogin(message = "") {
  session = null;
  selected = null;
  $("workspace").hidden = true;
  $("identity").hidden = true;
  $("login").hidden = false;
  $("login-error").textContent = message;
  $("detail").replaceChildren();
}
async function api(path, options = {}) {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(session && options.method && options.method !== "GET"
        ? { "X-CSRF-Token": session.csrf_token }
        : {}),
      ...options.headers,
    },
  });
  let data = null;
  if (response.status !== 204) {
    try {
      data = await response.json();
    } catch {
      data = { error: "服务器响应无法读取" };
    }
  }
  if (!response.ok) {
    if (response.status === 401 && path !== "/console/login")
      showLogin("会话已过期，请重新登录。");
    const error = new Error(data?.error || "请求失败，请重试。");
    error.status = response.status;
    throw error;
  }
  return data;
}
function panel(title) {
  const p = element("section", undefined, "card");
  p.append(element("h2", title));
  $("detail").append(p);
  return p;
}
function table(parent, headers, rows) {
  if (!rows.length) {
    parent.append(element("p", "暂无数据", "muted"));
    return;
  }
  const wrap = element("div", undefined, "table-wrap"),
    t = element("table"),
    head = element("tr");
  for (const h of headers) head.append(element("th", h));
  t.append(head);
  for (const cells of rows) {
    const row = element("tr");
    for (const value of cells) {
      const cell = element("td");
      if (value instanceof Node) cell.append(value);
      else cell.textContent = value;
      row.append(cell);
    }
    t.append(row);
  }
  wrap.append(t);
  parent.append(wrap);
}
function projectPath() {
  return `/subdomains/${encodeURIComponent(selected.subdomain_name)}/projects/${encodeURIComponent(selected.project)}`;
}
function siteLink(label, text) {
  if (!label) return "—";
  const a = element("a", text);
  a.href = `https://${label}.${session.base_domain}`;
  a.target = "_blank";
  a.rel = "noopener noreferrer";
  return a;
}
async function mutation(project, path, method, body, message) {
  if (!window.confirm(message)) return;
  $("notice").textContent = "正在提交…";
  try {
    await api(path, { method, body: body ? JSON.stringify(body) : undefined });
    if (!session) return;
    await loadProjects();
    $("notice").className = "";
    $("notice").textContent =
      `${project.subdomain_name} / ${project.project}：操作完成，已刷新服务端状态。`;
  } catch (error) {
    showError(error);
  }
}
function actionButton(label, callback) {
  const button = element("button", label);
  button.addEventListener("click", async () => {
    button.disabled = true;
    try {
      await callback();
    } finally {
      button.disabled = false;
    }
  });
  return button;
}
function deploymentActions(project, base, d) {
  const actions = element("div", undefined, "actions");
  if (
    !["owner", "deployer"].includes(project.role) ||
    !d.available ||
    d.production
  )
    return actions;
  const operation = d.environment === "preview" ? "promote" : "rollback",
    label = operation === "promote" ? "发布为生产" : "回滚";
  actions.append(
    actionButton(`${label} v${d.version}`, () =>
      mutation(
        project,
        `${base}/${operation}/${d.version}`,
        "POST",
        null,
        `确认将 ${project.subdomain_name} / ${project.project} 的生产流量切换到 v${d.version}？`,
      ),
    ),
  );
  return actions;
}
function accessPanel(project, base, rule) {
  const p = panel("项目访问规则");
  p.append(
    element(
      "p",
      rule
        ? `IP：${(rule.allowed_ips || []).join(", ") || "不限"}；密码：${rule.has_password ? "已启用" : "未启用"}；企业微信：${rule.wework_enabled ? "已启用" : "未启用"}`
        : "未设置项目规则；站点仍可能继承子域名规则。",
      "muted",
    ),
  );
  if (project.role !== "owner") return;
  p.append(
    element(
      "p",
      "保存会完整替换本项目的规则。现有密码不会回填；如需保留密码保护，请重新输入密码。企业微信规则请使用 CLI 管理。",
      "muted",
    ),
  );
  const form = element("form"),
    ips = element("input"),
    password = element("input"),
    ttl = element("input");
  ips.id = "access-ips";
  ips.value = (rule?.allowed_ips || []).join(", ");
  password.id = "access-password";
  password.type = "password";
  password.autocomplete = "new-password";
  ttl.id = "access-ttl";
  ttl.type = "number";
  ttl.min = "300";
  ttl.max = "315360000";
  ttl.value = rule?.session_ttl || 86400;
  for (const [text, input] of [
    ["允许的 IP / CIDR（逗号分隔）", ips],
    ["站点密码（至少 8 字符）", password],
    ["访问会话秒数", ttl],
  ]) {
    const label = element("label", text);
    label.htmlFor = input.id;
    form.append(label, input);
  }
  const save = element("button", "保存访问规则");
  save.type = "submit";
  save.disabled = !!rule?.wework_enabled;
  form.append(save);
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    save.disabled = true;
    try {
      await mutation(
        project,
        base + "/access",
        "PUT",
        {
          allowed_ips: ips.value
            .split(",")
            .map((x) => x.trim())
            .filter(Boolean),
          password: password.value,
          session_ttl: Number(ttl.value),
        },
        `确认完整替换 ${project.subdomain_name} / ${project.project} 的访问规则？未重新输入的现有密码将被移除。`,
      );
    } finally {
      password.value = "";
      save.disabled = !!rule?.wework_enabled;
    }
  });
  p.append(form);
  if (rule)
    p.append(
      actionButton("删除项目规则", () =>
        mutation(
          project,
          base + "/access",
          "DELETE",
          null,
          `确认删除 ${project.subdomain_name} / ${project.project} 的项目访问规则？将恢复子域名继承规则或公开访问。`,
        ),
      ),
    );
}
async function fetchProjectDetail(base) {
  const [deployments, domains, stats, access, audit] = await Promise.allSettled(
    [
      api(base + "/deployments"),
      api(base + "/domains"),
      api(base + "/stats"),
      api(base + "/access").catch((error) => {
        if (error.status === 404) return null;
        throw error;
      }),
      api(base + "/audit?limit=50"),
    ],
  );
  return { deployments, domains, stats, access, audit };
}
function renderDetailResult(title, result, render) {
  if (result.status === "fulfilled") {
    render(result.value);
  } else {
    panel(title).append(element("p", result.reason.message, "error"));
  }
}
function deploymentsPanel(project, base, deployments) {
  table(
    panel("部署版本"),
    ["版本", "环境 / 状态", "时间", "访问", "错误", "操作"],
    deployments.map((deployment) => [
      `v${deployment.version}`,
      `${deployment.environment} / ${deployment.status}${deployment.production ? " · 生产" : ""}`,
      new Date(deployment.created_at).toLocaleString(),
      siteLink(
        deployment.preview_label || project.host_label,
        deployment.preview_label ? "打开预览" : "打开站点",
      ),
      deployment.failure_reason || "—",
      deploymentActions(project, base, deployment),
    ]),
  );
}
function domainsPanel(domains) {
  table(
    panel("自定义域名"),
    ["域名", "状态"],
    domains.map((domain) => [domain.domain, domain.status]),
  );
}
function statsPanel(data) {
  const p = panel("访问统计");
  const stats = element("div", undefined, "stats");
  for (const [label, value] of [
    ["浏览量", data.total_pv],
    ["访客数", data.total_uv],
  ]) {
    const metric = element("div", label);
    metric.append(element("strong", value ?? 0));
    stats.append(metric);
  }
  p.append(stats);
  table(
    p,
    ["路径", "浏览量", "访客数"],
    (data.pages || []).map((page) => [page.path, page.pv, page.uv]),
  );
}
function auditPanel(data) {
  const p = panel("操作审计");
  p.append(element("p", "最近 50 条操作记录", "muted"));
  table(
    p,
    ["时间", "操作者", "操作 / 目标", "结果"],
    (data.events || []).map((event) => [
      new Date(event.created_at).toLocaleString(),
      `${event.actor_kind}:${event.actor_id}`,
      `${event.action} / ${event.target}`,
      `${event.result} (${event.status_code})`,
    ]),
  );
}
function renderProjectDetail(project, base, detail) {
  renderDetailResult("部署版本", detail.deployments, (data) =>
    deploymentsPanel(project, base, data),
  );
  renderDetailResult("自定义域名", detail.domains, domainsPanel);
  renderDetailResult("访问统计", detail.stats, statsPanel);
  renderDetailResult("项目访问规则", detail.access, (data) =>
    accessPanel(project, base, data),
  );
  renderDetailResult("操作审计", detail.audit, auditPanel);
}
async function loadDetail(project) {
  selected = project;
  drawProjects();
  $("detail").replaceChildren();
  const title = panel(`${project.subdomain_name} / ${project.project}`);
  title.append(element("span", project.role, "pill"));
  title.append(document.createTextNode(" · "));
  title.append(siteLink(project.host_label, "打开生产站点"));
  const base = projectPath();
  const detail = await fetchProjectDetail(base);
  if (selected !== project || !session) return;
  renderProjectDetail(project, base, detail);
}
function drawProjects() {
  $("projects").replaceChildren();
  if (!projects.length) {
    $("projects").append(element("p", "暂无获授权项目", "muted"));
    return;
  }
  for (const project of projects) {
    const button = element(
      "button",
      `${project.subdomain_name} / ${project.project}`,
      "project" +
        (selected?.project_id === project.project_id ? " active" : ""),
    );
    button.append(element("span", project.role, "role"));
    button.addEventListener("click", () =>
      loadDetail(project).catch(showError),
    );
    $("projects").append(button);
  }
}
function showError(error) {
  $("notice").textContent = error.message;
  $("notice").className = "error";
}
async function loadProjects() {
  const list = await api("/projects");
  projects = list;
  drawProjects();
  if (selected) {
    const current = projects.find((p) => p.project_id === selected.project_id);
    if (current) await loadDetail(current);
    else {
      selected = null;
      $("detail").replaceChildren();
      panel("项目权限已变更");
    }
  }
}
async function startSession(data) {
  session = data;
  $("email").textContent = data.user.email;
  $("login").hidden = true;
  $("workspace").hidden = false;
  $("identity").hidden = false;
  $("notice").textContent = "";
  await loadProjects().catch(showError);
}
$("login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = event.currentTarget.querySelector("button");
  button.disabled = true;
  $("login-error").textContent = "";
  try {
    const data = await api("/console/login", {
      method: "POST",
      body: JSON.stringify({
        email: $("email-input").value,
        password: $("password-input").value,
      }),
    });
    $("password-input").value = "";
    await startSession(data);
  } catch (error) {
    $("login-error").textContent = error.message;
  } finally {
    button.disabled = false;
  }
});
$("logout").addEventListener("click", async () => {
  try {
    await api("/console/logout", { method: "POST" });
    showLogin();
  } catch (error) {
    showError(error);
  }
});
$("refresh").addEventListener("click", () => loadProjects().catch(showError));
api("/console/session")
  .then(startSession)
  .catch(() => showLogin());
