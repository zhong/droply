// Contracts: internal/server/console.go, analytics.go, access.go, audit.go,
// and internal/model/{model,membership}.go.
export interface Session {
  user: { id: number; email: string };
  csrf_token: string;
  base_domain: string;
  expires_at: string;
}
export interface Project {
  project_id: number;
  subdomain_name: string;
  project: string;
  role: "owner" | "deployer" | "viewer";
  host_label: string;
}
export interface Deployment {
  id: number;
  version: number;
  environment: string;
  status: string;
  available: boolean;
  production: boolean;
  created_at: string;
  preview_label?: string;
  failure_reason?: string;
}
export interface Domain {
  id: number;
  domain: string;
  status: string;
}
export interface Stats {
  total_pv: number;
  total_uv: number;
  pages: { path: string; pv: number; uv: number }[];
}
export interface AccessRule {
  allowed_ips?: string[];
  has_password: boolean;
  wework_enabled: boolean;
  session_ttl: number;
}
export interface Audit {
  events:
    | {
        id: number;
        created_at: string;
        actor_kind: string;
        actor_id: number;
        action: string;
        target: string;
        result: string;
        status_code: number;
      }[]
    | null;
  next_cursor: number;
}
export class APIError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}
export function errorMessage(error: unknown): string {
  if (error instanceof APIError && error.status === 403)
    return "权限不足：当前账户无法执行此操作，请联系项目所有者。";
  if (error instanceof TypeError) return "无法连接服务器，请检查网络后重试。";
  return error instanceof Error ? error.message : "请求失败，请重试。";
}
export function createAPI(csrf = "", onExpired: () => void = () => {}) {
  return async function api<T>(
    path: string,
    options: RequestInit = {},
  ): Promise<T> {
    const headers = new Headers(options.headers);
    if (options.body) headers.set("Content-Type", "application/json");
    if (csrf && options.method && options.method !== "GET")
      headers.set("X-CSRF-Token", csrf);
    const response = await fetch(path, {
      ...options,
      credentials: "same-origin",
      headers,
    });
    if (response.status === 401 && path !== "/console/login") onExpired();
    if (response.status === 204) return undefined as T;
    const data = await response.json().catch(() => {
      throw new APIError("服务器响应无法读取，请重试。", response.status);
    });
    if (!response.ok)
      throw new APIError(data?.error || "请求失败，请重试。", response.status);
    return data as T;
  };
}
export type API = ReturnType<typeof createAPI>;
export const projectName = (project: Project) =>
  `${project.subdomain_name} / ${project.project}`;
export const projectPath = (project: Project) =>
  `/subdomains/${encodeURIComponent(project.subdomain_name)}/projects/${encodeURIComponent(project.project)}`;
export const roleNames = {
  owner: "所有者",
  deployer: "部署者",
  viewer: "只读成员",
};
