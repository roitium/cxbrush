<script>
  import { app } from '../lib/state.svelte.js'
  import TaskRow from '../lib/TaskRow.svelte'

  let filter = $state('all')

  const tabs = [
    { key: 'all', label: '全部' },
    { key: 'live', label: '进行中' },
    { key: 'done', label: '已完成' },
    { key: 'failed', label: '失败' },
  ]

  let list = $derived.by(() => {
    if (filter === 'live') return app.tasks.filter((t) => t.status === 'running' || t.status === 'queued')
    if (filter === 'done') return app.tasks.filter((t) => t.status === 'done')
    if (filter === 'failed') return app.tasks.filter((t) => t.status === 'failed' || t.status === 'canceled')
    return app.tasks
  })

  const count = (k) => {
    if (k === 'live') return app.tasks.filter((t) => t.status === 'running' || t.status === 'queued').length
    if (k === 'done') return app.tasks.filter((t) => t.status === 'done').length
    if (k === 'failed') return app.tasks.filter((t) => t.status === 'failed' || t.status === 'canceled').length
    return app.tasks.length
  }
</script>

<div class="page">
  <div class="head">
    <div>
      <h1>任务</h1>
      <p class="hint">每个任务是一次选定章节的完整执行记录。</p>
    </div>
    <span class="sp"></span>
    <a class="btn primary" href="#/">开始新任务</a>
  </div>

  <div class="tabs">
    {#each tabs as t}
      <button class="tab" class:on={filter === t.key} onclick={() => (filter = t.key)}>
        {t.label}<span class="num">{count(t.key)}</span>
      </button>
    {/each}
  </div>

  <section class="card list">
    {#if app.tasksLoading}
      <div class="skeleton" style="height:200px"></div>
    {:else if !list.length}
      <div class="empty">
        <span class="ico">◌</span>
        <b>{filter === 'all' ? '还没有任务' : '这个筛选下没有任务'}</b>
        <span class="hint">{filter === 'all' ? '去概览页选一门课开始。' : '换个筛选看看。'}</span>
        {#if filter === 'all'}<a class="btn primary sm" href="#/">去开始</a>{/if}
      </div>
    {:else}
      {#each list as t (t.id)}
        <TaskRow task={t} />
      {/each}
    {/if}
  </section>
</div>

<style>
  .page { max-width: 1080px; margin: 0 auto; padding: 22px 20px 60px; display: grid; gap: 14px; }
  .head { display: flex; align-items: center; gap: 14px; }
  .head h1 { font-size: 24px; }
  .head p { margin-top: 2px; }
  .tabs { display: flex; gap: 6px; flex-wrap: wrap; }
  .tab {
    display: inline-flex; align-items: center; gap: 6px;
    font: inherit; font-size: 12.5px;
    color: var(--dim);
    background: transparent;
    border: 1px solid var(--line-soft);
    border-radius: 999px;
    padding: 5px 12px;
    cursor: pointer;
  }
  .tab:hover { color: var(--fg); background: #161b24; }
  .tab.on { color: var(--fg); background: #1b212b; border-color: var(--line); }
  .tab .num { color: var(--dim-2); font-size: 11px; }
  .list { display: grid; gap: 2px; padding: 10px; }
  .list :global(.taskrow + .taskrow) { box-shadow: inset 0 1px 0 var(--line-soft); }
  @media (max-width: 680px) {
    .page { padding: 16px 12px 48px; }
  }
</style>
