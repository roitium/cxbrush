<script>
  import { api, ago, sameDay } from '../lib/api.js'
  import { app, loadTasks } from '../lib/state.svelte.js'
  import { nav } from '../lib/router.svelte.js'
  import CoursePicker from '../lib/CoursePicker.svelte'
  import StatusPill from '../lib/StatusPill.svelte'
  import TaskRow from '../lib/TaskRow.svelte'

  let busy = $state(0)

  let stats = $derived.by(() => {
    const today = app.tasks.filter((t) => sameDay(t.createdAt))
    return {
      running: app.tasks.filter((t) => t.status === 'running' || t.status === 'queued').length,
      today: today.length,
      done: today.filter((t) => t.status === 'done').length,
      failed: today.filter((t) => t.status === 'failed').length,
    }
  })

  let sorted = $derived([...app.tasks].sort((a, b) => new Date(b.createdAt) - new Date(a.createdAt)))
  let last = $derived(sorted[0] || null)
  let recent = $derived(sorted.slice(0, 5))

  let greeting = $derived.by(() => {
    const h = new Date().getHours()
    if (h < 6) return '夜深了'
    if (h < 11) return '早上好'
    if (h < 14) return '中午好'
    if (h < 18) return '下午好'
    return '晚上好'
  })

  let today = $derived(
    new Date().toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'long' }),
  )

  let showGuide = $derived(!app.hasCookie || !app.hasAI || app.tasks.length === 0)

  let lastLabel = $derived.by(() => {
    if (!last) return ''
    if (last.status === 'running' || last.status === 'queued') return '正在运行'
    if (last.status === 'failed') return '上次失败'
    if (last.status === 'canceled') return '上次已取消'
    return '上次任务'
  })

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
    nav(`/tasks/${r.task.id}`)
  }

  async function retry(t) {
    busy = t.id
    try {
      const r = await api(`/api/tasks/${t.id}/retry`, { method: 'POST' })
      await loadTasks()
      nav(`/tasks/${r.task.id}`)
    } catch (e) {
      alert(e.message)
    } finally {
      busy = 0
    }
  }
</script>

