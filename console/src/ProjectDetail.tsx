import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Badge, Banner, Button, Input } from "@cloudflare/kumo";
import type {
  API,
  AccessRule,
  Audit,
  Deployment,
  Domain,
  Project,
  Stats,
} from "./api";
import {
  APIError,
  errorMessage,
  projectName,
  projectPath,
  roleNames,
} from "./api";
import {
  DataTable,
  ErrorNotice,
  Loading,
  Panel,
  SiteLink,
  dateTime,
} from "./ui";

async function fetchDetail(api: API, base: string, signal: AbortSignal) {
  const [deployments, domains, stats, access, audit] = await Promise.allSettled(
    [
      api<Deployment[]>(base + "/deployments", { signal }),
      api<Domain[]>(base + "/domains", { signal }),
      api<Stats>(base + "/stats", { signal }),
      api<AccessRule | null>(base + "/access", { signal }).catch((error) => {
        if (error instanceof APIError && error.status === 404) return null;
        throw error;
      }),
      api<Audit>(base + "/audit?limit=50", { signal }),
    ],
  );
  return { deployments, domains, stats, access, audit };
}
type Detail = Awaited<ReturnType<typeof fetchDetail>>;
type Mutate = (
  path: string,
  method: string,
  body: unknown,
  confirmation: string,
) => Promise<void>;

function Result<T>({
  title,
  result,
  children,
}: {
  title: string;
  result?: PromiseSettledResult<T>;
  children: (data: T) => ReactNode;
}) {
  return (
    <Panel title={title}>
      {!result ? (
        <Loading />
      ) : result.status === "rejected" ? (
        <ErrorNotice message={errorMessage(result.reason)} />
      ) : (
        children(result.value)
      )}
    </Panel>
  );
}

export function ProjectDetail({
  project,
  domain,
  api,
  setMutating,
}: {
  project: Project;
  domain: string;
  api: API;
  setMutating: (busy: boolean) => void;
}) {
  const [detail, setDetail] = useState<Detail>();
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ error: boolean; text: string }>();
  const lock = useRef(false);
  const controller = useRef<AbortController | null>(null);
  const base = projectPath(project);
  useEffect(() => {
    const abort = new AbortController();
    controller.current = abort;
    fetchDetail(api, base, abort.signal).then((data) => {
      if (!abort.signal.aborted) setDetail(data);
    });
    return () => abort.abort();
  }, [api, base]);
  const mutate: Mutate = async (path, method, body, confirmation) => {
    if (lock.current || !window.confirm(confirmation)) return;
    lock.current = true;
    setBusy(true);
    setMutating(true);
    setNotice(undefined);
    const signal = controller.current!.signal;
    try {
      await api(path, {
        method,
        body: body === null ? undefined : JSON.stringify(body),
        signal,
      });
      const data = await fetchDetail(api, base, signal);
      if (signal.aborted) return;
      setDetail(data);
      const partial = Object.values(data).some(
        (result) => result.status === "rejected",
      );
      setNotice({
        error: false,
        text: partial
          ? `${projectName(project)}：操作已完成，但部分数据刷新失败，请刷新重试。`
          : `${projectName(project)}：操作完成，已刷新服务端状态。`,
      });
    } catch (error) {
      if (!signal.aborted)
        setNotice({ error: true, text: errorMessage(error) });
    } finally {
      lock.current = false;
      setBusy(false);
      setMutating(false);
    }
  };
  return (
    <div className="detail-stack" aria-busy={busy}>
      <div className="flex flex-wrap items-center gap-3">
        <Badge variant="outline">{roleNames[project.role]}</Badge>
        <SiteLink label={project.host_label} domain={domain}>
          打开生产站点
        </SiteLink>
      </div>
      {project.role === "viewer" && (
        <Banner
          title="当前为只读权限"
          description="可以查看项目数据；发布和回滚需要部署者或所有者权限。"
        />
      )}
      {notice &&
        (notice.error ? (
          <ErrorNotice message={notice.text} />
        ) : (
          <Banner role="status" title={notice.text} />
        ))}
      {busy && <Loading text="正在提交并同步服务端状态…" />}
      <Result title="部署版本" result={detail?.deployments}>
        {(data) => (
          <DataTable
            label="部署版本"
            headers={["版本", "环境 / 状态", "时间", "访问", "错误", "操作"]}
            rows={data.map((deployment) => {
              const promote = deployment.environment === "preview";
              const eligible =
                ["active", "archived"].includes(deployment.status) ||
                (promote && deployment.status === "preview");
              return [
                <span className="font-semibold">v{deployment.version}</span>,
                <div className="flex flex-wrap items-center gap-2">
                  <span>
                    {promote ? "预览" : "生产"} / {deployment.status}
                  </span>
                  {deployment.production && (
                    <Badge variant="success">当前生产</Badge>
                  )}
                </div>,
                dateTime(deployment.created_at),
                <SiteLink
                  label={deployment.preview_label || project.host_label}
                  domain={domain}
                >
                  {deployment.preview_label ? "打开预览" : "打开站点"}
                </SiteLink>,
                deployment.failure_reason || "—",
                project.role !== "viewer" &&
                deployment.available &&
                !deployment.production &&
                eligible ? (
                  <Button
                    size="sm"
                    disabled={busy}
                    onClick={() =>
                      void mutate(
                        `${base}/${promote ? "promote" : "rollback"}/${deployment.version}`,
                        "POST",
                        null,
                        `确认将 ${projectName(project)} 的生产流量切换到 v${deployment.version}？`,
                      )
                    }
                  >
                    {promote ? "发布为生产" : "回滚"} v{deployment.version}
                  </Button>
                ) : (
                  "—"
                ),
              ];
            })}
          />
        )}
      </Result>
      <div className="summary-grid">
        <Result title="访问统计" result={detail?.stats}>
          {(data) => (
            <>
              <p className="text-sm text-kumo-subtle">最近 30 天</p>
              <div className="metrics">
                <div>
                  <span>浏览量</span>
                  <strong>{data.total_pv.toLocaleString("zh-CN")}</strong>
                </div>
                <div>
                  <span>访客数</span>
                  <strong>{data.total_uv.toLocaleString("zh-CN")}</strong>
                </div>
              </div>
              <DataTable
                label="页面访问统计"
                headers={["路径", "浏览量", "访客数"]}
                rows={(data.pages || []).map((page) => [
                  page.path,
                  page.pv,
                  page.uv,
                ])}
              />
            </>
          )}
        </Result>
        <Result title="自定义域名" result={detail?.domains}>
          {(data) => (
            <DataTable
              label="自定义域名"
              headers={["域名", "状态"]}
              rows={data.map((item) => [item.domain, item.status])}
            />
          )}
        </Result>
      </div>
      <Result title="项目访问规则" result={detail?.access}>
        {(rule) => (
          <Access project={project} rule={rule} busy={busy} mutate={mutate} />
        )}
      </Result>
      <Result title="操作审计" result={detail?.audit}>
        {(data) => (
          <>
            <p className="text-sm text-kumo-subtle">最近 50 条操作记录</p>
            <DataTable
              label="操作审计"
              headers={["时间", "操作者", "操作 / 目标", "结果"]}
              rows={(data.events || []).map((event) => [
                dateTime(event.created_at),
                `${event.actor_kind}:${event.actor_id}`,
                `${event.action} / ${event.target}`,
                <Badge
                  variant={
                    event.result === "success"
                      ? "success"
                      : event.result === "failure"
                        ? "error"
                        : "secondary"
                  }
                >
                  {event.result} ({event.status_code})
                </Badge>,
              ])}
            />
          </>
        )}
      </Result>
    </div>
  );
}

