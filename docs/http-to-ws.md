# HTTP Responses 请求使用上游 WebSocket

账号管理 → 编辑 OpenAI 账号 → WS mode 选择 `ctx_pool` → 开启「HTTP 请求使用上游 WS」。

该开关默认为关闭，保存在 `accounts.extra.openai_http_to_ws_enabled`。开启后，客户端继续使用 HTTP Responses API；网关从现有 WS 连接池连接上游，再返回 SSE 流或普通 JSON。客户端无需支持 WebSocket。

开关遵循全局 WS 开关、账号类型开关和账号 WS mode。关闭 WS 或强制 HTTP 时，HTTP 转发仍然生效。`/responses/compact` 以及映射到其他上游协议的请求继续走对应的原有路径。HTTP 转 WS 使用连接池，不为每次 HTTP 请求配置客户端 WS 专属会话。

账号「自动透传」开启时，该开关同样有效：符合条件的请求进入已有 WS 转发路径。模型、推理强度、usage 计费、会话隔离及 WS 错误处理沿用该路径。WS 失败不会在本次转发内静默回落 HTTP；网关仍可以按现有调度规则切换账号，因此故障切换后成功的请求可能记录为 HTTP。

调用记录中的 WS 标记表示本次成功请求使用了上游 WS，客户端入站仍可以是 HTTP。WS 能减少上游建连开销；客户端每轮发送完整请求体时，客户端到本站的上传量不会因此减少，也不能保证模型思考更快。

关闭「HTTP 请求使用上游 WS」即可恢复该账号的普通 HTTP 转发。新功能不需要数据库迁移。

验证覆盖：模拟 WS 上游下的 HTTP 流式与非流式响应、自动透传兼容、模型及 `xhigh` 保留、usage 解析、默认 HTTP、compact 与全局强制 HTTP、WS 错误不静默回落 HTTP，以及编辑账号开关的加载、保存和关闭。

2026-10-11 上游实测：账号 118（Vulcan）接受 WS v2 握手，但 `gpt-6.1-sol` / `xhigh` 生成请求没有返回事件，约 15 秒后以 `1013: no available account` 关闭连接。补充 WS v2 `generate=false` 预热请求同样失败；握手状态 101，耗时 0.335 秒，关闭耗时 15.285 秒。近期该账号的 HTTP 调用仍有成功记录。握手成功不能证明对应模型在 WS 渠道可用。启用后应以实际生成和调用记录的 WS 标记验收；上游仍不可用时关闭此开关即可恢复 HTTP。
