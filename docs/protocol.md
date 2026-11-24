# cxbrush POC — 学习通答题页字体还原

把超星（学习通）答题页里的**内联混淆字体**提取出来，还原被替换成「载体字」的题干和选项，输出可直接交给 AI 作答的结构化 JSON。

> **现状：纯 Go 实现已可用**（Web 版），不需要 Python、不需要浏览器/无头浏览器。
> 最初的 Python POC 已归档到 `docs/archive/poc/`，本文保留其协议分析与对照说明；
> Go 用法见末尾「Go 实现」一节。

## 字体在哪

答题页是三层 iframe：

```
studentstudy (top)
└─ iframe#iframe                       /mooc-ans/knowledge/cards
   └─ iframe[src*="modules/work"]      /ananas/modules/work/index.html
      └─ iframe#frame_content          /mooc-ans/work/doHomeWorkNew
```

字体在**最内层文档的 `<head>`** 里，是服务端直接写进 HTML 的：

```html
<style id="cxSecretStyle">
  @font-face { font-family: font-cxsecret; src: url(data:font/ttf;base64,AAEAAA...) format("truetype"); }
  .font-cxsecret { font-family: font-cxsecret; }
</style>
```

- 不是 JS 动态注入，也没有配套的映射表接口。
- 字体是 **TTF 子集**（约 15.6KB，50 字形 / 49 条 cmap），只含当前页用到的字。
- 页面上的「寥」「寡」「寠」只是载体字：它们的 cmap 指向真字的**字形轮廓**，真字只能靠形状比对还原。

## 原理

1. 从 `#cxSecretStyle` 取出 base64，还原成 TTF。
2. 把子集里每个字形栅格化（96px，等比补方，48×48 归一化）。
3. 和一份完整中文字体的全部 CJK 字形做余弦相似度比对，取最相似的字。
4. 用 top1/top2 的分差给出 high / medium / low 置信度；low 的会连同候选一起写进 `uncertain`。

### 为什么默认用 Noto Sans CJK SC DemiLight

子集自带的 name 表写着 `Source Han Sans CN Normal`（思源黑体 1.000，字重 350）。
`Noto Sans CJK SC DemiLight` 与它同源同字重，字形几乎逐点一致，比对分数普遍在 0.96–0.995，
远高于 PingFang / 冬青黑体 / 宋体这类系统字体（后者会出现 `寵→杏`、`戰→乩` 之类的错判）。

字体已内嵌在 Go 二进制里（`internal/assets/NotoSansCJKsc-DemiLight.otf`）。
归档的 Python 脚本可以直接指向它：

```bash
python3 docs/archive/poc/extract.py --html /tmp/work.html \
  --ref-font internal/assets/NotoSansCJKsc-DemiLight.otf -o /tmp/work.json
```

## 用法

归档脚本的依赖：`pip install -r docs/archive/poc/requirements.txt`。

```bash
# 本地 HTML
python3 docs/archive/poc/extract.py --html /tmp/work.html \
  --ref-font internal/assets/NotoSansCJKsc-DemiLight.otf \
  -o /tmp/work.json --md-out /tmp/work.md

# 或直接抓（Cookie 从浏览器复制）
python3 docs/archive/poc/extract.py --url '<答题页 URL>' --cookie '<Cookie>' \
  --ref-font internal/assets/NotoSansCJKsc-DemiLight.otf -o /tmp/work.json
```

其他参数：`--ref-font` 指定比对字体、`--keep-raw` 保留乱码原文、`--no-cache` 跳过解码表缓存。

### 从浏览器里取最内层 HTML

用 ego lite 打开答题页后：

```js
const top = document.querySelector('iframe#iframe').contentDocument;
const work = top.querySelector('iframe[src*="modules/work"]').contentDocument;
const inner = work.querySelector('iframe#frame_content').contentDocument;
inner.documentElement.outerHTML
```

## 输出

```jsonc
{
  "font": {
    "family": "font-cxsecret",
    "bytes": 15588,
    "carriers": 49,
    "sourceFont": "NotoSansCJKsc-DemiLight.otf",
    "lowConfidence": { "寱": { "char": "略", "score": 0.864, "alternatives": ["峈","珞","峪"] } },
    "charMap": { "寥": { "char": "生", "score": 0.991, "confidence": "high" } }
  },
  "questionCount": 5,
  "questions": [
    {
      "id": "405957436",
      "number": "1",
      "section": "一. 单选题（共2题）",
      "type": "单选题",
      "typeCode": "0",
      "stem": "面对腐败问题严峻挑战，（）直抵基层，聚焦民生领域微腐败，切实维护群众利益。",
      "options": [
        { "letter": "A", "value": "B", "text": "“打虎”" },
        { "letter": "B", "value": "C", "text": "“猎狐”" }
      ]
    }
  ]
}
```

