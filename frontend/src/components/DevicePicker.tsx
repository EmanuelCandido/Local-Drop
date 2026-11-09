import type { Device } from '../api';
import { Icon } from './Icon';

type Props = {
  devices: Device[];
  selectedId: string | null;
  onSelect: (deviceId: string) => void;
};

function isMobile(type: string) {
  return type.toUpperCase().includes('MOBILE') || type.toUpperCase().includes('TABLET');
}

export function DevicePicker({ devices, selectedId, onSelect }: Props) {
  if (!devices.length) {
    return (
      <p className="send-empty" data-testid="offline-state">
        Nenhum outro dispositivo encontrado. Abra o endereço da rede em outro aparelho e conclua o pareamento.
      </p>
    );
  }

  return (
    <div className="device-chips" role="radiogroup" aria-label="Dispositivo de destino">
      {devices.map((device) => (
        <button
          type="button"
          role="radio"
          aria-checked={selectedId === device.deviceId}
          aria-label={`${device.displayName}, ${device.online ? 'online' : 'offline'}`}
          className={`device-chip ${selectedId === device.deviceId ? 'selected' : ''}`}
          key={device.deviceId}
          disabled={!device.online}
          onClick={() => onSelect(device.deviceId)}
        >
          <Icon name={isMobile(device.deviceType) ? 'smartphone' : 'monitor'} size={16} />
          <span>{device.displayName}</span>
          <span className={`status-dot ${device.online ? 'is-online' : ''}`} />
        </button>
      ))}
    </div>
  );
}
