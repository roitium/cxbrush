<script>
  import { api } from './api.js'
  import TriCheck from './TriCheck.svelte'

  let { courses = [], hasAI = true, onStart } = $props()

  let current = $state(0)
  let chapters = $state([])
  let selected = $state(new Set())
  let loading = $state(false)
  let error = $state('')
  let doWorks = $state(true)
  let starting = $state(false)
  let manual = $state(false)
  let picked = $state(false)
  let nonce = $state(0)

  let course = $derived(courses[current] ?? null)
  let chosen = $derived(chapters.filter((c) => selected.has(c.id)))

  let groups = $derived.by(() => {
    const m = new Map()
    for (const ch of chapters) {
      const key = ch.section || '其他章节'
      if (!m.has(key)) m.set(key, [])
      m.get(key).push(ch)
    }
    return [...m].map(([name, items]) => ({
      name,
      items,
      selected: items.filter((c) => selected.has(c.id)).length,
      pending: items.reduce((n, c) => n + (c.pending > 0 ? c.pending : 0), 0),
    }))
  })

  let pendingTotal = $derived(chapters.reduce((n, c) => n + (c.pending > 0 ? c.pending : 0), 0))
  let allSelected = $derived(chapters.length > 0 && chosen.length === chapters.length)

  // 首次拿到课程时，回到上次用过的课程。
  $effect(() => {
    if (picked || !courses.length) return
    picked = true
    const saved = localStorage.getItem('cxbrush.course')
    const i = courses.findIndex((c) => String(c.courseId) === saved)
    if (i > 0) current = i
  })

  $effect(() => {
    void nonce
    const c = course
    if (!c) {
      chapters = []
      return
    }
    let alive = true
    loading = true
    error = ''
    chapters = []
    api(`/api/chapters?courseId=${c.courseId}&classId=${c.classId}&cpi=${c.cpi}`)
      .then((r) => {
        if (!alive) return
        chapters = r.chapters || []
        selected = new Set(chapters.map((x) => x.id))
        localStorage.setItem('cxbrush.course', String(c.courseId))
      })
      .catch((e) => {
        if (!alive) return
        error = e.message
        // 有些课程的章节列表本来就抓不到，自动跳到下一门，免得一进来就像坏了。
        if (!manual && current + 1 < courses.length) current += 1
      })
      .finally(() => {
        if (alive) loading = false
      })
    return () => {
      alive = false
    }
  })

  function toggle(id) {
    const next = new Set(selected)
    next.has(id) ? next.delete(id) : next.add(id)
    selected = next
  }

  function toggleGroup(g) {
    const next = new Set(selected)
    const on = g.selected < g.items.length
    for (const c of g.items) on ? next.add(c.id) : next.delete(c.id)
    selected = next
  }

  function all(on) {
    selected = on ? new Set(chapters.map((c) => c.id)) : new Set()
  }

  function selectPending() {
    const todo = chapters.filter((c) => c.pending !== 0)
    selected = new Set((todo.length ? todo : chapters).map((c) => c.id))
  }

  async function start() {
    if (!course || !chosen.length || starting) return
    starting = true
    error = ''
    try {
      await onStart(course, chosen, doWorks)
    } catch (e) {
      error = e.message
    } finally {
      starting = false
    }
  }
</script>

