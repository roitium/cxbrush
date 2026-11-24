<script>
  import { ago } from './api.js'
  import StatusPill from './StatusPill.svelte'

  let { task, compact = false } = $props()
</script>

<a class="taskrow" href="#/tasks/{task.id}">
  <div class="body">
    <div class="title truncate">{task.courseName || `任务 #${task.id}`}</div>
    <div class="meta truncate">
      <span class="num">#{task.id}</span>
      <span>·</span>
      <span>{task.chapters?.length ?? 0} 章</span>
      {#if !compact && task.message}
        <span>·</span>
        <span>{task.message}</span>
      {/if}
      <span>·</span>
      <span>{ago(task.createdAt)}</span>
    </div>
  </div>
  <StatusPill status={task.status} />
</a>

<style>
  .taskrow {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 12px;
    border-radius: 10px;
    border: 1px solid transparent;
    color: var(--fg);
    transition: background .15s, border-color .15s;
  }
  .taskrow:hover { background: #171c25; border-color: var(--line); color: var(--fg); }
  .body { flex: 1; min-width: 0; }
  .title { font-size: 13.5px; font-weight: 500; }
  .meta { display: flex; gap: 5px; font-size: 11.5px; color: var(--dim-2); margin-top: 1px; }
</style>
