export async function api(path, opt = {}) {
  const r = await fetch(path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    ...opt,
  })
  const text = await r.text()
  let body = {}
  try { body = text ? JSON.parse(text) : {} } catch { body = { error: text } }
  if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`)
  return body
}

export function fmtTime(iso) {
  const d = new Date(iso)
  if (Number.isNaN(+d)) return ''
  return d.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
}
