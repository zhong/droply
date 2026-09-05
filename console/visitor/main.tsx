import { StrictMode, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { Badge, Banner, Button, Input, LinkButton } from "@cloudflare/kumo";
import { CubeIcon, LockKeyIcon } from "@phosphor-icons/react";
import { ThemeToggle } from "../src/ThemeToggle";
import "./style.css";

// These public presentation values are escaped by Go's html/template.
// Passwords and visitor cookies are never injected into the page.
const root = document.getElementById("visitor-root")!;
const {
  host = "",
  redirect = "/",
  error = "",
  password,
  wework,
} = root.dataset;

function VisitorLogin() {
  const [busy, setBusy] = useState(false);
  const [visible, setVisible] = useState(false);
  const submitting = useRef(false);
  useEffect(() => {
    const reset = (event: PageTransitionEvent) => {
      if (!event.persisted) return;
      submitting.current = false;
      setBusy(false);
    };
    window.addEventListener("pageshow", reset);
    return () => window.removeEventListener("pageshow", reset);
  }, []);
  const showPassword = password === "true";
  const showWeWork = wework === "true";
  const weworkURL = `/_droply/wework/auth?${new URLSearchParams({ redirect, host })}`;
  return (
    <>
      <header className="app-header">
        <div className="brand">
          <CubeIcon size={26} weight="duotone" aria-hidden="true" />
          Droply
        </div>
        <ThemeToggle />
      </header>
      <main className="visitor-layout">
        <section className="panel visitor-card" aria-labelledby="visitor-title">
          <div className="flex items-center justify-between gap-4">
            <div className="rounded-xl bg-kumo-tint p-3">
              <LockKeyIcon size={28} aria-hidden="true" />
            </div>
            <Badge variant="outline">受保护的站点</Badge>
          </div>
          <div>
            <h1 id="visitor-title">验证后继续访问</h1>
            <p className="mt-3 leading-7 text-kumo-subtle">
              此页面已启用访问保护。
              {showPassword
                ? "请输入访问密码，以查看分享的内容。"
                : "请使用企业微信验证身份。"}
            </p>
          </div>
          <p className="break-all rounded-lg bg-kumo-tint px-4 py-3 text-sm text-kumo-subtle">
            {host}
          </p>
          {error && <Banner variant="error" role="alert" title={error} />}
          {showPassword && (
            <form
              method="POST"
              action="/_droply/login"
              className="form-stack"
              aria-busy={busy}
              onSubmit={(event) => {
                if (submitting.current) {
                  event.preventDefault();
                  return;
                }
                // Submit the existing native form, preserving redirects and HttpOnly cookies.
                submitting.current = true;
                setBusy(true);
              }}
            >
              <Input
                label="访问密码"
                name="password"
                type={visible ? "text" : "password"}
                placeholder="输入访问密码"
                autoComplete="current-password"
                autoFocus
                required
                readOnly={busy}
              />
              <Button
                type="button"
                size="sm"
                variant="ghost"
                className="self-end"
                aria-pressed={visible}
                disabled={busy}
                onClick={() => setVisible(!visible)}
              >
                {visible ? "隐藏密码" : "显示密码"}
              </Button>
              <input type="hidden" name="redirect" value={redirect} />
              <input type="hidden" name="host" value={host} />
              <Button
                type="submit"
                className="w-full justify-center"
                variant="primary"
                size="lg"
                loading={busy}
                aria-label={busy ? "正在验证…" : "验证并访问"}
              >
                {busy ? "正在验证…" : "验证并访问"}
              </Button>
            </form>
          )}
          {showPassword && showWeWork && (
            <p className="text-center text-sm text-kumo-subtle">
              或使用企业身份验证
            </p>
          )}
          {showWeWork && (
            <LinkButton
              href={weworkURL}
              variant="secondary"
              size="lg"
              className="w-full justify-center"
              disabled={busy}
            >
              使用企业微信登录
            </LinkButton>
          )}
          {!showPassword && !showWeWork && (
            <Banner
              variant="alert"
              role="alert"
              title="暂时无法验证访问权限，请联系站点管理员。"
            />
          )}
          <p className="border-t border-kumo-hairline pt-5 text-sm leading-6 text-kumo-subtle">
            如需获取访问权限，请联系分享此链接的人。
          </p>
        </section>
      </main>
    </>
  );
}

createRoot(root).render(
  <StrictMode>
    <VisitorLogin />
  </StrictMode>,
);
