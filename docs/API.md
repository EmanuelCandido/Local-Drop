# API HTTP e WebSocket

Base HTTP padrão: `http://<host>:8088/api/v1`

WebSocket: `ws://<host>:8088/ws/events`

A API sustenta o desktop nativo em Go e a interface web móvel do LocalDrop e não promete compatibilidade entre versões pré-1.0.

## Autenticação

Endpoints privados exigem os dois headers:

```http
Authorization: Device <deviceToken>
X-Device-Id: 6f7e94b8-0000-4000-8000-000000000000
```

Papéis usados abaixo:

- **Público:** sem token;
- **Dispositivo:** qualquer identidade confiável ativa;
- **Host:** identidade local do computador servidor;
- **Remetente/Receptor/Participante:** além de autenticado, deve ocupar esse papel na transferência.

## Erros

Erros HTTP retornam JSON:

```json
{
  "timestamp": "2026-08-23T20:00:00Z",
  "status": 409,
  "code": "TRANSFER_INVALID_STATE",
  "message": "Transfer cannot transition to the requested state.",
  "path": "/api/v1/transfers/3c0.../accept"
}
```

Status principais:

- `400`: JSON/header obrigatório ausente ou inválido;
- `401`: credenciais ausentes/inválidas;
- `403`: papel ou participação insuficiente;
- `404`: recurso inexistente ou conteúdo temporário expirado;
- `409`: conflito de identidade/estado/chunk;
- `422`: metadados, tamanho, hash, cursor ou diretório inválido;
- `429`: rate limit, com `Retry-After: 60`;
- `500`: falha inesperada de armazenamento/montagem.

## Rede e saúde

### `GET /api/v1/health` — Público

```json
{
  "status": "READY",
  "version": "0.3.0",
  "runtime": "go",
  "checkedAt": "2026-08-23T20:00:00Z",
  "port": 8088,
  "url": "http://192.168.0.15:8088",
  "addresses": [
    { "name": "Wi-Fi", "ipv4": "192.168.0.15", "score": 40 }
  ],
  "downloadDirectoryWritable": true
}
```

### `GET /api/v1/network` — Público

Retorna URL preferida e todas as interfaces IPv4 LAN candidatas, ordenadas por score.

### `GET /api/v1/network/qr?address=<ipv4>` — Público

Retorna `image/png` com a URL preferida codificada localmente. `address` é opcional e precisa corresponder exatamente a uma das interfaces ativas retornadas por `/network`; valores arbitrários recebem 400.

## Identidade e pareamento

### `POST /api/v1/host/claim` — Público, somente loopback

```json
{
  "deviceId": "6f7e94b8-0000-4000-8000-000000000000",
  "displayName": "Emanuel-PC",
  "deviceType": "DESKTOP"
}
```

Retorna `201` na criação e `200` na rotação do token do host:

```json
{
  "deviceId": "6f7e94b8-0000-4000-8000-000000000000",
  "deviceToken": "<segredo-base64url>"
}
```

Chamadas que não venham de loopback recebem `403 HOST_CLAIM_REMOTE_DENIED`.

### `POST /api/v1/pairing/requests` — Público

Usa o mesmo body de device do claim. Retorna `201`:

```json
{
  "requestId": "...",
  "code": "847291",
  "pollToken": "<segredo-base64url>",
  "status": "PENDING",
  "expiresAt": "2026-08-23T20:05:00Z"
}
```

### `GET /api/v1/pairing/requests/{requestId}/status` — Público com segredo

Header obrigatório:

```http
X-Pairing-Poll-Token: <pollToken>
```

```json
{
  "requestId": "...",
  "status": "COMPLETED",
  "deviceToken": "<segredo-base64url-ou-null>"
}
```

O `deviceToken` é entregue uma única vez. Estados possíveis: `PENDING`, `APPROVED`, `COMPLETED`, `REJECTED`, `EXPIRED`.

### Administração de pareamento — Host

| Método | Rota | Body/resultado |
|---|---|---|
| `GET` | `/api/v1/pairing/requests` | solicitações pendentes, incluindo o código a conferir |
| `POST` | `/api/v1/pairing/requests/{id}/approve` | `{ "code": "847291" }`; `204` |
| `POST` | `/api/v1/pairing/requests/{id}/reject` | sem body; `204` |

### Dispositivos

| Método | Rota | Papel | Resultado |
|---|---|---|---|
| `GET` | `/api/v1/device/me` | Dispositivo | perfil autenticado |
| `PATCH` | `/api/v1/device/me` | Dispositivo | atualiza `{displayName,deviceType}` |
| `GET` | `/api/v1/devices` | Dispositivo | confiáveis com presença online/offline |
| `GET` | `/api/v1/trusted-devices` | Host | mesma visão, para administração |
| `DELETE` | `/api/v1/trusted-devices/{deviceId}` | Host | revoga e desconecta; `204` |

