# cxbrush

学习通（超星）课程辅助工具：自动完成视频/文档/阅读/直播任务点，遇到章节测验时自动抓题、
还原混淆字体、调用模型作答并提交，最后回查批阅页确认分数。

面向多用户的 Web 版（Go + Svelte），单文件部署。

## 功能

- 扫码/账号密码登录、Cookie 导入；登录态 7 天滑动续期，进程内每小时自动保活。
- 视频按合法倍速上报进度，文档/阅读任务点自动完成，直播按真实时长上报，章节测验自动作答并提交。
- 答题页内联字体还原（Noto Sans CJK 向量比对），解析失败时整块明文交给模型兜底。
- 模型走任意 OpenAI 兼容接口：在页面「模型配置」里填地址/Key/模型名，也可以用 `CX_AI_*` 环境变量。
- 题目答案缓存：按「题型 + 题干 + 选项正文」把模型答过的题存进 SQLite，再遇到直接复用，不再请求模型。
- Web 版多用户：每个用户独立 Cookie，任务/事件/进度落 SQLite，SSE 实时推送。
- 单文件二进制：前端构建产物和比对字体都 embed，运行时只依赖数据目录。

界面分三个页面（`#/` 概览、`#/tasks/:id` 任务详情、`#/settings` 设置）：
概览里直接看运行状态、历史任务和章节分组选择，任务详情页实时显示任务点与日志。

## 快速开始

```bash
# 构建（会先构建前端）
mise run build        # 等价于 cd web && pnpm build && go build -o cxbrush ./cmd/cxbrush

# 启动 Web 版
./cxbrush -addr 127.0.0.1:8080 -data ~/.local/share/cxbrush
```

启动参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8080` | 监听地址 |
| `-data` | `~/.local/share/cxbrush` | 数据目录 |
| `-open` | `false` | 允许任何人注册（默认只有第一个用户能注册） |
| `-workers` | `8` | 全进程同时运行任务数的硬上限（1-32） |

学习通账号、模型 Key 等都在页面里配置。

直播任务点会在「开始任务」时单独开关；它按真实时长上报（59 分钟回放就要挂 59 分钟），
未开始或已结束且不允许回放的直播会跳过。

首次运行 Web 版时第一个注册的用户直接生效，之后默认关闭注册（`-open` 可放开）。
学习通建议在页面里直接账号密码登录；遇到验证码/二次验证的账号再粘贴 Cookie。

## 配置

**并行度**：每个用户在「设置 → 并行度」里自己设置，只影响自己的任务。

- 同时运行的任务数：1–4，默认 2
- 每个任务的视频并发：1–8，默认 2

不建议开高：学习通对进度上报有频率限制，2–3 比较稳，开高更容易出现进度不确认
甚至触发风控。`-workers` 是全进程的硬上限，超过时任务会排队。

**模型**（优先级从高到低）：

1. Web 页面「模型配置」里保存的值（AES-GCM 加密存 SQLite）
2. `CX_AI_BASE` / `CX_AI_KEY` / `CX_AI_MODEL` 环境变量

没有配置时页面会提示，自动做题不会启用。

**字体**：默认使用 `internal/assets/NotoSansCJKsc-DemiLight.otf`（已 embed）。
需要用别的字体做对照时，设置 `CX_REF_FONT=/path/to/font.otf` 即可。

**数据目录**：默认 `~/.local/share/cxbrush`，包含 `cxbrush.db`（答案缓存在 `answer_cache` 表）、
`secret.key`（0600）和运行日志。`secret.key` 丢了 Cookie 和模型 Key 就解不开。

## 目录结构

```
cmd/cxbrush/        服务入口、启动参数、内嵌字体加载
internal/cx/        学习通协议实现：登录、课程、视频、答题、字体还原、评测
internal/server/    Web API、SSE、任务调度、内嵌前端产物
internal/store/     SQLite 持久层（用户、Cookie、任务、事件、配置）
internal/assets/    内嵌字体及许可证
web/                Svelte 5 + Vite 前端源码
docs/protocol.md    协议分析：字体、题目、提交、批阅
docs/archive/poc/   最初的 Python POC（已归档，仅作参考）
```

## 文档

- [协议说明](docs/protocol.md)：答题页三层 iframe、字体混淆、提交与批阅字段
- [POC 归档说明](docs/archive/poc/README.md)

## 致谢

- [Pybsama/Caochaoxing](https://github.com/Pybsama/Caochaoxing)：本项目的学习通登录与刷课协议
  （课程/章节、视频进度上报、文档任务点）参考了该项目，自动答题、字体解密与阅读任务点为独立实现。

## 说明

本项目仅用于个人学习与技术研究。使用前请确认符合所在平台的服务条款，
因使用产生的后果由使用者自行承担。
