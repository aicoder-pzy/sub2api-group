# 神鸡号池展示接口

管理入口：Sub2 后台 → 上游站点 → 公用 → 神鸡 → 号池展示。点「刷新号池」用该站点已保存的登录采集；失败时沿用最后一次完整快照。修改展示名称后立刻生效，清空名称并保存可恢复默认名称。

## 对接

- 接口：`GET /api/v1/integrations/account-pool`。
- 服务端通过 `Authorization: Bearer <专用导出令牌>` 访问；不要把令牌装入浏览器代码、URL 或日志。它不是 Sub2 登录凭证、推理 Key 或神鸡授权。
- Sub2 容器环境设置 `SUB2_ACCOUNT_POOL_SITE_ID` 为神鸡公用站点的 UUID，`SUB2_ACCOUNT_POOL_EXPORT_TOKEN` 为独立随机令牌（至少 32 字符）。不配置则接口关闭。设置变化随 Sub2 应用重启生效。
- 返回 `{ "code": 0, "message": "success", "data": { "updated": null, "pools": [], "rows": [] } }`。`updated` 为最近成功刷新的 RFC3339 时间，`null` 表示尚无快照。
- `pools` 项有 `id`、`name`、`count`；`rows` 项有快照内编号 `number`、`platform`、`plan`、`status`，以及按可用情况出现的 `current`、`limit`、`windows`、`pools`、`last_used`。号池 `id` 由 Sub2 分配并跨刷新保留，`rows[].pools` 只引用这些展示 ID。窗口 `used` 为上游所报百分比，例如 `25` 表示 25%。账号编号不是上游账号 ID，不保证跨刷新恒定。
- 不返回原分组名称、源账号／分组 ID、倍率、账号邮箱或登录凭证。导出只读取快照，不触发对上游的请求。HTTPS 必须开启，响应禁止缓存。

本变更只包含 Sub2；NewAPI 不在施工范围内。