要点：

- `typeCode`：`0` 单选、`1` 多选、`3` 判断。
- `letter` 是**页面显示顺序**，`value` 是选项真正的提交值，**两者顺序不一致**，提交时用 `value`。
- 判断题的 `value` 是 `"true"` / `"false"`。
- `uncertain` 里的 `char` 已经是选定的还原结果，`alternatives` 是候选，供 AI 结合语义纠错。

## 准确率

已知 11 个载体字的真值全部命中的样本：

```
苧→败 寠→严 寡→峻 實→层 寥→生 寑→实 寣→群 冩→虎 寬→狐 寵→奋 潯→战
```

实测当前 5 题整页还原 100% 正确（`寱→略` 分数偏低但仍是 top1）。
低置信不会中断流程，只会被标记出来。

## 已知限制

- 页面结构变了（选择器、iframe 层级）就要跟着改 `parse_questions`。
- 站点换字体版本时，同源字体可能对不上；届时会退化到系统字体投票，错字率上升。
- 本 POC 只做**提取 + 还原**；提交协议见下节，目前未实现自动提交。
- 自动抓取需要登录 Cookie；过期就重新从浏览器复制。

## 提交协议（2026-11 实测）

提交字段与实测抓包已核对：5 题 100 分（原始抓包未随仓库提交）。

1. 提交前先 `GET /mooc-ans/work/validate?classId&courseId&knowledgeId&cpi&_=<ts>`；
   `status=3` 才继续，`2` 表示要弹验证码，`1` 表示账号异常。
2. 正式提交是 **POST**（不是 GET）：

```
POST /mooc-ans/work/addStudentWorkNewWeb
     ?_classId=..&courseid=..&token=<enc_work>&totalQuestionNum=..
     &workid=..&cpi=..&jobid=..&knowledgeid=..
     &ua=pc&formType=post&saveStatus=1&version=1
Content-Type: application/x-www-form-urlencoded; charset=UTF-8

pyFlag=&courseId=..&classId=..&workAnswerId=..&answerId=..
&totalQuestionNum=..&fullScore=..&knowledgeId=..&oldWorkId=..&jobid=..
&workRelationId=..&enc=&enc_work=..&userId=..&cpi=..&randomOptions=true
&answer<qid>=<data值>&answertype<qid>=<0|1|3>
&answerwqbid=<qid>,<qid>,...,&      ← 按题号顺序，末尾带逗号
```

3. 成功响应（Content-Type 虽是 `text/html`，body 是 JSON）：

```json
{ "msg": "success!", "stuStatus": 4, "status": true, "url": "/mooc-ans/api/work?..." }
```

之后页面 `location.href = url`，跳转到已批阅页；`url` 里的 `oldWorkId` 是**新的作答记录 id**。

### 坑

- `randomOptions=true`：页面显示字母和 `data` 值不是一回事。实测 Q1 第一个选项显示 `A`、`data="B"`，
  提交必须用 `data`，用显示字母会错位。
- 多选题提交前用 `sortMultiAnswer()` 把 key 升序排列（如 `ABCD`），不要按点击顺序。
- `answerwqbid` 必须覆盖所有题号且顺序一致，末尾保留逗号。
- `token`（即 `enc_work`）在 **query** 里，不在 body；`enc` 用 validate 返回的值。
- 每次提交都会生成新的 `oldWorkId`，存在提交次数上限（本例 `limitWorkSubmitTimes=100`）。

---

## Go 实现

上述 Python POC 的能力已全部用纯 Go 重写，并补齐了提交：

- **提取**：`internal/cx/font.go` 从 `#cxSecretStyle` 取 base64 TTF。
- **还原**：`internal/cx/font.go` 用 `golang.org/x/image` 栅格化 + 余弦比对；支持 TTF/OTF/TTC，
  载入完整中文字体后并发构建 2 万+ CJK 参考向量（~1s）。
