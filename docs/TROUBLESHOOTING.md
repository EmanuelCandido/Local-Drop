# Solução de problemas

## O executável não abre

Abra `dist\LocalDrop.exe` diretamente. Ele deve iniciar a janela nativa; nenhum navegador é necessário ou aberto.

Se a porta 8088 já estiver ocupada por outra instância saudável, um novo duplo clique tenta trazer a janela existente para frente.

Consulte o log:

```text
%USERPROFILE%\.localdrop\localdrop-go.log
```

E confirme o serviço:

```powershell
Invoke-RestMethod http://127.0.0.1:8088/api/v1/health
```

## O celular não alcança o computador

- confirme que ambos estão na mesma rede Wi-Fi/LAN;
- use **Conectar celular** e escaneie novamente o QR Code do pop-up ou copie exatamente o endereço exibido;
- desative temporariamente VPNs que isolem a rede local;
- permita o LocalDrop somente em redes privadas no Firewall do Windows;
- redes de convidados podem bloquear comunicação entre clientes.

O celular não usa APK: o QR deve abrir uma página no navegador.

## O código de pareamento não aparece

Na janela do desktop, abra **Início** e use **Atualizar**. O pedido expira após alguns minutos; se necessário, solicite outro pelo navegador do celular. Confira nome e código antes de aprovar.

## O envio ficou pausado

Selecione a transferência e use **Retomar**. O cliente consulta os chunks ausentes e continua sem reenviar blocos já confirmados. Não limpe os dados do site móvel durante uma transferência pendente.

## O download no celular não aparece

Depois da conclusão, use **Baixar** no cartão do arquivo. A pasta de destino é controlada pelo navegador e pelo sistema do aparelho.

## A interface móvel parece desatualizada

Recarregue a página. Se um service worker antigo persistir, feche as abas do LocalDrop e limpe os dados somente daquele endereço local; esse navegador precisará ser pareado novamente.

## Não há espaço para aceitar

O host precisa manter os chunks e uma montagem verificada. Libere espaço em `%USERPROFILE%\.localdrop` e na pasta de recebimento. A margem padrão é 100 MiB além dos arquivos necessários.

## O build falha

Use Go 1.27+ e Node.js compatível com Vite (`^20.19.0` ou `>=22.12.0`):

```powershell
.\scripts\build.ps1
```

Para diagnosticar separadamente:

```powershell
Set-Location frontend
npm run lint
npm run test
npm run build

Set-Location ..\go-backend
go test ./...
go vet ./...
```

Ao relatar uma falha, inclua sistema operacional, versão, etapa, estado da transferência e as últimas linhas do log. Nunca compartilhe tokens nem o arquivo `localdrop-go.json` completo.
