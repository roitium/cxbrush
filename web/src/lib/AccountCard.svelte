<script>
  import { api } from './api.js'
  import CookieCard from './CookieCard.svelte'

  let { hasCookie = false, onSaved } = $props()
  let phone = $state('')
  let password = $state('')
  let msg = $state(hasCookie ? '已登录（Cookie 已保存）' : '尚未登录学习通')
  let cls = $state('hint')
  let busy = $state(false)
  let showCookie = $state(false)

  async function login() {
    if (!phone.trim() || !password) return
    busy = true
    msg = '登录中…'
    cls = 'hint'
    try {
      const r = await api('/api/chaoxing/login', {
        method: 'POST',
        body: JSON.stringify({ phone: phone.trim(), password }),
      })
      msg = `登录成功${r.name ? `（${r.name}）` : ''}，共 ${r.courses} 门课程`
      cls = 'ok'
      password = ''
      onSaved?.()
    } catch (e) {
      msg = e.message
      cls = 'err'
    } finally {
      busy = false
    }
  }
</script>

<div class="card">
  <h2>学习通账号</h2>
  <div class="form">
    <input bind:value={phone} placeholder="手机号 / 账号" autocomplete="username" />
    <input
      bind:value={password}
      type="password"
      placeholder="密码"
      autocomplete="current-password"
      onkeydown={(e) => e.key === 'Enter' && login()}
    />
    <button disabled={busy} onclick={login}>{busy ? '登录中…' : '登录'}</button>
  </div>
  <p class={cls}>{msg}</p>

  <details bind:open={showCookie} class="fallback">
    <summary>验证码/扫码登录，或直接粘贴 Cookie</summary>
    <CookieCard {hasCookie} {onSaved} />
  </details>
</div>

<style>
  .form { display: grid; grid-template-columns: 1fr 1fr auto; gap: 8px; align-items: center; }
  .form input { min-width: 0; }
  .fallback { margin-top: 10px; }
  .fallback summary { font-size: 12px; color: var(--dim); cursor: pointer; }
  .fallback :global(.card) { border: 0; padding: 8px 0 0; background: transparent; }
  .fallback :global(.card h2) { display: none; }
</style>
