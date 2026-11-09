# Arquitetura do LocalDrop

## Visão geral

O LocalDrop combina um desktop nativo em Go com um cliente web exclusivo para celulares:

```text
┌────────────────────────────┐      REST em loopback      ┌────────────────────────────┐
│ Windows desktop            │ ◄────────────────────────► │ Núcleo Go                  │
│ Go + canvas Win32 próprio  │                            │ API, relay e persistência  │
└────────────────────────────┘                            └──────────────┬─────────────┘
                                                                         │ HTTP/WS na LAN
                                                                         ▼
                                                          ┌────────────────────────────┐
                                                          │ Celular                    │
                                                          │ React no navegador         │
                                                          └────────────────────────────┘
```

O desktop não abre navegador, WebView ou motor HTML. A interface React é incorporada ao executável apenas para ser servida aos celulares da rede.

## Desktop Windows

`go-backend/panel_windows.go` cria a janela nativa e o sistema visual próprio:

- um único canvas visível, com shell, navegação, cards, botões, seletores, tabelas, estados e modais desenhados por Go;
- layout calculado a partir da área cliente atual, com largura útil limitada em telas grandes e linhas adaptadas ao espaço vertical;
- pareamentos, seleção e envio de arquivos;
- transferências ativas, histórico e configurações;
- ação **Conectar celular** com QR Code grande em um pop-up nativo dedicado, sem ocupar a tela principal;
- navegação por teclado e estados de ação explícitos;
- seletor de arquivos e pasta fornecido pelo sistema operacional.

`native_client.go` roda no mesmo processo do servidor: lê o estado direto da memória e recebe eventos por um ouvinte interno do `Hub`, sem consultas periódicas. Escondida na bandeja ou minimizada, a janela não consulta nem redesenha nada; avisos do Windows informam pedidos de pareamento e arquivos recebidos. Arquivos enviados pelo computador não são copiados nem reenviados por loopback: a oferta aponta para o original e o celular baixa direto dele.

Walk é usado somente como ponte Go para a janela Win32, eventos, clipboard e seletores do sistema. A superfície do produto é autoral e não usa os controles visuais padrão da biblioteca. Não há navegador incorporado. O manifesto de DPI por monitor e o ícone multirresolução do programa são incluídos em `rsrc_windows_amd64.syso`; os SVGs em `go-backend/assets` são renderizados localmente pelo canvas nativo. A moldura padrão do Windows é preservada para manter redimensionamento e maximização confiáveis, mas suas cores são integradas à paleta do LocalDrop por atributos DWM. O canvas raiz usa margens e espaçamento explicitamente zerados, ocupando toda a área cliente sem faixas intermediárias.

## Web móvel

`frontend/` contém React e TypeScript para o acesso pelo navegador do celular:

- onboarding e pareamento;
- envio de arquivos ou texto;
- progresso, pausa, retomada e download;
- histórico e dispositivos confiáveis;
- tema claro, escuro ou automático;
- service worker limitado ao shell visual.

O cliente usa a mesma origem HTTP do servidor, guarda identidade e token no armazenamento privado daquela origem e mantém eventos em tempo real por WebSocket. Não existe APK nem código Android.

## Núcleo Go

- `server.go`: API, autenticação, headers de segurança e assets web via `go:embed`;
- `identity.go`: host, dispositivos confiáveis, tokens e pareamento;
- `transfer.go`: máquina de estados, chunks, montagem, integridade e limpeza;
- `network.go`: interfaces LAN e QR Code web;
- `settings.go`: preferências e pasta de recebimento do host;
- `websocket.go`: presença e eventos para os clientes web;
- `disk_space*.go`: reserva e validação de espaço;
- `legacy_import.go`: importação opcional dos dados da versão Java antiga.

## Identidade

O primeiro acesso por loopback reivindica o host. Um celular cria um pedido de pareamento com código visual; depois da aprovação no desktop, recebe uma única vez o token do dispositivo. O servidor persiste apenas hashes SHA-256 dos segredos.

## Máquina de estados

```text
CREATED → WAITING_ACCEPTANCE → ACCEPTED → TRANSFERRING → VERIFYING → COMPLETED
                     │             │             │
                     ├→ REJECTED   ├→ PAUSED ────┘
                     └→ CANCELLED  └→ FAILED
```

Transições inválidas retornam HTTP 409. Ao retomar, o remetente consulta os intervalos ausentes e não repete blocos confirmados.

## Pipeline de arquivos

1. O cliente envia metadados; o SHA-256 completo é opcional, então o celular lê cada arquivo uma única vez.
2. O servidor valida nomes, tamanhos, limites e quantidade de chunks.
3. O destinatário aceita ou recusa a oferta.
4. O remetente envia blocos de 4 MiB com `X-Chunk-SHA256`, quatro por vez.
5. Cada bloco é conferido e gravado direto na sua posição de um único arquivo parcial (`nome.xxxxxxxx.localdrop`) na pasta de destino. Os recibos vão para o estado em lotes de um segundo; eventos de progresso saem no máximo quatro vezes por segundo.
6. Com todos os blocos, o servidor confere o hash completo (se informado) e publica o arquivo por renomeação, sem sobrescrever nada e sem cópia extra.
7. Para o host, o resultado fica na pasta configurada; para o celular, um link temporário entrega o arquivo ao gerenciador de downloads do navegador.

## Build

`npm run build` gera `frontend/dist`. Os scripts copiam esse bundle para `go-backend/web`; `go:embed` o inclui no mesmo executável do servidor e da UI nativa.

No Windows, o build usa `CGO_ENABLED=0`, inclui o manifesto Win32 e produz `dist/LocalDrop.exe`. Go e Node.js são requisitos de desenvolvimento, não de execução.

## Decisões

- **Desktop realmente nativo:** nenhuma dependência de navegador e integração direta ao Windows.
- **Web somente no celular:** elimina APK e mantém atualização simples.
- **Um núcleo de transferência:** ambos os clientes seguem o mesmo contrato e as mesmas regras.
- **Relay local:** simplifica autorização, retomada e descoberta.
- **HTTP na LAN:** reduz fricção de certificados, exigindo uma rede privada confiável.
- **JSON atômico:** suficiente para uma única instância local e simples de recuperar.
