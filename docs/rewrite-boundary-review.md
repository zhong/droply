# 200 rewrite 循环边界评估

Issue #59 的结论：本轮不移除全部 200 rewrite 的后继边，保留现有发布校验。前提“所有 200 rewrite 都终结 HTTP 跳转”不成立；规则匹配终结与 HTTP 响应终结需要区分。

## 可复现反例

产物包含 `folder/index.html`，并设置：

```text
/folder /folder 200
```

`GET /folder` 返回 `301 Location: /folder/`，随后请求 `/folder/` 返回目录首页。规则本身标记为 200，但选中的路径仍等于原路径，现有静态文件处理会执行目录补斜杠。`TestSelfRewriteStillCanonicalizesDirectory` 从正常的配置加载入口和真实 HTTP handler 固定此行为。

这不是规则递归：处理器遇到 200 后停止规则匹配，而后续文件解析仍可产生 HTTP 跳转。不同源路径重写到目录（例如 `/alias/folder` 重写到 `/assets/folder`）直接返回文件；重写到缺失目标返回 404，即使目标本身能匹配另一条 3xx 规则，也不会继续匹配。`TestRewriteBoundaryWithoutRecursiveMatching` 覆盖通配符、存在/缺失目标及该区别。

## 保留的边界

现有 `TestInvalidDeploymentRulesFailValidation` 继续拒绝相互指向的缺失目标 200、3xx 多环和增长型通配符循环，以及路径穿越、隐藏目标和外部代理 rewrite。`TestDirectoryCanonicalizationCannotCreateRedirectLoop` 继续拒绝目录补斜杠与 3xx 组成的循环。`TestRedirectQueriesPrefixesAndTerminalRewrites` 保留常见 SPA、查询参数和不递归行为。

缺失目标间的 200 相互引用仍可能被保守拒绝，接受规则集合没有扩大。本轮测试没有证明一律删除 200 图边会引入新的可利用循环，也不声称发现了新的生产循环漏洞；它证明的是原提案所依赖的统一终结假设不成立。按照 Issue 的条件验收，选择不实施，而不是在本次评估中改变目录规范化语义或引入更复杂的路径分析器。后续若扩大规则集合，应明确区分“停止匹配规则”和“不会产生新的 HTTP 请求”，并将目录规范化纳入分析。

本次只有回归测试和评估记录，未修改生产代码或放松安全校验。
