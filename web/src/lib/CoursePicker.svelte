<script>
  import { api } from './api.js'

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

  let course = $derived(courses[current] ?? null)
  let chosen = $derived(chapters.filter((c) => selected.has(c.id)))

  // 第一次加载时，优先回到上次用过的课程。
  $effect(() => {
    if (picked || !courses.length) return
    picked = true
    const saved = localStorage.getItem('cxbrush.course')
    const i = courses.findIndex((c) => String(c.courseId) === saved)
    if (i > 0) current = i
  })

  $effect(() => {
    const c = course
    if (!c) return
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

  function all(on) {
    selected = on ? new Set(chapters.map((c) => c.id)) : new Set()
  }

  async function start() {
    if (!course || !chosen.length) return
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

<div class="card">
  <h2>开始刷课</h2>
  <div class="grid">
    <div>
      <span class="lbl">课程</span>
      <select bind:value={current} onchange={() => (manual = true)}>
        {#each courses as c, i}
          <option value={i}>{c.name}</option>
        {/each}
      </select>
    </div>
    <div>
      <span class="lbl">选项</span>
      <label class="check"><input type="checkbox" bind:checked={doWorks} /> 自动做题（章节测验）</label>
      {#if doWorks && !hasAI}
        <p class="hint err">还没有配置答题模型，自动做题会失败。</p>
      {/if}
    </div>
  </div>

  <div class="row tools">
    <button class="ghost" onclick={() => all(true)}>全选</button>
    <button class="ghost" onclick={() => all(false)}>全不选</button>
    <span class="hint">已选 {chosen.length} / {chapters.length} 个章节</span>
    {#if loading}<span class="hint">加载中…</span>{/if}
  </div>

  {#if error}
    <p class="err">{error}</p>
  {/if}

  <div class="list">
    {#each chapters as ch (ch.id)}
      <label class="ch">
        <input type="checkbox" checked={selected.has(ch.id)} onchange={() => toggle(ch.id)} />
        <span>
          <b>{ch.section ? ch.section + ' / ' : ''}{ch.title}</b>
          <small>ID {ch.id}{ch.pending > 0 ? ` · 待完成 ${ch.pending}` : ''}</small>
        </span>
      </label>
    {/each}
    {#if !chapters.length && !loading && !error}
      <p class="hint pad">没有章节</p>
    {/if}
  </div>

  <div class="row" style="margin-top:12px">
    <button class="primary" disabled={starting || !chosen.length} onclick={start}>开始</button>
  </div>
</div>

<style>
  .lbl { display: block; font-size: 12px; color: var(--dim); margin-bottom: 6px; }
  .check { display: flex; gap: 8px; align-items: center; color: var(--fg); margin: 0; padding: 8px 0; }
  .check input { width: auto; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
  .tools { margin-top: 12px; }
  .list { max-height: 320px; overflow: auto; border: 1px solid var(--line); border-radius: 8px; margin-top: 10px; }
  .ch { display: flex; gap: 10px; align-items: flex-start; padding: 8px 10px; border-bottom: 1px solid var(--line); margin: 0; color: var(--fg); font-size: 14px; cursor: pointer; }
  .ch:last-child { border-bottom: 0; }
  .ch input { width: auto; margin-top: 3px; }
  .ch b { font-weight: 500; }
  .ch small { display: block; color: var(--dim); font-size: 12px; }
  .pad { padding: 10px; margin: 0; }
  p { margin: 10px 0 0; }
</style>
