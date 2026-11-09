import { createSHA256 } from 'hash-wasm';
import { ApiError, type Api, type Transfer } from './api';

export const DEFAULT_CHUNK_SIZE = 4 * 1024 * 1024;
const PARALLEL_UPLOADS = 4;
const MAX_UPLOAD_ATTEMPTS = 4;

export type LiveProgress = {
  transferredBytes: number;
  totalBytes: number;
  speedBytesPerSecond: number;
  etaSeconds: number | null;
};

export type FileMetadata = { name: string; size: number; mimeType: string; sha256: string };

type Hasher = Awaited<ReturnType<typeof createSHA256>>;

function hexDigest(hasher: Hasher, bytes: Uint8Array) {
  hasher.init();
  hasher.update(bytes);
  return hasher.digest('hex');
}

export async function digest(blob: Blob) {
  return hexDigest(await createSHA256(), new Uint8Array(await blob.arrayBuffer()));
}

/**
 * Metadados da oferta. O SHA-256 do arquivo inteiro não é calculado: cada bloco
 * leva o próprio hash e é conferido no computador, então o arquivo é lido uma
 * única vez — durante o envio.
 */
export function metadataForFiles(files: File[]): FileMetadata[] {
  return files.map((file) => ({
    name: file.name,
    size: file.size,
    mimeType: file.type || 'application/octet-stream',
    sha256: '',
  }));
}

export function attachFilesToTransfer(transfer: Transfer, selected: File[]) {
  const orderedRemote = [...transfer.files].sort((a, b) => a.ordinal - b.ordinal);
  if (orderedRemote.length !== selected.length) {
    throw new Error(`Selecione exatamente ${orderedRemote.length} arquivo(s).`);
  }
  const attached = new Map<string, File>();
  orderedRemote.forEach((remote, index) => {
    const local = selected[index];
    if (remote.name !== local.name || remote.size !== local.size) {
      throw new Error(`O arquivo ${index + 1} não corresponde a ${remote.name}.`);
    }
    attached.set(remote.id, local);
  });
  return attached;
}

export async function uploadTransfer(
  api: Api,
  transfer: Transfer,
  attached: Map<string, File>,
  onProgress: (progress: LiveProgress) => void,
) {
  const chunkSize = transfer.chunkSize || DEFAULT_CHUNK_SIZE;
  const startedAt = performance.now();
  const initialBytes = transfer.receivedBytes;
  let transferredBytes = initialBytes;
  let smoothedSpeed = 0;
  let lastReport = 0;

  const report = (force = false) => {
    const now = performance.now();
    if (!force && now - lastReport < 200) return;
    lastReport = now;
    const elapsedSeconds = Math.max((now - startedAt) / 1000, 0.05);
    const measured = Math.max(0, transferredBytes - initialBytes) / elapsedSeconds;
    smoothedSpeed = smoothedSpeed === 0 ? measured : smoothedSpeed * 0.7 + measured * 0.3;
    const remaining = Math.max(0, transfer.totalBytes - transferredBytes);
    onProgress({
      transferredBytes,
      totalBytes: transfer.totalBytes,
      speedBytesPerSecond: smoothedSpeed,
      etaSeconds: smoothedSpeed > 0 ? remaining / smoothedSpeed : null,
    });
  };

  // Uma fila única com os blocos ausentes de todos os arquivos.
  const queue: { fileId: string; file: File; index: number }[] = [];
  for (const remote of [...transfer.files].sort((a, b) => a.ordinal - b.ordinal)) {
    const file = attached.get(remote.id);
    if (!file) throw new Error(`Reanexe o arquivo ${remote.name} para continuar.`);
    let cursor: number | null = 0;
    while (cursor !== null) {
      const missing = await api.missing(transfer.id, remote.id, cursor);
      for (const index of ranges(missing.ranges)) queue.push({ fileId: remote.id, file, index });
      cursor = missing.nextCursor;
    }
  }

  report(true);
  let next = 0;
  const worker = async () => {
    const hasher = await createSHA256();
    while (next < queue.length) {
      const { fileId, file, index } = queue[next];
      next += 1;
      const start = index * chunkSize;
      // Os bytes lidos para o hash são os mesmos enviados: nada é lido duas vezes.
      const bytes = new Uint8Array(
        await file.slice(start, Math.min(file.size, start + chunkSize)).arrayBuffer(),
      );
      await uploadWithRetry(api, transfer.id, fileId, index, bytes, hexDigest(hasher, bytes));
      transferredBytes = Math.min(transfer.totalBytes, transferredBytes + bytes.byteLength);
      report();
    }
  };
  await Promise.all(Array.from({ length: Math.min(PARALLEL_UPLOADS, queue.length) }, worker));
  report(true);
}

async function uploadWithRetry(
  api: Api,
  transferId: string,
  fileId: string,
  chunkIndex: number,
  bytes: Uint8Array<ArrayBuffer>,
  sha256: string,
) {
  let lastError: unknown;
  for (let attempt = 1; attempt <= MAX_UPLOAD_ATTEMPTS; attempt += 1) {
    try {
      await api.upload(transferId, fileId, chunkIndex, bytes, sha256);
      return;
    } catch (error) {
      lastError = error;
      if (!isRetriable(error) || attempt === MAX_UPLOAD_ATTEMPTS) throw error;
      await delay(500 * 2 ** (attempt - 1));
    }
  }
  throw lastError;
}

function isRetriable(error: unknown) {
  if (!(error instanceof ApiError)) return true;
  return error.status === 429 || error.status >= 500;
}

function delay(milliseconds: number) {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
}

export const ranges = (input: { startInclusive: number; endExclusive: number }[]) =>
  input.flatMap((range) =>
    Array.from(
      { length: range.endExclusive - range.startInclusive },
      (_, index) => range.startInclusive + index,
    ),
  );
