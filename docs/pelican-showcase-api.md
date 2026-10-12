# 鹈鹕测智结果开放 API

面向下游的只读接口：读取 `/pelican-showcase` 已收录的测试作品，不发起测试、不调用上游模型、不扣 API Key 余额。需要本站用户的 API Key，支持浏览器跨域 GET/HEAD。

采用 **JSON 结果清单 + 按 ID 按需获取正文**。清单含元数据和结构化降智判断，完整 HTML/SVG 保存在单条结果的 `response_text` 中，不重复内联到清单、不转换为截图或 Base64。单条结果另提供 `preview_html`，使用与本站相同的预览代码。新生成作品先经 HTML-only 评估，再保存结论；已有作品沿用已保存的结论，不自动重新评估。

## 管理与接入入口

管理员在「后台侧栏 → 鹈鹕测智 → 用户展示」配置「允许 API Key 读取作品」，与「开放给用户」及保留规则一起保存。API 开关独立于网页展示：关闭 API 后，已登录用户仍可查看已开放的展示页，开放 API 请求返回 403。未配置新开关的已有站点默认保持 API 开放，兼容原有接入。旧客户端保存展示设置时省略 `api_enabled` 会保留当前开关值。

用户在「鹈鹕测智」顶部点击「API 调用」可查看当前可用状态、清单和正文地址、鉴权方式，以及可复制的 curl 示例；弹窗也提供 API Key 管理入口。管理员可在配置旁查看已保存设置的同一组调用信息。地址使用当前面板站点的 origin，不使用可能指向纯 gateway 实例的模型 API 地址。

## 鉴权

清单、正文、GET/HEAD 和条件请求都必须携带本站用户创建的 API Key，推荐 `Authorization: Bearer <API_KEY>`，兼容现有 `X-API-Key` / `X-Goog-API-Key` 请求头。不接受网页登录 JWT 或 URL 查询参数中的 Key。路径保留 `/public/` 表示已发布作品，**不表示允许匿名访问**。

复用站点 API Key 鉴权、用户/分组状态检查、IP 白黑名单和无效鉴权防刷。停用、删除、过期 Key 或不可用的用户/分组不能访问；缓存命中和 304 也必须先通过鉴权。鉴权沿用站点现有 Key 缓存及失效机制。结果范围仍与展示页一致，不按 Key 所属模型分组裁剪。

此接口不执行模型计费、订阅用量检查或最后使用时间写入；余额为零或模型额度耗尽仍可读取，但 Key 本身不能过期。建议下游后端持有 Key 统一拉取，避免把 Key 放入公开前端源码。

## 1. 读取结果清单

```http
GET /api/v1/public/pelican-showcase
Authorization: Bearer <API_KEY>
Accept: application/json
Accept-Encoding: gzip
```

