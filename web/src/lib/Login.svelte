<script>
  import { api } from './api.js'

  let { onDone } = $props()
  let username = $state('')
  let password = $state('')
  let error = $state('')
  let busy = $state(false)

  async function submit(register) {
    error = ''
    busy = true
    try {
      await api(register ? '/api/register' : '/api/login', {
        method: 'POST',
        body: JSON.stringify({ username, password }),
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
  <div class="card">
    <h1>cxbrush</h1>
    <p class="hint">登录后才能使用</p>
    <input bind:value={username} placeholder="用户名" autocomplete="username" />
    <input bind:value={password} type="password" placeholder="密码" autocomplete="current-password" />
    <button class="primary" disabled={busy} onclick={() => submit(false)}>登录</button>
    <button class="ghost" disabled={busy} onclick={() => submit(true)}>没有账号？注册</button>
    {#if error}<p class="err">{error}</p>{/if}
  </div>
</div>

<style>
  .wrap { max-width: 360px; margin: 12vh auto; padding: 0 16px; }
  .card { display: grid; gap: 12px; }
  h1 { font-size: 16px; }
  p { margin: 0; }
</style>
