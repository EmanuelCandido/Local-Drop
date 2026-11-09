export function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return bytes === 0 ? '0 B' : '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const unit = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / 1024 ** unit;
  return `${value.toLocaleString('pt-BR', { maximumFractionDigits: unit === 0 || value >= 100 ? 0 : 1 })} ${units[unit]}`;
}

export function formatEta(seconds: number | null) {
  if (seconds == null || !Number.isFinite(seconds)) return 'calculando…';
  if (seconds < 2) return 'quase lá';
  if (seconds < 60) return `${Math.ceil(seconds)} s restantes`;
  const minutes = Math.ceil(seconds / 60);
  return `${minutes} min restantes`;
}

export function formatDate(raw: string) {
  return new Intl.DateTimeFormat('pt-BR', {
    dateStyle: 'short',
    timeStyle: 'short',
  }).format(new Date(raw));
}

/** Horário curto e relativo: "agora", "há 5 min", "14:32", "ontem 09:10". */
export function formatTime(raw: string, now = new Date()) {
  const date = new Date(raw);
  const elapsed = (now.getTime() - date.getTime()) / 1000;
  if (elapsed < 60) return 'agora';
  if (elapsed < 3600) return `há ${Math.floor(elapsed / 60)} min`;
  const time = new Intl.DateTimeFormat('pt-BR', { hour: '2-digit', minute: '2-digit' }).format(date);
  if (date.toDateString() === now.toDateString()) return time;
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (date.toDateString() === yesterday.toDateString()) return `ontem ${time}`;
  return formatDate(raw);
}

export function statusLabel(state: string, incoming = false) {
  const labels: Record<string, string> = {
    CREATED: 'Criada',
    WAITING_ACCEPTANCE: incoming ? 'Aguardando você' : 'Aguardando aceite',
    ACCEPTED: 'Iniciando',
    TRANSFERRING: 'Enviando',
    PAUSED: 'Pausada',
    VERIFYING: 'Conferindo',
    COMPLETED: incoming ? 'Pronto' : 'Entregue',
    REJECTED: 'Recusada',
    CANCELLED: 'Cancelada',
    FAILED: 'Falhou',
  };
  return labels[state] ?? state;
}
