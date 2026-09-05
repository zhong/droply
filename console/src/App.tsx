import { useEffect, useMemo, useRef, useState } from "react";
import { Badge, Button, Empty, Input } from "@cloudflare/kumo";
import { ArrowClockwiseIcon, CubeIcon } from "@phosphor-icons/react";
import {
  APIError,
  createAPI,
  errorMessage,
  projectName,
  roleNames,
} from "./api";
import type { API, Project, Session } from "./api";
import { ErrorNotice, Loading } from "./ui";
import { ProjectDetail } from "./ProjectDetail";
import { ThemeToggle } from "./ThemeToggle";

export function App() {
  const [session, setSession] = useState<Session | null>();
  const [message, setMessage] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    createAPI()<Session>("/console/session", { signal: controller.signal })
      .then(setSession)
      .catch((error) => {
        if (controller.signal.aborted) return;
        setSession(null);
        if (!(error instanceof APIError && error.status === 401))
          setMessage(errorMessage(error));
      });
    return () => controller.abort();
  }, [attempt]);
  const api = useMemo(
    () =>
      createAPI(session?.csrf_token, () => {
        setSession(null);
        setMessage("会话已过期，请重新登录。");
      }),
    [session],
  );

  return (
    <>
      <a className="skip-link" href="#main">
        跳转到主要内容
      </a>
      <header className="app-header">
        <a href="/console/" className="brand">
          <CubeIcon size={26} weight="duotone" aria-hidden="true" />
          Droply
          <span className="text-sm font-normal text-kumo-subtle">控制台</span>
        </a>
        <ThemeToggle />
      </header>
      {session === undefined ? (
        <main id="main">
          <Loading text="正在检查登录状态…" />
        </main>
      ) : session ? (
        <Workspace
          key={session.csrf_token}
          session={session}
          api={api}
          onLogout={() => {
            setSession(null);
            setMessage("");
          }}
        />
      ) : (
        <main id="main" className="login-layout">
          <div className="login-intro">
            <Badge variant="outline">DROPLY CONSOLE</Badge>
            <h1>
              让每一次发布，
              <br />
              都清晰可控。
            </h1>
            <p>
              在同一个工作空间查看部署、管理访问规则，
              <br className="hidden md:block" />
              追踪站点表现与团队操作。
            </p>
          </div>
          <section className="panel login-card">
            <h2 className="text-2xl font-semibold">登录控制台</h2>
            <p className="text-kumo-subtle">使用团队账户访问获授权的项目。</p>
            {message && <ErrorNotice message={message} />}
            <Login
              onSession={(data) => {
                setSession(data);
                setMessage("");
              }}
            />
            {message && (
              <Button
                variant="ghost"
                onClick={() => {
                  setMessage("");
                  setSession(undefined);
                  setAttempt((value) => value + 1);
                }}
              >
                重新检查会话
              </Button>
            )}
          </section>
        </main>
      )}
    </>
  );
}

