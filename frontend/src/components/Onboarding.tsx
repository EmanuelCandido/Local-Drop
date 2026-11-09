import type { NetworkInfo, PairingRequestState } from '../api';
import { apiUrl } from '../api';
import { Icon } from './Icon';

type Props = {
  name: string;
  network: NetworkInfo | null;
  pairing: PairingRequestState | null;
  busy: boolean;
  canClaimHost: boolean;
  error: string;
  onName: (name: string) => void;
  onHost: () => void;
  onPair: () => void;
  onCancelPairing: () => void;
};

export function Onboarding({
  name,
  network,
  pairing,
  busy,
  canClaimHost,
  error,
  onName,
  onHost,
  onPair,
  onCancelPairing,
}: Props) {
  return (
    <main className="onboarding">
      <div className="brand-row">
        <span className="brand-symbol" aria-hidden="true"><Icon name="send" size={18} /></span>
        <strong>LocalDrop</strong>
      </div>

      {!pairing ? (
        <section className="onboarding-card" aria-labelledby="onboarding-title">
          <h1 id="onboarding-title">{canClaimHost ? 'Prepare este computador' : 'Conecte ao seu PC'}</h1>
          <p>
            {canClaimHost
              ? 'Dê um nome a este computador. Depois, abra o endereço da rede no celular.'
              : 'Troque arquivos com o computador pela sua rede Wi-Fi, sem cabo, conta ou nuvem.'}
          </p>

          <label className="field">
            <span>Como este aparelho vai aparecer</span>
            <input
              value={name}
              onChange={(event) => onName(event.target.value)}
              maxLength={100}
              autoComplete="nickname"
              disabled={busy}
              placeholder={canClaimHost ? 'Ex.: Notebook da sala' : 'Ex.: Celular da Ana'}
            />
          </label>

          {canClaimHost ? (
            <>
              <button type="button" className="button-primary button-block" onClick={onHost} disabled={busy || !name.trim()}>
                {busy ? 'Preparando…' : 'Iniciar neste computador'}
              </button>
              {network && (
                <div className="onboarding-network">
                  <img src={apiUrl('/api/v1/network/qr')} alt={`QR Code para ${network.url}`} />
                  <span>Depois, escaneie com o celular<strong>{network.url}</strong></span>
                </div>
              )}
              <button type="button" className="button-ghost button-block" onClick={onPair} disabled={busy || !name.trim()}>
                Parear como outro dispositivo
              </button>
            </>
          ) : (
            <button type="button" className="button-primary button-block" onClick={onPair} disabled={busy || !name.trim()}>
              <Icon name="link" size={18} /> {busy ? 'Solicitando…' : 'Parear com este computador'}
            </button>
          )}
        </section>
      ) : (
        <section className="onboarding-card pairing-wait" role="status" aria-live="polite">
          <span className="pairing-pulse" aria-hidden="true"><Icon name="monitor" size={26} /></span>
          <h1>Confirme no computador</h1>
          <p>O LocalDrop do PC vai mostrar este mesmo código. Se for igual, toque em <strong>Confiar</strong> lá.</p>
          <strong className="pairing-code">{pairing.code.replace(/(\d{3})(\d{3})/, '$1 $2')}</strong>
          <span className="waiting-label"><span className="status-dot is-waiting" /> Aguardando aprovação…</span>
          <button type="button" className="button-ghost" onClick={onCancelPairing}>Cancelar</button>
        </section>
      )}

      {error && <p className="inline-error" role="alert"><Icon name="alert" size={17} /> {error}</p>}
      <p className="footnote"><Icon name="shield" size={15} /> Os arquivos nunca saem da sua rede local.</p>
    </main>
  );
}
