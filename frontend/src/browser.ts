/**
 * Entrega o arquivo ao gerenciador de downloads do navegador. O link já é
 * autorizado, então nada passa pela memória da página — importante para
 * vídeos grandes no celular.
 */
export function startDownload(url: string, fileName: string) {
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = fileName;
  anchor.rel = 'noopener';
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
}

/** Copia texto também em http:// na rede local, onde a Clipboard API não existe. */
export async function copyText(text: string) {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.setAttribute('readonly', '');
  textarea.className = 'clipboard-fallback';
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand('copy');
  textarea.remove();
  if (!copied) throw new Error('O navegador recusou a cópia.');
}

const DOWNLOADED_KEY = 'localdrop.downloaded';

export function loadDownloaded(): Set<string> {
  try {
    return new Set(JSON.parse(localStorage.getItem(DOWNLOADED_KEY) ?? '[]') as string[]);
  } catch {
    return new Set();
  }
}

export function saveDownloaded(ids: Set<string>) {
  // Guarda só os mais recentes para o armazenamento não crescer sem limite.
  localStorage.setItem(DOWNLOADED_KEY, JSON.stringify([...ids].slice(-300)));
}

export function isTextMessage(file: { mimeType: string; size: number }) {
  return file.mimeType.startsWith('text/plain') && file.size <= 1024 * 1024;
}