function Login({ onSession }: { onSession: (session: Session) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const [error, setError] = useState("");
  return (
    <form
      className="form-stack"
      onSubmit={async (event) => {
        event.preventDefault();
        if (lock.current) return;
        lock.current = true;
        setBusy(true);
        setError("");
        try {
          onSession(
            await createAPI()<Session>("/console/login", {
              method: "POST",
              body: JSON.stringify({ email, password }),
            }),
          );
        } catch (error) {
          setError(
            error instanceof APIError && error.status === 401
              ? "邮箱或密码不正确，请重试。"
              : errorMessage(error),
          );
        } finally {
          setPassword("");
          lock.current = false;
          setBusy(false);
        }
      }}
    >
      <Input
        label="邮箱"
        type="email"
        autoComplete="username"
        required
        value={email}
        onChange={(event) => setEmail(event.target.value)}
      />
      <Input
        label="密码"
        type="password"
        autoComplete="current-password"
        required
        value={password}
        onChange={(event) => setPassword(event.target.value)}
      />
      {error && <ErrorNotice message={error} />}
      <Button variant="primary" size="lg" type="submit" loading={busy}>
        登录
      </Button>
    </form>
  );
}

function Workspace({
  session,
  api,
  onLogout,
}: {
  session: Session;
  api: API;
  onLogout: () => void;
}) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [selectedID, setSelectedID] = useState<number>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [revision, setRevision] = useState(0);
  const [mutating, setMutating] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const logoutLock = useRef(false);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    api<Project[]>("/projects", { signal: controller.signal })
      .then(setProjects)
      .catch((error) => {
        if (!controller.signal.aborted) {
          setProjects([]);
          setError(errorMessage(error));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [api, revision]);
  const selected = projects.find(
    (project) => project.project_id === selectedID,
  );
  const filtered = projects.filter((project) =>
    projectName(project).toLowerCase().includes(query.toLowerCase()),
  );
  const busy = mutating || loggingOut;
  return (
    <div className="workspace">
      <aside className="sidebar" aria-label="项目导航">
        <div className="flex items-center justify-between">
          <h2 className="font-semibold">工作空间</h2>
          <Badge variant="secondary">{projects.length} 个项目</Badge>
        </div>
        <Input
          label="搜索项目"
          placeholder="搜索项目或子域名"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <nav aria-label="获授权项目" className="project-list">
          {loading ? (
            <Loading text="正在加载项目…" />
          ) : error ? (
            <>
              <ErrorNotice message={error} />
              <Button onClick={() => setRevision((value) => value + 1)}>
                重试
              </Button>
            </>
          ) : !projects.length ? (
            <Empty
              size="sm"
              title="暂无获授权项目"
              description="请联系所有者添加项目权限。"
            />
          ) : !filtered.length ? (
            <Empty
              size="sm"
              title="没有匹配的项目"
              description="试试其他项目名称。"
            />
          ) : (
            filtered.map((project) => (
              <Button
                className="project-button"
                key={project.project_id}
                variant={
                  selectedID === project.project_id ? "secondary" : "ghost"
                }
                aria-pressed={selectedID === project.project_id}
                aria-label={projectName(project)}
                disabled={busy}
                onClick={() => setSelectedID(project.project_id)}
              >
                <CubeIcon size={18} aria-hidden="true" />
                <span className="min-w-0 flex-1 text-left">
                  <span className="block break-words">
                    {projectName(project)}
                  </span>
                  <span className="text-xs font-normal text-kumo-subtle">
                    {roleNames[project.role]}
                  </span>
                </span>
              </Button>
            ))
          )}
        </nav>
        <div className="account">
          <p className="break-all text-sm text-kumo-subtle">
            {session.user.email}
          </p>
          <Button
            variant="ghost"
            disabled={mutating}
            loading={loggingOut}
            onClick={async () => {
              if (logoutLock.current) return;
              logoutLock.current = true;
              setLoggingOut(true);
              try {
                await api("/console/logout", { method: "POST" });
                onLogout();
              } catch (error) {
                setError(errorMessage(error));
              } finally {
                logoutLock.current = false;
                setLoggingOut(false);
              }
            }}
          >
            退出
          </Button>
        </div>
      </aside>
      <main id="main" className="main-content">
        <div className="page-heading">
          <div>
            <p className="eyebrow">项目管理</p>
            <h1>{selected ? projectName(selected) : "项目概览"}</h1>
          </div>
          <Button
            icon={ArrowClockwiseIcon}
            loading={loading}
            disabled={busy}
            onClick={() => setRevision((value) => value + 1)}
          >
            刷新
          </Button>
        </div>
        {loading ? (
          <Loading text="正在同步项目权限…" />
        ) : error ? (
          <ErrorNotice message={error} />
        ) : selected ? (
          <ProjectDetail
            key={`${selected.project_id}:${revision}`}
            project={selected}
            domain={session.base_domain}
            api={api}
            setMutating={setMutating}
          />
        ) : (
          <section className="panel">
            <Empty
              icon={<CubeIcon size={40} aria-hidden="true" />}
              title={
                selectedID
                  ? "项目权限已变更"
                  : projects.length
                    ? "选择一个项目开始"
                    : "暂无可管理的项目"
              }
              description={
                selectedID
                  ? "此项目已不可访问，请选择其他项目或联系所有者。"
                  : "从项目列表进入，查看部署版本、访问统计和操作记录。"
              }
            />
          </section>
        )}
      </main>
    </div>
  );
}
