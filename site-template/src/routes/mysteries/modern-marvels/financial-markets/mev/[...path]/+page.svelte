<script lang="ts">
  import CorpusBrowser from '$lib/CorpusBrowser.svelte';
  import type { PageProps } from './$types';

  let { data }: PageProps = $props();
</script>

<CorpusBrowser
  {data}
  prefix="/mysteries/modern-marvels/financial-markets/mev"
  rootLabel="MEV"
  rootBackHref="/mysteries/modern-marvels/financial-markets"
/>
