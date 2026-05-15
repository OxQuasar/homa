import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [sveltekit()],
	server: {
		// homa sandboxes are reached via reverse proxy / Tailscale Serve
		// with arbitrary Host headers (e.g. <node>.<tailnet>.ts.net).
		// Vite's default Host-header allow-list rejects those as a CSRF
		// protection; the sandbox boundary is the actual security gate
		// here, not Vite's allow-list. Allow any Host.
		allowedHosts: true
	}
});
