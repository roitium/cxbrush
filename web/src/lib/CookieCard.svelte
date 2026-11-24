<script>
  import { api } from './api.js'

  let { hasCookie = false, onSaved } = $props()
  let value = $state('')
  let msg = $state(hasCookie ? '已保存，可重新粘贴覆盖' : '尚未配置')
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

<div class="card">
  <h2>学习通 Cookie</h2>
  <textarea bind:value placeholder="从浏览器复制 Cookie 请求头，例如：_uid=...; fid=...; ..."></textarea>
  <div class="row">
    <button disabled={busy} onclick={save}>保存并验证</button>
    <span class={cls}>{msg}</span>
  </div>
</div>

<style>
  textarea { min-height: 84px; margin-bottom: 12px; }
</style>