返回沿用本站 `code / message / data` 封装。以下为结构示例，ID、分组、模型和时间以实际响应为准：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "schema_version": 1,
    "enabled": true,
    "result_scope": "published_successes",
    "poll_interval_seconds": 60,
    "max_items_per_group": 20,
    "retention_days": 7,
    "latest_generated_at": "2026-09-30T12:00:00Z",
    "groups": [
      {
        "id": 3,
        "name": "示例分组",
        "platform": "openai",
        "intelligence_probe": {
          "normal_count": 85,
          "degraded_count": 15,
          "sample_count": 100,
          "normal_rate": 85,
          "window_size": 100,
          "min_samples": 11,
          "last_probed_at": "2026-09-30T12:00:00Z"
        },
        "items": [
          {
            "id": 7,
            "group_id": 3,
            "model_id": "示例模型",
            "reasoning_effort": "high",
            "status": "success",
            "quality_verdict": "degraded",
            "quality_label": "疑似降智",
            "quality_reason": "作品缺少自行车结构",
            "latency_ms": 1200,
            "generated_at": "2026-09-30T12:00:00Z",
            "content_url": "/api/v1/public/pelican-showcase/items/7"
          }
        ]
      }
    ]
  }
}
```

- `groups` 按本站分组显示顺序排列，`items` 按生成时间、ID 降序排列。返回当前保留窗口内的全部作品摘要，包含同轮并发产生的多份结果。空分组的 `items` 是 `[]`。不需要分页或增量游标。
- 分组新增 `intelligence_probe`，指标名「智力探测」。`normal_rate` 是 0–100 的正常率：`normal_count / sample_count * 100`，其中 `sample_count = normal_count + degraded_count`。有效总数大于 10（至少 11）次才返回数值；不足时为 `null`，不要显示成 0%。无样本时计数为 0、`last_probed_at=null`。
- 智力探测按分组汇总全部模型的分组测试，每份生成结果只计一次；只统计成功且结论为 `normal` / `degraded` 的记录，失败尝试、异常、`unknown`、账号独立测试不计入。独立数据库队列按测试完成时间、结果 ID 倒序保留最新最多 100 条有效结论，超过 100 条淘汰最早的结论；并发完成不重复、不丢计数。作品数量/天数限制、管理员历史清理、删除作品或单个测试计划不清空此统计。下游卡片、表格直接读取该字段，不使用清单作品数推算。统计与清单共用 ETag/60 秒缓存，即使作品没变，统计变动也会更新 ETag。
- 升级从仍存储的分组结果、结构化调度证据和已发布快照回填并去重，不生成付费请求、不重新评估。早已删除且没有任何证据的历史不能恢复；升级后每次完成的有效分组测试均独立记录。
- `id` 是作品快照 ID，正文在其生命周期内不变；下游以 ID 去重。不要把生成时间或最大 ID 当作完整同步游标：并发测试可能晚完成、早开始。
- `content_url` 是相对于 API 站点根路径的地址，用 API origin 解析，读取正文同样需要附带 API Key。
- `generated_at` 沿用展示页口径，通常是该次测试的开始时间；`latency_ms` 为该份测试耗时（毫秒）。`latest_generated_at` 是当前作品中最大的生成时间；没有作品时为 `null`。它不是删除/设置变动游标，应使用 ETag 检查清单变化。
- `result_scope=published_successes` 表示只包含已成功生成 HTML/SVG 并发布的作品，**不是全部测试记录，不是成功率、可用性或智力分数**。不公开账号身份、管理员错误记录、提示词配置和成本字段。
- 清单和正文均返回 `quality_verdict`：`normal` 正常、`degraded` 疑似降智、`unknown` 无法判定。`quality_reason` 是保存的判断原因，没有原因时为 `""`。`status=success` 仅指作品生成成功，不能据此判断是否降智；历史作品未评估或评估失败均归入 `unknown`，不当作正常。结论随展示快照独立保存，管理员历史清理后仍保留。
- 保留数量、天数和分组可见性与展示页一致。`retention_days=0` 表示关闭按天清理，仍受每组数量上限约束。管理员删除、计划删除、分组停用、作品过期或收紧保留数量后，该作品会移出清单。
- API 开关关闭时，清单和正文接口均返回 403（`reason=PELICAN_SHOWCASE_API_DISABLED`），GET/HEAD、缓存命中和条件请求都受此开关控制。API 开关开启但展示开关关闭时返回 `enabled=false, groups=[]`，正文接口返回 404；同时开启两个开关后，已发布作品可供通过 API Key 鉴权的下游读取。

## 2. 按需读取完整结果

```http
GET /api/v1/public/pelican-showcase/items/7
Authorization: Bearer <API_KEY>
Accept-Encoding: gzip
```

`data` 包含清单中的该条元数据，以及 `response_text` 和 `preview_html` 字符串。`response_text` 保留原始模型输出（可能带 Markdown 代码围栏和已保存的角标），适合存档或下载。`preview_html` 是可直接放入隔离 iframe 的完整预览文档，复用本站同一渲染器：保持逻辑画布，等比适应窗口，自动测量超出画布和延迟出现的内容，保留动画，在缩放画布外显示可读的判断角标。判断使用保存的结构化结论，未从作品任意标签推断。

清单和正文另提供 `quality_label`（正常／疑似降智／无法判定）。`preview_html` 直接包含 `id="intelligence-verdict"` 的右上角判定标签，标签及原因来自已保存的结构化结论，不需要调用方再拼接，也不会随作品画布缩小。正常、降智和未知分别采用绿色、红色和灰色；原因作为标签提示。`response_text` 继续保留原始输出，兼容已有存档和下载。

显示时使用 `preview_html`，不要直接缩放原始 `response_text`；否则长图、SVG 尺寸及角标可能与本站不同。相同浏览器和容器尺寸下，作品画布与本站“适应窗口”模式一致。文档包含渲染代码和作品，不下载外部资源、不访问网络、不需要 API Key。API 仍以 `application/json` 返回；没有内嵌前端的开发版不提供 `preview_html`。

```html
<iframe id="artwork" title="鹈鹕作品" sandbox="allow-scripts"
        referrerpolicy="no-referrer" style="width:100%;height:480px;border:0"></iframe>