Formato de device:

```json
{
  "deviceId": "...",
  "displayName": "Galaxy A55",
  "deviceType": "MOBILE",
  "host": false,
  "online": true,
  "lastSeenAt": "2026-08-23T20:00:00Z"
}
```

## Configurações

### `GET /api/v1/settings` — Dispositivo

```json
{
  "device": { "deviceId": "...", "displayName": "Emanuel-PC", "deviceType": "DESKTOP", "host": true, "online": true, "lastSeenAt": "..." },
  "downloadDirectory": "C:\\Users\\Emanuel\\Downloads\\LocalDrop",
  "autoAcceptTrusted": false,
  "port": 8088,
  "updatedAt": "2026-08-23T20:00:00Z"
}
```

Para um peer, `downloadDirectory` é `null` e `autoAcceptTrusted` é `false`.

### `PATCH /api/v1/settings` — Dispositivo/Host

Todos os campos são opcionais:

```json
{
  "displayName": "Emanuel-PC",
  "deviceType": "DESKTOP",
  "downloadDirectory": "D:\\Recebidos\\LocalDrop",
  "autoAcceptTrusted": false
}
```

Qualquer dispositivo altera o próprio nome/tipo. Somente o host altera pasta e aceite automático. O diretório não pode mudar enquanto o host recebe uma transferência ativa. A porta é somente informativa neste endpoint e muda na próxima inicialização por configuração do processo.

## Transferências

### Criar oferta

`POST /api/v1/transfers` — Dispositivo/remetente

```json
{
  "receiverId": "device-do-destino",
  "files": [
    {
      "name": "video.mp4",
      "size": 293601280,
      "mimeType": "video/mp4",
      "sha256": ""
    }
  ]
}
```

`sha256` é opcional: envie `""` para começar sem ler o arquivo inteiro antes (cada chunk já leva o próprio hash). Se informado, o servidor confere o arquivo completo na montagem.

Retorna `201` e uma visão da transferência. A oferta começa em `WAITING_ACCEPTANCE`; nenhum chunk é aceito antes do aceite. Ofertas feitas pela janela do computador apontam para os arquivos originais e ficam `COMPLETED` no momento do aceite — o destinatário baixa direto do original, sem cópia.

### Visão de transferência

```json
{
  "id": "...",
  "senderId": "...",
  "receiverId": "...",
  "state": "TRANSFERRING",
  "totalBytes": 293601280,
  "receivedBytes": 8388608,
  "chunkSize": 4194304,
  "createdAt": "...",
  "updatedAt": "...",
  "acceptedAt": "...",
  "completedAt": null,
  "failureCode": null,
  "failureMessage": null,
  "files": [
    {
      "id": "...",
      "ordinal": 0,
      "name": "video.mp4",
      "size": 293601280,
      "mimeType": "video/mp4",
      "sha256": "...",
      "totalChunks": 70,
      "receivedChunks": 2,
      "receivedBytes": 8388608,
      "state": "TRANSFERRING",
      "downloadAvailable": false,
      "deliveredName": null
    }
  ]
}
```

Estados: `CREATED`, `WAITING_ACCEPTANCE`, `ACCEPTED`, `TRANSFERRING`, `PAUSED`, `VERIFYING`, `COMPLETED`, `REJECTED`, `CANCELLED`, `FAILED`.

### Consultas e ações

| Método | Rota | Papel | Comportamento |
|---|---|---|---|
| `GET` | `/api/v1/transfers` | Dispositivo | até 100 transferências recentes do participante |
| `GET` | `/api/v1/transfers/{id}` | Participante | detalhe |
| `POST` | `/api/v1/transfers/{id}/accept` | Receptor | reserva espaço e aceita |
| `POST` | `/api/v1/transfers/{id}/reject` | Receptor | rejeita e libera temporários |
| `POST` | `/api/v1/transfers/{id}/cancel` | Participante | cancela e libera temporários |
| `POST` | `/api/v1/transfers/{id}/pause` | Participante | pausa upload ativo |
| `POST` | `/api/v1/transfers/{id}/resume` | Remetente | retoma ou agenda verificação |

### Consultar chunks ausentes

`GET /api/v1/transfers/{transferId}/files/{fileId}/missing?cursor=0&limit=1000` — Remetente

```json
{
  "totalChunks": 70,
  "receivedChunks": 2,
  "ranges": [
    { "startInclusive": 2, "endExclusive": 70 }
  ],
  "nextCursor": null
}
```

