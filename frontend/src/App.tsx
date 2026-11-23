import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Api,
  ApiError,
  claimHost,
  defaultDeviceName,
  detectDeviceType,
  getNetwork,
  loadSession,
  pollPairing,
  requestPairing,
  saveSession,
  type Device,
  type NetworkInfo,
  type PairingPending,
  type PairingRequestState,
  type Session,
  type Settings,
  type Transfer,
} from './api';
import { copyText, loadDownloaded, saveDownloaded, startDownload } from './browser';
import { FileComposer } from './components/FileComposer';
import { Icon } from './components/Icon';
import { IncomingOffer } from './components/IncomingOffer';
import { NetworkPanel } from './components/NetworkPanel';
import { Onboarding } from './components/Onboarding';
import { PairingRequests } from './components/PairingRequests';
import { SettingsPanel, type ThemePreference } from './components/SettingsPanel';
import { TextPreview } from './components/TextPreview';
import { TransferCard } from './components/TransferCard';
import { attachFilesToTransfer, metadataForFiles, uploadTransfer, type LiveProgress } from './transfer';
import { useEventSocket, type ServerEvent } from './useEventSocket';

type Toast = { message: string; error?: boolean };
type Action = 'accept' | 'reject' | 'cancel' | 'pause' | 'resume';

const PAIRING_STORAGE_KEY = 'localdrop.pendingPairing';

function loadPendingPairing() {
  try {
    return JSON.parse(sessionStorage.getItem(PAIRING_STORAGE_KEY) ?? 'null') as PairingRequestState | null;
  } catch {
    return null;
  }
}

function upsert(list: Transfer[], transfer: Transfer) {
  const index = list.findIndex((item) => item.id === transfer.id);
  if (index < 0) return [transfer, ...list];
  const next = list.slice();
  next[index] = transfer;
  return next;
}

