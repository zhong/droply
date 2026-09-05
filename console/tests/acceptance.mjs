import assert from "node:assert/strict";

// Runs against the existing real HTTPS/SQLite Go fixture, after owner mutations.
export async function checkConsoleUI(page) {
  const refresh = page.getByRole("button", { name: "刷新", exact: true });
  const project = page.getByRole("button", {
    name: "team / site",
    exact: true,
  });
  const production = page.getByText("当前生产", { exact: true });
  await production.waitFor();

  // Keyboard interaction and theme tokens must work under the server's CSP.
  const background = await page
    .locator("body")
    .evaluate((node) => getComputedStyle(node).backgroundColor);
  const theme = page.getByRole("button", { name: "切换深色主题", exact: true });
  await theme.focus();
  await page.keyboard.press("Enter");
  await page.waitForFunction(
    () => document.documentElement.dataset.mode === "dark",
  );
  const dark = await page.locator("body").evaluate((node) => ({
    background: getComputedStyle(node).backgroundColor,
    color: getComputedStyle(node).color,
    scheme: getComputedStyle(node).colorScheme,
  }));
  assert.notEqual(dark.background, background);
  assert.notEqual(dark.background, dark.color);
  assert.ok(dark.scheme.includes("dark"));
  if (process.env.DROPLY_CONSOLE_SCREENSHOT)
    await page.screenshot({
      path: process.env.DROPLY_CONSOLE_SCREENSHOT.replace(".png", "-dark.png"),
      fullPage: true,
    });
  await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();

  await page.setViewportSize({ width: 390, height: 844 });
  assert.equal(await page.locator("html").getAttribute("lang"), "zh-CN");
  assert.ok(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "Chinese mobile layout must not overflow the viewport",
  );
  if (process.env.DROPLY_CONSOLE_SCREENSHOT)
    await page.screenshot({
      path: process.env.DROPLY_CONSOLE_SCREENSHOT.replace(
        ".png",
        "-mobile.png",
      ),
      fullPage: true,
    });
  await page.getByRole("region", { name: "部署版本", exact: true }).focus();
  await page.keyboard.press("ArrowRight");
  assert.equal(
    await page.evaluate(() =>
      document.activeElement?.getAttribute("aria-label"),
    ),
    "部署版本",
  );
  await page.setViewportSize({ width: 1280, height: 900 });

  // A failing panel must not hide healthy deployments; refresh recovers it.
  await page.route("**/stats", (route) =>
    route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({ error: "forbidden" }),
    }),
  );
  await refresh.click();
  await page
    .getByText("权限不足：当前账户无法执行此操作，请联系项目所有者。", {
      exact: true,
    })
    .waitFor();
  await production.waitFor();
  await page.unroute("**/stats");
  await page.route("**/domains", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: "域名服务暂不可用" }),
    }),
  );
  await refresh.click();
  await page.getByText("域名服务暂不可用", { exact: true }).waitFor();
  await production.waitFor();
  await page.unroute("**/domains");

  // Hold a request to observe loading, then switch projects before it completes.
  let release;
  const held = new Promise((resolve) => {
    release = resolve;
  });
  let entered;
  const requested = new Promise((resolve) => {
    entered = resolve;
  });
  await page.route("**/site/stats", async (route) => {
    entered();
    await held;
    await route.continue().catch(() => {});
  });
  await refresh.click();
  await requested;
  await page.getByText("正在加载…", { exact: true }).first().waitFor();
  await page
    .getByRole("button", { name: "team / secret", exact: true })
    .focus();
  await page.keyboard.press("Enter");
  await page
    .getByRole("heading", { name: "team / secret", exact: true })
    .waitFor();
  release();
  await page.unroute("**/site/stats");
  await page.waitForFunction(
    () => !document.body.innerText.includes("正在加载…"),
  );
  assert.equal(
    await production.count(),
    0,
    "stale site response must not populate secret project",
  );
  await project.click();
  await production.waitFor();

  // Pending writes lock all competing actions, including a second click.
  let finish;
  const gate = new Promise((resolve) => {
    finish = resolve;
  });
  let started;
  const submitted = new Promise((resolve) => {
    started = resolve;
  });
  let requests = 0;
  await page.route("**/promote/2", async (route) => {
    requests++;
    started();
    await gate;
    await route.continue();
  });
  const promote = page.getByRole("button", {
    name: "发布为生产 v2",
    exact: true,
  });
  page.once("dialog", (dialog) => dialog.accept());
  await promote.click();
  await submitted;
  assert.equal(await promote.isDisabled(), true);
  assert.equal(await refresh.isDisabled(), true);
  assert.equal(await project.isDisabled(), true);
  assert.equal(
    await page.getByRole("button", { name: "退出", exact: true }).isDisabled(),
    true,
  );
  assert.equal(
    await page
      .getByRole("button", { name: "保存访问规则", exact: true })
      .isDisabled(),
    true,
  );
  await promote.evaluate((button) => button.click());
  assert.equal(requests, 1);
  finish();
  await page
    .getByText("team / site：操作完成，已刷新服务端状态。", { exact: true })
    .waitFor();
  await page.unroute("**/promote/2");
  console.log(
    "PASS Kumo themes, Chinese mobile layout, keyboard controls, partial failures, loading, stale requests and duplicate submission lock",
  );
}