`limit` aceita 1–1000 intervalos por página. Se `nextCursor` não for nulo, repita a chamada a partir dele.

### Enviar chunk

`PUT /api/v1/transfers/{transferId}/files/{fileId}/chunks/{chunkIndex}` — Remetente

```http
Content-Type: application/octet-stream
Content-Length: 4194304
X-Chunk-SHA256: <64 caracteres hex>
```

O corpo é o binário puro. Todos os chunks, exceto o último, têm exatamente `chunkSize` bytes (informado na visão da transferência). Cada chunk é gravado direto na sua posição do arquivo final, então podem chegar em qualquer ordem e em paralelo. Resposta:

```json
{
  "duplicate": false,
  "receivedBytes": 8388608,
  "totalBytes": 293601280,
  "state": "TRANSFERRING"
}
```

Reenviar o mesmo índice/corpo/hash retorna `duplicate: true`. Um recibo divergente retorna conflito; tamanho ou hash inválido retorna 422.

### Baixar arquivo concluído

`POST /api/v1/transfers/{transferId}/files/{fileId}/download-link` — Receptor

Retorna `{ "url": "/api/v1/downloads/<ticket>/<nome>", "expiresAt": "..." }`. O link vale 15 minutos, não exige cabeçalhos e aceita `Range`, então o navegador do celular baixa com o gerenciador nativo, em streaming e com retomada.

`GET /api/v1/transfers/{transferId}/files/{fileId}/download` — Receptor, autenticado por cabeçalho

Ambos retornam o tipo do arquivo, `Content-Length` e `Content-Disposition: attachment` com nome UTF-8. Se o original de uma oferta do computador mudou de tamanho, a resposta é `409 TRANSFER_SOURCE_CHANGED`. O receptor host normalmente já possui o arquivo na pasta configurada; o endpoint também continua sujeito à participação e disponibilidade do caminho final.

## Histórico

`GET /api/v1/history?before=<Instant>&limit=50` — Dispositivo

`limit` deve estar entre 1 e 100. `before` é um cursor ISO-8601 exclusivo para páginas anteriores.

```json
[
  {
    "id": "...",
    "direction": "INCOMING",
    "peerId": "...",
    "peerName": "Galaxy A55",
    "state": "COMPLETED",
    "totalBytes": 4328382,
    "createdAt": "...",
    "completedAt": "...",
    "failureCode": null,
    "files": [
      { "name": "foto.jpg", "size": 4328382, "state": "COMPLETED" }
    ]
  }
]
```

O histórico é metadado; `COMPLETED` não garante que um recebimento web temporário ainda exista no servidor.

## WebSocket (compatibilidade legada)

O desktop nativo usa atualização periódica autenticada. A interface web móvel usa o WebSocket para presença e eventos em tempo real; a API REST continua sendo a fonte de verdade para snapshots e ações.

O navegador móvel deve usar a mesma autoridade HTTP. O handshake valida `Origin` contra `Host`; o cliente nativo e ferramentas de diagnóstico podem omitir `Origin`.

O primeiro frame, dentro de dez segundos, deve ser:

```json
{
  "type": "AUTHENTICATE",
  "deviceId": "...",
  "token": "<deviceToken>"
}
```

Resposta:

```json
{
  "type": "AUTHENTICATED",
  "occurredAt": "2026-08-23T20:00:00Z",
  "payload": { "deviceId": "..." }
}
```

O cliente envia a cada 15 segundos:

```json
{ "type": "HEARTBEAT" }
```

e recebe `HEARTBEAT_ACK`. A sessão expira após 45 segundos sem heartbeat no padrão.

Eventos enviados pelo servidor:

- `DEVICE_CONNECTED`, `DEVICE_DISCONNECTED`, `DEVICE_UPDATED`;
- `PAIRING_REQUESTED`, `PAIRING_ACCEPTED`, `PAIRING_REJECTED`;
- `TRANSFER_OFFERED`, `TRANSFER_ACCEPTED`, `TRANSFER_REJECTED`;
- `TRANSFER_CANCELLED`, `TRANSFER_PAUSED`, `TRANSFER_RESUMED`;
- `TRANSFER_PROGRESS`, `TRANSFER_COMPLETED`, `TRANSFER_FAILED`.

Envelope geral:

```json
{
  "type": "TRANSFER_PROGRESS",
  "occurredAt": "2026-08-23T20:00:00Z",
  "payload": {
    "transfer": { "id": "...", "state": "TRANSFERRING" },
    "fileId": "...",
    "chunkIndex": 12
  }
}
```

Eventos servem como sinal para atualizar a UI; o cliente pode refazer `GET /transfers` para reconciliar o estado durável.
