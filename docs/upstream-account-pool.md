# 神鸡号池展示接口

秋 API 公用站点可以查看已保存的号池快照。目前仅神鸡自动采集，秋 API 暂停手动和自动采集；私用站点不启用。

管理入口：Sub2 后台 → 上游站点 → 公用 → 神鸡 → 号池展示。点「刷新号池」用该站点已保存的登录在后台限速采集，按上游 `Retry-After` 退避。若上游分页重叠，最多再补读一轮；总数变化或覆盖不足时沿用最后一次完整快照。修改展示名称后立刻生效，清空名称并保存可恢复默认名称。

## 号池管理与独立导出

- 后台「号池管理」可选择上游分组、对外名称、套餐映射和套餐白名单。管理员预览与对外接口使用同一套筛选和状态数据，另仅在管理员预览中提供原始账号名称和 ID。未启用导出时也能预览。神鸡正常约每 10 分钟启动一次后台采集，页面和导出只读最后一次完整快照。
- 对接服务端使用 `GET /api/v1/integrations/account-pool-status?page=1&page_size=20`，请求头为 `Authorization: Bearer <独立导出令牌>`。令牌在后台启用导出时生成，不放进浏览器。接口默认关闭，关闭时返回 `404`，令牌错误返回 `401`。响应含 `Cache-Control: no-store`。
- `data` 含 `pools`、`page` 和 `page_size`，每个号池独立分页。号池含 `id`、`source`、`name`、`snapshot_available`、`updated`、`checked_at`、`total`、`pages`、`plans`、`statuses` 和 `rows`。套餐与状态统计覆盖全部符合配置的账号，不受分页影响。`updated` 是快照的更新时间，`checked_at` 是本次完整采集完成时间。没有当前快照时 `snapshot_available` 为 `false`，`updated` 为 `null`。
- 账号字段：`number` 是当前快照中按排序分配的展示编号；`platform`、`account_type`、`plan`、`status` 用于展示。`plan` 是映射后的公开套餐名。`current` 和 `limit` 是采集到的当前并发和上限；`windows[].name` 标识额度窗口，`windows[].used` 是使用百分比，`25` 表示 25%。`reset_5h`、`reset_7d`、`recover_at` 分别表示额度重置及状态预计恢复时间。
- `last_used` 是上游报告的最近使用，`usage_updated` 是上游报告的额度更新时间，`observed_at` 是采集到账号状态的时间。`activity_at` 是与上游站点页同口径的展示及排序时间，`activity_kind` 为 `usage_updated`、`active_concurrency` 或 `last_used`。上游未给的数据不返回，`0` 保留实际含义；时间均为 RFC3339。不要把采集时正在使用的观测时间当作上游报告的最后使用时间。
- 只返回配置选中的号池与套餐。邮箱、原始账号和分组标识、凭证和采购倍率保留在站内。对外接口不触发采集。

## 旧版神鸡接口

- 接口：`GET /api/v1/integrations/account-pool`。
- 服务端通过 `Authorization: Bearer <专用导出令牌>` 访问；不要把令牌装入浏览器代码、URL 或日志。它不是 Sub2 登录凭证、推理 Key 或神鸡授权。
- Sub2 容器环境设置 `SUB2_ACCOUNT_POOL_SITE_ID` 为神鸡公用站点的 UUID，`SUB2_ACCOUNT_POOL_EXPORT_TOKEN` 为独立随机令牌（至少 32 字符）。不配置则接口关闭。设置变化随 Sub2 应用重启生效。
- 返回 `{ "code": 0, "message": "success", "data": { "updated": null, "pools": [], "rows": [] } }`。`updated` 为最近成功刷新的 RFC3339 时间，`null` 表示尚无快照。
- `pools` 项有 `id`、`name`、`count`；`rows` 项有快照内编号 `number`、`platform`、`plan`、`status`，以及按可用情况出现的 `current`、`limit`、`windows`、`pools`、`last_used`。号池 `id` 由 Sub2 分配并跨刷新保留，`rows[].pools` 只引用这些展示 ID。窗口 `used` 为上游所报百分比，例如 `25` 表示 25%。账号编号不是上游账号 ID，不保证跨刷新恒定。
- 不返回原分组名称、源账号／分组 ID、倍率、账号邮箱或登录凭证。导出只读取快照，不触发对上游的请求。HTTPS 必须开启，响应禁止缓存。

## 聚合 Sub2 分组的上游号池

- 管理员可从「分组管理 → 号池预览」查看与导出一致的结果，不需要启用导出或配置令牌。
- `GET /api/v1/integrations/account-pools/{group_id}?page=1&page_size=20`，只在 Sub2 提供接口。以 Sub2 分组的当前名称为展示名。
- Sub2 容器环境设置 `SUB2_ACCOUNT_POOLS_GROUP_IDS`（明确允许的 Sub2 分组 ID，逗号分隔）和 `SUB2_ACCOUNT_POOLS_EXPORT_TOKEN`（至少 32 字符的独立随机令牌，不可复用旧接口令牌）。默认关闭。仅对接服务端通过 `Authorization: Bearer <令牌>` 使用，禁止把令牌放进浏览器；HTTPS，响应 `Cache-Control: no-store`。
- 按组内启用且可调度的接入账号，取公用神鸡、秋 API 的 Key 最近一次成功同步的上游分组 ID，只合并对应分组的 OAuth 账号。同站账号只计一次，跨站不猜测身份。私用及其他上游不计入。组内限流等实时调度状态不改变展示成员。
- `data` 包含 `group_id`、`name`、`total`、`plans`（`code`、`name`、`count`）、`rows`、`page`、`page_size`（上限 1000）、`pages`。套餐统计为全组统计；神鸡 `promax` / `Pro Max` 与秋 API `Pro 500` 统一为 `pro_500`，显示 `Pro 500`。行字段与旧接口一致（无 `pools`），另有 `account_type: "oauth"` 和 `plan_code`。账号编号仅在当前聚合结果内有效。不会返回上游来源、原分组、邮箱、凭证或倍率。
- `updated` 和 `verified_at` 分别是已纳入结果的最旧号池快照时间和 Key 同步时间，无可用数据时为 `null`。`complete: false` 表示存在未同步、换组后尚未复核、停用、缺快照或快照未覆盖的 Key 映射。只显示已匹配到的账号，不回退到全站号池或导入时的旧分组。分组内没有适用公用站点时返回空列表；接口不触发上游采集。
- 未开启或组不在白名单返回 `404`；开启后令牌无效返回 `401`。旧神鸡接口和令牌不变，NewAPI 不在施工范围内。
