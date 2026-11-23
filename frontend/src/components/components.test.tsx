import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Device, Transfer } from '../api';
import { DevicePicker } from './DevicePicker';
import { FileComposer } from './FileComposer';
import { IncomingOffer } from './IncomingOffer';
import { NetworkPanel } from './NetworkPanel';
import { TransferCard } from './TransferCard';

const devices: Device[] = [
  {
    deviceId: 'phone',
    displayName: 'Galaxy A55',
    deviceType: 'MOBILE',
    host: false,
    online: true,
    lastSeenAt: '2026-08-23T12:00:00Z',
  },
  {
    deviceId: 'notebook',
    displayName: 'Notebook',
    deviceType: 'DESKTOP',
    host: false,
    online: false,
    lastSeenAt: '2026-08-23T12:00:00Z',
  },
];

const transfer: Transfer = {
  id: 'transfer-1',
  senderId: 'host',
  receiverId: 'phone',
  state: 'WAITING_ACCEPTANCE',
  totalBytes: 100,
  receivedBytes: 0,
  chunkSize: 4194304,
  createdAt: '2026-08-23T12:00:00Z',
  updatedAt: '2026-08-23T12:00:00Z',
  acceptedAt: null,
  completedAt: null,
  failureCode: null,
  failureMessage: null,
  files: [{
    id: 'file-1',
    ordinal: 0,
    name: 'foto.jpg',
    size: 100,
    mimeType: 'image/jpeg',
    sha256: 'a'.repeat(64),
    totalChunks: 1,
    receivedChunks: 0,
    receivedBytes: 0,
    state: 'PENDING',
    downloadAvailable: false,
    deliveredName: null,
  }],
};

describe('DevicePicker', () => {
  it('requires an explicit online destination and exposes offline state', () => {
    const select = vi.fn();
    render(<DevicePicker devices={devices} selectedId={null} onSelect={select} />);
    fireEvent.click(screen.getByRole('radio', { name: /Galaxy A55/i }));
    expect(select).toHaveBeenCalledWith('phone');
    expect(screen.getByRole('radio', { name: /Notebook/i })).toBeDisabled();
  });

  it('renders a useful empty state', () => {
    render(<DevicePicker devices={[]} selectedId={null} onSelect={vi.fn()} />);
    expect(screen.getByTestId('offline-state')).toHaveTextContent('Nenhum outro dispositivo');
  });
});

describe('IncomingOffer', () => {
  it('accepts or rejects an incoming offer', () => {
    const accept = vi.fn();
    const reject = vi.fn();
    render(
      <IncomingOffer
        transfer={transfer}
        sender={{ ...devices[1], deviceId: 'host', displayName: 'Emanuel-PC', online: true }}
        busy={false}
        onAccept={accept}
        onReject={reject}
      />,
    );
    expect(screen.getByRole('region')).toHaveTextContent('Emanuel-PC quer enviar um arquivo');
    fireEvent.click(screen.getByRole('button', { name: 'Aceitar transferência' }));
    fireEvent.click(screen.getByRole('button', { name: 'Recusar' }));
    expect(accept).toHaveBeenCalledOnce();
    expect(reject).toHaveBeenCalledOnce();
  });
});

describe('FileComposer', () => {
  it('starts sending as soon as files are picked', () => {
    const send = vi.fn();
    const { container } = render(
      <FileComposer devices={[devices[0]]} selectedDeviceId="phone" busy={false} onSelectedDevice={vi.fn()} onSend={send} />,
    );
    expect(screen.getByText('Galaxy A55')).toBeInTheDocument();
    const file = new File(['oi'], 'nota.txt', { type: 'text/plain' });
    fireEvent.change(container.querySelectorAll('input[type="file"]')[1], { target: { files: [file] } });
    expect(send).toHaveBeenCalledWith([file]);
  });

  it('turns a typed message into a text file', () => {
    const send = vi.fn();
    render(<FileComposer devices={[devices[0]]} selectedDeviceId="phone" busy={false} onSelectedDevice={vi.fn()} onSend={send} />);
    fireEvent.click(screen.getByRole('button', { name: /Texto ou link/ }));
    fireEvent.change(screen.getByLabelText('Texto para enviar'), { target: { value: 'https://exemplo.com' } });
    fireEvent.click(screen.getByRole('button', { name: /Enviar texto/ }));
    const [[files]] = send.mock.calls as [[File[]]];
    expect(files[0].name).toBe('Mensagem LocalDrop.txt');
    expect(files[0].type).toContain('text/plain');
  });
});

describe('TransferCard', () => {
  it('offers native downloads and text preview for finished incoming files', () => {
    const download = vi.fn();
    const previewText = vi.fn();
    render(
      <TransferCard
        transfer={{
          ...transfer,
          state: 'COMPLETED',
          files: [{ ...transfer.files[0], name: 'Mensagem LocalDrop.txt', mimeType: 'text/plain; charset=utf-8', downloadAvailable: true }],
        }}
        self="phone"
        devices={devices}
        hasAttachedFiles={false}
        busy={false}
        onAction={vi.fn()}
        onDownload={download}
        onPreview={previewText}
        onReattach={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: /Ver texto/ }));
    fireEvent.click(screen.getByRole('button', { name: /Baixar/ }));
    expect(previewText).toHaveBeenCalledWith('file-1', 'Mensagem LocalDrop.txt');
    expect(download).toHaveBeenCalledWith('file-1', 'Mensagem LocalDrop.txt');
  });

  it('shows progress derived from real byte counts', () => {
    render(
      <TransferCard
        transfer={{ ...transfer, state: 'TRANSFERRING', receivedBytes: 50 }}
        self="host"
        devices={devices}
        live={{ transferredBytes: 75, totalBytes: 100, speedBytesPerSecond: 25, etaSeconds: 1 }}
        hasAttachedFiles
        busy={false}
        onAction={vi.fn()}
        onDownload={vi.fn()}
        onReattach={vi.fn()}
      />,
    );
    expect(screen.getByText('75%')).toBeInTheDocument();
    expect(screen.getByText(/25 B\/s/)).toBeInTheDocument();
  });

  it('renders a rejection or failure message accessibly', () => {
    render(
      <TransferCard
        transfer={{ ...transfer, state: 'FAILED', failureMessage: 'Checksum inválido.' }}
        self="host"
        devices={devices}
        hasAttachedFiles={false}
        busy={false}
        onAction={vi.fn()}
        onDownload={vi.fn()}
        onReattach={vi.fn()}
      />,
    );
    expect(screen.getByRole('alert')).toHaveTextContent('Checksum inválido.');
  });
});

describe('NetworkPanel', () => {
  it('lets the host select a detected interface and updates the QR target', () => {
    render(
      <NetworkPanel
        network={{
          url: 'http://192.168.1.10:8088',
          addresses: [
            { name: 'Wi-Fi', ipv4: '192.168.1.10', score: 40 },
            { name: 'Ethernet', ipv4: '10.0.0.8', score: 35 },
          ],
        }}
        host
        onToast={vi.fn()}
      />,
    );

    fireEvent.change(screen.getByLabelText('Interface'), { target: { value: '10.0.0.8' } });

    expect(screen.getByText('http://10.0.0.8:8088')).toBeInTheDocument();
    expect(screen.getByAltText('QR Code para abrir http://10.0.0.8:8088 no navegador'))
      .toHaveAttribute('src', '/api/v1/network/qr?address=10.0.0.8');

    expect(screen.queryByRole('button', { name: 'Aplicativo' })).not.toBeInTheDocument();
    expect(screen.getByText(/nenhum app é necessário/i)).toBeInTheDocument();
  });
});