- **解析**：`internal/cx/quiz.go` 用 `golang.org/x/net/html` 解析题干/选项，只对 `font-cxsecret`
  节点解密，不会误伤页面正常文字。
- **题目 API 构造**：`internal/cx/work.go` 的 `BuildWorkURL` 完全复刻前端拼接逻辑，URL 全部来自
  `Courses() → Chapters() → /mooc-ans/knowledge/cards` 返回的 `mArg.attachments`，
  无任何硬编码；`ChapterContent.Works` 已从 cards 里收集 work 项。
- **提交**：`internal/cx/work.go` 的 `SubmitWork` 先 `validate` 再 POST，默认 dry-run 不提交。

### 答题与提交

网页端的「自动做题」在扫描章节时如果发现 `work` 任务点，会自动：

1. 用 `Courses → Chapters → knowledge/cards` 的数据拼出题目 API 并抓取答题页；
2. 提取内联字体并还原题干/选项；
3. 调用模型作答（模型在页面「模型配置」里填写，或使用 `CX_AI_*` 环境变量）；
4. 校验后提交；
5. 重新打开一次该测验（提交后会 302 到批阅页），回查成绩与对错题数。

回查只取与选项字母无关的信息（本次成绩、`aria-label="答案正确/错误"`），
因为批阅页的「我的答案」用的是页面字母，而 `randomOptions` 会把字母和 value 打乱。

答案匹配规则（`cx.NormalizeAnswer`）：

- **优先按 `value` 匹配**（`randomOptions=true` 时必须如此）。
- 也接受页面显示字母；多选支持 `AC`/`A,C`/`A C`，提交前按 value 升序拼接。
- 判断题接受 `对/错`、`true/false`、`√/×`、`A/B`。

提交会真正写入一次作答记录（有次数上限），由页面上的开关控制；
`randomOptions=true` 时页面字母与提交值顺序不一致，因此一律按 value 提交。

### 阅读任务点（2026-11 实测）

cards 里的阅读任务是 `module=insertreadV2` 的 iframe，`mArg.attachments` 对应项形如：

```json
{"type":"read","enc":"…","jtoken":"…","mid":"…","job":true,
 "property":{"module":"insertreadV2","id":"249705972","_jobid":"read-249705972",
             "title":"习近平法治思想学习纲要","read":true}}
```

任务点是否完成看 `job`：未完成时 `job:true`，iframe 的 `data` 里也会带 `jobid`；
完成后 `job` 字段消失，只剩 `property._jobid`。

完成接口由阅读模块的 `finishJob()` 发出（点「去阅读」时调用，与阅读时长无关）：

```
GET /mooc-ans/job/readv2
  ?jobid=read-249705972&knowledgeid=<chapterId>&courseid=<courseId>&clazzid=<classId>
  &jtoken=<jtoken>&checkMicroTopic=true&microTopicId=<可选>&courseEngineInfo=false&_dc=<毫秒>
→ {"msg":"考核点已经完成","status":true}
```

实测点一次「去阅读」后章节 `pending` 立即从 1 变成 0；接口幂等，重复调用仍返回
`status:true`。专题页里的 `multimedia/readlog` 与 `data-xxt.aichaoxing.com/analysis/ac_mark`
只是阅读时长统计（“当天阅读时长，次日更新”），**不是**任务点完成条件，可以忽略。
Go 侧见 `CompleteRead`，扫描到 `Read.Completable()` 的任务点会直接调用该接口。

### 直播任务点（2026-11 调研）

cards 里是 `module=insertlive`，附件包含 `liveSetEnc` / `authEnc` / `liveId` / `streamName` / `vdoid`，
`job:true` 表示未完成。和视频一样，进度以平台为准：

```
GET /ananas/live/liveinfo?liveid=&userid=&clazzid=&knowledgeid=&courseid=&jobid=&ut=s
→ temp.data: duration(秒) / timeLong(毫秒) / timeLongValue(分钟) / percentValue / liveStatus / ifReview
```

- `liveStatus=0`：未开始，跳过；
- `liveStatus=4 && ifReview=1`：已结束且**不允许回放**，无法完成；
- `liveStatus=4 && ifReview=0`：已结束可回放；
- `liveStatus=1`：正在直播。

完成方式是每 60 秒上报一次观看时长（与网页播放器一致）：