function Access({
  project,
  rule,
  busy,
  mutate,
}: {
  project: Project;
  rule: AccessRule | null;
  busy: boolean;
  mutate: Mutate;
}) {
  const [ips, setIPs] = useState((rule?.allowed_ips || []).join(", "));
  const [password, setPassword] = useState("");
  const [ttl, setTTL] = useState(String(rule?.session_ttl || 86400));
  useEffect(() => {
    setIPs((rule?.allowed_ips || []).join(", "));
    setTTL(String(rule?.session_ttl || 86400));
  }, [rule]);
  const base = projectPath(project);
  return (
    <>
      <p className="text-sm text-kumo-subtle">
        {rule
          ? `IP：${(rule.allowed_ips || []).join(", ") || "不限"}；密码：${rule.has_password ? "已启用" : "未启用"}；企业微信：${rule.wework_enabled ? "已启用" : "未启用"}`
          : "未设置项目规则；站点仍可能继承子域名规则。"}
      </p>
      {project.role === "owner" ? (
        <>
          <Banner
            variant="alert"
            title="保存将完整替换项目规则"
            description="现有密码不会回填；如需保留密码保护，请重新输入密码。企业微信规则请使用 CLI 管理。"
          />
          <form
            className="form-stack"
            onSubmit={async (event) => {
              event.preventDefault();
              try {
                await mutate(
                  base + "/access",
                  "PUT",
                  {
                    allowed_ips: ips
                      .split(",")
                      .map((ip) => ip.trim())
                      .filter(Boolean),
                    password,
                    session_ttl: Number(ttl),
                  },
                  `确认完整替换 ${projectName(project)} 的访问规则？未重新输入的现有密码将被移除。`,
                );
              } finally {
                setPassword("");
              }
            }}
          >
            <fieldset
              disabled={busy || !!rule?.wework_enabled}
              className="access-fields"
            >
              <Input
                label="允许的 IP / CIDR（逗号分隔）"
                value={ips}
                onChange={(event) => setIPs(event.target.value)}
              />
              <Input
                label="站点密码（至少 8 字符）"
                type="password"
                autoComplete="new-password"
                minLength={8}
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
              <Input
                label="访问会话秒数"
                type="number"
                min={300}
                max={315360000}
                required
                value={ttl}
                onChange={(event) => setTTL(event.target.value)}
              />
              <Button
                variant="primary"
                type="submit"
                disabled={busy || !!rule?.wework_enabled}
              >
                保存访问规则
              </Button>
            </fieldset>
          </form>
          {rule?.wework_enabled && (
            <p className="text-sm text-kumo-subtle">
              已启用企业微信，暂不支持在控制台编辑此规则。
            </p>
          )}
          {rule && (
            <Button
              variant="secondary-destructive"
              disabled={busy}
              onClick={() =>
                void mutate(
                  base + "/access",
                  "DELETE",
                  null,
                  `确认删除 ${projectName(project)} 的项目访问规则？将恢复子域名继承规则或公开访问。`,
                )
              }
            >
              删除项目规则
            </Button>
          )}
        </>
      ) : (
        <p className="text-sm text-kumo-subtle">
          仅项目所有者可以修改访问规则。
        </p>
      )}
    </>
  );
}
