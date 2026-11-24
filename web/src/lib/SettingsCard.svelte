<script>
  import { onMount } from 'svelte'
  import { api } from './api.js'

  let { onSaved } = $props()
  let base = $state('')
  let model = $state('')
  let key = $state('')
  let keyHint = $state('')
  let source = $state('')
  let msg = $state('')
  let cls = $state('hint')
  let busy = $state(false)

  const sourceText = { custom: '自定义', env: '环境变量', none: '未配置' }

  async function load() {
    try {
      const r = await api('/api/settings/ai')
      base = r.base || ''
      model = r.model || ''
      keyHint = r.keyHint || ''
      source = r.source || 'none'
    } catch (e) {
      msg = e.message
      cls = 'err'
    }
  }

  async function save(test = false) {
    const noKey = !key.trim() && !keyHint
    if (!test && noKey) {
      msg = '请填写 API Key'
      cls = 'err'
      return
    }
    busy = true
    msg = test ? '保存并测试中…' : '保存中…'
    cls = 'hint'
    try {
      if (noKey) {
        // 没有自定义 Key：只验证当前生效的页面配置或环境变量。
        const t = await api('/api/settings/ai/test', {
          method: 'POST',
          body: JSON.stringify({ base, model }),
        })
        msg = `可用 · ${t.model} · ${t.latencyMs}ms（未保存）`
        cls = 'ok'
        return
      }
      await api('/api/settings/ai', {
        method: 'PUT',
        body: JSON.stringify({ base, model, key }),
      })
      key = ''
      let text = '已保存'
      if (test) {
        const t = await api('/api/settings/ai/test', { method: 'POST', body: '{}' })
        text = `可用 · ${t.model} · ${t.latencyMs}ms`
      }
      msg = text
      cls = 'ok'
      await load()
      onSaved?.()
    } catch (e) {
      msg = e.message
      cls = 'err'
    } finally {
      busy = false
    }
  }

  async function clear() {
    busy = true
    try {
      await api('/api/settings/ai', { method: 'PUT', body: JSON.stringify({ clear: true }) })
      msg = '已清除，回退到环境变量'
      cls = 'ok'
      await load()
      onSaved?.()
    } catch (e) {
      msg = e.message
      cls = 'err'
    } finally {
      busy = false
    }
  }

  onMount(load)
</script>

<details class="card">
  <summary>
    <h2>模型配置</h2>
    <span class="hint">当前：{sourceText[source] || source}{model ? ` · ${model}` : ''}</span>
  </summary>
  <div class="form">
    <label>
      <span class="hint">接口地址（OpenAI 兼容）</span>
      <input bind:value={base} placeholder="https://api.example.com/v1" />
    </label>
    <label>
      <span class="hint">模型名</span>
      <input bind:value={model} placeholder="deepseek/deepseek-flash" />
    </label>
    <label>
      <span class="hint">API Key</span>
      <input bind:value={key} type="password" placeholder={keyHint ? `已保存 ${keyHint}，留空不修改` : 'sk-...'} />
    </label>
  </div>
  <div class="row">
    <button class="primary" disabled={busy} onclick={() => save(true)}>保存并测试</button>
    <button disabled={busy} onclick={() => save(false)}>仅保存</button>
    <button class="ghost" disabled={busy} onclick={clear}>清除自定义</button>
    <span class={cls}>{msg}</span>
  </div>
  {#if !keyHint}
    <p class="hint">不填 Key 时只测试当前生效的页面配置或环境变量；要换模型再填完整。</p>
  {/if}
</details>

<style>
  details.card { padding: 12px 16px; }
  summary { display: flex; align-items: baseline; gap: 10px; cursor: pointer; list-style: none; }
  summary::-webkit-details-marker { display: none; }
  summary h2 { margin: 0; }
  .form { display: grid; gap: 10px; margin-top: 12px; }
  .form label { display: grid; gap: 4px; }
  .row { display: flex; align-items: center; gap: 10px; margin-top: 12px; flex-wrap: wrap; }
</style>
