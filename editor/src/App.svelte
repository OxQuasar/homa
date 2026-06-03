<script lang="ts">
  import Signup from './routes/signup.svelte';
  import Login from './routes/login.svelte';
  import Editor from './routes/editor.svelte';
  import Admin from './routes/admin.svelte';
  import Guidelines from './routes/guidelines.svelte';
  import Forgot from './routes/forgot.svelte';
  import { parseRoute } from './lib/route';

  // Resolve from both pathname (orchestrator's 302s + direct navigation)
  // and hash (post-form `location.hash = '#/editor'` redirects). Hash wins
  // when it names a known route — see lib/route.ts.
  function currentRoute() {
    return parseRoute(window.location.pathname, window.location.hash);
  }

  let route = $state(currentRoute());

  $effect(() => {
    const update = () => (route = currentRoute());
    window.addEventListener('hashchange', update);
    window.addEventListener('popstate', update);
    return () => {
      window.removeEventListener('hashchange', update);
      window.removeEventListener('popstate', update);
    };
  });
</script>

{#if route === 'signup'}
  <Signup />
{:else if route === 'editor'}
  <Editor />
{:else if route === 'admin'}
  <Admin />
{:else if route === 'guidelines'}
  <Guidelines />
{:else if route === 'forgot'}
  <Forgot />
{:else}
  <Login />
{/if}

<style>
  :global(body, html) { margin: 0; padding: 0; font-family: system-ui, -apple-system, sans-serif; }
  :global(*) { box-sizing: border-box; }
</style>
