import { useEffect, useRef, useState } from 'react';
import type { Session } from './api';

export type ConnectionState = 'connecting' | 'online' | 'reconnecting' | 'offline';
export type ServerEvent = { type: string; payload?: Record<string, unknown> };

export function useEventSocket(
  session: Session | null,
  onEvent: (event: ServerEvent) => void,
) {
  const [state, setState] = useState<ConnectionState>(session ? 'connecting' : 'offline');
  const callback = useRef(onEvent);

  useEffect(() => {
    callback.current = onEvent;
  }, [onEvent]);

  useEffect(() => {
    if (!session) {
      setState('offline');
      return;
    }

    let socket: WebSocket | null = null;
    let heartbeat: number | undefined;
    let reconnectTimer: number | undefined;
    let attempts = 0;
    let disposed = false;

    const connect = () => {
      if (disposed) return;
      window.clearTimeout(reconnectTimer);
      setState(attempts === 0 ? 'connecting' : 'reconnecting');
      const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const current = new WebSocket(`${protocol}//${location.host}/ws/events`);
      socket = current;
      current.onopen = () => {
        current.send(JSON.stringify({
          type: 'AUTHENTICATE',
          deviceId: session.deviceId,
          token: session.token,
        }));
        heartbeat = window.setInterval(() => {
          if (current.readyState === WebSocket.OPEN) {
            current.send(JSON.stringify({ type: 'HEARTBEAT' }));
          }
        }, 20_000);
      };
      current.onmessage = (event) => {
        try {
          const message = JSON.parse(String(event.data)) as ServerEvent;
          if (message.type === 'AUTHENTICATED') {
            attempts = 0;
            setState('online');
            // Ao (re)conectar, uma sincronização completa cobre o que foi perdido.
            callback.current({ type: 'RESYNC' });
          } else if (message.type && message.type !== 'HEARTBEAT_ACK') {
            callback.current(message);
          }
        } catch {
          // Um evento malformado é ignorado; a próxima sincronização prevalece.
        }
      };
      current.onclose = () => {
        if (heartbeat) window.clearInterval(heartbeat);
        if (disposed || socket !== current) return;
        attempts += 1;
        setState('reconnecting');
        const wait = Math.min(16_000, 1_000 * 2 ** Math.min(attempts - 1, 4));
        reconnectTimer = window.setTimeout(connect, wait);
      };
      current.onerror = () => current.close();
    };

    // Celulares suspendem abas em segundo plano: ao voltar, reconecta na hora.
    const onVisible = () => {
      if (document.visibilityState === 'visible' && socket?.readyState !== WebSocket.OPEN) {
        attempts = 0;
        const previous = socket;
        socket = null;
        previous?.close();
        connect();
      }
    };
    document.addEventListener('visibilitychange', onVisible);

    connect();
    return () => {
      disposed = true;
      document.removeEventListener('visibilitychange', onVisible);
      if (heartbeat) window.clearInterval(heartbeat);
      if (reconnectTimer) window.clearTimeout(reconnectTimer);
      socket?.close();
    };
  }, [session]);

  return state;
}
