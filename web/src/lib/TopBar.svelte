<script>
  import { route, nav } from './router.svelte.js'
  import { app, runningTask, logout } from './state.svelte.js'

  let run = $derived(runningTask())
  let open = $state(false)
  let box = $state(null)

  $effect(() => {
    if (!open) return
    const close = (e) => {
      if (box && !box.contains(e.target)) open = false
    }
    document.addEventListener('click', close)
    return () => document.removeEventListener('click', close)
  })
</script>

<header>
  <div class="in">
    <a class="brand" href="#/">
      <span class="logo">刷</span>
      <b>cxbrush</b>
    </a>
    <nav>
      <a href="#/" class:on={route.name === 'overview' || route.name === 'task'}>概览</a>
      <a href="#/tasks" class:on={route.name === 'tasks'}>任务</a>
      <a href="#/settings" class:on={route.name === 'settings'}>设置</a>
      {#if app.me?.isAdmin}
        <a href="#/admin" class:on={route.name === 'admin'}>管理</a>
      {/if}
    </nav>
    <span class="sp"></span>

    {#if run}
      <a class="runpill" href="#/tasks/{run.id}" title="查看运行中的任务">
        <i class="dot pulse"></i>
        运行中
        <span class="runname">{run.courseName}</span>
      </a>
    {/if}

    <a class="chip" class:on={app.hasAI} href="#/settings" title={app.hasAI ? `模型：${app.aiModel}` : '还没有配置模型'}>
      <i class="dot"></i> AI
    </a>
    <a class="chip" class:on={app.hasCookie} href="#/settings" title={app.hasCookie ? '学习通已连接' : '还没有连接学习通'}>
      <i class="dot"></i> 学习通
    </a>

    <div class="user" bind:this={box}>
      <button class="avatar" onclick={() => (open = !open)} aria-label="账号菜单">
        {app.me?.username?.slice(0, 1)?.toUpperCase() || '?'}
      </button>
      <div class="menu" hidden={!open}>
        <div class="who">
          <b>{app.me?.username}</b>
          <span class="hint">本机账号</span>
        </div>
        <button class="item" onclick={() => nav('/settings')}>设置</button>
        {#if app.me?.isAdmin}
          <button class="item" onclick={() => nav('/admin')}>管理</button>
        {/if}
        <button class="item danger" onclick={logout}>退出登录</button>
      </div>
    </div>
  </div>
</header>

<style>
  header {
    position: sticky;
    top: 0;
    z-index: 20;
    border-bottom: 1px solid var(--line-soft);
    background: rgba(10, 12, 16, .78);
    backdrop-filter: blur(14px) saturate(140%);
  }
  .in {
    max-width: 1200px;
    margin: 0 auto;
    padding: 10px 20px;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .brand { display: flex; align-items: center; gap: 9px; color: var(--fg); margin-right: 10px; }
  .brand b { font-size: 14px; letter-spacing: .02em; }
  .logo {
    width: 26px; height: 26px;
    display: grid; place-items: center;
    border-radius: 8px;
    font-size: 13px; font-weight: 700;
    color: #fff;
    background: linear-gradient(145deg, #6f9bff, #3f6fe0);
    box-shadow: 0 6px 16px -8px rgba(91, 140, 255, .9);
  }
  nav { display: flex; gap: 2px; }
  nav a {
    color: var(--dim);
    padding: 6px 11px;
    border-radius: 8px;
    font-size: 13px;
  }
  nav a:hover { color: var(--fg); background: #161b24; }
  nav a.on { color: var(--fg); background: #1b212b; }
  .sp { flex: 1; }
  .runpill {
    display: inline-flex; align-items: center; gap: 7px;
    font-size: 12px; color: #8fb0ff;
    border: 1px solid #2c4370;
    background: rgba(91, 140, 255, .10);
    padding: 4px 11px; border-radius: 999px;
    max-width: 260px;
  }
  .runname { color: #b9c2d3; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .chip {
    display: inline-flex; align-items: center; gap: 6px;
    font-size: 11px; font-weight: 600;
    color: var(--dim-2);
    padding: 4px 9px;
    border: 1px solid var(--line);
    border-radius: 999px;
  }
  .chip.on { color: #7fdc9c; border-color: #24503a; background: rgba(65, 196, 106, .07); }
  .chip .dot { background: currentColor; opacity: .9; }
  .user { position: relative; margin-left: 4px; }
  .avatar {
    width: 30px; height: 30px;
    border-radius: 50%;
    border: 1px solid var(--line);
    background: #1b212b;
    color: var(--fg-2);
    font-size: 12px;
    display: grid; place-items: center;
    padding: 0;
    cursor: pointer;
  }
  .avatar:hover { border-color: #3b4557; }
  .menu {
    position: absolute; right: 0; top: 38px;
    width: 180px;
    padding: 6px;
    border-radius: 12px;
    border: 1px solid var(--line);
    background: #171c25;
    box-shadow: 0 20px 44px -18px rgba(0, 0, 0, .95);
    display: grid;
    gap: 2px;
  }
  .menu[hidden] { display: none; }
  .who { padding: 7px 9px 9px; border-bottom: 1px solid var(--line-soft); margin-bottom: 4px; }
  .who b { display: block; font-size: 13px; }
  .item {
    text-align: left;
    border: 0;
    background: none;
    color: var(--fg-2);
    padding: 7px 9px;
    border-radius: 7px;
    cursor: pointer;
    font: inherit;
    font-size: 13px;
  }
  .item:hover { background: #212836; color: var(--fg); }
  .item.danger { color: #f4868a; }
  .item.danger:hover { background: #2a1719; }
  @media (max-width: 680px) {
    .in { padding: 8px 12px; gap: 4px; }
    .brand b, .chip, .runname { display: none; }
    nav a { padding: 6px 9px; }
  }
</style>
