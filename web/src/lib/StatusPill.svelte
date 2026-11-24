<script>
  let { status = '', label = '' } = $props()
  const text = { queued: '排队中', running: '运行中', done: '已完成', failed: '失败', canceled: '已取消' }
</script>

<span class="pill {status}">
  {#if status === 'running' || status === 'queued'}<i class="dot pulse"></i>{/if}
  {label || text[status] || status}
</span>