<div class="page">
  {#if !app.hasCookie}
    <div class="banner">
      <span>还没有连接学习通，先登录才能读取课程。</span>
      <span class="sp"></span>
      <a class="btn sm" href="#/settings">去连接</a>
    </div>
  {:else if !app.hasAI}
    <div class="banner">
      <span>还没有配置答题模型。视频、文档和阅读会正常完成，章节测验会被跳过。</span>
      <span class="sp"></span>
      <a class="btn sm" href="#/settings">去配置</a>
    </div>
  {/if}

  <div class="head">
    <div>
      <h1>{greeting}{app.me ? `，${app.me.username}` : ''}</h1>
      <p class="hint">{today}</p>
    </div>
  </div>

  <div class="stats">
    <div class="stat">
      <span class="label">进行中</span>
      <b class="num" class:on={stats.running > 0}>{stats.running}</b>
    </div>
    <div class="stat">
      <span class="label">今日任务</span>
      <b class="num">{stats.today}</b>
    </div>
    <div class="stat">
      <span class="label">今日完成</span>
      <b class="num ok">{stats.done}</b>
    </div>
    <div class="stat">
      <span class="label">今日失败</span>
      <b class="num" class:err={stats.failed > 0}>{stats.failed}</b>
    </div>
  </div>

  <div class="cols">
    <section class="card continue">
      <div class="card-head">
        <h2>{lastLabel || '开始使用'}</h2>
        {#if last}<span class="sp"></span><StatusPill status={last.status} />{/if}
      </div>
      {#if last}
        <a class="big" href="#/tasks/{last.id}">{last.courseName || `任务 #${last.id}`}</a>
        <p class="hint meta">
          <span class="num">#{last.id}</span> · {last.chapters?.length ?? 0} 个章节 · {ago(last.createdAt)}
          {#if last.message} · {last.message}{/if}
        </p>
        <div class="row act">
          <a class="btn primary" href="#/tasks/{last.id}">
            {last.status === 'running' || last.status === 'queued' ? '查看实时进度' : '查看任务详情'}
          </a>
          {#if last.status === 'done' || last.status === 'failed' || last.status === 'canceled'}
            <button class="btn ghost" disabled={busy === last.id} onclick={() => retry(last)}>
              {busy === last.id ? '创建中…' : '按相同章节再来一次'}
            </button>
          {/if}
        </div>
      {:else}
        <div class="empty">
          <span class="ico">◎</span>
          <b>还没有跑过任务</b>
          <span class="hint">在下方选一门课和章节，几分钟就能跑完一章。</span>
        </div>
      {/if}
    </section>

    <aside class="side">
      {#if showGuide}
        <section class="card guide">
          <div class="card-head"><h2>三步开始</h2></div>
          <ol>
            <li class:done={app.hasCookie}>
              <span class="idx">{app.hasCookie ? '✓' : '1'}</span>
              <div>
                <b>连接学习通</b>
                <small>{app.hasCookie ? '已连接' : '账号密码或 Cookie'}</small>
              </div>
              {#if !app.hasCookie}<a class="btn sm ghost" href="#/settings">去连接</a>{/if}
            </li>
            <li class:done={app.hasAI}>
              <span class="idx">{app.hasAI ? '✓' : '2'}</span>
              <div>
                <b>配置答题模型</b>
                <small>{app.hasAI ? app.aiModel : '任意 OpenAI 兼容接口'}</small>
              </div>
              {#if !app.hasAI}<a class="btn sm ghost" href="#/settings">去配置</a>{/if}
            </li>
            <li class:done={app.tasks.length > 0}>
              <span class="idx">{app.tasks.length ? '✓' : '3'}</span>
              <div>
                <b>选择章节开始</b>
                <small>{app.tasks.length ? '已经跑过任务' : '在下方选课'}</small>
              </div>
            </li>
          </ol>
        </section>
      {/if}

      <section class="card">
        <div class="card-head">
          <h2>最近任务</h2>
          <span class="sp"></span>
          <a class="hint" href="#/tasks">全部</a>
        </div>
        {#if app.tasksLoading}
          <div class="skeleton" style="height:120px"></div>
        {:else if !recent.length}
          <p class="hint">还没有任务记录。</p>
        {:else}
          <div class="recent">
            {#each recent as t (t.id)}
              <TaskRow task={t} compact />
            {/each}
          </div>
        {/if}
      </section>
    </aside>
  </div>

  <section class="card newtask">
    <div class="card-head">
      <h2>开始新任务</h2>
      <span class="sp"></span>
      <span class="hint">只跑你勾选的章节</span>
    </div>
    {#if !app.hasCookie}
      <div class="empty">
        <span class="ico">🔒</span>
        <b>先连接学习通</b>
        <span class="hint">连接后才能拉取课程和章节。</span>
        <a class="btn primary sm" href="#/settings">去设置</a>
      </div>
    {:else if app.coursesLoading && !app.courses.length}
      <div class="skeleton" style="height:180px"></div>
    {:else if app.coursesError}
      <div class="empty">
        <span class="ico">⚠</span>
        <b>课程列表加载失败</b>
        <span class="hint">{app.coursesError}</span>
      </div>
    {:else if !app.courses.length}
      <div class="empty">
        <span class="ico">□</span>
        <b>没有读到课程</b>
        <span class="hint">确认账号已加入课程，或者到设置里重新登录。</span>
      </div>
    {:else}
      <CoursePicker courses={app.courses} hasAI={app.hasAI} onStart={start} />
    {/if}
  </section>
</div>

<style>
  .page { max-width: 1200px; margin: 0 auto; padding: 22px 20px 60px; display: grid; gap: 16px; }
  .head { display: flex; align-items: flex-end; justify-content: space-between; gap: 14px; }
  .head h1 { font-size: 24px; }
  .head p { margin-top: 2px; }
  .stats {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 10px;
  }
  .stat {
    display: grid;
    gap: 2px;
    padding: 12px 15px;
    border: 1px solid var(--line-soft);
    border-radius: 12px;
    background: linear-gradient(180deg, rgba(23, 28, 37, .7), rgba(17, 21, 28, .6));
  }
  .stat b { font-size: 22px; font-weight: 600; line-height: 1.2; }
  .stat b.on { color: var(--acc-2); }
  .stat b.err { color: var(--err); }
  .cols { display: grid; grid-template-columns: minmax(0, 1.25fr) minmax(0, .95fr); gap: 16px; align-items: start; }
  .side { display: grid; gap: 16px; }
  .continue { position: relative; overflow: hidden; }
  .continue::after {
    content: "";
    position: absolute;
    inset: auto -30% -70% auto;
    width: 320px; height: 220px;
    background: radial-gradient(closest-side, rgba(91, 140, 255, .16), transparent);
    pointer-events: none;
  }
  .big {
    display: block;
    font-size: 20px;
    font-weight: 600;
    color: var(--fg);
    letter-spacing: -.01em;
    margin: 2px 0 2px;
  }
  .big:hover { color: var(--acc-2); }
  .meta { font-size: 12px; }
  .act { margin-top: 16px; }
  .recent { display: grid; gap: 2px; }
  .guide ol { list-style: none; margin: 0; padding: 0; display: grid; gap: 4px; }
  .guide li {
    display: flex; align-items: center; gap: 10px;
    padding: 6px 4px;
  }
  .guide li div { flex: 1; display: grid; line-height: 1.4; }
  .guide b { font-size: 13px; font-weight: 500; }
  .guide small { font-size: 11.5px; color: var(--dim-2); }
  .guide .idx {
    width: 22px; height: 22px;
    border-radius: 50%;
    display: grid; place-items: center;
    font-size: 11px; font-weight: 600;
    color: var(--dim);
    border: 1px solid var(--line);
    background: var(--bg-soft);
    flex: none;
  }
  .guide li.done b { color: var(--dim); }
  .guide li.done .idx { color: #7fdc9c; border-color: #24503a; background: rgba(65, 196, 106, .1); }
  @media (max-width: 900px) {
    .cols { grid-template-columns: 1fr; }
    .stats { grid-template-columns: repeat(2, 1fr); }
  }
  @media (max-width: 520px) {
    .page { padding: 16px 12px 48px; }
    .head h1 { font-size: 20px; }
  }
</style>
