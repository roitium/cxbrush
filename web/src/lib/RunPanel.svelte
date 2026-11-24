<script>
  import { api } from './api.js'

  let { taskId = 0, onDone } = $props()

  let logs = $state([])
  let items = $state([])
  let scan = $state({ done: 0, total: 0 })
  let queued = $state(null)
  let progress = $state({})
  let phase = $state('')
  let summary = $state('')
  let ended = $state(false)

  const phaseText = { queued: '排队中', running: '运行中', done: '已完成', failed: '失败', canceled: '已取消' }
  const icon = { passed: '✓', failed: '✗', running: '…', skipped: '–' }

  let pct = $derived.by(() => {
    if (ended) return 100
    if (scan.total && scan.done < scan.total) return (scan.done / scan.total) * 100
    const vals = Object.values(progress)
    if (vals.length) {
      const total = vals.reduce((a, v) => a + (v.total || 0), 0)
      const done = vals.reduce((a, v) => a + (v.confirmed || 0), 0)
      if (total) return Math.min(100, (done / total) * 100)
    }
    return 0
  })

  let status = $derived(
    phase === 'running' && scan.total && scan.done < scan.total
      ? `扫描章节 ${scan.done}/${scan.total}`
      : summary || (queued ? `视频 ${queued.videos} · 文档 ${queued.docs} · 测验 ${queued.works}` : ''),
  )

  function log(text, err = false) {
    logs = [...logs.slice(-499), { text, err, ts: new Date() }]
  }

  function upsert(it) {
    const i = items.findIndex((x) => x.id === it.id)
    if (i >= 0) {
      items = items.map((x, j) => (j === i ? { ...x, ...it } : x))
    } else {
      items = [...items, it]
    }
  }

  function describe(it) {
    const bits = []
    if (it.kind === 'video' && it.total) bits.push(`${it.cur || 0}/${it.total} 秒`)
    if (it.kind === 'work') {
      bits.push(it.state === 'skipped' ? `跳过：${it.reason || ''}` : `${it.score || 0} 分`)
      if (it.correct || it.wrong) bits.push(`对 ${it.correct || 0} / 错 ${it.wrong || 0}`)
    }
    if (it.reason && it.kind !== 'work') bits.push(it.reason)
    return bits.join(' · ')
  }

  function reset() {
    logs = []
    items = []
    scan = { done: 0, total: 0 }
    queued = null
    progress = {}
    phase = ''
    summary = ''
    ended = false
  }

  $effect(() => {
    const id = taskId
    if (!id) return
    reset()

    // 先用数据库里的快照打底，事件回放只负责补增量。
    api(`/api/tasks/${id}`)
      .then((r) => {
        if (taskId !== id) return
        for (const it of r.items || []) upsert(it)
        phase = r.task?.status || ''
        if (r.task?.message) summary = r.task.message
      })
      .catch(() => {})

    const es = new EventSource(`/api/tasks/${id}/events?after=0`)
    const on = (kind, fn) => es.addEventListener(kind, (e) => fn(JSON.parse(e.data)))

    on('log', (d) => log(d.text, d.err))
    on('scan', (d) => (scan = d))
    on('queued', (d) => {
      queued = d
      log(`任务点：视频 ${d.videos}，文档 ${d.docs}，测验 ${d.works}，已跳过 ${d.skipped}`)
    })
    on('progress', (d) => {
      progress = { ...progress, [d.id]: d }
      upsert({ id: d.id, kind: 'video', chapter: d.chapter, name: d.name, state: 'running', cur: d.confirmed, total: d.total })
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
      summary = `通过 ${d.passed}，失败 ${d.failed}，文档 ${d.docs}，测验 ${d.works}`
    })
    on('status', (d) => (phase = d.status))
    on('end', (d) => {
      ended = true
      phase = d.status
      summary = summary || d.message
      es.close()
      onDone?.()
    })
    es.onerror = () => {}
    return () => es.close()
  })
</script>

<div class="card">
  <div class="head">
    <h2>运行详情 {taskId ? `#${taskId}` : ''}</h2>
    {#if phase}<span class="badge {phase}">{phaseText[phase] || phase}</span>{/if}
  </div>

  {#if !taskId}
    <p class="hint">在左侧选择一个任务，或新建一个。</p>
  {:else}
    <div class="bar"><i style="width:{pct}%"></i></div>
    <p class="hint status">{status}</p>

    {#if items.length}
      <div class="box">
        <h3>任务点 {items.filter((i) => i.state === 'passed').length}/{items.length}</h3>
        <div class="rows">
          {#each items as it (it.id)}
            <div class="row-item">
              <span class={it.state} title={it.state}>{icon[it.state] || '·'}</span>
              <span class="name">{it.name}</span>
              <span class="hint">{describe(it)}</span>
            </div>
          {/each}
        </div>
      </div>
    {/if}

    <div class="log">
      {#each logs as l}
        <div class="line">
          <span class="ts">{l.ts.toLocaleTimeString('zh-CN', { hour12: false })}</span>
          <span class:err={l.err}>{l.text}</span>
        </div>
      {/each}
      {#if !logs.length}<span class="hint">等待事件…</span>{/if}
    </div>
  {/if}
</div>

<style>
  .head { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; }
  .head h2 { margin: 0; }
  .badge { font-size: 11px; padding: 2px 8px; border-radius: 999px; border: 1px solid var(--line); color: var(--dim); }
  .badge.running { color: var(--acc); border-color: #27456f; }
  .badge.done { color: var(--ok); border-color: #1f4b2a; }
  .badge.failed { color: var(--err); border-color: #5a2224; }
  .badge.queued { color: var(--warn); border-color: #5a4a1f; }
  .bar { height: 6px; background: #0b0d11; border-radius: 3px; overflow: hidden; }
  .bar i { display: block; height: 100%; background: var(--acc); transition: width .3s; }
  .status { margin: 6px 0 12px; min-height: 18px; }
  .box { border: 1px solid var(--line); border-radius: 8px; padding: 10px; margin-bottom: 12px; }
  .box h3 { font-size: 12px; color: var(--dim); margin-bottom: 8px; font-weight: 600; }
  .rows { display: grid; gap: 6px; max-height: 240px; overflow: auto; }
  .row-item { display: flex; gap: 8px; align-items: baseline; font-size: 13px; }
  .row-item .name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .row-item .passed { color: var(--ok); }
  .row-item .failed { color: var(--err); }
  .row-item .running { color: var(--acc); }
  .row-item .skipped { color: var(--dim); }
  .log { height: 280px; overflow: auto; background: #0b0d11; border: 1px solid var(--line); border-radius: 8px; padding: 10px; font: 12px/1.6 ui-monospace, SFMono-Regular, Menlo, monospace; }
  .line { display: flex; gap: 8px; }
  .ts { color: #4b5563; flex: none; }
  .line span:last-child { white-space: pre-wrap; word-break: break-word; }
  .hint { font-size: 12px; color: var(--dim); }
</style>
