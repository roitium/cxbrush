<script>
  import { onMount } from 'svelte'
  import { api } from './api.js'

  let { onSaved } = $props()
  let base = $state('')
  let model = $state('')
  let key = $state('')
  let keyHint = $state('')
  let source = $state('none')
  let msg = $state('')
  let cls = $state('hint')
  let busy = $state(false)

  const sourceText = { custom: '页面配置', env: '环境变量', none: '未配置' }

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
        const t = await api('/api/settings/ai/test', { method: 'POST', body: JSON.stringify({ base, model }) })
        msg = `可用 · ${t.model} · ${t.latencyMs}ms（未保存）`
        cls = 'ok'
        return
      }
      await api('/api/settings/ai', { method: 'PUT', body: JSON.stringify({ base, model, key }) })
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
      msg = '已清除自定义配置'
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

<section class="card">
  <div class="card-head">
    <h2>答题模型</h2>
    <span class="sp"></span>
    <span class="pill" class:done={source !== 'none' && (keyHint !== '' || source === 'env')}>
      <i class="dot"></i>{sourceText[source] || source}{model ? ` · ${model}` : ''}
    </span>
  </div>

  <p class="hint intro">
    支持任意 OpenAI 兼容的 <code>/chat/completions</code> 接口。Key 会用 AES-GCM 加密后存进本地数据库。
  </p>

  <div class="form">
    <label class="field">
      <span>接口地址</span>
      <input bind:value={base} placeholder="https://api.deepseek.com/v1" spellcheck="false" />
    </label>
    <label class="field">
      <span>模型名</span>
      <input bind:value={model} placeholder="deepseek/deepseek-flash" spellcheck="false" />
    </label>
    <label class="field full">
      <span>API Key</span>
      <input
        bind:value={key}
        type="password"
        placeholder={keyHint ? `已保存 ${keyHint}，留空表示不修改` : 'sk-...'}
        autocomplete="off"
      />
    </label>
  </div>

  <div class="row act">
    <button class="btn primary" disabled={busy} onclick={() => save(true)}>{busy ? '处理中…' : '保存并测试'}</button>
    <button class="btn" disabled={busy} onclick={() => save(false)}>仅保存</button>
    {#if source === 'custom'}<button class="btn ghost" disabled={busy} onclick={clear}>清除自定义</button>{/if}
    {#if msg}<span class={cls}>{msg}</span>{/if}
  </div>
  {#if !keyHint}
    <p class="hint note">没有填 Key 时，「保存并测试」只验证当前页面配置或 <code>CX_AI_*</code> 环境变量，不会保存。</p>
  {/if}
</section>

<style>
  .intro { margin-bottom: 14px; }
  .form { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
  .full { grid-column: 1 / -1; }
  .act { margin-top: 14px; }
  .note { margin-top: 10px; }
  code { font-family: var(--mono); font-size: 11.5px; color: var(--fg-2); background: #1b212b; padding: 1px 5px; border-radius: 5px; }
  @media (max-width: 680px) {
    .form { grid-template-columns: 1fr; }
  }
</style>
