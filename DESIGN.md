# DESIGN.md · LocalDrop

> Se este arquivo e o código discordarem, o código vale até o usuário decidir. Anote o conflito em Perguntas em aberto.

## Produto
Transferência de arquivos entre aparelhos da mesma rede, sem conta e sem nuvem.
- **Windows:** janela nativa em Go (Win32 + GDI, sem navegador), uma tela só: escolher o celular, arrastar arquivos, ver a atividade. Ajustes numa segunda página. Fica na bandeja.
- **Celular:** interface React no navegador, sem APK. Três botões de envio (Fotos e vídeos, Arquivos, Texto ou link), ofertas do PC no topo, lista de atividade.
Tarefa principal: mandar um arquivo de um aparelho para o outro em poucos segundos, sem pensar.

## Público e tom
Pessoas comuns em casa, não técnicas (assumido). Tom curto, direto e amigável, verbos no imperativo ("Arraste", "Permitir"). Português do Brasil; tamanhos como "1,7 GB", tempos como "há 2 min".

## Clima
Palavras: rápido, calmo, confiável (assumido). Nunca parecer: painel corporativo, app de nuvem cheio de upsell, tela técnica de rede (assumido).

## Marca
- Ícone: setas de transferência (`go-backend/assets/arrow-transfer2.svg`, `frontend/public/icon.svg`).
- Cor principal: violeta `#6456E7` (tokens em `frontend/src/style.css` e `go-backend/panel_windows.go`).
- Fonte: Segoe UI no Windows; fonte do sistema no celular.
- Fonte do design system: `frontend/src/style.css` (variáveis CSS) e as constantes de cor de `go-backend/panel_windows.go`. As duas devem continuar iguais.

## Referências
Nenhuma dada ainda (o usuário deu liberdade total em 23/11/2026).

## Restrições
- Desktop: desenhado à mão com GDI. Sem sombras desfocadas, sem gradientes, sem fontes externas; cantos arredondados e linhas simples. Escala por DPI (96 e 150 testados). Janela mínima menor que 1000 px.
- Celular: uma coluna, alvos de toque de 44 px, texto de 16 px, tema claro, escuro e automático, `prefers-reduced-motion` respeitado.
- Sem CDN, sem fonte remota, sem analytics.
- Contraste WCAG AA.

## Registro de decisões
- 2026-11-23 — Entrevista do craft-setup pulada — o usuário pediu o redesenho com "liberdade total"; lacunas ficam em Perguntas em aberto.
- 2026-11-23 — Cabeçalho e barra de título claros (sem a faixa escura) nos dois apps — o roxo fica reservado para a ação principal de cada momento.
- 2026-11-23 — Cartões com contorno de 1 px em vez de sombra — o GDI não desfoca, e a faixa sólida embaixo parecia borda grossa; o celular segue igual.
- 2026-11-23 — No Windows, "Enviar para" e a área de soltar viram um cartão só, com aparelhos em pílulas — sobra mais altura para a atividade (3 linhas em vez de 2 na janela padrão com dois avisos).
- 2026-11-23 — Avisos em faixa no tom da cor (laranja para pareamento e rede, cinza para sugestões), com botões secundários brancos — só uma ação roxa por tela.
- 2026-11-23 — Abrir e Copiar texto na atividade passam a secundários — com vários concluídos, cada linha tinha um botão roxo competindo.
- 2026-11-23 — Títulos de seção em frase normal ("Atividade", "Meus aparelhos"), sem rótulos em caixa alta roxa.
- 2026-11-23 — Tokens do tema claro alinhados entre `style.css` e `panel_windows.go` (fundo, linha, cinza de apoio, texto secundário).

## Perguntas em aberto
- Público e clima acima são suposições; confirmar.
- Nenhuma referência visual definida.
