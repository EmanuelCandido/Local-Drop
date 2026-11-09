import { useRef, useState } from 'react';
import type { Device } from '../api';
import { DevicePicker } from './DevicePicker';
import { Icon } from './Icon';

type Props = {
  devices: Device[];
  selectedDeviceId: string | null;
  busy: boolean;
  onSelectedDevice: (deviceId: string) => void;
  onSend: (files: File[]) => void;
};

/**
 * Envio em um toque: escolher os arquivos já inicia a transferência para o
 * destino selecionado. Texto vira um arquivo pequeno que o PC pode copiar.
 */
export function FileComposer({ devices, selectedDeviceId, busy, onSelectedDevice, onSend }: Props) {
  const filesInput = useRef<HTMLInputElement>(null);
  const mediaInput = useRef<HTMLInputElement>(null);
  const [writing, setWriting] = useState(false);
  const [text, setText] = useState('');
  const [dragging, setDragging] = useState(false);
  const destination = devices.find((device) => device.deviceId === selectedDeviceId);
  const disabled = busy || !destination?.online;

  const pick = (list: FileList | null) => {
    const files = Array.from(list ?? []);
    if (files.length && !disabled) onSend(files);
  };

  const sendText = () => {
    if (!text.trim()) return;
    onSend([new File([text], 'Mensagem LocalDrop.txt', { type: 'text/plain;charset=utf-8', lastModified: Date.now() })]);
    setText('');
    setWriting(false);
  };

  return (
    <section
      className={`send-card ${dragging ? 'dragging' : ''}`}
      aria-labelledby="send-title"
      onDragOver={(event) => { event.preventDefault(); setDragging(true); }}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node)) setDragging(false);
      }}
      onDrop={(event) => {
        event.preventDefault();
        setDragging(false);
        pick(event.dataTransfer.files);
      }}
    >
      <div className="send-heading">
        <h2 id="send-title">Enviar</h2>
        {devices.length > 1 ? (
          <DevicePicker devices={devices} selectedId={selectedDeviceId} onSelect={onSelectedDevice} />
        ) : destination ? (
          <span className="send-target">
            para <strong>{destination.displayName}</strong>
            <span className={`status-dot ${destination.online ? 'is-online' : ''}`} />
          </span>
        ) : null}
      </div>

      {!destination ? (
        <p className="send-empty" data-testid="offline-state">
          Nenhum computador disponível. Peça para aprovar este celular no LocalDrop do PC.
        </p>
      ) : (
        <>
          <div className="send-actions">
            <button type="button" className="send-action" disabled={disabled} onClick={() => mediaInput.current?.click()}>
              <span className="send-action-icon"><Icon name="image" size={24} /></span>
              <strong>Fotos e vídeos</strong>
            </button>
            <button type="button" className="send-action" disabled={disabled} onClick={() => filesInput.current?.click()}>
              <span className="send-action-icon"><Icon name="file" size={24} /></span>
              <strong>Arquivos</strong>
            </button>
            <button
              type="button"
              className={`send-action ${writing ? 'active' : ''}`}
              disabled={disabled}
              aria-expanded={writing}
              onClick={() => setWriting((value) => !value)}
            >
              <span className="send-action-icon"><Icon name="message" size={24} /></span>
              <strong>Texto ou link</strong>
            </button>
          </div>
          {!destination.online && (
            <p className="send-hint"><Icon name="wifi" size={15} /> {destination.displayName} está offline. Abra o LocalDrop no PC.</p>
          )}
          {writing && (
            <div className="text-composer">
              <textarea
                value={text}
                onChange={(event) => setText(event.target.value)}
                rows={4}
                maxLength={100_000}
                autoFocus
                aria-label="Texto para enviar"
                placeholder="Cole um link ou escreva uma mensagem…"
              />
              <button type="button" className="button-primary" onClick={sendText} disabled={!text.trim() || disabled}>
                <Icon name="send" size={17} /> Enviar texto
              </button>
            </div>
          )}
        </>
      )}

      <input ref={mediaInput} type="file" accept="image/*,video/*" multiple hidden onChange={(event) => { pick(event.target.files); event.target.value = ''; }} />
      <input ref={filesInput} type="file" multiple hidden onChange={(event) => { pick(event.target.files); event.target.value = ''; }} />
    </section>
  );
}