<script>
// payload 是后端用 API Key 获取的单条 JSON 响应，不在前端暴露 Key。
let preview = payload.data.preview_html;
// srcdoc 继承父页面 CSP；使用 nonce 的页面需授权预览文档中的脚本。
const nonce = document.querySelector('script[nonce]')?.nonce;
if (nonce) {
  const doc = new DOMParser().parseFromString(preview, 'text/html');
  doc.querySelectorAll('script').forEach(script => { script.nonce = nonce; });
  preview = '<!doctype html>' + doc.documentElement.outerHTML;
}
document.getElementById('artwork').srcdoc = preview;
</script>
```

必须保留 `sandbox="allow-scripts"`，不要添加 `allow-same-origin` 或通过 `innerHTML` 注入父页面。预览文档内部再将作品放入独立沙箱，并先插入限制网络访问的 CSP。外层容器大小由下游页面控制，不会把本站账号信息或管理接口传入作品。

本站渲染器沿用页面的 CSP nonce，使尺寸测量和作品脚本在隔离沙箱内运行；页面本身的 CSP 不放宽。下游若有 CSP，也需允许 `about:srcdoc` 子框架及预览脚本；以上示例兼容使用 nonce 的父页面。尺寸测量尊重作品内部的裁剪区域，不会把已被场景裁掉的云朵或山丘算作额外画布。

不存在、已移除、超出保留窗口或展示关闭的作品返回 404；非法 ID 返回 400。清单和正文之间若遇到清理，404 属于正常竞争情况，刷新清单即可。该 API 是当前展示窗口，不是永久存档。需要长期保留时应由下游在窗口内存储正文。

## 3. 缓存与推荐同步流程

1. 首次携带 API Key 请求清单，保存响应 `ETag`、清单和已成功取得的作品正文。
2. 后续每 **60 秒或更久** 请求一次清单，同时带 API Key 和 `If-None-Match: <上次 ETag 原值>`。不要添加时间戳查询参数，不需要反复登录或抓取网页。
3. `304 Not Modified` 无响应正文，继续使用本地清单，不再请求任何作品。返回 200 时替换清单，仅抓取尚未存储的作品 ID；建议正文请求并发不超过 2。单条下载失败保留待重试 ID，不能因为随后清单为 304 就忘记失败任务。
4. 下游展示以最新清单为准，移除已不在清单里的展示项；自行保存的历史归档可按下游策略保留。
5. 429 和 503 时遵守 `Retry-After`，网络错误或其他 5xx 使用带随机抖动的指数退避，避免立即循环重试。

清单和正文均支持 GET/HEAD、ETag、`If-None-Match` 和 gzip 协商（适合压缩的响应从 1 KiB 起启用）。ETag 基于最终 JSON 内容，不因读取时间变化；压缩与未压缩版本共用弱 ETag，`Vary` 按编码及鉴权头区分缓存。收到 304 后仍应更新本地缓存响应头。

服务端每实例共享一份 **60 秒结果快照**，并发刷新合并为一次读取；正文按 ID 缓存和合并请求，缓存最多 64 项且 JSON 与 gzip 合计不超过 16 MiB。不在结果快照内的任意 ID 不访问正文数据库。读取只使用已存储数据，不产生模型用量。

成功响应（含 304）使用 `Cache-Control: private, no-cache, must-revalidate`：允许客户端保留数据，但复用前必须携带 Key 回源验证；公共 CDN/代理不得缓存，也不得配置规则覆盖此策略。服务端仍保留 60 秒共享快照，减少数据库访问；ETag/304 继续节省传输。错误响应为 `no-store`。新作品、可见性和展示设置变更通常在最多约 60 秒后反映（另加请求处理和下游轮询等待时间）；本站删除作品和展示设置接口会主动失效当前实例缓存，其他实例最长等待原快照有效期结束。

鉴权前应用公开接口 IP 限流（`public_ip_rpm`，默认每 IP 每分钟 300 次）；鉴权后应用用户限流（`user_rpm`，默认每用户每分钟 240 次），同一用户更换 Key 或 IP 仍共用用户限额。均沿用面板限流配置、开关、管理员豁免及 Redis 异常策略，触发返回 429 和 `Retry-After`。生产应正确设置可信反代和客户端 IP；沿用现有策略，回环/内网代理地址不计入 IP 限流。

跨域读取允许任意 Origin，但不允许携带 Cookie 凭据；浏览器使用 `credentials: 'omit'` 并显式设置鉴权头。预检允许 `Authorization`、`X-API-Key`、`X-Goog-API-Key` 和 `If-None-Match`，并暴露 `ETag`、`Cache-Control`、`Retry-After` 响应头。原登录接口和管理员接口的鉴权不变。

## 4. 调用示例

```bash
# 先在调用端安全设置 SUB2API_API_KEY 环境变量
# 首次：保存响应头和轻量清单
curl --compressed -sS -D headers.txt \
  -H "Authorization: Bearer $SUB2API_API_KEY" \
  https://sub2api.example.com/api/v1/public/pelican-showcase \
  -o manifest.json

# 后续：将 YOUR_ETAG 替换为 headers.txt 中 ETag 的完整原值（包括 W/ 和双引号）
curl --compressed -i \
  -H "Authorization: Bearer $SUB2API_API_KEY" \
  -H 'If-None-Match: YOUR_ETAG' \
  https://sub2api.example.com/api/v1/public/pelican-showcase

# 仅在本地尚无该结果时，读取清单返回的 content_url
curl --compressed -sS \
  -H "Authorization: Bearer $SUB2API_API_KEY" \
  https://sub2api.example.com/api/v1/public/pelican-showcase/items/7
```

错误状态：400 非法 ID/URL 传 Key；401 缺少、无效或停用 Key/用户；403 API 读取关闭、Key 过期、IP 或分组访问受限；404 作品不可见；429 IP/用户限流或无效鉴权防刷；503 配置、清单或鉴权暂时不可用；500 存储读取失败。服务部署在主实例的面板 API 路由上，纯 gateway 角色实例不注册此接口。API 读取复用已收录作品，不创建额外定时测试。本 custom 初次安装整个功能时由 `244_custom_pelican_tests.sql` 建立相关表。