```
GET https://zhibo.chaoxing.com/saveTimePc?streamName=&vdoid=&userId=&isStart=0|1&t=<毫秒>&courseId=
→ @success
```

第一次上报前先打开一次
`https://zhibo.chaoxing.com/{liveId}?courseId=…&classId=…&knowledgeId=…&jobId=…&userId=…&rt=0.9&livesetenc=…&isjob=true&watchingInCourse=1&customPara1=<classId>_<courseId>&customPara2=<authEnc>&isNotDrag=1&jobfs=0`
让 `zhibo.chaoxing.com` 建立会话。之后用 `liveinfo` 的 `percentValue` 判断是否达到 `rt`（默认 90%），
不要用本地计数推断完成。直播是**真实时间**的，没有可靠的一次性完成接口。

参考实现：[bashijiuhou/cx-study-assistant](https://github.com/bashijiuhou/cx-study-assistant)、
[Kerxs/XueXiTong-Plus](https://github.com/Kerxs/XueXiTong-Plus)（作者用 59/42 分钟真实回放验证过）、
[yatori-dev/yatori-go-core](https://github.com/yatori-dev/yatori-go-core)。旧项目里的
`zhibo.chaoxing.com/live/saveCourseJob?token=` 一次性完成已不可靠。

### 解析策略（为什么不怕改版）

题目解析只认「提交时真正用到的字段」，CSS class 只用来辅助切分文字：

| 字段 | 用途 |
| --- | --- |
| `input[name=answer<qid>]` | 答案位（隐藏）/ 判断题选项（`value=true|false`） |
| `input[name=answercheck<qid>]` | 多选题选项 |
| `input[name=answertype<qid>]` | 题型编码（0 单选 / 1 多选 / 2 填空 / 3 判断 / 4 简答） |

由此得到 `qid → 可提交 value 集合`，提交一定使用 value，不用显示字母
（`randomOptions=true` 时两者顺序是打乱的）。题目容器靠「从字段往上爬，直到
祖先里出现别的 qid」确定，不依赖 `singleQuesId` / `TiMu`。

题干优先取外层 `font-cxsecret` 容器（有 `fontLabel` 时优先它），排除选项行和表单控件；
题型从 `answertype` 取，取不到再从题干开头的 `【多选题】` 推断。

以下情况会标记 `degraded`，并把解密后的整块明文放进 `raw` 一起交给模型：

- 题干为空；
- 该有选项（单选/多选/判断）却一个 value 都没找到；
- 选项 value 找到了但正文全是空的。

`raw` 里每个选项会带真实提交值标记，例如 `〔B〕A “打虎”`，即使显示字母和 value
错位，模型也能对上。这样即使页面结构大改，也能「尽量答对」而不是直接失败；
提交前仍会用 `qid → value` 白名单校验，绝不提交页面里不存在的值。

## Web 版（多用户）

```bash
cxbrush                                        # 默认 127.0.0.1:8080
cxbrush -addr 0.0.0.0:8080 -open               # 对外监听并放开注册
cxbrush -data /srv/cxbrush                     # 指定数据目录
```

- 前端是 `web/` 下的 Svelte 5 + Vite 单页应用，构建产物在 `internal/server/web/dist`，编译时 `embed` 进二进制：

```bash
mise run build                          # 改完前端必须重新 build，否则 go build 里还是旧资源
cd web && pnpm dev                      # 调试用，/api 自动代理到 127.0.0.1:8080
```

- 第一个注册的用户直接生效；之后默认关闭注册，需要 `-open`。
- 学习通登录支持账号密码：`POST /api/chaoxing/login` 走官方 `fanyalogin`
  （AES-128-CBC，key/iv 都是 `u2oh6Vu^HWe4_AES`），登录成功后跟随服务端下发的
  跳转补全会话 Cookie。**密码不落库**，只保存会话 Cookie（AES-GCM 加密后存 SQLite）。
  遇到验证码/扫码/二次验证的账号，仍可用 `PUT /api/chaoxing` 粘贴 Cookie 兜底。
- 登录态有效期：`p_auth_token` 是 JWT，`exp - loginTime` 固定 604800 秒（7 天）；
  `fid` / `xxtenc` 是 30 天；`JSESSIONID` / `uf` 是会话级。普通页面请求
  （首页、课程页、passport2 登录页）**不会**续期，但
  `GET https://sso.chaoxing.com/apis/login/userLogin4Uname.do` 每次调用都会重发
  `_uid/_d/cx_p_token/p_auth_token` 并把 7 天窗口整体后移。
  所以服务启动时立刻续期一次，之后每 1 小时续一次（`Runner.StartKeepAlive`）：
  只要 7 天内有任意一次运行就不会掉登录，进程停超过 7 天才需要重新登录。
- 每个用户保存自己的 Cookie，各自排队跑任务。
- 整个进程**共享一份字体比对表**（约 200MB）和一个答题模型，所以不要「一个用户一个进程」。
- 数据目录里有 `cxbrush.db` 和 `secret.key`（0600，别丢，丢了 Cookie 就解不开）。
- 字体还原用的比对字体已内嵌，`CX_REF_FONT` 只是可选覆盖，
  所以 release 是单文件，不依赖工作目录或外置盘。
- 答题模型可以在页面的「模型配置」里改，Key 同样加密落库；优先级是
  **页面配置 > `CX_AI_*` 环境变量**。改完立即生效，不用重启。
- 任务进度用 SSE 推送（`/api/tasks/{id}/events`），刷新页面可从 `after` 续上。
- 页面刷新后的「任务点 N/M」不建投影表：`GET /api/tasks/{id}` 在服务端折叠
  已落库的事件流，直接返回 `items`（每个任务点的 kind/章节/状态/进度/分数），
  前端先拿这份快照打底，再用 SSE 补增量。
- 服务重启时，上次还在跑的任务会被标记为「中断」，不会卡在运行中。

接口一览：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/register` `/api/login` `/api/logout` | 账号 |
| GET | `/api/me` | 当前用户与 Cookie 状态 |
| PUT | `/api/chaoxing` | 保存并校验学习通 Cookie |
| POST | `/api/chaoxing/login` | 手机号/密码登录学习通（只落 Cookie） |
| GET/PUT | `/api/settings/ai` | 读取/保存答题模型配置（`clear:true` 清除） |
| POST | `/api/settings/ai/test` | 用当前或指定的配置做一次连通性测试 |
| GET | `/api/courses` `/api/chapters` | 课程/章节 |
| POST | `/api/tasks` | 新建刷课任务 |
| GET | `/api/tasks` `/api/tasks/{id}` | 任务列表/详情（详情含折叠出的事件 `items`） |
| POST | `/api/tasks/{id}/cancel` | 取消 |
| POST | `/api/tasks/{id}/retry` | 用同样参数重跑 |
| GET | `/api/tasks/{id}/events` | SSE 事件流 |

### 相关文件

- `internal/cx/work.go`：`Work` 模型、URL 构造、页面抓取/解析、`ValidateWork`/`SubmitWork`
- `internal/cx/quiz.go`：题目/选项模型、解密、答案归一化、`BuildAnswers`、`SubmitURL`
- `internal/cx/qparse.go`：以提交字段为准的鲁棒解析（容器切分、题干/选项提取、退化兜底）
- `internal/cx/font.go`：字体提取、字形比对
- `internal/cx/course.go`：`ChapterContent.Works` 从 cards 收集 work
- `internal/cx/study.go`：`CompleteRead` 调用 `/mooc-ans/job/readv2` 完成专题阅读任务点
- `internal/cx/live.go`：直播状态查询、`saveTimePc` 上报与按平台进度收尾
- `internal/cx/ai.go`：模型作答（OpenAI 兼容 / `response_format=json_object`）
- `internal/cx/solve.go`：抓题 → 解密 → 作答 → 提交 的完整流程
- `internal/cx/review.go`：提交后回查批阅页（成绩、对错题数）
- `cmd/cxbrush/serve_cli.go`：服务启动参数（`-addr` / `-data` / `-open`）
- `internal/store`：SQLite 持久层（用户、凭据、任务、事件）
- `internal/server`：HTTP API + SSE + 任务调度 + 内嵌 Web UI
- `internal/assets`：内嵌比对字体（Noto Sans CJK，OFL）
- `web/`：Svelte 5 + Vite 前端源码
- `internal/cx/testdata/work.html`：脱敏测试样本（结构 A：`span[name=answer<qid>][data]`）
- `internal/cx/testdata/work_variant_b.html`：脱敏测试样本（结构 B：`input[name=answercheck<qid>]`）
- `internal/cx/work_test.go`：URL 构造、两种结构整页解析、退化兜底测试
