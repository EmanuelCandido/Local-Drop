# Segurança

## Escopo do modelo

O LocalDrop foi projetado para uma LAN privada controlada pelo usuário, como a rede doméstica. Ele protege contra dispositivos não pareados fazendo chamadas casuais, enumeração de dados privados, alteração de transferências de terceiros e caminhos de arquivo maliciosos.

O modo HTTP padrão **não protege contra um atacante capaz de observar ou modificar o tráfego da rede**. Pareamento não é criptografia de transporte. Uma rede pública, um ponto de acesso comprometido ou malware no host continuam fora do limite de confiança.

Recomendação operacional:

- use apenas Wi-Fi/Ethernet privado e confiável;
- no alerta do Firewall do Windows, permita somente **Redes privadas**;
- não encaminhe a porta 8088 no roteador;
- não exponha o processo diretamente à internet;
- confira o código de pareamento nos dois dispositivos;
- revogue dispositivos que você não reconhece.

## Identidade e pareamento

- Cada aplicativo cria um UUID persistente, mas o UUID não autentica ninguém.
- O claim do host só aceita conexão loopback e ignora cabeçalhos forwarded.
- Um dispositivo remoto abre uma solicitação com validade padrão de cinco minutos.
- O código visual tem seis dígitos e deve coincidir nas duas telas.
- O polling exige um segredo aleatório separado no header `X-Pairing-Poll-Token`; ele não aparece na URL.
- Depois da aprovação, o token de dispositivo tem 32 bytes produzidos por `SecureRandom`.
- Código, poll token e device token são persistidos somente como SHA-256.
- O device token aprovado é coletado uma única vez; revogação invalida a autenticação imediatamente.
- Solicitações de pareamento têm rate limiting por endereço remoto.

O token bruto do celular fica no armazenamento privado da origem web para permitir reconexão. A sessão do host nativo permanece no processo local. No servidor, somente o hash SHA-256 do token é persistido.

## Autenticação e autorização

Chamadas privadas exigem simultaneamente:

```http
Authorization: Device <token>
X-Device-Id: <uuid>
```

O servidor compara o hash em tempo constante e distingue host de dispositivo comum. Administração de pareamentos e revogação exige papel de host.

No domínio de transferências:

- somente o destinatário aceita, rejeita e baixa;
- somente o remetente envia e consulta chunks ausentes;
- ambos os participantes podem visualizar, pausar ou cancelar;
- um terceiro dispositivo recebe 403 mesmo que descubra UUIDs de transferência/arquivo.

## Proteção do filesystem

- Nomes são normalizados em Unicode NFC e validados.
- Separadores, nomes `.`/`..`, caracteres de controle e nomes inválidos são rejeitados ou neutralizados pela política de nome.
- IDs do servidor, e não nomes do cliente, formam os caminhos temporários.
- Todo caminho construído é normalizado e verificado contra a raiz permitida.
- Arquivos são montados em staging; publicação não substitui um destino existente.
- Duplicatas recebem sufixo determinístico.
- O LocalDrop nunca executa automaticamente um arquivo recebido e responde como `application/octet-stream` no download.
- MIME informado pelo cliente serve apenas como metadado visual.

## Integridade e disponibilidade

- SHA-256 é conferido para cada chunk e para o arquivo montado.
- Tamanho declarado, índice e quantidade esperada são verificados.
- Retry de chunk com o mesmo conteúdo é idempotente; conteúdo divergente é recusado.
- Estados terminais não podem voltar a estados ativos.
- Reservas de disco e uma margem mínima evitam aceitar conteúdo sem capacidade conhecida.
- Rate limiting cobre pareamento e criação de ofertas, sem limitar artificialmente o fluxo legítimo de chunks.
- Temporários abandonados expiram; arquivos já publicados na pasta do host não entram nessa limpeza.

SHA-256 detecta corrupção e divergência; ele não torna o conteúdo secreto.

## Clientes e WebSocket

- O desktop nativo e a interface web enviam o token REST somente no header `Authorization`, nunca em query string.
- O WebSocket móvel envia o token no primeiro frame, nunca em query string.
- O handshake valida `Origin` contra `Host` para reduzir uso cross-site do serviço local.
- Sessões sem autenticação e heartbeats expirados são encerradas.
- O servidor Go aplica CSP, `nosniff`, bloqueio de frames e políticas de cache diretamente.
- Não há CDN, telemetria, analytics ou requests operacionais para serviços externos.

## HTTP, HTTPS e proxies

HTTPS local não está habilitado por padrão porque certificados autoassinados quebram a primeira experiência em muitos celulares. Se confidencialidade de transporte for requisito, opere em uma LAN isolada ou adicione TLS conscientemente e distribua/confie o certificado nos dispositivos.

Reverse proxy não faz parte do contrato atual: forwarded headers são ignorados e a validação de origem assume acesso direto ao host. Não coloque o LocalDrop atrás de um proxy sem revisar bootstrap, origem WebSocket, endereços anunciados e TLS.

## Dados e privacidade

- Dados ficam no computador host.
- O arquivo JSON atômico guarda metadados e hashes, nunca o conteúdo dos arquivos.
- O servidor não possui telemetria ou conta externa.
- O histórico inclui nomes, tamanhos, participantes e horários; qualquer pessoa com acesso ao perfil/arquivos do SO do host pode ler esses dados.
- Recebimentos destinados ao celular ficam temporariamente armazenados até download/expiração; ajuste `LOCALDROP_TRANSFER_TEMPORARY_LIFETIME` conforme a necessidade.

## Resposta a suspeita de comprometimento

1. Pare o LocalDrop.
2. Desconecte-se da rede não confiável.
3. Revogue dispositivos desconhecidos em **Configurações**.
4. Para invalidar todas as identidades, mova a pasta `~/.localdrop` para um backup e reinicie; isso também remove histórico e transferências temporárias, mas não os arquivos já entregues na pasta de downloads.
5. Inicie novamente e refaça os pareamentos.
