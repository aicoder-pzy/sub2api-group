# Custom 分支的 Prism 管理员测试

入口：后台侧栏 **Prism 测试**，路径 `/admin/prism`。默认关闭。
管理员可保存适配器地址和桥接密钥，选择现有启用中的 OpenAI OAuth 账号、模型、思考强度和文本，手动运行一次测试。浏览器运行在服务器，用户本地不需要开浏览器助手。

本阶段仅提供管理员文本测试，不接入公开 `/v1` 转发、账号调度或用户计费。测试消耗账号上游额度；Prism 不提供可靠 token 用量，不伪造用量。适配器列表支持 `gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-6-luna`、`gpt-6.1-sol`；强度为 `low/medium/high/xhigh`。账号仍需有相应权限；不支持 Astra，不自动换模型或重试。

## 来源及更新

适配器取自 ranxi2001/sub2api production v2.10.0，固定提交：
`5ca3cca21eeaf4ca8a694a7f2f8f0ecd9575c549`。原说明见 `prism-adapter/README.md`，来源和许可证见 `prism-adapter/NOTICE.md`。
原说明包含下游转发、工具和 multiplex 的功能；这些不属于当前管理员入口。
升级时单独审查适配器变更并运行 Linux 测试，不能直接整体合并 donor 分支。

## Docker 部署（可选，默认不启动）

GitHub Actions 的 **Custom image** 构建主程序；**Prism adapter** 在 Linux 单元测试、真实 Chromium 沙箱启动及服务健康检查通过后发布：

- `ghcr.io/aicoder-pzy/sub2api-group-prism:custom`
- `ghcr.io/aicoder-pzy/sub2api-group-prism:sha-<完整提交号>`

生产建议固定提交标签。主程序和适配器应使用对应版本。

以下命令在 Linux 的 Compose 部署目录执行。把仓库的 `deploy/docker-compose.prism.yml` 和 `prism-adapter/seccomp_profile.json` 复制到该目录。基础 Compose 必须包含 `sub2api` 服务。不要在生产服务器上编译镜像。

```sh
umask 077
python3 -c 'import secrets; print("PRISM_ADAPTER_API_KEY=" + secrets.token_hex(32))' > .prism.env
docker compose -f docker-compose.yml -f docker-compose.prism.yml pull prism
docker compose -f docker-compose.yml -f docker-compose.prism.yml up -d prism
```

若基础文件名为 `docker-compose.local.yml`，相应替换。不要覆盖已有 `.prism.env`；其中的密钥须与后台设置一致。该文件必须限制权限且不能提交到 Git。升级或重建 `sub2api` 容器时，使用相同的两个 Compose 文件一起更新，确保适配器重新加入主程序的新网络命名空间。

后台填写 `http://127.0.0.1:8319/v1` 和 `.prism.env` 中的密钥值，启用并保存。点击服务检查后，再选账号和模型测试。服务检查只验证 HTTP 进程，不验证登录、模型权限或上游可用性。密钥保存后不会返回前端，留空保存保留原密钥。

适配器与主程序共享网络命名空间，仅绑定 loopback，不发布端口。桥接请求禁止代理和重定向，OAuth 凭据只发往本机适配器。浏览器上网使用服务器出口；此入口不接入 BPS 动态代理池。

适配器以 UID 10001 运行，开启 Chromium sandbox，并使用 Playwright 的 seccomp 配置；不要添加 `--no-sandbox`。运行限额为 1 CPU、900 MiB 内存、不使用 swap、256 个进程；镜像另需磁盘空间。主机必须支持 Chromium 沙箱，GitHub CI 成功不代表所有生产内核都支持。

## 运行约束与失败处理

管理员请求最长约 5 分钟，文本最多 4096 UTF-8 字节。一次只接受一个测试。成功响应必须匹配所选模型和强度，包含完整文本；错误页和未完成响应不会显示为成功。

`prism_state` 卷保存待决请求状态，属于敏感数据。超时或 `pending_turn` 后检查适配器状态及原说明，不连续点击重试，不删除待决文件或换账号绕过未知结果。不要在升级、回滚时删除卷（不要运行 `down -v`）。

回滚时先在后台关闭 Prism，然后停止 `prism` 服务并保留状态卷；主程序原有原生 OpenAI/BPS 路由不受此开关影响。

## 验证

```sh
cd backend
go test -tags unit ./internal/service -run '^TestPrismAdmin' -count=1
cd ../frontend
pnpm typecheck
pnpm exec vitest run src/views/admin/__tests__/PrismView.spec.ts
```

适配器完整测试及浏览器沙箱烟测由 `.github/workflows/prism-adapter.yml` 在 Linux 容器中执行，不使用生产 OAuth 凭据、不调用上游。实际账号登录和模型权限需要部署后由管理员手动确认。
