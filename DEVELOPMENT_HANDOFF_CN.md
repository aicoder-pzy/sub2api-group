# Sub2 定制版开发交接

更新日期：2026-11-26。本文件说明当前开发入口，历史 `CUSTOM_DEVOPS_CN.md` 和 `DEV_GUIDE.md` 中的版本、仓库及工具链可能已经过时。

## 当前基线

- 自有仓库：`https://github.com/aicoder-pzy/sub2api-group.git`。
- 官方上游：`https://github.com/Wei-Shaw/sub2api.git`，用于读取官方更新。
- 当前开发分支：`codex/sub2-routing-evidence-20261123`。
- 本次整理前业务提交：`497dd4019a017a243bc6089a736bad0015da1d76`。
- 对应运行版本：`0.2.25-smart-channel-497dd401`；开发说明提交不代表重新构建或部署。
- `main`、`custom` 及早期 `sub2api-custom` 都有独立历史，不能按名称假定它们比此分支更新。

本机整理后的项目根为 `V:\0_Work\0_Project\16_Sub2`，主仓库在其 `sub2api` 子目录。服务器连接、历史资料、账单工具和未提交工作备份在项目根；这些本机资料没有公开到 GitHub。另一台电脑只克隆本仓库不会自动获得本机凭据或这些本地归档。

## Git 同步

```powershell
git clone --branch codex/sub2-routing-evidence-20261123 https://github.com/aicoder-pzy/sub2api-group.git
Set-Location sub2api-group
git remote add upstream https://github.com/Wei-Shaw/sub2api.git
git remote set-url --push upstream DISABLED_UPSTREAM_PUSH
git status
git pull --ff-only
```

本机 GitHub 连接沿用仓库级代理配置；它只适用于当前电脑。新电脑按自身网络和独立 GitHub 登录配置，不复制认证令牌或盲目沿用本机代理端口。

提交前审阅文件和凭据，显式暂存本次修改；完成相应检查后推送功能分支。不强推，不自动合并归档分支，不以同步源码为由发布镜像或部署。部署必须先向用户列明本次实际功能、组件、环境和排除项并得到当次确认。

## 本地工具与构建

当前 `backend/go.mod` 要求 **Go 1.27.4**。前端使用 **pnpm 9.15.9**，本机已有 Node **24.14.0**；不要混用 npm 安装依赖。Go 工具链以 `backend` 目录内 `go version` 为准，系统默认 Go 可能较旧。

```powershell
Set-Location frontend
corepack pnpm install --frozen-lockfile
corepack pnpm run dev
```

后端开发数据库和 Redis 应独立配置，参考 `deploy/` 的示例。不要把生产数据、`.env`、管理员密码或上游凭据放入 Git 或带入测试。

前端生产构建：在 `frontend` 执行 `corepack pnpm run build`。运行测试范围按实际修改确定；本次目录整理没有业务代码改动，不重复执行全仓应用测试。

Linux 网页版发布必须显式使用 **`-tags embed`** 并准备前端构建资源，例如在 `backend` 构建 `./cmd/server`。漏掉 `embed` 曾导致应用健康检查正常但首页 404，部署核验必须同时覆盖页面和静态资源。当前 Windows 环境有已知依赖 `sh` / `pg_dump` 的测试限制，不能把定向测试当作全部集成测试通过。

## 当前调度规则

正常推理、流式响应及网关稳定性优先。号池预览和状态接口读取已有快照，采集让出资源时减频或延后，保留完整快照并显示实际更新时间。性能验证优先真实用户首字延迟和 5xx。

调度智能体只由真实用户 SSE/WebSocket 故障唤起；同步请求和监控探针不触发。保留模型、思考强度、会话绑定和缓存，控制切换频率，沿用现有 10 把 Key。单次请求的瞬时容量问题不直接升级成账号健康故障。

2026-11-25 的业务改造已区分探针与真实请求、请求级兜底与账号冷却，并向智能体补充失败和备用线路救回证据。整理开发目录不授权再次部署、切分组或发起收费推理。
