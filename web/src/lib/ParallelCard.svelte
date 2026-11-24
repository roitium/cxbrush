<script>
  import { api } from './api.js'
  import { app, loadMe } from './state.svelte.js'

  let { onSaved } = $props()
  let tasks = $state(app.parallel.tasks || 2)
  let videos = $state(app.parallel.videos || 2)
  let busy = $state(false)
  let msg = $state('')
  let cls = $state('hint')

  const taskOpts = [1, 2, 3, 4]
  const videoOpts = [1, 2, 3, 4, 5, 6, 7, 8]

  async function save() {
    busy = true
    msg = '保存中…'
    cls = 'hint'
    try {
      await api('/api/settings/parallel', {
        method: 'PUT',
        body: JSON.stringify({ tasks, videos }),
      })
      await loadMe()
      msg = '已保存，新任务生效'
      cls = 'ok'
      onSaved?.()
    } catch (e) {
      msg = e.message
      cls = 'err'
    } finally {
      busy = false
    }
  }
</script>

<section class="card">
  <div class="card-head">
    <h2>并行度</h2>
    <span class="sp"></span>
    <span class="hint">只影响你自己的任务</span>
  </div>

  <div class="form">
    <label class="field">
      <span>同时运行的任务</span>
      <select bind:value={tasks}>
        {#each taskOpts as n (n)}<option value={n}>{n} 个</option>{/each}
      </select>
    </label>
    <label class="field">
      <span>每个任务的视频并发</span>
      <select bind:value={videos}>
        {#each videoOpts as n (n)}<option value={n}>{n} 路</option>{/each}
      </select>
    </label>
    <button class="btn primary" disabled={busy} onclick={save}>{busy ? '保存中…' : '保存'}</button>
    {#if msg}<span class={cls}>{msg}</span>{:else}<span class="hint">改动对新任务生效</span>{/if}
  </div>

  <p class="hint warnline">
    别开太高：学习通对进度上报有频率限制，建议保持 2–3；开得越高越容易出现进度不确认，
    甚至触发风控。跑不稳时先调回 2。
  </p>
  {#if app.parallel.ceiling && tasks > app.parallel.ceiling}
    <p class="hint">
      服务端同时任务上限是 {app.parallel.ceiling}，超出的任务会排队等待。
    </p>
  {/if}
</section>

<style>
  .form {
    display: grid;
    grid-template-columns: 150px 180px auto 1fr;
    gap: 12px;
    align-items: end;
  }
  .warnline { margin-top: 10px; color: var(--warn); }
  @media (max-width: 760px) {
    .form { grid-template-columns: 1fr 1fr; }
  }
</style>
