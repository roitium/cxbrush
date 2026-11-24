<script>
  import { onMount } from 'svelte'
  import { api, fmtTime } from '../lib/api.js'
  import { app, loadMe, toast } from '../lib/state.svelte.js'
  import AICard from '../lib/AICard.svelte'

  let users = $state([])
  let loading = $state(true)
  let error = $state('')
  let busy = $state(0)
  let adminCount = $derived(users.filter((u) => u.isAdmin).length)

  async function load() {
    loading = true
    error = ''
    try {
      const r = await api('/api/admin/users')
      users = r.users || []
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  async function toggle(u) {
    busy = u.id
    try {
      await api(`/api/admin/users/${u.id}/admin`, {
        method: 'POST',
        body: JSON.stringify({ admin: !u.isAdmin }),
      })
      await Promise.all([load(), loadMe()])
      toast(u.isAdmin ? '已取消管理员' : '已设为管理员', 'ok')
    } catch (e) {
      toast(e.message, 'err')
    } finally {
      busy = 0
    }
  }

  onMount(load)
</script>

<div class="page">
  <div class="head">
    <h1>管理</h1>
    <p class="hint">模型配置对所有用户生效，答案缓存全局共享；只有管理员能进入这个页面。</p>
  </div>

  <AICard onSaved={loadMe} />

  <section class="card">
    <div class="card-head">
      <h2>用户</h2>
      <span class="sp"></span>
      <span class="hint">{users.length} 人</span>
    </div>

    {#if error}
      <p class="err">{error}</p>
    {:else if loading}
      <p class="hint">加载中…</p>
    {:else}
      <div class="users">
        <div class="row head-row">
          <span>用户</span>
          <span>学习通</span>
          <span>注册时间</span>
          <span>角色</span>
        </div>
        {#each users as u (u.id)}
          <div class="row">
            <span class="who">
              <b>{u.username}</b>
              {#if u.id === app.me?.id}<span class="tag">我</span>{/if}
            </span>
            <span class:ok={u.hasCookie}>{u.hasCookie ? '已连接' : '未连接'}</span>
            <span class="hint">{fmtTime(u.createdAt)}</span>
            <span class="act">
              {#if u.isAdmin}<span class="badge">管理员</span>{/if}
              <button
                class="btn tiny"
                disabled={busy === u.id || (u.isAdmin && u.id === app.me?.id && adminCount === 1)}
                onclick={() => toggle(u)}
              >
                {u.isAdmin ? '取消管理员' : '设为管理员'}
              </button>
            </span>
          </div>
        {/each}
      </div>
      <p class="hint foot">至少要保留一个管理员；第一个注册的用户默认是管理员。</p>
    {/if}
  </section>
</div>

<style>
  .page { max-width: 1080px; margin: 0 auto; padding: 22px 20px 60px; display: grid; gap: 16px; }
  .head h1 { font-size: 24px; }
  .head p { margin-top: 3px; }
  .users { display: grid; gap: 1px; }
  .row {
    display: grid;
    grid-template-columns: 1.6fr .8fr 1fr 1.4fr;
    align-items: center;
    gap: 12px;
    padding: 9px 2px;
    border-bottom: 1px solid var(--line-soft);
    font-size: 13px;
  }
  .row:last-child { border-bottom: 0; }
  .head-row { font-size: 11.5px; color: var(--dim); border-bottom: 1px solid var(--line); }
  .who { display: flex; align-items: center; gap: 7px; }
  .tag {
    font-size: 10.5px; color: var(--dim-2);
    border: 1px solid var(--line); border-radius: 999px; padding: 0 6px;
  }
  .badge {
    font-size: 11px; color: #8fb0ff;
    border: 1px solid #2c4370; background: rgba(91, 140, 255, .10);
    padding: 1px 8px; border-radius: 999px;
  }
  .act { display: flex; align-items: center; gap: 8px; justify-content: flex-end; }
  .tiny { padding: 3px 9px; font-size: 12px; }
  .foot { margin-top: 12px; }
  .ok { color: #7fdc9c; }
  @media (max-width: 880px) {
    .page { padding: 16px 12px 48px; }
    .row { grid-template-columns: 1.4fr .8fr 1fr; }
    .row > :nth-child(3) { display: none; }
  }
</style>
