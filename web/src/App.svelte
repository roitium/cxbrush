<script>
  import { onMount } from 'svelte'
  import { api } from './lib/api.js'
  import Login from './lib/Login.svelte'
  import AccountCard from './lib/AccountCard.svelte'
  import SettingsCard from './lib/SettingsCard.svelte'
  import CoursePicker from './lib/CoursePicker.svelte'
  import TaskList from './lib/TaskList.svelte'
  import RunPanel from './lib/RunPanel.svelte'

  let me = $state(null)
  let hasCookie = $state(false)
  let hasAI = $state(false)
  let courses = $state([])
  let tasks = $state([])
  let activeId = $state(0)
  let booted = $state(false)
  let courseError = $state('')

  async function loadMe() {
    try {
      const r = await api('/api/me')
      me = r.user
      hasCookie = r.hasCookie
      hasAI = !!r.hasAI
      if (hasCookie) loadCourses()
      loadTasks()
    } catch {
      me = null
    } finally {
      booted = true
    }
  }

  async function loadCourses() {
    try {
      const r = await api('/api/courses')
      courses = r.courses || []
      courseError = ''
    } catch (e) {
      courses = []
      courseError = e.message
    }
  }

  async function loadTasks() {
    try {
      const r = await api('/api/tasks')
      tasks = r.tasks || []
    } catch {}
  }

  async function start(course, chapters, doWorks) {
    const r = await api('/api/tasks', {
      method: 'POST',
      body: JSON.stringify({
        courseId: course.courseId,
        classId: course.classId,
        cpi: course.cpi,
        courseName: course.name,
        chapters: chapters.map((c) => ({ id: c.id, title: c.title })),
        doWorks,
      }),
    })
    await loadTasks()
    activeId = r.task.id
  }

  async function retry(id) {
    const r = await api(`/api/tasks/${id}/retry`, { method: 'POST' })
    await loadTasks()
    activeId = r.task.id
  }

  async function logout() {
    await api('/api/logout', { method: 'POST' })
    location.reload()
  }

  function onCookieSaved() {
    hasCookie = true
    loadCourses()
  }

  onMount(loadMe)
</script>

{#if !booted}
  <p class="hint center">加载中…</p>
{:else if !me}
  <Login onDone={loadMe} />
{:else}
  <header>
    <strong>cxbrush</strong>
    <span class="sp"></span>
    <span class="hint">{me.username}</span>
    <button class="ghost" onclick={logout}>退出</button>
  </header>

  <main>
    <div class="col">
      <AccountCard {hasCookie} onSaved={onCookieSaved} />
      <SettingsCard onSaved={loadMe} />
      {#if courseError}
        <div class="card"><p class="err">课程列表：{courseError}</p></div>
      {/if}
      {#if courses.length}
        <CoursePicker {courses} {hasAI} onStart={start} />
      {/if}
      <TaskList {tasks} {activeId} onSelect={(id) => (activeId = id)} onRetry={retry} />
    </div>
    <div class="col">
      <RunPanel taskId={activeId} onDone={loadTasks} />
    </div>
  </main>
{/if}

<style>
  header {
    display: flex; align-items: center; gap: 12px;
    padding: 12px 18px; border-bottom: 1px solid var(--line);
    position: sticky; top: 0; background: rgba(15,17,21,.92);
    backdrop-filter: blur(8px); z-index: 5;
  }
  header strong { font-size: 15px; letter-spacing: .3px; }
  .sp { flex: 1; }
  main {
    max-width: 1240px; margin: 0 auto; padding: 18px;
    display: grid; grid-template-columns: 420px minmax(0, 1fr); gap: 16px;
    align-items: start;
  }
  .col { display: grid; gap: 16px; min-width: 0; }
  .center { text-align: center; margin-top: 30vh; }
  p { margin: 0; }
  @media (max-width: 900px) {
    main { grid-template-columns: 1fr; }
  }
</style>
