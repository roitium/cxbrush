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

export function fmtClock(iso) {
  const d = new Date(iso)
  if (Number.isNaN(+d)) return ''
  return d.toLocaleTimeString('zh-CN', { hour12: false })
}

export function ago(iso) {
  const d = new Date(iso)
  if (Number.isNaN(+d)) return ''
  const s = Math.round((Date.now() - d.getTime()) / 1000)
  if (s < 60) return '刚刚'
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`
  if (s < 86400 * 7) return `${Math.floor(s / 86400)} 天前`
  return fmtTime(iso)
}

export const sameDay = (a, b = new Date()) => {
  const d = new Date(a)
  return d.getFullYear() === b.getFullYear() && d.getMonth() === b.getMonth() && d.getDate() === b.getDate()
}
