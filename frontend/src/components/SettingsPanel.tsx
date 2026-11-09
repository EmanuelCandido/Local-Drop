import { useEffect, useRef, useState } from 'react';
import type { Device, Settings } from '../api';
import { Icon } from './Icon';

export type ThemePreference = 'system' | 'light' | 'dark';

type Props = {
  settings: Settings;
  trustedDevices: Device[];
  theme: ThemePreference;
  saving: boolean;
  children?: React.ReactNode;
  onTheme: (theme: ThemePreference) => void;
  onSave: (update: { displayName?: string; autoAcceptTrusted?: boolean }) => void;
  onRevoke: (device: Device) => void;
  onForgetSession: () => void;
  onClose: () => void;
};

const themes: Array<{ value: ThemePreference; label: string; icon: 'monitor' | 'sun' | 'moon' }> = [
  { value: 'system', label: 'Automático', icon: 'monitor' },
  { value: 'light', label: 'Claro', icon: 'sun' },
  { value: 'dark', label: 'Escuro', icon: 'moon' },
];

/** Ajustes em uma folha deslizante: cada mudança é salva na hora. */
export function SettingsPanel({
  settings,
  trustedDevices,
  theme,
  saving,
  children,
  onTheme,
  onSave,
  onRevoke,
  onForgetSession,
  onClose,
}: Props) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [name, setName] = useState(settings.device.displayName);

  useEffect(() => setName(settings.device.displayName), [settings.device.displayName]);
  useEffect(() => {
    const element = dialog.current;
    if (element && !element.open) element.showModal?.();
    return () => element?.close?.();
  }, []);

  const saveName = () => {
    const trimmed = name.trim();
    if (trimmed && trimmed !== settings.device.displayName) onSave({ displayName: trimmed });
  };

  return (
    <dialog ref={dialog} className="sheet" aria-labelledby="settings-title" onClose={onClose} onCancel={onClose}>
      <div className="sheet-header">
        <h2 id="settings-title">Ajustes</h2>
        <button type="button" className="icon-button" aria-label="Fechar ajustes" onClick={onClose}><Icon name="x" size={20} /></button>
      </div>

      <label className="field">
        <span>Nome deste aparelho</span>
        <div className="inline-field">
          <input
            value={name}
            maxLength={100}
            onChange={(event) => setName(event.target.value)}
            onKeyDown={(event) => { if (event.key === 'Enter') saveName(); }}
          />
          <button type="button" className="button-secondary" disabled={saving || !name.trim() || name.trim() === settings.device.displayName} onClick={saveName}>
            Salvar
          </button>
        </div>
      </label>

      <div className="field">
        <span>Aparência</span>
        <div className="segmented" role="radiogroup" aria-label="Tema">
          {themes.map((option) => (
            <button
              key={option.value}
              type="button"
              role="radio"
              aria-checked={theme === option.value}
              className={theme === option.value ? 'active' : ''}
              onClick={() => onTheme(option.value)}
            ><Icon name={option.icon} size={16} /> {option.label}</button>
          ))}
        </div>
      </div>

      {settings.device.host && (
        <>
          <label className="toggle-row">
            <span><strong>Aceitar automaticamente</strong><small>Arquivos de aparelhos confiáveis entram sem perguntar.</small></span>
            <input
              type="checkbox"
              role="switch"
              checked={settings.autoAcceptTrusted}
              disabled={saving}
              onChange={(event) => onSave({ autoAcceptTrusted: event.target.checked })}
            />
          </label>
          <div className="field">
            <span>Aparelhos confiáveis</span>
            {!trustedDevices.length ? (
              <p className="muted">Nenhum outro aparelho pareado ainda.</p>
            ) : (
              <ul className="trusted-list">
                {trustedDevices.map((device) => (
                  <li key={device.deviceId}>
                    <span className={`status-dot ${device.online ? 'is-online' : ''}`} />
                    <span>{device.displayName}</span>
                    <button type="button" className="button-ghost danger button-small" aria-label={`Remover ${device.displayName}`} onClick={() => onRevoke(device)}>
                      Remover
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </>
      )}

      {children}

      <div className="sheet-footer">
        <button type="button" className="button-ghost danger" onClick={onForgetSession}>
          <Icon name="trash" size={16} /> Desconectar este navegador
        </button>
      </div>
    </dialog>
  );
}
