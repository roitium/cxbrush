import { api } from './api.js'

export const app = $state({
  booted: false,
  me: null,
  hasCookie: false,
  hasAI: false,
  aiModel: '',
  courses: [],
  coursesError: '',
  coursesLoading: false,
  tasks: [],
  tasksLoading: true,
  toast: null,
})

let toastTimer

export function toast(text, kind = 'info') {
  app.toast = { text, kind, id: Date.now() }
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (app.toast = null), 4200)
}

export async function loadMe() {
  try {
    const r = await api('/api/me')
    app.me = r.user
    app.hasCookie = !!r.hasCookie
    app.hasAI = !!r.hasAI
    app.aiModel = r.aiModel || ''
  } catch {
    app.me = null
    app.hasCookie = false
    app.hasAI = false
  }
}

export async function loadTasks() {
  try {
    const r = await api('/api/tasks')
    app.tasks = r.tasks || []
  } catch {
    /* 轮询失败保持上次状态 */
  } finally {
    app.tasksLoading = false
  }
}

export async function loadCourses() {
  if (!app.hasCookie) {
    app.courses = []
    return
  }
  app.coursesLoading = true
  app.coursesError = ''
  try {
    const r = await api('/api/courses')
    app.courses = r.courses || []
  } catch (e) {
    app.courses = []
    app.coursesError = e.message
  } finally {
    app.coursesLoading = false
  }
}

export async function bootstrap() {
  await loadMe()
  if (app.me) {
    await Promise.all([loadTasks(), loadCourses()])
  }
  app.booted = true
}

export async function refreshAfterAccountChange() {
  await loadMe()
  await Promise.all([loadTasks(), loadCourses()])
}

export async function logout() {
  try {
    await api('/api/logout', { method: 'POST' })
  } finally {
    app.me = null
    app.courses = []
    app.tasks = []
    location.hash = ''
    location.reload()
  }
}

export const runningTask = () =>
  app.tasks.find((t) => t.status === 'running' || t.status === 'queued') || null
