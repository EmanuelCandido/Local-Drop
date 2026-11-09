export type Device = {
  deviceId: string;
  displayName: string;
  deviceType: string;
  host: boolean;
  online: boolean;
  lastSeenAt: string;
};

export type TransferFile = {
  id: string;
  ordinal: number;
  name: string;
  size: number;
  mimeType: string;
  sha256: string;
  totalChunks: number;
  receivedChunks: number;
  receivedBytes: number;
  state: string;
  downloadAvailable: boolean;
  deliveredName: string | null;
};

export type Transfer = {
  id: string;
  senderId: string;
  receiverId: string;
  state: string;
  totalBytes: number;
  receivedBytes: number;
  chunkSize: number;
  createdAt: string;
  updatedAt: string;
  acceptedAt: string | null;
  completedAt: string | null;
  failureCode: string | null;
  failureMessage: string | null;
  files: TransferFile[];
};

export type MissingChunks = {
  totalChunks: number;
  receivedChunks: number;
  ranges: { startInclusive: number; endExclusive: number }[];
  nextCursor: number | null;
};

export type Session = {
  deviceId: string;
  token: string;
  displayName: string;
  deviceType: string;
  host: boolean;
};

export type PairingPending = {
  requestId: string;
  deviceId: string;
  displayName: string;
  deviceType: string;
  code: string;
  status: string;
  createdAt: string;
  expiresAt: string;
};

export type NetworkInfo = {
  url: string;
  addresses: { name: string; ipv4: string; score: number }[];
};

export type Settings = {
  device: Device;
  downloadDirectory: string | null;
  autoAcceptTrusted: boolean;
  port: number;
  updatedAt: string;
};

export type HistoryItem = {
  id: string;
  direction: 'INCOMING' | 'OUTGOING';
  peerId: string;
  peerName: string;
  state: string;
  totalBytes: number;
  createdAt: string;
  completedAt: string | null;
  failureCode: string | null;
  files: { name: string; size: number; state: string }[];
};

export type PairingRequestState = {
  requestId: string;
  code: string;
  pollToken: string;
  deviceId: string;
  displayName: string;
  status: string;
};

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

const SESSION_KEY = 'localdrop.session';
const DEVICE_ID_KEY = 'localdrop.deviceId';
const rawBase = (import.meta.env.VITE_API_BASE as string | undefined) ?? '';
export const API_BASE = rawBase.replace(/\/$/, '');

export function apiUrl(path: string) {
  return `${API_BASE}${path}`;
}

export function loadSession(): Session | null {
  try {
    return JSON.parse(localStorage.getItem(SESSION_KEY) ?? 'null') as Session | null;
  } catch {
    return null;
  }
}

export function saveSession(session: Session | null) {
  if (session) localStorage.setItem(SESSION_KEY, JSON.stringify(session));
  else localStorage.removeItem(SESSION_KEY);
}

export function createDeviceId() {
  const bytes = new Uint8Array(16);
  if (typeof globalThis.crypto?.getRandomValues === 'function') {
    globalThis.crypto.getRandomValues(bytes);
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256);
    }
  }

  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = [...bytes].map((value) => value.toString(16).padStart(2, '0'));
  return [
    hex.slice(0, 4).join(''),
    hex.slice(4, 6).join(''),
    hex.slice(6, 8).join(''),
    hex.slice(8, 10).join(''),
    hex.slice(10, 16).join(''),
  ].join('-');
}

export function stableDeviceId() {
  let id = localStorage.getItem(DEVICE_ID_KEY);
  if (!id) {
    id = createDeviceId();
    localStorage.setItem(DEVICE_ID_KEY, id);
  }
  return id;
}

export function detectDeviceType() {
  const ua = navigator.userAgent.toLowerCase();
  if (/ipad|tablet/.test(ua)) return 'TABLET';
  if (/android|iphone|mobile/.test(ua)) return 'MOBILE';
  return 'DESKTOP';
}

export function defaultDeviceName() {
  return detectDeviceType() === 'MOBILE' ? 'Meu celular' : 'Meu PC';
}

async function errorFrom(response: Response) {
  const body = (await response.json().catch(() => null)) as
    | { code?: string; message?: string }
    | null;
  return new ApiError(
    response.status,
    body?.code ?? 'REQUEST_FAILED',
    body?.message ?? response.statusText ?? 'A solicitação falhou.',
  );
}

async function publicJson<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(apiUrl(path), init);
  if (!response.ok) throw await errorFrom(response);
  return response.json() as Promise<T>;
}

export class Api {
  constructor(readonly session: Session) {}

  private headers(initial?: HeadersInit) {
    const headers = new Headers(initial);
    headers.set('Authorization', `Device ${this.session.token}`);
    headers.set('X-Device-Id', this.session.deviceId);
    return headers;
  }

