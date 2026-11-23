import type { Device, Transfer } from '../api';
import { formatBytes } from '../format';
import { Icon } from './Icon';

type Props = {
  transfer: Transfer;
  sender?: Device;
  busy: boolean;
  onAccept: () => void;
  onReject: () => void;
};

/** Oferta recebida, em destaque no topo — sem bloquear o restante da tela. */
export function IncomingOffer({ transfer, sender, busy, onAccept, onReject }: Props) {
  const single = transfer.files.length === 1;
  return (
    <section className="offer-card" role="region" aria-labelledby={`offer-${transfer.id}`}>
      <div className="offer-heading">
        <span className="offer-icon" aria-hidden="true"><Icon name="arrow-down" size={20} /></span>
        <div>
          <h2 id={`offer-${transfer.id}`}>{sender?.displayName ?? 'Um dispositivo'} quer enviar {single ? 'um arquivo' : `${transfer.files.length} arquivos`}</h2>
          <p>{formatBytes(transfer.totalBytes)}</p>
        </div>
      </div>
      <ul className="offer-files">
        {transfer.files.slice(0, 4).map((file) => (
          <li key={file.id}><Icon name="file" size={15} /><span>{file.name}</span><small>{formatBytes(file.size)}</small></li>
        ))}
        {transfer.files.length > 4 && <li className="offer-more">e mais {transfer.files.length - 4}…</li>}
      </ul>
      <div className="offer-actions">
        <button type="button" className="button-secondary" disabled={busy} onClick={onReject}>Recusar</button>
        <button type="button" className="button-primary" aria-label="Aceitar transferência" disabled={busy} onClick={onAccept}>
          <Icon name="download" size={17} /> {busy ? 'Aceitando…' : single ? 'Aceitar e baixar' : 'Aceitar'}
        </button>
      </div>
    </section>
  );
}
