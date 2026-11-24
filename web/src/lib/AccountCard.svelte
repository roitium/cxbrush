<script>
  import { api } from './api.js'
  import CookieCard from './CookieCard.svelte'

  let { hasCookie = false, onSaved } = $props()
  let phone = $state('')
  let password = $state('')
  let msg = $state('')
  let cls = $state('hint')
  let busy = $state(false)
  let showCookie = $state(false)
  let name = $state('')

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
      msg = `登录成功，共 ${r.courses} 门课程`
      cls = 'ok'
      name = r.name || ''
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

<section class="card">
  <div class="card-head">
    <h2>学习通</h2>
    <span class="sp"></span>
    <span class="pill" class:done={hasCookie}>
      <i class="dot"></i>{hasCookie ? '已连接' : '未连接'}
    </span>
  </div>

  <p class="hint intro">
    账号密码只用于换取学习通 Cookie，不会保存在本机；会话过期时会自动尝试续期。
  </p>

  <div class="form">
    <label class="field">
      <span>手机号 / 账号</span>
      <input bind:value={phone} placeholder="13900000000" autocomplete="username" />
    </label>
    <label class="field">
      <span>密码</span>
      <input
        bind:value={password}
        type="password"
        placeholder="学习通密码"
        autocomplete="current-password"
        onkeydown={(e) => e.key === 'Enter' && login()}
      />
    </label>
  </div>
  <div class="row act">
    <button class="btn primary" disabled={busy || !phone.trim() || !password} onclick={login}>
      {busy ? '登录中…' : '登录并保存会话'}
    </button>
    {#if msg}<span class={cls}>{msg}</span>{/if}
  </div>

  <details bind:open={showCookie} class="more">
    <summary>遇到验证码 / 扫码登录，改用 Cookie 导入</summary>
    <CookieCard {hasCookie} {onSaved} />
  </details>
</section>

<style>
  .intro { margin-bottom: 14px; }
  .form { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
  .act { margin-top: 12px; }
  .more { margin-top: 16px; border-top: 1px solid var(--line-soft); padding-top: 12px; }
  .more summary { font-size: 12px; color: var(--dim); cursor: pointer; }
  .more summary:hover { color: var(--fg-2); }
  .more :global(.cookie) { margin-top: 12px; }
  @media (max-width: 680px) {
    .form { grid-template-columns: 1fr; }
  }
</style>
