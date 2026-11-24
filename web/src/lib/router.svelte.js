function parse(hash) {
  const path = (hash || '').replace(/^#/, '') || '/'
  const parts = path.split('/').filter(Boolean)
  if (parts[0] === 'settings') return { name: 'settings', path }
  if (parts[0] === 'tasks') {
    const id = Number(parts[1])
    if (Number.isFinite(id) && id > 0) return { name: 'task', id, path }
    return { name: 'tasks', path }
  }
  return { name: 'overview', path: '/' }
}

export const route = $state(parse(typeof location === 'undefined' ? '' : location.hash))

export function nav(path) {
  if (typeof location === 'undefined') return
  const next = '#' + path
  if (location.hash === next) return
  location.hash = path
}

if (typeof window !== 'undefined') {
  window.addEventListener('hashchange', () => {
    Object.assign(route, parse(location.hash))
  })
}
