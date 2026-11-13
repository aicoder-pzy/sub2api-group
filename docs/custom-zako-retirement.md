# Zako v0.14.0 与 Prism / BPS 下线

策略数据固定来自 ZakoProxyCLI `94fb746c4f70f07ef0cf2e26397e2e29dd7b6299`。
更新 42 个既有策略的内容，保留策略 ID、精确模型列表及已保存的 Key 选择。
通用默认策略不变。新增 11 个席位与 4 个协作模板，均需显式填写实际使用的
Key 映射后模型名，不能作为 `*` 默认策略。DeepSeek 模型追加上游 API 适配说明。
策略内容与来源清单位于 `backend/internal/service/zako_profiles/`；保留 MIT
许可证。效果仍为上游标注的 `unverified`，不会自动为任何 Key 开启策略。

Prism 管理页面、API、适配器及其镜像发布流程已删除。BPS 的管理页面、API、
转发协议、采票/自动切换任务、Mihomo 代理池与 Excel 授权入口已删除。
原生账号检测、普通代理与通用 OAuth 会话/Refresh Token 的 issuing-client
处理继续由原有链路提供。已有数据库迁移及其校验和不变；旧配置不再被读取。

部署前核对存量账号无启用的 BPS 路由，备份数据库、应用数据、Compose、
环境配置、Prism 状态及旧镜像。等待网关请求与 Prism 测试结束后，仅替换
主应用；从有效 Compose 配置移除 Prism 服务并停止适配器，保留回滚所需状态。
更新验证包含应用健康、认证路由、58 项策略目录、旧功能 API 不再注册，以及
其他容器身份保持不变。不要对整个项目执行 `down` 或删除状态卷。

恢复时使用配套备份的旧镜像和 Compose；不要把旧的 BPS 配置自动迁移到原生路由。
