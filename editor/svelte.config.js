import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

// Runes-only enforcement: every component compiles as if it has `<svelte:options runes />`.
export default {
  preprocess: vitePreprocess(),
  compilerOptions: { runes: true }
};