export function App() {
  const [session, setSession] = useState<Session | null>(loadSession);
  const [booting, setBooting] = useState(true);
  const [network, setNetwork] = useState<NetworkInfo | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
  const [transfers, setTransfers] = useState<Transfer[]>([]);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [pendingPairings, setPendingPairings] = useState<PairingPending[]>([]);
  const [pairing, setPairing] = useState<PairingRequestState | null>(loadPendingPairing);
  const [name, setName] = useState(() => localStorage.getItem('localdrop.name') ?? defaultDeviceName());
  const [selectedDeviceId, setSelectedDeviceId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [toast, setToast] = useState<Toast | null>(null);
  const [theme, setTheme] = useState<ThemePreference>(
    () => (localStorage.getItem('localdrop.theme') as ThemePreference | null) ?? 'system',
  );
  const [liveProgress, setLiveProgress] = useState<Record<string, LiveProgress>>({});
  const [attachmentRevision, setAttachmentRevision] = useState(0);
  const [attachedTransferIds, setAttachedTransferIds] = useState<Set<string>>(() => new Set());
  const [downloaded, setDownloaded] = useState<Set<string>>(loadDownloaded);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [preview, setPreview] = useState<{ title: string; text: string } | null>(null);
  const api = useMemo(() => (session ? new Api(session) : null), [session]);
  const attachedFiles = useRef(new Map<string, Map<string, File>>());
  const uploading = useRef(new Set<string>());
  const refreshTimer = useRef<number | undefined>(undefined);

  const showToast = useCallback((message: string, error = false) => setToast({ message, error }), []);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(null), toast.error ? 7_000 : 4_000);
    return () => window.clearTimeout(timer);
  }, [toast]);

  useEffect(() => {
    void getNetwork().then(setNetwork).catch(() => undefined);
  }, []);

  useEffect(() => localStorage.setItem('localdrop.name', name), [name]);

  useEffect(() => {
    localStorage.setItem('localdrop.theme', theme);
    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const apply = () => {
      document.documentElement.dataset.theme = theme === 'system' ? (media.matches ? 'dark' : 'light') : theme;
    };
    apply();
    media.addEventListener('change', apply);
    return () => media.removeEventListener('change', apply);
  }, [theme]);

  const handleApiFailure = useCallback((error: unknown, fallback: string) => {
    if (error instanceof ApiError && error.status === 401) {
      saveSession(null);
      setSession(null);
      showToast('Este aparelho foi desconectado. Faça o pareamento novamente.', true);
      return;
    }
    showToast(error instanceof Error ? error.message : fallback, true);
  }, [showToast]);

  const refresh = useCallback(async () => {
    if (!api || !session) return;
    try {
      const [nextDevices, nextTransfers, nextSettings] = await Promise.all([
        api.devices(),
        api.transfers(),
        api.settings(),
      ]);
      setDevices(nextDevices);
      setTransfers(nextTransfers);
      setSettings(nextSettings);
      if (session.host) setPendingPairings(await api.pendingPairings());
    } catch (error) {
      handleApiFailure(error, 'Falha ao sincronizar o LocalDrop.');
    }
  }, [api, session, handleApiFailure]);

  // Rajadas de eventos viram uma única sincronização.
  const scheduleRefresh = useCallback(() => {
    window.clearTimeout(refreshTimer.current);
    refreshTimer.current = window.setTimeout(() => void refresh(), 250);
  }, [refresh]);

  useEffect(() => {
    if (!api || !session) {
      setBooting(false);
      return;
    }
    let cancelled = false;
    setBooting(true);
    api.me().then((device) => {
      if (cancelled) return;
      const validated: Session = { ...session, deviceId: device.deviceId, displayName: device.displayName, deviceType: device.deviceType, host: device.host };
      if (JSON.stringify(validated) !== JSON.stringify(session)) {
        saveSession(validated);
        setSession(validated);
      }
      setName(device.displayName);
      setBooting(false);
    }).catch((error) => {
      if (cancelled) return;
      setBooting(false);
      handleApiFailure(error, 'O computador não respondeu.');
    });
    return () => { cancelled = true; };
  }, [api, session, handleApiFailure]);

  const connection = useEventSocket(session, useCallback((event: ServerEvent) => {
    const transfer = event.payload?.transfer as Transfer | undefined;
    if (transfer?.id) {
      // O evento já traz a transferência completa: nada de recarregar tudo.
      setTransfers((current) => upsert(current, transfer));
      return;
    }
    scheduleRefresh();
  }, [scheduleRefresh]));

  // Com o WebSocket ativo, a consulta periódica é só uma rede de segurança.
  useEffect(() => {
    if (!session || booting) return;
    void refresh();
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') void refresh();
    }, connection === 'online' ? 60_000 : 10_000);
    return () => window.clearInterval(timer);
  }, [session, booting, refresh, connection]);

  useEffect(() => {
    if (!pairing) {
      sessionStorage.removeItem(PAIRING_STORAGE_KEY);
      return;
    }
    sessionStorage.setItem(PAIRING_STORAGE_KEY, JSON.stringify(pairing));
    let cancelled = false;
    let timer: number;
    const check = async () => {
      try {
        const result = await pollPairing(pairing.requestId, pairing.pollToken);
        if (cancelled) return;
        if ((result.status === 'COMPLETED' || result.status === 'APPROVED') && result.deviceToken) {
          const nextSession: Session = {
            deviceId: pairing.deviceId,
            token: result.deviceToken,
            displayName: pairing.displayName,
            deviceType: detectDeviceType(),
            host: false,
          };
          saveSession(nextSession);
          setSession(nextSession);
          setPairing(null);
          showToast('Pronto! Este aparelho já pode trocar arquivos com o PC.');
          return;
        }
        if (['REJECTED', 'EXPIRED'].includes(result.status)) {
          setPairing(null);
          showToast(result.status === 'REJECTED' ? 'O pareamento foi recusado no computador.' : 'O código expirou. Tente de novo.', true);
          return;
        }
        timer = window.setTimeout(check, 1_500);
      } catch (error) {
        if (!cancelled) {
          handleApiFailure(error, 'Falha ao consultar o pareamento.');
          timer = window.setTimeout(check, 3_000);
        }
      }
    };
    void check();
    return () => {
      cancelled = true;
      if (timer) window.clearTimeout(timer);
    };
  }, [pairing, handleApiFailure, showToast]);

  useEffect(() => {
    if (!api || !session) return;
    for (const transfer of transfers) {
      if (transfer.senderId !== session.deviceId) continue;
      if (!['ACCEPTED', 'TRANSFERRING'].includes(transfer.state)) continue;
      const files = attachedFiles.current.get(transfer.id);
      if (!files || uploading.current.has(transfer.id)) continue;
      uploading.current.add(transfer.id);
      uploadTransfer(api, transfer, files, (progress) => {
        setLiveProgress((current) => ({ ...current, [transfer.id]: progress }));
      }).catch((error) => {
        handleApiFailure(error, 'A transferência foi interrompida.');
      }).finally(() => {
        uploading.current.delete(transfer.id);
      });
    }
  }, [api, session, transfers, attachmentRevision, handleApiFailure]);

  useEffect(() => {
    if (!transfers.some((transfer) => uploading.current.has(transfer.id))) return;
    const warn = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [transfers, liveProgress]);

  const beginHost = async () => {
    setBusy(true);
    try {
      const next = await claimHost(name.trim());
      saveSession(next);
      setSession(next);
    } catch (error) {
      handleApiFailure(error, 'Não foi possível iniciar o host.');
    } finally {
      setBusy(false);
    }
  };

  const beginPairing = async () => {
    setBusy(true);
    try {
      setPairing(await requestPairing(name.trim()));
    } catch (error) {
      handleApiFailure(error, 'Não foi possível solicitar o pareamento.');
    } finally {
      setBusy(false);
    }
  };

  const peers = session ? devices.filter((device) => device.deviceId !== session.deviceId) : [];
  const destinationId = (selectedDeviceId && peers.some((device) => device.deviceId === selectedDeviceId))
    ? selectedDeviceId
    : (peers.find((device) => device.host && device.online) ?? peers.find((device) => device.online) ?? peers[0])?.deviceId ?? null;

  const sendFiles = async (files: File[]) => {
    if (!api || !destinationId) return;
    const destination = peers.find((device) => device.deviceId === destinationId);
    setBusy(true);
    try {
      const transfer = await api.create(destinationId, metadataForFiles(files));
      attachedFiles.current.set(transfer.id, attachFilesToTransfer(transfer, files));
      setAttachedTransferIds((current) => new Set(current).add(transfer.id));
      setAttachmentRevision((value) => value + 1);
      setTransfers((current) => upsert(current, transfer));
      if (transfer.state === 'WAITING_ACCEPTANCE') {
        showToast(`Aguardando ${destination?.displayName ?? 'o destino'} aceitar.`);
      }
    } catch (error) {
      handleApiFailure(error, 'Não foi possível iniciar o envio.');
    } finally {
      setBusy(false);
    }
  };

  const download = async (transfer: Transfer, fileId: string, fileName: string) => {
    if (!api) return;
    try {
      startDownload(await api.downloadLink(transfer.id, fileId), fileName);
      setDownloaded((current) => {
        const next = new Set(current).add(fileId);
        saveDownloaded(next);
        return next;
      });
    } catch (error) {
      handleApiFailure(error, 'Não foi possível baixar o arquivo.');
    }
  };

  const openPreview = async (transfer: Transfer, fileId: string, fileName: string) => {
    if (!api) return;
    try {
      const text = await (await api.download(transfer.id, fileId)).text();
      setPreview({ title: fileName.replace(/\.txt$/i, ''), text });
    } catch (error) {
      handleApiFailure(error, 'Não foi possível abrir o texto.');
    }
  };

  const runAction = async (transfer: Transfer, action: Action) => {
    if (!api) return;
    if (action === 'cancel' && !window.confirm('Cancelar esta transferência?')) return;
    setBusyId(transfer.id);
    try {
      const changed = await api.action(transfer.id, action);
      setTransfers((current) => upsert(current, changed));
      // Arquivos do PC ficam prontos no aceite: um único arquivo já começa a baixar.
      if (action === 'accept' && changed.state === 'COMPLETED' && changed.files.length === 1 && changed.files[0].downloadAvailable) {
        await download(changed, changed.files[0].id, changed.files[0].deliveredName ?? changed.files[0].name);
      }
    } catch (error) {
      handleApiFailure(error, 'A ação não pôde ser concluída.');
    } finally {
      setBusyId(null);
    }
  };

  const reattach = async (transfer: Transfer, files: File[]) => {
    setBusyId(transfer.id);
    try {
      attachedFiles.current.set(transfer.id, attachFilesToTransfer(transfer, files));
      setAttachedTransferIds((current) => new Set(current).add(transfer.id));
      setAttachmentRevision((value) => value + 1);
      if (transfer.state === 'PAUSED' && api) {
        setTransfers((current) => upsert(current, transfer));
        const resumed = await api.action(transfer.id, 'resume');
        setTransfers((current) => upsert(current, resumed));
      }
      showToast('Arquivos reanexados. Continuando o envio…');
    } catch (error) {
      handleApiFailure(error, 'Os arquivos escolhidos não correspondem ao envio.');
    } finally {
      setBusyId(null);
    }
  };

  const decidePairing = async (request: PairingPending, approve: boolean) => {
    if (!api) return;
    setBusyId(request.requestId);
    try {
      if (approve) await api.approvePairing(request.requestId, request.code);
      else await api.rejectPairing(request.requestId);
      await refresh();
    } catch (error) {
      handleApiFailure(error, 'Não foi possível responder ao pareamento.');
    } finally {
      setBusyId(null);
    }
  };

  const saveSettings = async (update: { displayName?: string; autoAcceptTrusted?: boolean }) => {
    if (!api || !session) return;
    setBusy(true);
    try {
      const changed = await api.updateSettings(update);
      setSettings(changed);
      if (update.displayName) {
        const nextSession = { ...session, displayName: changed.device.displayName };
        saveSession(nextSession);
        setSession(nextSession);
      }
      showToast('Ajuste salvo.');
    } catch (error) {
      handleApiFailure(error, 'Não foi possível salvar.');
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (device: Device) => {
    if (!api || !window.confirm(`Remover ${device.displayName} dos aparelhos confiáveis?`)) return;
    try {
      await api.revoke(device.deviceId);
      await refresh();
    } catch (error) {
      handleApiFailure(error, 'Não foi possível remover o aparelho.');
    }
  };

  const forgetSession = () => {
    if (!window.confirm('Desconectar este navegador? Será preciso parear de novo.')) return;
    saveSession(null);
    setSession(null);
    setDevices([]);
    setTransfers([]);
    setSettings(null);
    setSettingsOpen(false);
  };

  if (booting) {
    return (
      <main className="loading-screen">
        <span className="brand-symbol"><Icon name="send" size={22} /></span>
        <p>Conectando ao computador…</p>
      </main>
    );
  }

  if (!session) {
    return (
      <Onboarding
        name={name}
        network={network}
        pairing={pairing}
        busy={busy}
        canClaimHost={['localhost', '127.0.0.1', '::1'].includes(location.hostname)}
        error={toast?.error ? toast.message : ''}
        onName={setName}
        onHost={() => void beginHost()}
        onPair={() => void beginPairing()}
        onCancelPairing={() => setPairing(null)}
      />
    );
  }

  const offers = transfers.filter((transfer) => transfer.receiverId === session.deviceId && transfer.state === 'WAITING_ACCEPTANCE');
  const activity = transfers.filter((transfer) => !offers.includes(transfer));
  const host = peers.find((device) => device.host);
  const online = connection === 'online';

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand-row">
          <span className="brand-symbol" aria-hidden="true"><Icon name="send" size={17} /></span>
          <div>
            <strong>LocalDrop</strong>
            <small className={online ? 'is-online' : ''}>
              <span className={`status-dot ${online ? 'is-online' : 'is-waiting'}`} />
              {online ? `Conectado${host && !session.host ? ` a ${host.displayName}` : ''}` : 'Reconectando…'}
            </small>
          </div>
        </div>
        <button type="button" className="icon-button" aria-label="Abrir ajustes" onClick={() => setSettingsOpen(true)}>
          <Icon name="settings" size={21} />
        </button>
      </header>

      <main className="content">
        {session.host && (
          <PairingRequests
            requests={pendingPairings}
            busyId={busyId}
            onApprove={(request) => void decidePairing(request, true)}
            onReject={(request) => void decidePairing(request, false)}
          />
        )}

        {offers.map((offer) => (
          <IncomingOffer
            key={offer.id}
            transfer={offer}
            sender={devices.find((device) => device.deviceId === offer.senderId)}
            busy={busyId === offer.id}
            onAccept={() => void runAction(offer, 'accept')}
            onReject={() => void runAction(offer, 'reject')}
          />
        ))}

        <FileComposer
          devices={peers}
          selectedDeviceId={destinationId}
          busy={busy}
          onSelectedDevice={setSelectedDeviceId}
          onSend={(files) => void sendFiles(files)}
        />

        <section className="activity" aria-labelledby="activity-title">
          <h2 id="activity-title">Atividade</h2>
          {!activity.length ? (
            <p className="empty-note">O que você enviar ou receber aparece aqui.</p>
          ) : (
            <div className="transfer-list">
              {activity.map((transfer) => (
                <TransferCard
                  key={transfer.id}
                  transfer={transfer}
                  self={session.deviceId}
                  devices={devices}
                  live={liveProgress[transfer.id]}
                  hasAttachedFiles={attachedTransferIds.has(transfer.id)}
                  downloaded={downloaded}
                  busy={busyId === transfer.id}
                  onAction={(action) => void runAction(transfer, action)}
                  onDownload={(fileId, fileName) => void download(transfer, fileId, fileName)}
                  onPreview={(fileId, fileName) => void openPreview(transfer, fileId, fileName)}
                  onReattach={(files) => void reattach(transfer, files)}
                />
              ))}
            </div>
          )}
        </section>
      </main>

      {settingsOpen && settings && (
        <SettingsPanel
          settings={settings}
          trustedDevices={peers}
          theme={theme}
          saving={busy}
          onTheme={setTheme}
          onSave={(update) => void saveSettings(update)}
          onRevoke={(device) => void revoke(device)}
          onForgetSession={forgetSession}
          onClose={() => setSettingsOpen(false)}
        >
          {session.host && <NetworkPanel network={network} host onToast={showToast} />}
        </SettingsPanel>
      )}

      {preview && (
        <TextPreview
          title={preview.title}
          text={preview.text}
          onCopy={() => {
            void copyText(preview.text)
              .then(() => showToast('Texto copiado.'))
              .catch(() => showToast('Não foi possível copiar. Toque e segure o texto para copiar.', true));
          }}
          onClose={() => setPreview(null)}
        />
      )}

      {toast && (
        <div className={`toast ${toast.error ? 'error' : ''}`} role={toast.error ? 'alert' : 'status'}>
          <Icon name={toast.error ? 'alert' : 'check'} size={18} />
          <span>{toast.message}</span>
          <button type="button" className="icon-button" aria-label="Fechar aviso" onClick={() => setToast(null)}><Icon name="x" size={16} /></button>
        </div>
      )}
    </div>
  );
}
