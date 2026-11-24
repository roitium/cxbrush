<script>
  import { onMount } from 'svelte'
  import { route } from './lib/router.svelte.js'
  import { app, bootstrap, loadTasks } from './lib/state.svelte.js'
  import TopBar from './lib/TopBar.svelte'
  import Toast from './lib/Toast.svelte'
  import Login from './lib/Login.svelte'
  import Overview from './pages/Overview.svelte'
  import Tasks from './pages/Tasks.svelte'
  import TaskDetail from './pages/TaskDetail.svelte'
  import Settings from './pages/Settings.svelte'

  onMount(bootstrap)

  $effect(() => {
    if (!app.me) return
    const timer = setInterval(loadTasks, 8000)
    return () => clearInterval(timer)
  })
</script>

{#if !app.booted}
  <div class="boot">
    <span class="logo">刷</span>
    <span class="hint">正在加载…</span>
  </div>
{:else if !app.me}
  <Login onDone={bootstrap} />
{:else}
  <TopBar />
  <main>
    {#if route.name === 'overview'}
      <Overview />
    {:else if route.name === 'tasks'}
      <Tasks />
    {:else if route.name === 'task'}
      <TaskDetail id={route.id} />
    {:else}
      <Settings />
    {/if}
  </main>
  <Toast />
{/if}

<style>
  .boot {
    min-height: 80vh;
    display: grid;
    place-content: center;
    justify-items: center;
    gap: 12px;
  }
  .logo {
    width: 34px; height: 34px;
    display: grid; place-items: center;
    border-radius: 10px;
    font-size: 15px; font-weight: 700;
    color: #fff;
    background: linear-gradient(145deg, #6f9bff, #3f6fe0);
    animation: bob 1.6s ease-in-out infinite;
  }
  @keyframes bob { 0%, 100% { transform: translateY(0); } 50% { transform: translateY(-4px); } }
</style>