<div class="picker">
  <div class="side">
    <label class="field">
      <span>课程</span>
      <select bind:value={current} onchange={() => (manual = true)}>
        {#each courses as c, i (c.courseId)}
          <option value={i}>{c.name}{c.teacher ? ` · ${c.teacher}` : ''}</option>
        {/each}
      </select>
    </label>

    <label class="switch" class:off={!doWorks}>
      <input type="checkbox" bind:checked={doWorks} />
      <span>
        <b>自动做章节测验</b>
        <small>{doWorks ? (hasAI ? 'AI 作答并提交' : '未配置模型，会跳过') : '只刷视频、文档和阅读'}</small>
      </span>
    </label>

    {#if doWorks && !hasAI}
      <p class="warnline">还没有配置答题模型，勾选的测验会被跳过。<a href="#/settings">去配置</a></p>
    {/if}

    <div class="summary">
      <div>
        <span class="label">已选章节</span>
        <b class="num">{chosen.length}<i>/{chapters.length}</i></b>
      </div>
      <div>
        <span class="label">待完成任务点</span>
        <b class="num">{pendingTotal}</b>
      </div>
    </div>

    <div class="quick">
      <button class="btn sm" onclick={selectPending} disabled={!chapters.length}>只选未完成</button>
      <button class="btn sm ghost" onclick={() => all(false)} disabled={!chapters.length}>清空</button>
    </div>

    <button class="btn primary start" disabled={starting || !chosen.length} onclick={start}>
      {starting ? '正在创建…' : `开始任务${chosen.length ? `（${chosen.length} 章）` : ''}`}
    </button>
    <p class="hint">
      {#if course}将按顺序完成所选章节，已完成的会自动跳过。{:else}选择一门课程{/if}
    </p>
  </div>

  <div class="chapters">
    <div class="chead">
      <TriCheck checked={allSelected} indeterminate={chosen.length > 0 && !allSelected} onchange={() => all(!allSelected)} />
      <b>章节</b>
      <span class="sp"></span>
      {#if pendingTotal > 0}<span class="hint num">待完成 {pendingTotal}</span>{/if}
    </div>

    <div class="list">
    {#if loading}
      {#each { length: 5 } as _, i}
        <div class="skeleton sk-row" style="opacity:{1 - i * 0.14}"></div>
      {/each}
    {:else if error}
      <div class="empty">
        <span class="ico">⚠</span>
        <span>{error}</span>
        <button class="btn sm" onclick={() => nonce++}>重试</button>
      </div>
    {:else if !chapters.length}
      <div class="empty"><span class="ico">□</span><span>这门课没有可用的章节</span></div>
    {:else}
      {#each groups as g (g.name)}
        <div class="group">
          <div class="ghead">
            <TriCheck
              checked={g.selected === g.items.length && g.items.length > 0}
              indeterminate={g.selected > 0 && g.selected < g.items.length}
              onchange={() => toggleGroup(g)}
            />
            <span class="gname truncate">{g.name}</span>
            <span class="gcount num">{g.selected}/{g.items.length}</span>
            {#if g.pending}<span class="gpending num">待完成 {g.pending}</span>{/if}
          </div>
          {#each g.items as ch (ch.id)}
            <label class="ch">
              <input type="checkbox" checked={selected.has(ch.id)} onchange={() => toggle(ch.id)} />
              <span class="chtitle truncate">{ch.title}</span>
              {#if ch.pending > 0}
                <span class="tag num">{ch.pending}</span>
              {:else if ch.pending === 0}
                <span class="tag done">✓</span>
              {/if}
            </label>
          {/each}
        </div>
      {/each}
    {/if}
    </div>
  </div>
</div>

<style>
  .picker {
    display: grid;
    grid-template-columns: 268px minmax(0, 1fr);
    gap: 18px;
    align-items: start;
  }
  .side { display: grid; gap: 12px; }
  .switch {
    display: flex; align-items: center; gap: 9px;
    padding: 8px 12px;
    border: 1px solid var(--line);
    border-radius: 10px;
    cursor: pointer;
    background: var(--bg-soft);
  }
  .switch span { display: grid; line-height: 1.35; }
  .switch b { font-size: 12.5px; font-weight: 550; }
  .switch small { color: var(--dim-2); font-size: 11px; }
  .switch.off b { color: var(--dim); }
  .warnline { font-size: 12px; color: var(--warn); }
  .summary { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
  .summary > div {
    display: grid; gap: 1px;
    padding: 9px 11px;
    border: 1px solid var(--line-soft);
    border-radius: 10px;
    background: rgba(10, 12, 16, .35);
  }
  .summary b { font-size: 18px; font-weight: 600; line-height: 1.2; }
  .summary b i { font-style: normal; font-size: 11px; color: var(--dim-2); }
  .quick { display: flex; gap: 6px; }
  .quick .btn { flex: 1; }
  .start { width: 100%; padding: 10px 14px; }
  .chapters { min-width: 0; display: grid; gap: 8px; }
  .chead { display: flex; align-items: center; gap: 9px; padding: 0 2px; }
  .chead b { font-size: 12px; color: var(--fg-2); }
  .list {
    max-height: 372px;
    overflow: auto;
    border: 1px solid var(--line-soft);
    border-radius: 11px;
    background: rgba(10, 12, 16, .35);
  }
  .sk-row { height: 34px; margin: 8px 10px; }
  .group + .group { border-top: 1px solid var(--line-soft); }
  .ghead {
    position: sticky; top: 0;
    display: flex; align-items: center; gap: 9px;
    padding: 8px 12px;
    background: #12161d;
    z-index: 1;
  }
  .gname { font-size: 12px; font-weight: 600; letter-spacing: .01em; color: var(--fg-2); flex: 1; }
  .gcount { font-size: 11px; color: var(--dim-2); }
  .gpending { font-size: 10.5px; color: var(--warn); border: 1px solid var(--warn-soft); background: rgba(224, 166, 60, .07); padding: 0 6px; border-radius: 999px; }
  .ch {
    display: flex; align-items: center; gap: 10px;
    padding: 7px 12px 7px 34px;
    cursor: pointer;
    font-size: 13px;
  }
  .ch:hover { background: #171c25; }
  .chtitle { flex: 1; color: var(--fg-2); }
  .tag { font-size: 10.5px; color: var(--dim); min-width: 20px; text-align: right; }
  .tag.done { color: #3f8f5c; }
  @media (max-width: 860px) {
    .picker { grid-template-columns: 1fr; }
  }
</style>
