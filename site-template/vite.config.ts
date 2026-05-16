import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig, type Plugin } from 'vite';

// homaErrorBeacon injects a tiny capture script into every page <head>
// so window.onerror + unhandledrejection events post up to the homa
// editor (window.parent). The editor surfaces them to the LLM with the
// user's next message, closing the "I wrote broken code → I see the
// failure" loop without copy-paste.
//
// No-op when not running in an iframe — the public main site at
// homa.tailnet.ts.net/ has no parent, so the script short-circuits.
//
// Cross-origin notes: editor and iframe are on different ports/origins.
// postMessage with '*' target is OK here because the payload contains
// only error reports we're voluntarily emitting; sensitive data never
// flows through this channel. The editor restricts incoming messages by
// origin so unrelated tabs can't inject fake "errors".
function homaErrorBeacon(): Plugin {
	return {
		name: 'homa-error-beacon',
		transformIndexHtml(html) {
			return html.replace('<head>', '<head>' + beaconScript);
		}
	};
}

const beaconScript = `<script>
(function () {
  if (window.parent === window) return;
  var THROTTLE_MS = 500;
  var lastSent = Object.create(null);
  function send(payload) {
    var key = payload.kind + '|' + payload.message;
    var now = Date.now();
    if (lastSent[key] && now - lastSent[key] < THROTTLE_MS) return;
    lastSent[key] = now;
    try {
      window.parent.postMessage({ type: 'homa:browser-error', payload: payload }, '*');
    } catch (e) {}
  }
  window.addEventListener('error', function (e) {
    send({
      kind: 'error',
      message: e.message || String(e),
      stack: (e.error && e.error.stack) || null,
      source: e.filename || null,
      line: e.lineno || null,
      col: e.colno || null,
      url: window.location.href,
      timestamp: Date.now()
    });
  });
  window.addEventListener('unhandledrejection', function (e) {
    var r = e.reason;
    var msg = (r && (r.message || (typeof r === 'string' ? r : null))) ||
              (r ? String(r) : 'Unknown rejection');
    send({
      kind: 'unhandledrejection',
      message: msg,
      stack: (r && r.stack) || null,
      url: window.location.href,
      timestamp: Date.now()
    });
  });
})();
</script>`;

export default defineConfig({
	plugins: [homaErrorBeacon(), sveltekit()],
	server: {
		// homa sandboxes are reached via reverse proxy / Tailscale Serve
		// with arbitrary Host headers (e.g. <node>.<tailnet>.ts.net).
		// Vite's default Host-header allow-list rejects those as a CSRF
		// protection; the sandbox boundary is the actual security gate
		// here, not Vite's allow-list. Allow any Host.
		allowedHosts: true
	}
});
