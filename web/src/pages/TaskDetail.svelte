<script>
  import { api, fmtTime } from '../lib/api.js'
  import { loadTasks } from '../lib/state.svelte.js'
  import { nav } from '../lib/router.svelte.js'
  import StatusPill from '../lib/StatusPill.svelte'

  let { id } = $props()

  let task = $state(null)
  let items = $state([])
  let logs = $state([])
  let scan = $state({ done: 0, total: 0 })
  let queued = $state(null)
  let progress = $state({})
  let phase = $state('')
  let summary = $state('')
  let ended = $state(false)
  let loading = $state(true)
  let error = $state('')
  let logsOpen = $state(true)
  let busy = $state(false)
  let logBox = $state(null)

  let hasErr = $derived(logs.some((l) => l.err))
  let errCount = $derived(logs.filter((l) => l.err).length)

  // 任务点总数以扫描出的计划为准（含已完成/跳过的），否则各计数口径不一致。
  let total = $derived(
    items.length ||
      (queued ? (queued.videos || 0) + (queued.docs || 0) + (queued.reads || 0) + (queued.lives || 0) + (queued.works || 0) : 0),
  )
  let settled = $derived(items.filter((i) => ['passed', 'failed', 'skipped'].includes(i.state)).length)
  let passed = $derived(items.filter((i) => i.state === 'passed').length)
  let works = $derived(items.filter((i) => i.kind === 'work'))
  let workDone = $derived(works.filter((i) => i.state === 'passed').length)

  // 已完成的任务点算 1，正在播的按视频自身进度折算。否则一节 10 分钟的视频
  // 会让整体进度一直停在 0%，看起来像卡住了。
  let progressUnits = $derived.by(() => {
    let sum = 0
    for (const it of items) {
      if (['passed', 'failed', 'skipped'].includes(it.state)) sum += 1
      else if (it.state === 'running' && it.total) sum += Math.min(1, (it.cur || 0) / it.total)
    }
    return sum
  })

  let pct = $derived.by(() => {
    if (scan.total && scan.done < scan.total) return (scan.done / scan.total) * 100
    if (ended && phase === 'done') return 100
    if (total) return Math.min(100, (progressUnits / total) * 100)
    if (ended) return phase === 'done' ? 100 : 0
    return 0
  })

  let barClass = $derived(phase === 'done' ? 'done' : phase === 'failed' ? 'failed' : '')

  let groups = $derived.by(() => {
    const m = new Map()
    for (const it of items) {
      const k = it.chapter || '未分组'
      if (!m.has(k)) m.set(k, [])
      m.get(k).push(it)
    }
    return [...m].map(([key, list]) => ({
      key,
      name: chapterName(key),
      items: list,
      passed: list.filter((x) => x.state === 'passed').length,
    }))
  })

  let active = $derived(items.filter((i) => i.state === 'running'))

  let statusLine = $derived.by(() => {
    if (phase === 'running' && scan.total && scan.done < scan.total) return `扫描章节 ${scan.done}/${scan.total}`
    if (active.length) {
      const playing = active.some((a) => a.kind === 'video' || a.kind === 'live')
      return `${playing ? '正在播放' : '正在处理'} ${active.length} 个任务点`
    }
    // 章节之间的空档（比如正在拉下一章、或在做文档）也要有个「在做的事」，避免看起来卡住。
    if (phase === 'running' && logs.length) return logs[logs.length - 1].text
    if (summary) return summary
    if (task?.message) return task.message
    if (queued) return `视频 ${queued.videos} · 文档 ${queued.docs} · 阅读 ${queued.reads} · 直播 ${queued.lives} · 测验 ${queued.works}`
    return ''
  })

  const icon = { passed: '✓', failed: '✗', running: '·', skipped: '–' }

  // 事件里只带 chapterId，用任务自身的章节表还原可读的章节名。
  function chapterName(id) {
    if (!id) return '未分组'
    const hit = task?.chapters?.find((c) => String(c.id) === String(id))
    return hit?.title || String(id)
  }

  function describe(it) {
    const bits = []
    if ((it.kind === 'video' || it.kind === 'live') && it.total) {
      bits.push(it.state === 'pending' ? `${it.total} 秒` : `${it.cur || 0}/${it.total} 秒`)
    }
    if (it.kind === 'work') {
      if (it.state === 'skipped') bits.push(`跳过${it.reason ? `：${it.reason}` : ''}`)
      else if (it.state === 'passed') bits.push(`${it.score ?? 0} 分`)
      if (it.correct || it.wrong) bits.push(`对 ${it.correct || 0} · 错 ${it.wrong || 0}`)
    }
    if (it.reason && it.kind !== 'work') bits.push(it.reason)
    return bits.join(' · ')
  }

  function log(text, err = false) {
    logs = [...logs.slice(-499), { text, err, ts: new Date() }]
    if (err) logsOpen = true
  }

  function upsert(it) {
    const i = items.findIndex((x) => x.id === it.id)
    if (i >= 0) items = items.map((x, j) => (j === i ? { ...x, ...it } : x))
    else items = [...items, it]
  }

  function reset() {
    task = null
    items = []
    logs = []
    scan = { done: 0, total: 0 }
    queued = null
    progress = {}
    phase = ''
    summary = ''
    ended = false
    logsOpen = true
    error = ''
  }

  $effect(() => {
    const tid = Number(id)
    let alive = true
    reset()
    loading = true

    api(`/api/tasks/${tid}`)
      .then((r) => {
        if (!alive) return
        task = r.task
        for (const it of r.items || []) upsert(it)
        phase = r.task?.status || ''
        summary = r.task?.message || ''
      })
      .catch((e) => {
        if (alive) error = e.message
      })
      .finally(() => {
        if (alive) loading = false
      })

    const es = new EventSource(`/api/tasks/${tid}/events?after=0`)
    const on = (kind, fn) => es.addEventListener(kind, (e) => fn(JSON.parse(e.data)))

    on('log', (d) => log(d.text, d.err))
    on('scan', (d) => (scan = d))
    on('plan', (d) => {
      for (const it of d.items || []) upsert(it)
    })
    on('queued', (d) => {
      queued = d
      log(`任务点：视频 ${d.videos}，文档 ${d.docs}，阅读 ${d.reads}，直播 ${d.lives}，测验 ${d.works}，已跳过 ${d.skipped}`)
    })
    on('progress', (d) => {
      progress = { ...progress, [d.id]: d }
      upsert({ id: d.id, kind: d.kind || 'video', chapter: d.chapter, name: d.name, state: 'running', cur: d.confirmed, total: d.total })
    })
    on('task', (d) => {
      upsert({
        id: d.id,
        kind: d.kind || 'video',
        chapter: d.chapter,
        name: d.name,
        state: d.passed ? 'passed' : 'failed',
        reason: d.reason,
      })
      log(`${d.passed ? '✓' : '✗'} ${d.name}${d.reason ? ' — ' + d.reason : ''}`, !d.passed)
    })
    on('work', (d) => {
      upsert({
        id: 'work_' + d.chapter,
        kind: 'work',
        chapter: d.chapter,
        name: d.title,
        state: d.submitted ? 'passed' : d.skipped ? 'skipped' : 'failed',
        reason: d.reason,
        score: d.score,
        correct: d.correct,
        wrong: d.wrong,
      })
      let s = d.questions ? `${d.title}：${d.questions} 题` : d.title
      if (d.skipped) s += `，跳过（${d.reason}）`
      else if (d.submitted) s += '，已提交'
      if (d.correct || d.wrong) s += `，${d.score} 分（对 ${d.correct} / 错 ${d.wrong}）`
      log(s, d.wrong > 0)
    })
    on('finished', (d) => {
      summary = `通过 ${d.passed}，失败 ${d.failed}，文档 ${d.docs}，阅读 ${d.reads}，直播 ${d.lives}，测验 ${d.works}`
    })
    on('status', (d) => (phase = d.status))
    on('end', (d) => {
      ended = true
      phase = d.status
      summary = summary || d.message
      task = task ? { ...task, status: d.status, message: d.message } : task
      es.close()
      loadTasks()
    })
    es.onerror = () => {}

    return () => {
      alive = false
      es.close()
    }
  })

  // 日志自动滚到底部，正在跑的时候不用手动找最新一行。
  $effect(() => {
    logs.length
    if (logsOpen && logBox) logBox.scrollTop = logBox.scrollHeight
  })

  async function cancel() {
    if (!task) return
    busy = true
    try {
      await api(`/api/tasks/${task.id}/cancel`, { method: 'POST' })
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }

  async function retry() {
    if (!task) return
    busy = true
    try {
      const r = await api(`/api/tasks/${task.id}/retry`, { method: 'POST' })
      await loadTasks()
      nav(`/tasks/${r.task.id}`)
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }

  const done = $derived(ended || ['done', 'failed', 'canceled'].includes(phase))
</script>

<div class="page">
  <div class="crumb">
    <a class="btn ghost sm" href="#/tasks">← 任务</a>
    <span class="hint">#{id}</span>
  </div>

  {#if error && !task}
    <div class="card empty">
      <span class="ico">⚠</span>
      <b>读取任务失败</b>
      <span class="hint">{error}</span>
      <a class="btn sm primary" href="#/tasks">回到任务列表</a>
    </div>
  {:else}
    <div class="head">
      <div class="title">
        <h1>{task?.courseName || `任务 #${id}`}</h1>
        <p class="hint">
          {task ? `${task.chapters?.length ?? 0} 个章节 · ${fmtTime(task.createdAt)}${task.doWorks ? ' · 自动答题' : ''}${task.doLive ? ' · 自动直播' : ''}` : '加载中…'}
        </p>
      </div>
      <span class="sp"></span>
      <StatusPill status={phase || 'queued'} />
      {#if !done}
        <button class="btn danger sm" disabled={busy} onclick={cancel}>取消</button>
      {:else}
        <button class="btn sm" disabled={busy} onclick={retry}>重跑</button>
      {/if}
    </div>

    <section class="card prog">
      <div class="prow">
        <b class="num pct">{Math.round(pct)}%</b>
        <div class="ptext">
          <span class="status">
            {#if !done && phase !== 'queued'}<i class="beat"></i>{/if}
            {statusLine || '等待开始…'}
          </span>
          <span class="hint">
            {#if total}
              {settled}/{total} 任务点
            {:else if items.length}
              {passed}/{items.length} 已通过
            {/if}
            {#if works.length} · 测验 {workDone}/{works.length}{/if}
          </span>
        </div>
      </div>
      <div class="bar {barClass}"><i style="width:{Math.min(100, pct)}%"></i></div>
      {#if active.length}
        {#each active.slice(0, 4) as a (a.id)}
          <div class="current">
            <span class="hint">当前</span>
            <span class="cname truncate">{a.name}</span>
            {#if a.total}
              <span class="cbar"><i style="width:{Math.min(100, ((a.cur || 0) / a.total) * 100)}%"></i></span>
              <span class="num hint">{a.cur || 0}/{a.total} 秒</span>
            {:else}
              <span class="num hint">处理中…</span>
            {/if}
          </div>
        {/each}
      {/if}
    </section>

    <div class="grid">
      <section class="card points">
        <div class="card-head">
          <h2>任务点</h2>
          <span class="sp"></span>
          <span class="hint num">{passed}/{items.length}</span>
        </div>
        {#if loading && !items.length}
          <div class="skeleton" style="height:160px"></div>
        {:else if !items.length}
          <div class="empty">
            <span class="ico">◌</span>
            <b>还没有任务点</b>
            <span class="hint">{done ? '任务没有产生任务点记录。' : '正在扫描章节…'}</span>
          </div>
        {:else}
          <div class="groups">
            {#each groups as g (g.key)}
              <div class="grp">
                <div class="ghead">
                  <span class="gname truncate">{g.name}</span>
                  <span class="hint num">{g.passed}/{g.items.length}</span>
                </div>
                {#each g.items as it (it.id)}
                  <div class="item">
                    <span class="mark {it.state}">{icon[it.state] || '·'}</span>
                    <span class="iname truncate">{it.name}</span>
                    <span class="idetail num">{describe(it)}</span>
                  </div>
                {/each}
              </div>
            {/each}
          </div>
        {/if}
      </section>

      <div class="rail">
        <section class="card logcard">
          <details bind:open={logsOpen}>
            <summary>
              <h2>运行日志</h2>
              {#if errCount}<span class="pill failed">{errCount} 条错误</span>{/if}
              <span class="hint num">{logs.length} 行</span>
              <span class="sp"></span>
              <span class="chev">▾</span>
            </summary>
            <div class="log" bind:this={logBox}>
              {#each logs as l}
                <div class="line">
                  <span class="ts">{l.ts.toLocaleTimeString('zh-CN', { hour12: false })}</span>
                  <span class:err={l.err}>{l.text}</span>
                </div>
              {/each}
              {#if !logs.length}<span class="hint">等待事件…</span>{/if}
            </div>
          </details>
        </section>

        {#if task}
          <section class="card facts">
            <div class="card-head"><h2>任务信息</h2></div>
            <div class="krow"><span class="hint">任务编号</span><span class="num">#{task.id}</span></div>
            <div class="krow"><span class="hint">创建时间</span><span class="num">{fmtTime(task.createdAt)}</span></div>
            <div class="krow"><span class="hint">章节数</span><span class="num">{task.chapters?.length ?? 0}</span></div>
            <div class="krow"><span class="hint">自动答题</span><span>{task.doWorks ? '开启' : '关闭'}</span></div>
            <div class="krow"><span class="hint">状态</span><StatusPill status={phase || task.status} /></div>
          </section>
        {/if}
      </div>
    </div>

    {#if ended && phase === 'failed' && summary}
      <div class="banner">
        <span>{summary}</span>
        <span class="sp"></span>
        <button class="btn sm" onclick={retry}>用相同章节重跑</button>
      </div>
    {/if}
  {/if}
</div>

<style>
  .page { max-width: 1200px; margin: 0 auto; padding: 20px 20px 60px; display: grid; gap: 14px; }
  .crumb { display: flex; align-items: center; gap: 8px; margin-left: -8px; }
  .head { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
  .title { min-width: 0; }
  .head h1 { font-size: 22px; }
  .prog { display: grid; gap: 12px; }
  .prow { display: flex; align-items: center; gap: 18px; }
  .pct { font-size: 28px; font-weight: 600; line-height: 1; min-width: 86px; }
  .ptext { display: grid; gap: 2px; min-width: 0; }
  .status { display: inline-flex; align-items: center; gap: 7px; font-size: 13px; color: var(--fg-2); }
  .beat {
    width: 7px; height: 7px; flex: none;
    border-radius: 50%;
    background: var(--acc-2);
    box-shadow: 0 0 0 0 rgba(91, 140, 255, .55);
    animation: beat 1.6s ease-out infinite;
  }
  @keyframes beat {
    0% { box-shadow: 0 0 0 0 rgba(91, 140, 255, .5); }
    70% { box-shadow: 0 0 0 7px rgba(91, 140, 255, 0); }
    100% { box-shadow: 0 0 0 0 rgba(91, 140, 255, 0); }
  }
  .current {
    display: flex; align-items: center; gap: 9px;
    padding-top: 2px; font-size: 12.5px;
  }
  .cname { color: var(--fg-2); min-width: 0; max-width: 46%; }
  .cbar { flex: 1; height: 3px; border-radius: 999px; background: var(--line); overflow: hidden; }
  .cbar i { display: block; height: 100%; background: var(--acc-2); border-radius: inherit; }
  .grid { display: grid; grid-template-columns: minmax(0, 1.15fr) minmax(0, .85fr); gap: 14px; align-items: start; }
  .rail { display: grid; gap: 14px; align-content: start; min-width: 0; }
  .groups { display: grid; gap: 4px; max-height: 560px; overflow: auto; }
  .grp + .grp { margin-top: 8px; border-top: 1px solid var(--line-soft); padding-top: 8px; }
  .ghead { display: flex; align-items: center; gap: 8px; padding: 2px 4px 6px; }
  .gname { flex: 1; font-size: 12px; font-weight: 600; color: var(--fg-2); }
  .item { display: flex; align-items: center; gap: 9px; padding: 6px 4px; font-size: 13px; }
  .item:hover { background: #171c25; border-radius: 8px; }
  .mark {
    width: 20px; height: 20px; flex: none;
    display: grid; place-items: center;
    border-radius: 50%;
    font-size: 11px;
    border: 1px solid var(--line);
    color: var(--dim-2);
  }
  .mark.passed { color: #6ed894; border-color: #24503a; background: rgba(65, 196, 106, .09); }
  .mark.failed { color: #f4868a; border-color: #5b2a2e; background: rgba(244, 103, 106, .09); }
  .mark.running { color: var(--acc-2); border-color: #2c4370; background: rgba(91, 140, 255, .1); }
  .mark.skipped { color: var(--dim-2); }
  .mark.pending { color: var(--dim-2); border-style: dashed; }
  .iname { flex: 1; color: var(--fg-2); }
  .idetail { font-size: 11px; color: var(--dim-2); flex: none; max-width: 46%; text-align: right; }
  .logcard { padding: 14px 18px; }
  .facts { padding: 14px 18px; }
  .krow {
    display: flex; align-items: center; justify-content: space-between;
    gap: 12px;
    padding: 7px 2px;
    border-bottom: 1px solid var(--line-soft);
    font-size: 12.5px;
  }
  .krow:last-child { border-bottom: 0; }
  summary {
    display: flex; align-items: center; gap: 10px;
    cursor: pointer; list-style: none;
  }
  summary::-webkit-details-marker { display: none; }
  summary h2 { font-size: 15px; }
  .chev { color: var(--dim-2); transition: transform .15s; }
  details[open] .chev { transform: rotate(180deg); }
  .log {
    margin-top: 12px;
    max-height: min(430px, 60vh);
    overflow: auto;
    background: #090c10;
    border: 1px solid var(--line-soft);
    border-radius: 10px;
    padding: 10px 12px;
    font: 11.5px/1.65 var(--mono);
  }
  .line { display: flex; gap: 9px; }
  .ts { color: #46505f; flex: none; }
  .line span:last-child { white-space: pre-wrap; word-break: break-word; color: var(--fg-2); }
  .line span.err { color: #f4868a; }
  @media (max-width: 900px) {
    .grid { grid-template-columns: 1fr; }
    .page { padding: 16px 12px 48px; }
    .pct { font-size: 24px; min-width: 70px; }
  }
</style>
