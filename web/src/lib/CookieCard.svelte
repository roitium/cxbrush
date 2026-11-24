<script>
  import { api } from './api.js'

  let { hasCookie = false, onSaved } = $props()
  let value = $state('')
  let msg = $state('')
  let cls = $state('hint')
  let busy = $state(false)

  async function save() {
    if (!value.trim()) return
    busy = true
    msg = '校验中…'
    cls = 'hint'
    try {
      const r = await api('/api/chaoxing', { method: 'PUT', body: JSON.stringify({ cookie: value.trim() }) })
      msg = `校验通过，共 ${r.courses} 门课程`
      cls = 'ok'
      value = ''
      onSaved?.()
    } catch (e) {
      msg = e.message
      cls = 'err'
    } finally {
      busy = false
    }
  }
</script>

<div class="cookie">
  <p class="hint">从浏览器开发者工具复制 Cookie 请求头，整段粘贴即可。</p>
  <textarea bind:value placeholder="_uid=...; fid=...; _d=...; UID=...; vc3=..." spellcheck="false"></textarea>
  <div class="row">
    <button class="btn sm" disabled={busy || !value.trim()} onclick={save}>{busy ? '校验中…' : '保存并验证'}</button>
    <span class={cls}>{msg || (hasCookie ? '当前已有保存的 Cookie，重新粘贴会覆盖。' : '')}</span>
  </div>
</div>

<style>
  .cookie { display: grid; gap: 10px; }
  textarea { min-height: 96px; }
  .row { align-items: center; }
</style>