  async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = this.headers(init.headers);
    if (typeof init.body === 'string' && !headers.has('Content-Type')) {
      headers.set('Content-Type', 'application/json');
    }
    const response = await fetch(apiUrl(path), { ...init, headers });
    if (!response.ok) throw await errorFrom(response);
    if (response.status === 204) return undefined as T;
    return response.json() as Promise<T>;
  }

  me() { return this.request<Device>('/api/v1/device/me'); }
  devices() { return this.request<Device[]>('/api/v1/devices'); }
  transfers() { return this.request<Transfer[]>('/api/v1/transfers'); }

  history(before?: string, limit = 50) {
    const query = new URLSearchParams({ limit: String(limit) });
    if (before) query.set('before', before);
    return this.request<HistoryItem[]>(`/api/v1/history?${query}`);
  }

  settings() { return this.request<Settings>('/api/v1/settings'); }

  chooseDownloadDirectory() {
    return this.request<{ downloadDirectory: string | null }>(
      '/api/v1/settings/download-directory/pick',
      { method: 'POST' },
    );
  }

  updateSettings(update: {
    displayName?: string;
    deviceType?: string;
    downloadDirectory?: string;
    autoAcceptTrusted?: boolean;
  }) {
    return this.request<Settings>('/api/v1/settings', {
      method: 'PATCH',
      body: JSON.stringify(update),
    });
  }

  pendingPairings() {
    return this.request<PairingPending[]>('/api/v1/pairing/requests');
  }

  approvePairing(requestId: string, code: string) {
    return this.request<void>(
      `/api/v1/pairing/requests/${encodeURIComponent(requestId)}/approve`,
      { method: 'POST', body: JSON.stringify({ code }) },
    );
  }

  rejectPairing(requestId: string) {
    return this.request<void>(
      `/api/v1/pairing/requests/${encodeURIComponent(requestId)}/reject`,
      { method: 'POST' },
    );
  }

  revoke(deviceId: string) {
    return this.request<void>(
      `/api/v1/trusted-devices/${encodeURIComponent(deviceId)}`,
      { method: 'DELETE' },
    );
  }

  create(
    receiverId: string,
    files: { name: string; size: number; mimeType: string; sha256: string }[],
  ) {
    return this.request<Transfer>('/api/v1/transfers', {
      method: 'POST',
      body: JSON.stringify({ receiverId, files }),
    });
  }

  action(id: string, action: 'accept' | 'reject' | 'cancel' | 'pause' | 'resume') {
    return this.request<Transfer>(
      `/api/v1/transfers/${encodeURIComponent(id)}/${action}`,
      { method: 'POST' },
    );
  }

  missing(id: string, fileId: string, cursor = 0) {
    const query = new URLSearchParams({ cursor: String(cursor), limit: '1000' });
    return this.request<MissingChunks>(
      `/api/v1/transfers/${encodeURIComponent(id)}/files/${encodeURIComponent(fileId)}/missing?${query}`,
    );
  }

  upload(id: string, fileId: string, index: number, data: BodyInit, hash: string) {
    return this.request<{ duplicate: boolean; receivedBytes: number; totalBytes: number; state: string }>(
      `/api/v1/transfers/${encodeURIComponent(id)}/files/${encodeURIComponent(fileId)}/chunks/${index}`,
      {
        method: 'PUT',
        body: data,
        headers: {
          'Content-Type': 'application/octet-stream',
          'X-Chunk-SHA256': hash,
        },
      },
    );
  }

  /** Link temporário para o gerenciador de downloads nativo do navegador. */
  async downloadLink(id: string, fileId: string) {
    const { url } = await this.request<{ url: string }>(
      `/api/v1/transfers/${encodeURIComponent(id)}/files/${encodeURIComponent(fileId)}/download-link`,
      { method: 'POST' },
    );
    return apiUrl(url);
  }

  async download(id: string, fileId: string) {
    const response = await fetch(
      apiUrl(`/api/v1/transfers/${encodeURIComponent(id)}/files/${encodeURIComponent(fileId)}/download`),
      { headers: this.headers() },
    );
    if (!response.ok) throw await errorFrom(response);
    return response.blob();
  }
}

export function getNetwork() {
  return publicJson<NetworkInfo>('/api/v1/network');
}

export async function claimHost(displayName: string): Promise<Session> {
  const response = await publicJson<{ deviceId: string; deviceToken: string }>(
    '/api/v1/host/claim',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        deviceId: stableDeviceId(),
        displayName,
        deviceType: detectDeviceType(),
      }),
    },
  );
  localStorage.setItem(DEVICE_ID_KEY, response.deviceId);
  return {
    deviceId: response.deviceId,
    token: response.deviceToken,
    displayName,
    deviceType: detectDeviceType(),
    host: true,
  };
}

export async function requestPairing(displayName: string): Promise<PairingRequestState> {
  const deviceId = stableDeviceId();
  const response = await publicJson<{
    requestId: string;
    code: string;
    pollToken: string;
    status: string;
  }>('/api/v1/pairing/requests', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      deviceId,
      displayName,
      deviceType: detectDeviceType(),
    }),
  });
  return { ...response, deviceId, displayName };
}

export function pollPairing(requestId: string, pollToken: string) {
  return publicJson<{ status: string; deviceToken: string | null }>(
    `/api/v1/pairing/requests/${encodeURIComponent(requestId)}/status`,
    { headers: { 'X-Pairing-Poll-Token': pollToken } },
  );
}
