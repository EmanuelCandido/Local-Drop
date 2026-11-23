import type { PairingPending } from '../api';
import { Icon } from './Icon';

type Props = {
  requests: PairingPending[];
  busyId: string | null;
  onApprove: (request: PairingPending) => void;
  onReject: (request: PairingPending) => void;
};

export function PairingRequests({ requests, busyId, onApprove, onReject }: Props) {
  if (!requests.length) return null;
  return (
    <section className="pairing-requests panel" aria-labelledby="pairing-title">
      <div className="pairing-banner-icon"><Icon name="shield" size={20} /></div>
      <div className="pairing-content">
        <div className="section-heading">
          <div>
            <h2 id="pairing-title">Quem pode entrar?</h2>
            <p>Confira o nome e o código antes de confiar no dispositivo.</p>
          </div>
          <span className="count-badge">{requests.length}</span>
        </div>
        <div className="pairing-list">
          {requests.map((request) => {
            const isMobile = request.deviceType.toUpperCase().includes('MOBILE')
              || request.deviceType.toUpperCase().includes('TABLET');
            return (
              <article key={request.requestId}>
                <span className="device-glyph" aria-hidden="true">
                  <Icon name={isMobile ? 'smartphone' : 'monitor'} size={19} />
                </span>
                <div>
                  <strong>{request.displayName}</strong>
                  <small>{isMobile ? 'Navegador móvel' : 'Navegador desktop'}</small>
                </div>
                <div className="pair-code">
                  <small>Código</small>
                  <b>{request.code.replace(/(\d{3})(\d{3})/, '$1 $2')}</b>
                </div>
                <div className="pair-actions">
                  <button
                    type="button"
                    className="button-secondary"
                    disabled={busyId === request.requestId}
                    onClick={() => onReject(request)}
                  ><Icon name="x" size={16} /> Recusar</button>
                  <button
                    type="button"
                    disabled={busyId === request.requestId}
                    onClick={() => onApprove(request)}
                  ><Icon name="check" size={16} /> Confiar</button>
                </div>
              </article>
            );
          })}
        </div>
      </div>
    </section>
  );
}
