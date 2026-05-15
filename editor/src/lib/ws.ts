// Native WebSocket wrapper for the /ws proxy. Frame-opaque on the orchestrator
// side; the wire payload here is JSON per nous's transport protocol.
//
// Reconnect is intentionally out of scope for MVP — surface a closed status
// and let the editor decide what to do.

import type { Event as NousEvent, Request as NousRequest } from './types';

export type WSStatus = 'connecting' | 'open' | 'closed';

export interface Session {
  status: () => WSStatus;
  send: (req: NousRequest) => void;
  close: () => void;
}

export interface OpenOptions {
  workDir: string;
  onEvent: (ev: NousEvent) => void;
  onStatus?: (status: WSStatus) => void;
}

export function openSession({ workDir, onEvent, onStatus }: OpenOptions): Session {
  const url = wsURL();
  const ws = new WebSocket(url);
  let status: WSStatus = 'connecting';
  const setStatus = (s: WSStatus) => { status = s; onStatus?.(s); };

  ws.addEventListener('open', () => {
    // First message MUST be the Hello so the daemon can spawn / attach a
    // session. Wire format is JSON per line (orchestrator proxy is opaque).
    ws.send(JSON.stringify({ work_dir: workDir }));
    setStatus('open');
  });
  ws.addEventListener('message', (e) => {
    try {
      const parsed = JSON.parse(e.data as string) as NousEvent;
      onEvent(parsed);
    } catch (err) {
      console.error('ws: bad event payload', err, e.data);
    }
  });
  ws.addEventListener('close', () => setStatus('closed'));
  ws.addEventListener('error', () => setStatus('closed'));

  return {
    status: () => status,
    send: (req: NousRequest) => {
      if (ws.readyState !== WebSocket.OPEN) return;
      ws.send(JSON.stringify(req));
    },
    close: () => ws.close()
  };
}

function wsURL(): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${window.location.host}/ws`;
}
