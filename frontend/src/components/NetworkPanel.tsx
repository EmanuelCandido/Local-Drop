import { useState } from 'react';
import type { NetworkInfo } from '../api';
import { apiUrl } from '../api';
import { Icon } from './Icon';

type Props = {
  network: NetworkInfo | null;
  host: boolean;
  connection: string;
  onToast: (message: string) => void;
};

export function NetworkPanel({ network, host, connection, onToast }: Props) {
  const [selectedIpv4, setSelectedIpv4] = useState('');
  if (!network) {
    return (
      <section className="network-panel panel network-panel-loading" aria-live="polite">
        <span className="network-icon"><Icon name="wifi" size={20} /></span>
        <div><strong>Localizando a rede…</strong><small>Isso leva só um instante.</small></div>
      </section>
    );
  }

  const selectedAddress = network.addresses.find((address) => address.ipv4 === selectedIpv4)
    ?? network.addresses[0]
    ?? null;
  const selectedUrl = (() => {
    if (!selectedAddress) return network.url;
    const url = new URL(network.url);
    url.hostname = selectedAddress.ipv4;
    return url.origin;
  })();
  const qrParameters = new URLSearchParams();
  if (selectedAddress) qrParameters.set('address', selectedAddress.ipv4);
  const qrQuery = qrParameters.size ? `?${qrParameters}` : '';

  const copy = async () => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(selectedUrl);
      } else {
        const textarea = document.createElement('textarea');
        textarea.value = selectedUrl;
        textarea.setAttribute('readonly', '');
        textarea.className = 'clipboard-fallback';
        document.body.appendChild(textarea);
        textarea.select();
        const copied = document.execCommand('copy');
        textarea.remove();
        if (!copied) throw new Error('Copy command was rejected');
      }
      onToast('Endereço copiado. Abra no navegador do outro dispositivo.');
    } catch {
      onToast(`Não foi possível copiar. Endereço: ${selectedUrl}`);
    }
  };

  return (
    <section className="network-panel panel" aria-labelledby="network-title">
      <div className="network-panel-heading">
        <span className="network-icon"><Icon name="wifi" size={20} /></span>
        <div>
          <p className="eyebrow">CONECTAR DISPOSITIVO</p>
          <h2 id="network-title">Abra no celular</h2>
        </div>
        <span className={`network-state ${connection === 'online' ? 'is-online' : ''}`}>
          <span className="status-dot" /> {connection === 'online' ? 'Disponível' : 'Reconectando'}
        </span>
      </div>

      {host ? (
        <div className="qr-card">
          <div className="qr-frame">
            <img
              src={apiUrl(`/api/v1/network/qr${qrQuery}`)}
              alt={`QR Code para abrir ${selectedUrl} no navegador`}
            />
            <span aria-hidden="true"><Icon name="send" size={14} /></span>
          </div>
          <div className="qr-copy">
            <strong>Escaneie com a câmera</strong>
            <span>O LocalDrop abrirá direto no navegador. Nenhum app é necessário.</span>
          </div>
        </div>
      ) : (
        <div className="web-connected-card">
          <Icon name="shield" size={22} />
          <div><strong>Conexão protegida por pareamento</strong><small>Esta aba conversa somente com o computador local.</small></div>
        </div>
      )}

      <div className="network-address-card">
        <div>
          <span>ENDEREÇO DA REDE</span>
          <strong>{selectedUrl}</strong>
        </div>
        <button type="button" className="icon-button button-secondary" onClick={() => void copy()} aria-label="Copiar endereço">
          <Icon name="copy" size={17} />
        </button>
      </div>

      {network.addresses.length > 1 && (
        <label className="field network-interface-field">
          <span>Interface de rede</span>
          <select
            id="network-interface"
            aria-label="Interface"
            value={selectedAddress?.ipv4 ?? ''}
            onChange={(event) => setSelectedIpv4(event.target.value)}
          >
            {network.addresses.map((address) => (
              <option key={address.ipv4} value={address.ipv4}>
                {address.name} · {address.ipv4}
              </option>
            ))}
          </select>
        </label>
      )}
    </section>
  );
}
