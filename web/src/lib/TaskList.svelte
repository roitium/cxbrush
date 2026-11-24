<script>
  import { fmtTime } from './api.js'

  let { tasks = [], activeId = 0, onSelect, onRetry } = $props()

  const text = { queued: '排队中', running: '运行中', done: '已完成', failed: '失败', canceled: '已取消' }
  const finished = (s) => s === 'done' || s === 'failed' || s === 'canceled'
</script>

<div class="card">
  <h2>任务</h2>
  {#if !tasks.length}
    <p class="hint">还没有任务</p>
  {/if}
  <div class="list">
    {#each tasks as t (t.id)}
      <div class="task" class:active={t.id === activeId}>
        <button class="pick" onclick={() => onSelect?.(t.id)}>
          <b>{t.courseName} · {t.chapters.length} 章</b>
          <small>#{t.id} · {fmtTime(t.createdAt)}{t.message ? ' · ' + t.message : ''}</small>
        </button>
        <span class="badge {t.status}">{text[t.status] || t.status}</span>
        {#if finished(t.status)}
          <button class="ghost small" onclick={() => onRetry?.(t.id)}>重跑</button>
        {/if}
      </div>
    {/each}
  </div>
</div>

<style>
  .list { display: grid; gap: 8px; }
  .task { display: flex; gap: 8px; align-items: center; border: 1px solid var(--line); border-radius: 8px; padding: 8px 10px; }
  .task.active { border-color: var(--acc); }
  .pick { flex: 1; min-width: 0; text-align: left; background: none; border: 0; padding: 0; }
  .pick b { display: block; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pick small { display: block; color: var(--dim); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .small { padding: 4px 10px; font-size: 12px; }
  .badge { font-size: 11px; padding: 2px 8px; border-radius: 999px; border: 1px solid var(--line); color: var(--dim); flex: none; }
  .badge.running { color: var(--acc); border-color: #27456f; }
  .badge.done { color: var(--ok); border-color: #1f4b2a; }
  .badge.failed { color: var(--err); border-color: #5a2224; }
  .badge.queued { color: var(--warn); border-color: #5a4a1f; }
</style>
