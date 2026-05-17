import type { HandleClientError } from '@sveltejs/kit';

// Client-side error boundary. SvelteKit invokes this for uncaught errors
// thrown during load, rendering, or lifecycle (incl. $effect).
// Wire this to Sentry/Datadog/etc. when ready; for now it just logs.
export const handleError: HandleClientError = ({ error, event, status, message }) => {
  console.error('[client error]', {
    status,
    message,
    url: event.url.pathname + event.url.search,
    error
  });

  return {
    message: 'Something went wrong on this page.'
  };
};
