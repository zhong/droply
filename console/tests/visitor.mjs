import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";

const { chromium } = await import(
  pathToFileURL(
    process.env.DROPLY_PLAYWRIGHT_PATH ||
      "/tmp/droply-console-browser-deps/node_modules/playwright/index.mjs",
  )
);
const origins = process.argv.slice(2);
assert.equal(
  origins.length,
  4,
  "Pass production, legacy, preview and custom HTTPS origins",
);
const browser = await chromium.launch({
  headless: true,
  args: [
    "--host-resolver-rules=MAP *.localhost 127.0.0.1",
    "--no-proxy-server",
  ],
});
try {
  for (const [index, origin] of origins.entries()) {
    const context = await browser.newContext({
      ignoreHTTPSErrors: true,
      colorScheme: "light",
    });
    const page = await context.newPage();
    page.setDefaultTimeout(10000);
    page.setDefaultNavigationTimeout(10000);
    const errors = [];
    const consoleRequests = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("request", (request) => {
      if (new URL(request.url()).pathname.startsWith("/console/"))
        consoleRequests.push(request.url());
    });
    await page.addInitScript(() => {
      window.cspViolations = [];
      document.addEventListener("securitypolicyviolation", (event) =>
        window.cspViolations.push(event.violatedDirective),
      );
    });
    const path = index === 1 ? "/site/" : "/";
    const destination = `${origin}${path}?lang=zh&next=%2Fintro%3Fa%3D1`;
    const response = await page.goto(destination);
    assert.equal(response.status(), 200);
    assert.ok(response.headers()["cache-control"].includes("no-store"));
    if (index === 2)
      assert.equal(response.headers()["x-robots-tag"], "noindex, nofollow");
    await page.getByRole("button", { name: "显示密码", exact: true }).waitFor();
    const password = page.getByLabel("访问密码", { exact: true });
    const submit = page.getByRole("button", {
      name: "验证并访问",
      exact: true,
    });
    const wework = new URL(
      await page
        .getByRole("link", { name: "使用企业微信登录" })
        .getAttribute("href"),
      origin,
    );
    assert.equal(wework.searchParams.get("host"), new URL(origin).host);
    assert.equal(
      wework.searchParams.get("redirect"),
      new URL(destination).pathname + new URL(destination).search,
    );

    if (index === 0) {
      const screenshot = process.env.DROPLY_VISITOR_SCREENSHOT;
      if (screenshot)
        await page.screenshot({ path: screenshot, fullPage: true });
      const light = await page
        .locator("body")
        .evaluate((node) => getComputedStyle(node).backgroundColor);
      await page.getByRole("button", { name: "切换深色主题" }).focus();
      await page.keyboard.press("Enter");
      await page.waitForFunction(
        () => document.documentElement.dataset.mode === "dark",
      );
      assert.notEqual(
        await page
          .locator("body")
          .evaluate((node) => getComputedStyle(node).backgroundColor),
        light,
      );
      if (screenshot)
        await page.screenshot({
          path: screenshot.replace(".png", "-dark.png"),
          fullPage: true,
        });
      await page.setViewportSize({ width: 390, height: 844 });
      assert.ok(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      );
      if (screenshot)
        await page.screenshot({
          path: screenshot.replace(".png", "-mobile.png"),
          fullPage: true,
        });
      await password.fill("incorrect");
      await page.getByRole("button", { name: "显示密码", exact: true }).click();
      assert.equal(await password.getAttribute("type"), "text");
      await page.getByRole("button", { name: "隐藏密码", exact: true }).click();
      assert.equal(await password.getAttribute("type"), "password");
      await password.focus();
      await page.keyboard.press("Enter");
      await page
        .getByRole("alert")
        .filter({ hasText: "密码不正确，请重试。" })
        .waitFor();
      await page
        .getByRole("button", { name: "显示密码", exact: true })
        .waitFor();
      assert.equal(await password.inputValue(), "");
      assert.equal(
        await page.locator('input[name="redirect"]').inputValue(),
        new URL(destination).pathname + new URL(destination).search,
      );
    }

    await password.fill("visitor-password");
    assert.deepEqual(await page.evaluate(() => window.cspViolations), []);
    let requests = 0;
    let release;
    let started;
    const held = new Promise((resolve) => {
      release = resolve;
    });
    const requested = new Promise((resolve) => {
      started = resolve;
    });
    await page.route("**/_droply/login", async (route) => {
      requests++;
      started();
      await held;
      await route.continue();
    });
    await submit.evaluate((button) => {
      button.form.requestSubmit();
      button.form.requestSubmit();
    });
    await requested;
    // A native POST unloads the old document; inspect the request count rather
    // than querying its DOM while the destination response is deliberately held.
    assert.equal(requests, 1);
    release();
    await page.waitForURL(destination);
    assert.equal(
      (await page.locator("body").innerText()).trim(),
      index === 2 ? "preview" : "production",
    );
    const cookie = (await context.cookies()).find(
      (cookie) => cookie.name === "_droply_access",
    );
    assert.ok(cookie.httpOnly && cookie.secure);
    assert.equal(
      await page.evaluate(() => document.cookie.includes("_droply_access")),
      false,
    );
    assert.deepEqual(consoleRequests, []);
    assert.deepEqual(errors, []);
    await context.close();
  }

  // The server-rendered form remains usable with JavaScript disabled.
  const fallback = await browser.newContext({
    ignoreHTTPSErrors: true,
    javaScriptEnabled: false,
  });
  const page = await fallback.newPage();
  const destination = origins[0] + "/?lang=zh&next=%2Fintro%3Fa%3D1";
  await page.goto(destination);
  await page.getByLabel("访问密码", { exact: true }).fill("incorrect");
  await page.getByRole("button", { name: "验证并访问", exact: true }).click();
  await page
    .getByRole("alert")
    .filter({ hasText: "密码不正确，请重试。" })
    .waitFor();
  await page.getByLabel("访问密码", { exact: true }).fill("visitor-password");
  await page.getByRole("button", { name: "验证并访问", exact: true }).click();
  await page.waitForURL(destination);
  assert.equal((await page.locator("body").innerText()).trim(), "production");
  await fallback.close();
  console.log(
    "PASS visitor Kumo login, wrong password retry, original URL, production/preview/legacy/custom hosts, WeCom links, no-JS form, CSP, themes, keyboard, mobile and duplicate submission",
  );
} finally {
  await browser.close();
}
