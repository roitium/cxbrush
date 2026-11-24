<script>
  import { app } from './state.svelte.js'
</script>

{#if app.toast}
  {#key app.toast.id}
    <div class="toast {app.toast.kind}" role="status">{app.toast.text}</div>
  {/key}
{/if}

<style>
  .toast {
    position: fixed;
    left: 50%;
    bottom: 28px;
    transform: translateX(-50%);
    z-index: 60;
    padding: 10px 16px;
    border-radius: 10px;
    border: 1px solid var(--line);
    background: #1b212b;
    box-shadow: 0 16px 40px -16px rgba(0, 0, 0, .9);
    font-size: 13px;
    max-width: min(92vw, 520px);
    animation: rise .22s ease;
  }
  .toast.ok { border-color: #24503a; color: #7fdc9c; }
  .toast.err { border-color: #5b2a2e; color: #f79094; }
  @keyframes rise {
    from { opacity: 0; transform: translate(-50%, 10px); }
    to { opacity: 1; transform: translate(-50%, 0); }
  }
</style>
