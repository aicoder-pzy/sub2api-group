# 定制版维护说明

## 分支与来源

| 名称 | 用途 |
| --- | --- |
| `origin` | 本仓库：`https://github.com/aicoder-pzy/sub2api-group.git` |
| `upstream` | 官方仓库：`https://github.com/Wei-Shaw/sub2api.git` |
| `main` | 跟踪官方代码，导入时为 `v0.2.14`，不直接添加定制改动 |
| `custom` | 定制源码、测试和维护说明，后续开发使用此分支 |

## 当前上游版本

2026-10-09 将官方 `v0.2.15` 发布后最新 `main`
（`3a6fd1c9db07203ca308aaba69e502bc1f35b307`）合并到 `custom`，保留双方提交历史。
此前基线为 `v0.2.14`（`3f1a2ea0a760730e3bc528105c00b4ee4f23e469`）。
本次纳入 Cline、Command Code、平台注册表、输出 TPS 以及协议转换、日志和界面修复。

8 个冲突文件逐项保留定制行为并兼容官方接口，包括质量检测、单次绘图、钱包通知、
全局调度和模型白名单。官方新增的 Anthropic 转 Chat 心跳经过现有首输出暂存器，
避免空心跳提前提交下游响应而阻止故障切换；普通模式继续即时发送心跳。

回归重点：健康渠道持续绑定、成功后才提交备用渠道、绑定版本检查、共享尝试预算、
首输出与空闲超时、输出后禁止重放、白名单、BPS、Prism、检测记录、钱包与余额通知。
CI 加入调度配置、主动更新调度、优先账号、直连、用量和模型发现的前端回归用例。
补齐账号页面余额配置与定时检测裁判分组的接口模拟，保证完整前端回归能够覆盖定制页面。
所有已发布 SQL 迁移保持原文件名和内容，仅新增官方 `242_drop_platform_check_constraints.sql`；
迁移以完整文件名标识，与既有定制迁移互不覆盖。

## 公共超时接入修复（2026-10-07）

将首输出超时、流空闲超时和模型冷却从 OpenAI 服务中抽为公共逻辑，补齐 Anthropic
普通 Messages、透传、Bedrock 和 Chat Completions/Responses 转换路径。
规则只按 `fastest_failover` 模式启用，不绑定特定分组、账号或供应商；阈值保持 60 秒、120 秒和 2 分钟。
暂存空开场事件以允许首输出前换号；已有输出时保留用量、禁止重放；选号时重新检查冷却，避免旧快照恢复选中优先账号。

回归覆盖多个任意分组、模型映射与模型隔离、不同转发入口、响应头等待、空开场、输出后超时、正常完成、断流、
真实本地 HTTP 连接取消及普通模式。接入范围和下游超时限制见 [调度说明](deploy/fastest-failover.md)。
此修复已随 `0.2.14-fastest.9` 于 2026-10-07 部署到生产服务器。

## 全局配置与主动更新调度（2026-10-07）

新增全局调度配置及账号管理页“更新调度”：配置通过管理员设置接口持久化，转发尝试热读取；
更新调度会对所选分组的全部候选账号发送真实文本测试，按首输出耗时及原优先/倍率规则重选所选模型的绑定。
取消或全部失败保留原绑定，失败优先账号在成功重分配时进入模型冷却。使用方法与限制见
[调度说明](deploy/fastest-failover.md)。该功能已随 `0.2.14-fastest.9` 于 2026-10-07 部署，代码提交为 `944f9e02b`。

## 首次导入记录

2026-10-07 从现有 `sub2api-0.2.13` 工作目录导入定制改动，保留官方
`v0.2.13` 基线 `3040209f205472038c1ba745a1bedd2edd9053b1`。
源码导入提交为 `3b84a769a`，包含 62 个修改文件和 24 个新增文件。
首次导入时未合并 `v0.2.14`；`main` 与 `custom` 共享官方历史，可以正常合并后续更新。

定制内容包括分组账号调度、质量和倍率选择、优先调度、超时故障切换、调度缓存修复、
用量页面显示及直连访问设置，并保留相关回归测试。调度规则见
[deploy/fastest-failover.md](deploy/fastest-failover.md)。

源码导入按 Git 跟踪文件和未忽略的新文件执行，4,220 个文件与来源目录逐一校验一致。
构建产物、依赖目录、本地环境配置和部署备份不属于源码迁移范围。
`deploy/fastest*-build.*`、`deploy/fastest*-deploy.*` 是历史发布脚本，使用固定版本和服务器路径，
不能直接作为任意新版本的一键部署入口。源码同步本身不修改正在运行的服务。

## 获取定制版

```bash
git clone --branch custom https://github.com/aicoder-pzy/sub2api-group.git
cd sub2api-group
git remote add upstream https://github.com/Wei-Shaw/sub2api.git
git fetch upstream --tags
```

`upstream` 是本机 Git 配置，新克隆需要添加一次；已经配置的目录无需重复添加。
GitHub 默认分支仍为 `main`，浏览定制代码时选择 `custom`。

## GitHub 构建与镜像

推送到 `custom` 后，[Custom image 工作流](https://github.com/aicoder-pzy/sub2api-group/actions/workflows/custom-image.yml)
在 GitHub Actions 编译前后端，验证程序及 PostgreSQL 客户端能够启动，再发布到 GHCR。
镜像使用当前服务器的 `linux/amd64` 架构，构建使用仓库自带的 `GITHUB_TOKEN`，无需配置 Docker Hub 密钥。

```bash
docker pull ghcr.io/aicoder-pzy/sub2api-group:custom
```

`custom` 标签随成功构建更新；`sha-<完整提交号>` 标签对应具体代码，部署或回退时优先固定提交标签或镜像摘要。
程序版本显示为 `0.2.15-custom.<短提交号>`（基础版本读取 `backend/cmd/server/VERSION`）。
源码与镜像发布不会自动重启生产服务；生产部署先验证固定提交镜像并备份当前配置和数据。

镜像页面：[GitHub Packages](https://github.com/aicoder-pzy/sub2api-group/pkgs/container/sub2api-group)。
2026-10-07 首次构建和发布成功，镜像已公开，服务器可匿名读取 GHCR 镜像清单，无需登录。
Actions 构建失败时可在该次运行页面使用 **Re-run jobs** 重试。

## 后续同步官方更新

开始前提交或暂存当前工作，确保工作区干净，然后更新官方镜像分支：

```bash
git fetch upstream --tags
git switch main
git merge --ff-only upstream/main
git push origin main
```

准备升级定制版时，将官方改动合入：

```bash
git switch custom
git merge main
```

发生冲突时，逐个保留定制行为并兼容上游接口，暂存解决后的文件并提交合并。
也可以用 `git merge --abort` 取消尚未完成的冲突合并。
不要用覆盖目录或强制重置的方法更新 `custom`。

合并后按仓库现有流程执行后端测试、前端测试、类型检查及构建，再推送：

```bash
git push origin custom
```

定制回归重点包括 `TestFastestFailover*`、`TestSchedulerCachePreferred*`、`TestDirectAccess*`，
以及前端的 `AccountPriorityCell`、`UsageTable`、`DirectAccessSettings` 和 `SettingsView` 测试。
涉及数据库结构时，检查新增迁移的编号、顺序和已发布迁移内容；不要改写已部署迁移。

GitHub 的 **Sync fork** 可用于更新 `main`；它不会自动将上游更新合入 `custom`。
`custom` 更新通过上述合并和验证流程完成，生产发布另行执行。
