<script>
  import { app, refreshAfterAccountChange } from '../lib/state.svelte.js'
  import AccountCard from '../lib/AccountCard.svelte'
  import ParallelCard from '../lib/ParallelCard.svelte'
  import { nav } from '../lib/router.svelte.js'

  function onSaved() {
    refreshAfterAccountChange()
  }
</script>

<div class="page">
  <div class="head">
    <h1>设置</h1>
    <p class="hint">学习通会话按用户隔离；答题模型由管理员统一配置，所有用户共用。</p>
  </div>

  <div class="grid">
    <AccountCard hasCookie={app.hasCookie} onSaved={onSaved} />
    <section class="card">
      <div class="card-head"><h2>答题模型</h2></div>
      <p class="hint">
        自动做题使用管理员配置的模型（当前：{app.hasAI ? app.aiModel : '未配置'}），
        答过的题会进全局答案缓存，不会重复请求模型。
      </p>
      {#if app.me?.isAdmin}
        <div class="row act">
          <button class="btn" onclick={() => nav('/admin')}>去管理页面配置</button>
        </div>
      {:else}
        <p class="hint note">需要修改请联系管理员。</p>
      {/if}
    </section>
  </div>

  <ParallelCard onSaved={onSaved} />

  <section class="card about">
    <div class="card-head"><h2>关于</h2></div>
    <div class="rows">
      <div class="r"><span class="hint">当前账号</span><span>{app.me?.username || '—'}</span></div>
      <div class="r"><span class="hint">学习通会话</span><span class:ok={app.hasCookie}>{app.hasCookie ? '已保存 Cookie，服务端每小时自动续期' : '未连接'}</span></div>
      <div class="r"><span class="hint">答题模型</span><span class:ok={app.hasAI}>{app.hasAI ? app.aiModel : '未配置'}</span></div>
      <div class="r">
        <span class="hint">并行度</span>
        <span class="num">{app.parallel.tasks} 个任务 · 每任务 {app.parallel.videos} 个视频</span>
      </div>
      <div class="r"><span class="hint">数据位置</span><span class="mono">~/.local/share/cxbrush</span></div>
    </div>
    <p class="hint foot">
      cxbrush 仅用于个人学习与技术研究。登录与刷课协议参考
      <a href="https://github.com/Pybsama/Caochaoxing" target="_blank" rel="noreferrer">Pybsama/Caochaoxing</a>。
    </p>
  </section>
</div>

<style>
  .page { max-width: 1080px; margin: 0 auto; padding: 22px 20px 60px; display: grid; gap: 16px; }
  .head h1 { font-size: 24px; }
  .head p { margin-top: 3px; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; align-items: stretch; }
  .about .rows { display: grid; gap: 1px; }
  .act { margin-top: 12px; }
  .note { margin-top: 10px; }
  .r { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 9px 2px; border-bottom: 1px solid var(--line-soft); font-size: 13px; }
  .r:last-child { border-bottom: 0; }
  .foot { margin-top: 12px; }
  @media (max-width: 880px) {
    .grid { grid-template-columns: 1fr; }
    .page { padding: 16px 12px 48px; }
  }
</style>
