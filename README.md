# LocalDrop

LocalDrop transfere arquivos entre dispositivos da mesma rede, sem conta e sem nuvem. No Windows, o computador executa um servidor e uma interface desktop inteiramente nativos em Go, sem navegador, WebView ou motor web. No celular, a experiência é uma interface React responsiva aberta no navegador, sem APK ou instalação obrigatória.

> O transporte padrão é HTTP local. Pareamento e tokens controlam o acesso, mas não cifram uma rede comprometida. Use o LocalDrop somente em uma LAN privada e confiável. Veja [Segurança](docs/SECURITY.md).

## Experiência

**No computador**

- janela nativa em Go, com uma tela só: escolha o celular, arraste arquivos (ou pastas) para a janela e eles já ficam disponíveis no celular;
- `Ctrl+V` envia o texto copiado; `Ctrl+O` abre o seletor de arquivos;
- pedidos de pareamento e arquivos chegando aparecem em destaque no topo, com **Confiar**/**Aceitar** ali mesmo;
- cada transferência tem atalhos: **Abrir**, **Mostrar na pasta**, **Copiar texto**, **Pausar** e **Cancelar**;
- fechar a janela mantém o LocalDrop na bandeja, pronto para receber, sem consultar nem redesenhar nada; avisos do Windows informam o que chegou;
- arquivos enviados ao celular não são copiados: o celular baixa direto do original.

**No celular** (navegador, sem APK)

- três botões: **Fotos e vídeos**, **Arquivos** e **Texto ou link** — escolher já inicia o envio;
- ofertas do PC aparecem no topo com **Aceitar e baixar**; o download vai para o gerenciador nativo do navegador, em streaming;
- textos recebidos abrem com **Ver texto** e **Copiar**;
- tema claro, escuro ou automático.

**Transferência**

- blocos de 4 MiB com SHA-256 por bloco, gravados direto na posição final do arquivo, quatro em paralelo;
- o arquivo é lido uma única vez no envio (sem pré-cálculo do hash completo);
- retomada sem reenviar blocos confirmados;
- nenhum serviço externo, CDN, telemetria ou analytics.

## Usar no Windows e no celular

Abra:

```text
dist\LocalDrop.exe
```

O executável inicia o serviço local e abre a janela nativa escrita em Go. Nenhum navegador é iniciado no desktop. O bundle web móvel já está incorporado ao binário; Go e Node.js não são necessários em tempo de execução.

Na primeira execução:

1. No computador, confirme o nome do host.
2. Escaneie o QR Code que aparece na própria tela inicial (ou use **Conectar celular**).
3. No celular, abra o link e toque em **Parear com este computador**.
4. Confira o mesmo código nas duas telas e toque em **Confiar** no computador.
5. Arraste arquivos para a janela do PC, ou toque em um dos botões de envio no celular.

O celular não usa APK. Se quiser um atalho, o navegador pode oferecer **Adicionar à tela inicial**, mas o produto continua sendo web.

Por padrão, arquivos recebidos pelo host ficam em:

- Windows: `%USERPROFILE%\Downloads\LocalDrop`
- Linux/macOS: `~/Downloads/LocalDrop`

## Construir

Requisitos de desenvolvimento:

- Go 1.27+ no `PATH`, ou a toolchain portátil em `.tools\go`;
- Node.js `^20.19.0` ou `>=22.12.0` e npm.

Windows:

```powershell
.\scripts\build.ps1
```

Linux/macOS:

```bash
./scripts/build.sh
```

O build compila o React, copia o bundle para `go-backend/web`, executa testes e análise estática do Go e produz um único executável em `dist`.

Para reaproveitar um `frontend/dist` já criado:

```powershell
.\scripts\build-go.ps1 -SkipFrontend
```

## Desenvolvimento

Windows:

```powershell
.\scripts\dev.ps1
```

Linux/macOS:

```bash
./scripts/dev.sh
```

O Vite fica em `http://localhost:5173` e encaminha a API para o Go em `http://localhost:8088`.

Comandos isolados:

```powershell
Set-Location frontend
npm run lint
npm run test
npm run build

Set-Location ..\go-backend
go test ./...
go vet ./...
```

## Configuração

Nome, pasta de recebimento, aceite automático e tema são editáveis na interface.

| Variável | Padrão | Finalidade |
|---|---:|---|
| `LOCALDROP_PORT` | `8088` | Porta HTTP/LAN |
| `LOCALDROP_AUTO_OPEN` | `true` | Abrir a janela nativa do desktop |
| `LOCALDROP_STORAGE_ROOT` | `~/.localdrop` | Estado e arquivos temporários |
| `LOCALDROP_DOWNLOADS` | `~/Downloads/LocalDrop` | Pasta inicial de recebimento do host |
| `LOCALDROP_TRANSFER_CHUNK_SIZE` | `4194304` | Tamanho de cada chunk em bytes |
| `LOCALDROP_TRANSFER_MINIMUM_FREE_SPACE` | `104857600` | Reserva mínima após a transferência |
| `LOCALDROP_TRANSFER_INACTIVITY_TIMEOUT` | `2m` | Tempo até pausar uma transferência ociosa |
| `LOCALDROP_TRANSFER_TEMPORARY_LIFETIME` | `24h` | Retenção de temporários |
| `LOCALDROP_TRANSFER_CREATE_RATE_LIMIT` | `20` | Ofertas por janela e dispositivo |

Também é possível iniciar sem abrir a janela:

```powershell
.\dist\LocalDrop.exe --port 8089 --headless
```

## Dados locais

```text
~/.localdrop/
├── localdrop-go.json   # metadados e hashes de autenticação
├── localdrop-go.log    # diagnóstico local, sem tokens
├── go-completed/       # recebimentos do celular, até o download
└── outbox/             # textos enviados pelo PC com Ctrl+V
```

O conteúdo dos arquivos nunca entra no JSON. Enquanto chega, um arquivo fica como `nome.xxxxxxxx.localdrop` na pasta de recebimento e é renomeado ao terminar. Arquivos entregues na pasta do host não são removidos pela limpeza de temporários.

## Limites atuais

- HTTP local não oferece confidencialidade contra interceptação da LAN.
- A interface desktop nativa é disponibilizada para Windows; em outros sistemas, o serviço pode rodar em modo headless.
- Não há descoberta mDNS nesta versão; o QR Code e o endereço IP são os caminhos determinísticos.
- Alterar a porta exige reiniciar o processo.

## Documentação

- [Arquitetura](docs/ARCHITECTURE.md)
- [API HTTP e WebSocket](docs/API.md)
- [Segurança](docs/SECURITY.md)
- [Solução de problemas](docs/TROUBLESHOOTING.md)
