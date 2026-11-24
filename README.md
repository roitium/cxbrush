# cxbrush

学习通（超星）课程辅助工具：自动完成视频/文档任务点，遇到章节测验时自动抓题、
还原混淆字体、调用模型作答并提交，最后回查批阅页确认分数。

支持三种形态：单机 TUI、命令行子命令、多用户 Web 版（Svelte + Go）。

## 功能

- 扫码/账号密码登录、Cookie 导入；登录态 7 天滑动续期，进程内每小时自动保活。
- 视频按合法倍速上报进度，文档任务点自动完成，章节测验自动作答并提交。
- 答题页内联字体还原（Noto Sans CJK 向量比对），解析失败时整块明文交给模型兜底。
- 模型走任意 OpenAI 兼容接口，默认读本机 magpie 的 `tokenflux` provider（DeepSeek Flash）。
- Web 版多用户：每个用户独立 Cookie，任务/事件/进度落 SQLite，SSE 实时推送。
- 单文件二进制：前端构建产物和比对字体都 embed，运行时只依赖数据目录。

## 快速开始

```bash
# 构建（会先构建前端）
make build            # 等价于 cd web && pnpm build && go build -o cxbrush ./cmd/cxbrush

# Web 版
./cxbrush serve -addr 127.0.0.1:8080 -data ~/.local/share/cxbrush

# 命令行：刷一门课
./cxbrush work -u <账号> -p <密码> -course <courseId> -chapter <chapterId>

# 直接跑 TUI
./cxbrush
```

首次运行 Web 版时第一个注册的用户直接生效，之后默认关闭注册（`-open` 可放开）。
学习通建议在页面里直接账号密码登录；遇到验证码/二次验证的账号再粘贴 Cookie。

## 配置

**模型**（优先级从高到低）：

1. Web 页面「模型配置」里保存的值（AES-GCM 加密存 SQLite）
2. `-ai-base` / `-ai-key` / `-ai-model` 启动参数
3. `CX_AI_BASE` / `CX_AI_KEY` / `CX_AI_MODEL` 环境变量
4. 本机 magpie `providers.json` 的 `tokenflux` provider

**字体**：默认使用 `internal/assets/NotoSansCJKsc-DemiLight.otf`（已 embed）。
`-ref-font` 或 `CX_REF_FONT` 可以覆盖；`work -fetch-font` 只是把字体单独下载出来给外部工具用。

**数据目录**：默认 `~/.local/share/cxbrush`，包含 `cxbrush.db`、`secret.key`（0600）
和运行日志。`secret.key` 丢了 Cookie 和模型 Key 就解不开。

## 目录结构

```
cmd/cxbrush/        CLI 入口（TUI / work / serve）与字体解析
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
  （课程/章节、视频进度上报、文档任务点）参考了该项目，自动答题与字体解密部分为独立实现。

## 说明

本项目仅用于个人学习与技术研究。使用前请确认符合所在平台的服务条款，
因使用产生的后果由使用者自行承担。
