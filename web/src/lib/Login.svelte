<script>
  import { api } from './api.js'

  let { onDone } = $props()
  let username = $state('')
  let password = $state('')
  let error = $state('')
  let busy = $state(false)
  let mode = $state('login')

  async function submit() {
    if (!username.trim() || !password) return
    error = ''
    busy = true
    try {
      await api(mode === 'register' ? '/api/register' : '/api/login', {
        method: 'POST',
        body: JSON.stringify({ username: username.trim(), password }),
      })
      onDone?.()
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }
</script>

<div class="wrap">
  <div class="brand">
    <span class="logo">刷</span>
    <b>cxbrush</b>
  </div>

  <div class="card">
    <h1>{mode === 'login' ? '欢迎回来' : '创建本机账号'}</h1>
    <p class="hint sub">
      {mode === 'login' ? '登录后管理你的学习通任务。' : '账号只保存在这台机器上，用于隔离多个用户的数据。'}
    </p>
    <input bind:value={username} placeholder="用户名" autocomplete="username" onkeydown={(e) => e.key === 'Enter' && submit()} />
    <input
      bind:value={password}
      type="password"
      placeholder={mode === 'login' ? '密码' : '密码（至少 6 位）'}
      autocomplete={mode === 'login' ? 'current-password' : 'new-password'}
      onkeydown={(e) => e.key === 'Enter' && submit()}
    />
    <button class="btn primary wide" disabled={busy} onclick={submit}>
      {busy ? '请稍候…' : mode === 'login' ? '登录' : '注册并登录'}
    </button>
    {#if error}<p class="err msg">{error}</p>{/if}
    <button class="switch" onclick={() => (mode = mode === 'login' ? 'register' : 'login')}>
      {mode === 'login' ? '没有账号？创建一个' : '已有账号？去登录'}
    </button>
  </div>

  <p class="hint tip">学习通账号不与本机账号绑定，登录后在「设置」里单独连接。</p>
</div>

<style>
  .wrap { max-width: 380px; margin: 10vh auto 0; padding: 0 16px 40px; display: grid; gap: 18px; }
  .brand { display: flex; align-items: center; gap: 10px; justify-content: center; }
  .brand b { font-size: 15px; letter-spacing: .02em; }
  .logo {
    width: 30px; height: 30px;
    display: grid; place-items: center;
    border-radius: 9px;
    font-size: 14px; font-weight: 700;
    color: #fff;
    background: linear-gradient(145deg, #6f9bff, #3f6fe0);
    box-shadow: 0 8px 20px -10px rgba(91, 140, 255, .9);
  }
  .card { display: grid; gap: 11px; padding: 22px; }
  .card h1 { font-size: 20px; }
  .sub { margin-bottom: 4px; }
  .wide { width: 100%; margin-top: 2px; }
  .msg { font-size: 12px; }
  .switch {
    border: 0;
    background: none;
    color: var(--dim);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
    padding: 2px;
  }
  .switch:hover { color: var(--fg-2); }
  .tip { text-align: center; }
</style>
