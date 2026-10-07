# 定制版维护说明

## 分支与来源

| 名称 | 用途 |
| --- | --- |
| `origin` | 本仓库：`https://github.com/aicoder-pzy/sub2api-group.git` |
| `upstream` | 官方仓库：`https://github.com/Wei-Shaw/sub2api.git` |
| `main` | 跟踪官方代码，导入时为 `v0.2.14`，不直接添加定制改动 |
| `custom` | 定制源码、测试和维护说明，后续开发使用此分支 |

## 当前上游版本

2026-10-07 已将官方 `v0.2.14`（`3f1a2ea0a760730e3bc528105c00b4ee4f23e469`）
合并到 `custom`，合并提交为 `0f2a71ec5`。本次纳入 9 个上游提交，涉及支付回调安全、
管理员首次初始化、远程模型目录发现配置和前端依赖更新；合并无冲突，原有定制改动全部保留。

同步修正上游遗漏的 3 处模型目录测试预期，并验证远程目录模式开启模型发现、文件模式不添加该开关。
本次验证通过：

- Go 1.27 的配置、支付提供商、初始化包测试，以及调度、超时、缓存、认证分组、质量统计、直连和支付返回 URL 的定向回归（`unit` 标签）。
- 前端 25 个测试文件、377 项用例，ESLint、TypeScript 检查和 Vite 生产构建。
- 后端 Windows 本地编译、部署脚本语法检查、Compose 安全配置测试，以及 Git 文件内容和合并历史检查。

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
