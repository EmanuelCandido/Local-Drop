import { useRef } from 'react';
import type { Device, Transfer } from '../api';
import { isTextMessage } from '../browser';
import { formatBytes, formatEta, formatTime, statusLabel } from '../format';
import type { LiveProgress } from '../transfer';
import { Icon } from './Icon';

type Action = 'accept' | 'reject' | 'cancel' | 'pause' | 'resume';

type Props = {
  transfer: Transfer;
  self: string;
  devices: Device[];
  live?: LiveProgress;
  hasAttachedFiles: boolean;
  downloaded?: Set<string>;
  busy: boolean;
  onAction: (action: Action) => void;
  onDownload: (fileId: string, fileName: string) => void;
  onPreview?: (fileId: string, fileName: string) => void;
  onReattach: (files: File[]) => void;
};

const terminal = new Set(['COMPLETED', 'REJECTED', 'CANCELLED', 'FAILED']);
const active = new Set(['ACCEPTED', 'TRANSFERRING', 'VERIFYING', 'PAUSED']);

export function TransferCard({
  transfer,
  self,
  devices,
  live,
  hasAttachedFiles,
  downloaded,
  busy,
  onAction,
  onDownload,
  onPreview,
  onReattach,
}: Props) {
  const attachInput = useRef<HTMLInputElement>(null);
  const incoming = transfer.receiverId === self;
  const peer = devices.find((device) => device.deviceId === (incoming ? transfer.senderId : transfer.receiverId));
  const transferred = Math.max(transfer.receivedBytes, live?.transferredBytes ?? 0);
  const percent = transfer.totalBytes === 0
    ? 100
    : Math.min(100, Math.round((transferred / transfer.totalBytes) * 100));
  const showProgress = active.has(transfer.state) && transfer.totalBytes > 0;
  const canReattach = !incoming && !terminal.has(transfer.state) && transfer.state !== 'WAITING_ACCEPTANCE' && !hasAttachedFiles;
  const title = transfer.files.length === 1
    ? transfer.files[0].name
    : `${transfer.files[0]?.name ?? 'Arquivos'} e mais ${transfer.files.length - 1}`;
  const peerName = peer?.displayName ?? 'Dispositivo removido';
  const readyFiles = incoming && transfer.state === 'COMPLETED'
    ? transfer.files.filter((file) => file.downloadAvailable)
    : [];

  return (
    <article className={`transfer-card state-${transfer.state.toLowerCase()}`}>
      <div className="transfer-row">
        <span className={`transfer-direction ${incoming ? 'incoming' : ''}`} aria-hidden="true">
          <Icon name={incoming ? 'arrow-down' : 'arrow-up'} size={18} />
        </span>
        <div className="transfer-copy">
          <strong>{title}</strong>
          <small>
            {incoming ? 'De ' : 'Para '}{peerName} · {formatBytes(transfer.totalBytes)} · {formatTime(transfer.createdAt)}
          </small>
        </div>
        {!showProgress && (
          <span className={`status-pill state-${transfer.state.toLowerCase()}`}>
            {statusLabel(transfer.state, incoming)}
          </span>
        )}
      </div>

      {showProgress && (
        <div className="transfer-progress">
          <progress value={Math.min(transferred, transfer.totalBytes)} max={transfer.totalBytes} aria-label="Progresso" />
          <div className="progress-meta">
            <strong>{percent}%</strong>
            <span>
              {live && live.speedBytesPerSecond > 0 && transfer.state === 'TRANSFERRING'
                ? `${formatBytes(live.speedBytesPerSecond)}/s · ${formatEta(live.etaSeconds)}`
                : `${formatBytes(transferred)} de ${formatBytes(transfer.totalBytes)} · ${statusLabel(transfer.state, incoming)}`}
            </span>
          </div>
          {!incoming && transfer.state === 'TRANSFERRING' && (
            <p className="transfer-hint">Mantenha esta tela aberta até terminar.</p>
          )}
        </div>
      )}

      {readyFiles.length > 0 && (
        <ul className="ready-files">
          {readyFiles.map((file) => {
            const done = downloaded?.has(file.id);
            return (
              <li key={file.id}>
                <span className="ready-name">{file.deliveredName ?? file.name}<small>{formatBytes(file.size)}</small></span>
                {isTextMessage(file) && onPreview && (
                  <button type="button" className="button-secondary button-small" onClick={() => onPreview(file.id, file.name)}>
                    <Icon name="message" size={15} /> Ver texto
                  </button>
                )}
                <button
                  type="button"
                  className={`button-small ${done ? 'button-secondary' : 'button-primary'}`}
                  disabled={busy}
                  onClick={() => onDownload(file.id, file.deliveredName ?? file.name)}
                >
                  <Icon name={done ? 'check' : 'download'} size={15} /> {done ? 'Baixar de novo' : 'Baixar'}
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {incoming && transfer.state === 'COMPLETED' && readyFiles.length === 0 && (
        <p className="transfer-hint">O link de download expirou. Peça para enviar de novo.</p>
      )}

      {transfer.failureMessage && transfer.state === 'FAILED' && (
        <p className="inline-error" role="alert"><Icon name="alert" size={16} /> {transfer.failureMessage}</p>
      )}

      {canReattach && (
        <div className="reattach-note">
          <span>Para continuar, escolha de novo {transfer.files.length === 1 ? 'o arquivo' : 'os mesmos arquivos'}.</span>
          <button type="button" className="button-secondary button-small" onClick={() => attachInput.current?.click()}>
            <Icon name="link" size={15} /> Reanexar
          </button>
          <input
            ref={attachInput}
            type="file"
            multiple
            hidden
            onChange={(event) => {
              if (event.target.files) onReattach(Array.from(event.target.files));
              event.target.value = '';
            }}
          />
        </div>
      )}

      {!terminal.has(transfer.state) && !(incoming && transfer.state === 'WAITING_ACCEPTANCE') && (
        <div className="transfer-actions">
          {['ACCEPTED', 'TRANSFERRING'].includes(transfer.state) && (
            <button type="button" className="button-ghost button-small" disabled={busy} onClick={() => onAction('pause')}>
              <Icon name="pause" size={15} /> Pausar
            </button>
          )}
          {!incoming && transfer.state === 'PAUSED' && (
            <button type="button" className="button-ghost button-small" disabled={busy || !hasAttachedFiles} onClick={() => onAction('resume')}>
              <Icon name="play" size={15} /> Retomar
            </button>
          )}
          <button type="button" className="button-ghost danger button-small" disabled={busy} onClick={() => onAction('cancel')}>
            <Icon name="x" size={15} /> Cancelar
          </button>
        </div>
      )}
    </article>
  );
}
