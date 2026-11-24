<script>
  let { checked = false, indeterminate = false, onchange } = $props()
  let el = $state(null)

  $effect(() => {
    if (el) el.indeterminate = !!indeterminate && !checked
  })
</script>

<input type="checkbox" bind:this={el} {checked} {onchange} />
