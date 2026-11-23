//go:build windows

package main

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"unsafe"

	qrcode "github.com/skip2/go-qrcode"
)

const nativeWindowTitle = "LocalDrop"

// releasesURL é a página de novidades. Fica vazia até o LocalDrop ser
// publicado; o build pode definir com -ldflags "-X main.releasesURL=...".
var releasesURL = ""

// canvasLayout descreve a coluna de conteúdo abaixo do cabeçalho.
type canvasLayout struct {
	bounds              rect
	header              int
	left, width         int
	top, bottom, status int
}

// computeCanvasLayout é pura: toda a geometria parte daqui, então redimensionar,
// maximizar ou mudar o DPI nunca reaproveita medidas antigas.
func computeCanvasLayout(b rect) canvasLayout {
	margin := 28
	if b.Width < 900 {
		margin = 20
	}
	left := margin
	width := b.Width - 2*margin
	if width > 1080 {
		left += (width - 1080) / 2
		width = 1080
	}
	if width < 0 {
		width = 0
	}
	header := 64
	top := header + 20
	bottom := b.Height - 34
	if bottom < top {
		bottom = top
	}
	return canvasLayout{bounds: b, header: header, left: left, width: width, top: top, bottom: bottom, status: b.Height - 27}
}

type homeLayout struct {
	banners  []rect
	devices  rect
	drop     rect
	activity rect
}

// computeHomeLayout empilha avisos, destinos, área de envio e atividade. A
// atividade ocupa o que sobrar até o rodapé. Com aparelhos conectados, destinos
// e área de envio ficam colados: são o mesmo cartão de envio.
func computeHomeLayout(l canvasLayout, banners int, onboarding bool) homeLayout {
	var result homeLayout
	y := l.top
	for index := 0; index < banners; index++ {
		result.banners = append(result.banners, rect{X: l.left, Y: y, Width: l.width, Height: 60})
		y += 72
	}
	devicesHeight, devicesGap := 64, 0
	if onboarding {
		devicesHeight, devicesGap = 216, 16
	}
	result.devices = rect{X: l.left, Y: y, Width: l.width, Height: devicesHeight}
	y += devicesHeight + devicesGap
	dropHeight := 116
	if onboarding {
		dropHeight = 92
	} else if l.bottom-y < dropHeight+16+260 {
		dropHeight = 92
	}
	result.drop = rect{X: l.left, Y: y, Width: l.width, Height: dropHeight}
	y += dropHeight + 16
	height := l.bottom - y
	if height < 0 {
		height = 0
	}
	result.activity = rect{X: l.left, Y: y, Width: l.width, Height: height}
	return result
}

// Paleta igual às variáveis de frontend/src/style.css (tema claro).
var (
	canvasBG    = rgb(244, 245, 248)
	ink         = rgb(27, 31, 51)
	muted       = rgb(78, 86, 112) // contraste de cerca de 7:1 sobre branco
	primary     = rgb(100, 86, 231)
	primaryDark = rgb(79, 66, 197)
	primarySoft = rgb(238, 235, 255)
	primaryLine = rgb(196, 190, 242)
	panel       = rgb(255, 255, 255)
	line        = rgb(226, 229, 238)
	success     = rgb(22, 128, 90)
	successSoft = rgb(225, 245, 236)
	warning     = rgb(166, 98, 10)
	warningSoft = rgb(253, 242, 224)
	danger      = rgb(196, 54, 68)
	dangerSoft  = rgb(252, 233, 235)
	subtleBG    = rgb(238, 240, 246)
	subtleHover = rgb(226, 229, 240)
	disabledBG  = rgb(238, 240, 245)
	disabledInk = rgb(140, 146, 166)
	white       = rgb(255, 255, 255)
	offlineDot  = rgb(160, 166, 186)
)

// Glifos da fonte Segoe MDL2 Assets, presente no Windows 10 e 11.
const (
	glyphUpload   = ""
	glyphDownload = ""
	glyphPhone    = ""
	glyphDesktop  = ""
	glyphQR       = ""
	glyphBack     = ""
	glyphAdd      = ""
	glyphCheck    = ""
	glyphShield   = ""
	glyphMessage  = ""
	glyphWarning  = ""
	glyphPower    = ""
)

const (
	oneLine      = dtVCenter | dtSingleLine | dtEndEllipsis
	centeredLine = dtCenter | dtVCenter | dtSingleLine
	rightLine    = dtRight | dtVCenter | dtSingleLine
	wrapped      = dtWordBreak | dtEndEllipsis
)

type buttonKind int

const (
	buttonPrimary buttonKind = iota
	buttonSubtle
	buttonDanger
	buttonOnTint // secundário sobre faixas coloridas (avisos)
)

type transferSpeed struct {
	bytes int64
	at    time.Time
	rate  float64
}

type pairedNotice struct {
	deviceID, name string
	at             time.Time
}

type nativeDesktopUI struct {
	server *Server
	client *nativeClient

	main   *canvasWindow
	target *canvasWindow // janela sendo desenhada agora (principal ou janela modal)
	cache  *gdiCache
	queue  uiQueue
	tray   trayIcon

	appIcon, appIconSmall uintptr
	navIcons              map[int]map[string]nativeNavIcon
	qr                    *qrcode.QRCode
	qrText                string

	snapshot       nativeSnapshot
	peers          []DeviceView
	incomingOffers []TransferView
	selectedPeer   string
	page, status   string
	headerStatus   string
	activityOffset int
	activityRows   int
	speeds         map[string]transferSpeed

	modal         string
	confirmAction func()

	addressIndex   int
	health         networkHealth
	healthAt       time.Time
	healthChecking bool
	fixing         bool
	paired         *pairedNotice
	autostartOn    bool
	storageBytes   int64
	qrPopup        *canvasWindow

	refreshMu      sync.Mutex
	refreshPending bool
	stale          bool

	closed    chan struct{}
	closeOnce sync.Once
	quitting  bool
}

func translateTransferState(s string) string {
	m := map[string]string{"CREATED": "Criado", "WAITING_ACCEPTANCE": "Aguardando", "ACCEPTED": "Começando", "TRANSFERRING": "Enviando", "PAUSED": "Pausado", "VERIFYING": "Conferindo", "COMPLETED": "Concluído", "REJECTED": "Recusado", "CANCELLED": "Cancelado", "FAILED": "Não concluído"}
	if v := m[s]; v != "" {
		return v
	}
	return s
}

func formatNativeBytes(v int64) string {
	if v < 1024 {
		return fmt.Sprintf("%d B", v)
	}
	a := float64(v)
	for _, u := range []string{"KB", "MB", "GB", "TB"} {
		a /= 1024
		if a < 1024 || u == "TB" {
			return strings.Replace(fmt.Sprintf("%.1f %s", a, u), ".", ",", 1)
		}
	}
	return fmt.Sprintf("%d B", v)
}

func transferFileSummary(f []TransferFileView) string {
	if len(f) == 0 {
		return "—"
	}
	if len(f) == 1 {
		return f[0].Name
	}
	return fmt.Sprintf("%s e mais %d", f[0].Name, len(f)-1)
}

func relativeTime(t time.Time) string {
	local := t.Local()
	now := time.Now()
	switch elapsed := now.Sub(local); {
	case elapsed < time.Minute:
		return "agora"
	case elapsed < time.Hour:
		return fmt.Sprintf("há %d min", int(elapsed.Minutes()))
	case local.YearDay() == now.YearDay() && local.Year() == now.Year():
		return local.Format("15:04")
	case now.Sub(local) < 48*time.Hour && local.YearDay() == now.Add(-24*time.Hour).YearDay():
		return "ontem, " + local.Format("15:04")
	}
	return local.Format("02/01 15:04")
}

func isTextMessage(file TransferFileView) bool {
	return strings.HasPrefix(file.MimeType, "text/plain") && file.Size <= 1<<20
}

func isMobileType(kind string) bool {
	upper := strings.ToUpper(kind)
	return strings.Contains(upper, "MOBILE") || strings.Contains(upper, "TABLET")
}

func availability(peer DeviceView) (string, color) {
	if peer.Online {
		return "Disponível", success
	}
	return "Fora do Wi-Fi · visto " + relativeTime(peer.LastSeenAt), muted
}

// ---------------------------------------------------------------- desenho

// textSize aumenta a escala antiga (8–9 pt) para a leitura confortável de 10–11 pt.
func textSize(size int) float64 {
	switch {
	case size <= 8:
		return 9.5
	case size == 9:
		return 10.5
	default:
		return float64(size) + 1
	}
}

func (ui *nativeDesktopUI) font(size int, bold bool) font {
	return font{points: textSize(size), bold: bold}
}

func (ui *nativeDesktopUI) glyphFont(size int) font { return font{points: float64(size), glyph: true} }

func fill(c *painter, col color, r rect)                   { c.fill(col, r) }
func rounded(c *painter, col color, r rect, rad int)       { c.rounded(col, r, rad) }
func outline(c *painter, col color, r rect, rad int)       { c.outline(col, r, rad) }
func dashedOutline(c *painter, col color, r rect, rad int) { c.dashed(col, r, rad) }
func dot(c *painter, col color, r rect)                    { c.dot(col, r) }

func label(c *painter, s string, f font, col color, r rect, format uint32) {
	c.text(s, f, col, r, format)
}

func inset(r rect, l, t, rr, b int) rect {
	return rect{X: r.X + l, Y: r.Y + t, Width: r.Width - l - rr, Height: r.Height - t - b}
}

func (ui *nativeDesktopUI) add(id string, r rect) { ui.target.add(id, r) }

func (ui *nativeDesktopUI) hovered(id string) bool {
	return ui.target.hover == id || ui.target.pressed == id
}

func (ui *nativeDesktopUI) invalidate() {
	if ui.main != nil {
		ui.main.invalidate()
	}
}

func (ui *nativeDesktopUI) textWidth(c *painter, s string, f font) int { return c.measure(s, f) }

func (ui *nativeDesktopUI) glyph(c *painter, g string, size int, col color, r rect) {
	label(c, g, ui.glyphFont(size), col, r, centeredLine)
}

// card é uma superfície branca com contorno fino. Sem sombra: o GDI não
// desfoca, e uma faixa sólida embaixo parecia borda grossa.
func (ui *nativeDesktopUI) card(c *painter, r rect) {
	rounded(c, panel, r, 14)
	outline(c, line, r, 14)
}

// navIcon desenha um dos SVGs fornecidos no tamanho exato em pixels; dark usa
// a versão escura, para fundos claros.
func (ui *nativeDesktopUI) navIcon(c *painter, id string, r rect, dark ...bool) {
	pixels := int(c.px(r.Width))
	if ui.navIcons == nil {
		ui.navIcons = map[int]map[string]nativeNavIcon{}
	}
	icons := ui.navIcons[pixels]
	if icons == nil {
		loaded, err := loadNativeNavIcons(pixels)
		if err != nil {
			return
		}
		ui.navIcons[pixels] = loaded
		icons = loaded
	}
	icon := icons[id].active
	if len(dark) > 0 && dark[0] && icons[id].ink != nil {
		icon = icons[id].ink
	}
	c.icon(icon, r)
}

func (ui *nativeDesktopUI) button(c *painter, id, text, g string, r rect, kind buttonKind, enabled bool) {
	bg, fg, hover := subtleBG, ink, subtleHover
	switch kind {
	case buttonPrimary:
		bg, fg, hover = primary, white, primaryDark
	case buttonDanger:
		bg, fg, hover = dangerSoft, danger, rgb(248, 215, 220)
	case buttonOnTint:
		bg, fg, hover = white, ink, subtleBG
	}
	if !enabled {
		bg, fg = disabledBG, disabledInk
	} else if ui.hovered(id) {
		bg = hover
	}
	rounded(c, bg, r, 10)
	f := ui.font(9, true)
	if g == "" {
		label(c, text, f, fg, r, centeredLine)
	} else if text == "" {
		ui.glyph(c, g, 11, fg, r)
	} else {
		tw := ui.textWidth(c, text, f)
		total := 16 + 8 + tw
		x := r.X + (r.Width-total)/2
		label(c, g, ui.glyphFont(11), fg, rect{X: x, Y: r.Y, Width: 16, Height: r.Height}, centeredLine)
		label(c, text, f, fg, rect{X: x + 24, Y: r.Y, Width: tw + 4, Height: r.Height}, dtVCenter|dtSingleLine)
	}
	if enabled {
		ui.add(id, r)
	}
}

// smallButton ajusta a largura ao texto e é desenhado da direita para a esquerda.
func (ui *nativeDesktopUI) smallButton(c *painter, id, text string, right, y int, kind buttonKind) int {
	w := ui.textWidth(c, text, ui.font(9, true)) + 28
	ui.button(c, id, text, "", rect{X: right - w, Y: y, Width: w, Height: 32}, kind, true)
	return right - w - 8
}

func (ui *nativeDesktopUI) pill(c *painter, text string, fg, bg color, right, y int) int {
	f := ui.font(8, true)
	w := ui.textWidth(c, text, f) + 22
	r := rect{X: right - w, Y: y, Width: w, Height: 26}
	rounded(c, bg, r, 13)
	label(c, text, f, fg, r, centeredLine)
	return r.X
}

func (ui *nativeDesktopUI) toggle(c *painter, id string, on bool, r rect) {
	track := rgb(190, 195, 212)
	if on {
		track = primary
	}
	rounded(c, track, r, r.Height)
	knob := rect{X: r.X + 3, Y: r.Y + 3, Width: r.Height - 6, Height: r.Height - 6}
	if on {
		knob.X = r.X + r.Width - r.Height + 3
	}
	dot(c, white, knob)
	ui.add(id, r)
}

func (ui *nativeDesktopUI) paint(c *painter, b rect) {
	ui.target = ui.main
	l := computeCanvasLayout(b)
	ui.paintHeader(c, l)
	if ui.page == "settings" {
		ui.paintSettings(c, l)
	} else {
		ui.paintHome(c, l)
	}
	label(c, ui.status, ui.font(8, false), muted, rect{X: l.left, Y: l.status, Width: l.width, Height: 20}, oneLine)
	if ui.modal != "" {
		ui.main.hits = ui.main.hits[:0]
		ui.paintModal(c, b)
	}
}

// paintHeader é uma faixa clara e baixa: marca e estado à esquerda, conectar e
// ajustes à direita. O roxo fica para as ações de cada aviso.
func (ui *nativeDesktopUI) paintHeader(c *painter, l canvasLayout) {
	fill(c, panel, rect{Width: l.bounds.Width, Height: l.header})
	fill(c, line, rect{Y: l.header - 1, Width: l.bounds.Width, Height: 1})
	logo := rect{X: l.left, Y: 14, Width: 36, Height: 36}
	rounded(c, primary, logo, 10)
	ui.navIcon(c, "nav-history", inset(logo, 8, 8, 8, 8))
	label(c, "LocalDrop", ui.font(12, true), ink, rect{X: logo.X + 48, Y: 10, Width: 260, Height: 24}, oneLine)
	statusColor := success
	if len(ui.peers) == 0 || ui.health.problem() {
		statusColor = warning
	}
	dot(c, statusColor, rect{X: logo.X + 49, Y: 40, Width: 7, Height: 7})
	label(c, ui.headerStatus, ui.font(8, false), muted, rect{X: logo.X + 62, Y: 33, Width: l.width/2 - 40, Height: 20}, oneLine)

	right := l.left + l.width
	gear := rect{X: right - 38, Y: 13, Width: 38, Height: 38}
	gearID, bg := "settings", panel
	if ui.page == "settings" {
		gearID, bg = "home", primarySoft
	} else if ui.hovered(gearID) {
		bg = subtleBG
	}
	rounded(c, bg, gear, 10)
	ui.navIcon(c, "nav-settings", inset(gear, 9, 9, 9, 9), true)
	ui.add(gearID, gear)
	ui.button(c, "connect", "Conectar celular", glyphQR, rect{X: gear.X - 180, Y: 13, Width: 172, Height: 38}, buttonSubtle, true)
}

// ---------------------------------------------------------------- início

type bannerKind int

const (
	bannerPairing bannerKind = iota
	bannerNetwork
	bannerPaired
	bannerOffer
	bannerAutostart
)

type bannerItem struct {
	kind     bannerKind
	pairing  pairingView
	transfer TransferView
}

// banners escolhe no máximo dois avisos, do mais urgente ao menos urgente.
func (ui *nativeDesktopUI) banners() []bannerItem {
	items := make([]bannerItem, 0, 2)
	for _, pairing := range ui.snapshot.Pairings {
		items = append(items, bannerItem{kind: bannerPairing, pairing: pairing})
	}
	if ui.health.problem() || ui.fixing {
		items = append(items, bannerItem{kind: bannerNetwork})
	}
	if ui.paired != nil {
		items = append(items, bannerItem{kind: bannerPaired})
	}
	for _, offer := range ui.incomingOffers {
		items = append(items, bannerItem{kind: bannerOffer, transfer: offer})
	}
	if len(ui.peers) > 0 && !ui.server.autostartAsked() && !ui.autostartOn {
		items = append(items, bannerItem{kind: bannerAutostart})
	}
	if len(items) > 2 {
		items = items[:2]
	}
	return items
}

func (ui *nativeDesktopUI) paintHome(c *painter, l canvasLayout) {
	items := ui.banners()
	layout := computeHomeLayout(l, len(items), len(ui.peers) == 0)
	for index, item := range items {
		r := layout.banners[index]
		switch item.kind {
		case bannerPairing:
			ui.paintPairingBanner(c, r, item.pairing)
		case bannerNetwork:
			ui.paintNetworkBanner(c, r)
		case bannerPaired:
			ui.paintPairedBanner(c, r)
		case bannerOffer:
			ui.paintOfferBanner(c, r, item.transfer)
		case bannerAutostart:
			ui.paintAutostartBanner(c, r)
		}
	}
	if len(ui.peers) == 0 {
		ui.paintOnboarding(c, layout.devices)
	} else {
		// Destinos e área de envio formam um cartão só.
		ui.card(c, rect{X: layout.devices.X, Y: layout.devices.Y, Width: layout.devices.Width, Height: layout.drop.Y + layout.drop.Height - layout.devices.Y})
		ui.paintDevices(c, layout.devices)
	}
	ui.paintDrop(c, layout.drop)
	ui.paintActivity(c, layout.activity)
}

// bannerFrame desenha o aviso como uma faixa no tom da sua cor, com o ícone num
// círculo branco; os botões vêm depois, por cima.
func (ui *nativeDesktopUI) bannerFrame(c *painter, r rect, accent, soft color, g string) {
	rounded(c, soft, r, 14)
	circle := rect{X: r.X + 14, Y: r.Y + (r.Height-34)/2, Width: 34, Height: 34}
	dot(c, white, circle)
	ui.glyph(c, g, 12, accent, circle)
}

// bannerText escreve título e detalhe até onde começam os botões.
func (ui *nativeDesktopUI) bannerText(c *painter, r rect, title, detail string, textRight int) {
	x := r.X + 62
	width := textRight - x - 12
	label(c, title, ui.font(10, true), ink, rect{X: x, Y: r.Y + 7, Width: width, Height: 24}, oneLine)
	label(c, detail, ui.font(8, false), muted, rect{X: x, Y: r.Y + 30, Width: width, Height: 22}, oneLine)
}

// bannerButtonY centraliza os botões de 32 px na faixa do aviso.
func bannerButtonY(r rect) int { return r.Y + (r.Height-32)/2 }

func (ui *nativeDesktopUI) paintPairingBanner(c *painter, r rect, p pairingView) {
	ui.bannerFrame(c, r, warning, warningSoft, glyphShield)
	x := r.X + r.Width - 14
	x = ui.smallButton(c, "pair-approve|"+p.RequestID, "Permitir", x, bannerButtonY(r), buttonPrimary)
	x = ui.smallButton(c, "pair-reject|"+p.RequestID, "Recusar", x, bannerButtonY(r), buttonOnTint)
	code := p.Code
	if len(code) == 6 {
		code = code[:3] + " " + code[3:]
	}
	label(c, code, ui.font(14, true), warning, rect{X: x - 120, Y: r.Y, Width: 112, Height: r.Height}, rightLine)
	ui.bannerText(c, r, p.DisplayName+" quer se conectar",
		"Permita só se o celular mostrar este mesmo código.", x-130)
}

func (ui *nativeDesktopUI) paintNetworkBanner(c *painter, r rect) {
	ui.bannerFrame(c, r, warning, warningSoft, glyphWarning)
	x := r.X + r.Width - 14
	title := "O Windows está bloqueando o celular"
	detail := "O firewall não liberou o LocalDrop. Corrigir pede uma permissão ao Windows."
	if ui.health.PublicNetwork {
		detail = "Este Wi-Fi está como rede pública. Corrigir resolve com uma permissão."
	}
	if ui.fixing {
		title, detail = "Aguardando a permissão do Windows…", "Confirme no aviso do Windows que apareceu na tela."
	} else {
		x = ui.smallButton(c, "net-fix", "Corrigir", x, bannerButtonY(r), buttonPrimary)
		x = ui.smallButton(c, "net-settings", "Configurações de rede", x, bannerButtonY(r), buttonOnTint)
	}
	ui.bannerText(c, r, title, detail, x)
}

func (ui *nativeDesktopUI) paintPairedBanner(c *painter, r rect) {
	ui.bannerFrame(c, r, success, successSoft, glyphCheck)
	x := r.X + r.Width - 14
	x = ui.smallButton(c, "paired-ok", "OK", x, bannerButtonY(r), buttonPrimary)
	x = ui.smallButton(c, "paired-undo", "Desfazer", x, bannerButtonY(r), buttonOnTint)
	ui.bannerText(c, r, ui.paired.name+" foi conectado",
		"Já pode enviar e receber arquivos. Não foi você? Clique em Desfazer.", x)
}

func (ui *nativeDesktopUI) paintOfferBanner(c *painter, r rect, t TransferView) {
	ui.bannerFrame(c, r, primary, primarySoft, glyphDownload)
	files := fmt.Sprintf("%d arquivos", len(t.Files))
	if len(t.Files) == 1 {
		files = t.Files[0].Name
	}
	x := r.X + r.Width - 14
	x = ui.smallButton(c, "t-accept|"+t.ID, "Aceitar", x, bannerButtonY(r), buttonPrimary)
	x = ui.smallButton(c, "t-reject|"+t.ID, "Recusar", x, bannerButtonY(r), buttonOnTint)
	ui.bannerText(c, r, ui.peerName(t.SenderID)+" quer enviar "+files,
		formatNativeBytes(t.TotalBytes)+" · vai para "+ui.downloadDirectory(), x)
}

func (ui *nativeDesktopUI) paintAutostartBanner(c *painter, r rect) {
	ui.bannerFrame(c, r, ink, subtleBG, glyphPower)
	x := r.X + r.Width - 14
	x = ui.smallButton(c, "autostart-yes", "Sim, abrir", x, bannerButtonY(r), buttonOnTint)
	x = ui.smallButton(c, "autostart-no", "Agora não", x, bannerButtonY(r), buttonOnTint)
	ui.bannerText(c, r, "Abrir o LocalDrop junto com o Windows?",
		"Ele fica na bandeja, pronto para receber.", x)
}

// inviteURL é o endereço levado pelo QR: abre o LocalDrop no celular já com
// um convite de uso único.
func (ui *nativeDesktopUI) inviteURL() (string, time.Time) {
	token, expires, err := ui.server.currentInvite()
	base := ui.addressURL()
	if err != nil {
		return base, time.Time{}
	}
	return base + "/#convite=" + token, expires
}

// addressURL devolve o endereço escolhido (o melhor, ou outro, se a pessoa
// pediu para tentar outro).
func (ui *nativeDesktopUI) addressURL() string {
	if ui.addressIndex == 0 {
		return ui.server.localURL()
	}
	addresses := ui.server.addresses()
	if len(addresses) == 0 {
		return ui.server.localURL()
	}
	return ui.server.urlForAddress(addresses[ui.addressIndex%len(addresses)].IPv4)
}

func (ui *nativeDesktopUI) ensureQR() {
	target, _ := ui.inviteURL()
	if ui.qr != nil && ui.qrText == target {
		return
	}
	code, err := qrcode.New(target, qrcode.Medium)
	if err != nil {
		return
	}
	code.DisableBorder = true
	ui.qr, ui.qrText = code, target
}

func (ui *nativeDesktopUI) paintQR(c *painter, r rect) {
	ui.ensureQR()
	rounded(c, white, r, 12)
	c.qr(ui.qr, inset(r, 10, 10, 10, 10), ink)
}

func (ui *nativeDesktopUI) paintOnboarding(c *painter, r rect) {
	ui.card(c, r)
	qr := rect{X: r.X + 16, Y: r.Y + 14, Width: 188, Height: 188}
	outline(c, line, qr, 12)
	ui.paintQR(c, qr)
	x := qr.X + qr.Width + 28
	w := r.X + r.Width - x - 24
	label(c, "Conecte seu celular", ui.font(15, true), ink, rect{X: x, Y: r.Y + 20, Width: w, Height: 32}, oneLine)
	steps := []string{
		"Abra a câmera do celular e aponte para o código ao lado.",
		"Toque no link que aparecer na tela do celular.",
		"Pronto: o celular já fica conectado a este computador.",
	}
	for i, step := range steps {
		y := r.Y + 62 + i*32
		circle := rect{X: x, Y: y, Width: 24, Height: 24}
		dot(c, primarySoft, circle)
		label(c, fmt.Sprint(i+1), ui.font(8, true), primary, circle, centeredLine)
		label(c, step, ui.font(9, false), ink, rect{X: x + 34, Y: y, Width: w - 34, Height: 24}, oneLine)
	}
	buttonX := r.X + r.Width - 24
	buttonX = ui.smallButton(c, "connect-help", "Não funcionou?", buttonX, r.Y+164, buttonSubtle)
	label(c, "O celular precisa estar no mesmo Wi-Fi deste computador.", ui.font(8, false), muted, rect{X: x, Y: r.Y + 164, Width: buttonX - x - 8, Height: 32}, oneLine)
}

// paintDevices é a linha de cima do cartão de envio: "Enviar para" e uma
// pílula por aparelho. Os que não cabem ficam para os Ajustes.
func (ui *nativeDesktopUI) paintDevices(c *painter, r rect) {
	labelFont := ui.font(9, true)
	title := "Enviar para"
	titleW := ui.textWidth(c, title, labelFont)
	label(c, title, labelFont, ink, rect{X: r.X + 20, Y: r.Y + 12, Width: titleW + 4, Height: 40}, oneLine)
	chipH, gap := 40, 8
	chipY := r.Y + 12
	x := r.X + 20 + titleW + 16
	limit := r.X + r.Width - 16 - chipH - gap
	nameFont := ui.font(9, true)
	for _, peer := range ui.peers {
		chipW := 34 + ui.textWidth(c, peer.DisplayName, nameFont) + 34
		if chipW > 260 {
			chipW = 260
		}
		if x+chipW > limit {
			break
		}
		chip := rect{X: x, Y: chipY, Width: chipW, Height: chipH}
		id := "peer|" + peer.DeviceID
		selected := peer.DeviceID == ui.selectedPeer
		bg, border, fg := panel, line, ink
		switch {
		case selected:
			bg, border, fg = primarySoft, primary, primaryDark
		case !peer.Online:
			fg = muted
		case ui.hovered(id):
			bg = subtleBG
		}
		rounded(c, bg, chip, chipH/2)
		outline(c, border, chip, chipH/2)
		g := glyphDesktop
		if isMobileType(peer.DeviceType) {
			g = glyphPhone
		}
		ui.glyph(c, g, 11, fg, rect{X: chip.X + 12, Y: chip.Y, Width: 18, Height: chipH})
		label(c, peer.DisplayName, nameFont, fg, rect{X: chip.X + 36, Y: chip.Y, Width: chipW - 66, Height: chipH}, oneLine)
		stateColor := success
		if !peer.Online {
			stateColor = offlineDot
		}
		dot(c, stateColor, rect{X: chip.X + chipW - 22, Y: chip.Y + chipH/2 - 4, Width: 8, Height: 8})
		ui.add(id, chip)
		x += chipW + gap
	}
	add := rect{X: x, Y: chipY, Width: chipH, Height: chipH}
	bg := panel
	if ui.hovered("connect-chip") {
		bg = primarySoft
	}
	rounded(c, bg, add, chipH/2)
	dashedOutline(c, primaryLine, add, chipH/2)
	ui.glyph(c, glyphAdd, 11, primary, add)
	ui.add("connect-chip", add)
}

// paintDrop é a área de soltar. Com aparelhos, fica dentro do cartão de envio;
// no primeiro uso, é um cartão próprio, desativado, explicando o que falta.
func (ui *nativeDesktopUI) paintDrop(c *painter, r rect) {
	enabled := ui.selectedPeer != ""
	zone := inset(r, 16, 0, 16, 16)
	if !enabled {
		zone = r
	}
	bg, border := rgb(250, 250, 255), primaryLine
	if !enabled {
		bg, border = panel, line
	} else if ui.hovered("drop") {
		bg, border = primarySoft, primary
	}
	rounded(c, bg, zone, 12)
	dashedOutline(c, border, zone, 12)
	size := 44
	circle := rect{X: zone.X + 20, Y: zone.Y + (zone.Height-size)/2, Width: size, Height: size}
	circleBG, glyphColor := primarySoft, primary
	if enabled {
		circleBG, glyphColor = primary, white
	} else {
		circleBG, glyphColor = subtleBG, muted
	}
	dot(c, circleBG, circle)
	ui.glyph(c, glyphUpload, 14, glyphColor, circle)
	x := circle.X + circle.Width + 16
	if !enabled {
		w := zone.X + zone.Width - x - 20
		label(c, "Arraste arquivos para cá", ui.font(10, true), muted, rect{X: x, Y: zone.Y + zone.Height/2 - 25, Width: w, Height: 24}, oneLine)
		label(c, "Fica disponível assim que um celular se conectar.", ui.font(9, false), muted, rect{X: x, Y: zone.Y + zone.Height/2 - 1, Width: w, Height: 24}, oneLine)
		return
	}
	ui.add("drop", zone)
	textButton := zone.X + zone.Width - 16
	textButton = ui.smallButton(c, "send-text", "Enviar texto", textButton, zone.Y+(zone.Height-32)/2, buttonOnTint)
	w := textButton - x - 8
	label(c, "Arraste arquivos para cá ou clique para escolher", ui.font(10, true), ink, rect{X: x, Y: zone.Y + zone.Height/2 - 25, Width: w, Height: 24}, oneLine)
	label(c, "Vão direto para "+ui.peerName(ui.selectedPeer)+", sem cópias extras.", ui.font(9, false), muted, rect{X: x, Y: zone.Y + zone.Height/2 - 1, Width: w, Height: 24}, oneLine)
}

func (ui *nativeDesktopUI) hasFinished() bool {
	for _, t := range ui.snapshot.Transfers {
		if isTerminalState(t.State) {
			return true
		}
	}
	return false
}

func (ui *nativeDesktopUI) paintActivity(c *painter, r rect) {
	ui.activityRows = 0
	if r.Height < 96 {
		return
	}
	ui.card(c, r)
	label(c, "Atividade", ui.font(10, true), ink, rect{X: r.X + 20, Y: r.Y + 12, Width: 200, Height: 28}, oneLine)
	transfers := ui.snapshot.Transfers
	if len(transfers) == 0 {
		label(c, "Nada enviado ou recebido ainda", ui.font(9, true), ink, rect{X: r.X + 20, Y: r.Y + 50, Width: r.Width - 40, Height: 24}, oneLine)
		label(c, "Cada envio aparece aqui, com o andamento e atalhos para abrir.", ui.font(9, false), muted, rect{X: r.X + 20, Y: r.Y + 74, Width: r.Width - 40, Height: 24}, oneLine)
		return
	}
	headerRight := r.X + r.Width - 16
	if ui.hasFinished() {
		headerRight = ui.smallButton(c, "clear-list", "Limpar lista", headerRight, r.Y+8, buttonSubtle)
	}
	rowH := 58
	listTop := r.Y + 48
	visible := (r.Y + r.Height - 8 - listTop) / rowH
	if visible < 1 {
		visible = 1
	}
	ui.activityRows = visible
	ui.clampActivity()
	scrollable := len(transfers) > visible
	rowRight := r.X + r.Width - 12
	if scrollable {
		end := ui.activityOffset + visible
		if end > len(transfers) {
			end = len(transfers)
		}
		label(c, fmt.Sprintf("%d–%d de %d", ui.activityOffset+1, end, len(transfers)), ui.font(8, false), muted,
			rect{X: headerRight - 200, Y: r.Y + 12, Width: 192, Height: 24}, rightLine)
		// Barra de rolagem: mostra onde a lista está; clicar na trilha avança uma página.
		track := rect{X: r.X + r.Width - 14, Y: listTop + 4, Width: 6, Height: visible*rowH - 8}
		rounded(c, subtleBG, track, 6)
		thumbH := track.Height * visible / len(transfers)
		if thumbH < 24 {
			thumbH = 24
		}
		thumbY := track.Y
		if span := len(transfers) - visible; span > 0 {
			thumbY += (track.Height - thumbH) * ui.activityOffset / span
		}
		rounded(c, primaryLine, rect{X: track.X, Y: thumbY, Width: track.Width, Height: thumbH}, 6)
		ui.add("scroll-up", rect{X: track.X - 4, Y: track.Y, Width: track.Width + 8, Height: thumbY - track.Y})
		ui.add("scroll-down", rect{X: track.X - 4, Y: thumbY + thumbH, Width: track.Width + 8, Height: track.Y + track.Height - thumbY - thumbH})
		rowRight = track.X - 6
	}
	for row := 0; row < visible && ui.activityOffset+row < len(transfers); row++ {
		rr := rect{X: r.X + 12, Y: listTop + row*rowH, Width: rowRight - r.X - 12, Height: rowH}
		if row > 0 {
			fill(c, line, rect{X: rr.X + 56, Y: rr.Y, Width: rr.Width - 64, Height: 1})
		}
		ui.paintTransferRow(c, rr, transfers[ui.activityOffset+row])
	}
}

func (ui *nativeDesktopUI) paintTransferRow(c *painter, r rect, t TransferView) {
	self := ui.client.session.DeviceID
	incoming := t.ReceiverID == self
	circle := rect{X: r.X + 8, Y: r.Y + 12, Width: 36, Height: 36}
	g, fg, bg := glyphUpload, primary, primarySoft
	if incoming {
		g, fg, bg = glyphDownload, success, successSoft
	}
	if len(t.Files) == 1 && isTextMessage(t.Files[0]) {
		g = glyphMessage
	}
	dot(c, bg, circle)
	ui.glyph(c, g, 11, fg, circle)

	// Ações e estado, da direita para a esquerda.
	right := r.X + r.Width - 8
	y := r.Y + 14
	switch {
	case incoming && t.State == "WAITING_ACCEPTANCE":
		right = ui.smallButton(c, "t-accept|"+t.ID, "Aceitar", right, y, buttonPrimary)
		right = ui.smallButton(c, "t-reject|"+t.ID, "Recusar", right, y, buttonSubtle)
	case incoming && t.State == "COMPLETED" && len(t.Files) > 0 && t.Files[0].SavedPath != nil:
		right = ui.smallButton(c, "t-folder|"+t.ID, "Mostrar na pasta", right, y, buttonSubtle)
		if len(t.Files) == 1 && isTextMessage(t.Files[0]) {
			right = ui.smallButton(c, "t-copy|"+t.ID, "Copiar texto", right, y, buttonSubtle)
		} else if len(t.Files) == 1 {
			right = ui.smallButton(c, "t-open|"+t.ID, "Abrir", right, y, buttonSubtle)
		}
	case !isTerminalState(t.State):
		right = ui.smallButton(c, "t-cancel|"+t.ID, "Cancelar", right, y, buttonDanger)
		if t.State == "ACCEPTED" || t.State == "TRANSFERRING" {
			right = ui.smallButton(c, "t-pause|"+t.ID, "Pausar", right, y, buttonSubtle)
		}
	}

	active := t.State == "ACCEPTED" || t.State == "TRANSFERRING" || t.State == "VERIFYING" || (t.State == "PAUSED" && t.ReceivedBytes > 0)
	if active && t.TotalBytes > 0 {
		barW := 160
		bar := rect{X: right - barW - 4, Y: r.Y + 22, Width: barW, Height: 6}
		rounded(c, subtleBG, bar, 6)
		progress := bar
		progress.Width = int(int64(barW) * t.ReceivedBytes / t.TotalBytes)
		if progress.Width > 0 {
			barColor := primary
			if t.State == "PAUSED" {
				barColor = warning
			}
			rounded(c, barColor, progress, 6)
		}
		detail := fmt.Sprintf("%d%%", t.ReceivedBytes*100/t.TotalBytes)
		if speed := ui.speeds[t.ID]; speed.rate > 0 && t.State == "TRANSFERRING" {
			detail += " · " + formatNativeBytes(int64(speed.rate)) + "/s"
		} else if t.State != "TRANSFERRING" {
			detail += " · " + translateTransferState(t.State)
		}
		label(c, detail, ui.font(8, false), muted, rect{X: bar.X, Y: r.Y + 30, Width: barW, Height: 20}, oneLine)
		right = bar.X - 12
	} else {
		text, pfg, pbg := translateTransferState(t.State), muted, subtleBG
		switch t.State {
		case "COMPLETED":
			text, pfg, pbg = "Concluído", success, successSoft
			if !incoming {
				text = "Pronto no celular"
				if len(t.Files) > 0 && !t.Files[0].DownloadAvailable {
					text, pfg, pbg = "Não está mais disponível", muted, subtleBG
				}
			}
		case "WAITING_ACCEPTANCE":
			text, pfg, pbg = "Esperando o celular", warning, warningSoft
			if incoming {
				text = "Esperando você"
			}
		case "FAILED", "REJECTED", "CANCELLED":
			pfg, pbg = danger, dangerSoft
		}
		right = ui.pill(c, text, pfg, pbg, right-4, r.Y+17) - 12
	}

	x := r.X + 56
	w := right - x
	peer := ui.peerName(t.ReceiverID)
	direction := "Para "
	if incoming {
		peer, direction = ui.peerName(t.SenderID), "De "
	}
	sub := direction + peer + " · " + formatNativeBytes(t.TotalBytes) + " · " + relativeTime(t.CreatedAt)
	if t.FailureMessage != nil && t.State == "FAILED" {
		sub = *t.FailureMessage
	} else if !incoming && t.State == "COMPLETED" && len(t.Files) > 0 && !t.Files[0].DownloadAvailable {
		sub = "Envie de novo se o celular ainda precisar · " + relativeTime(t.CreatedAt)
	}
	label(c, transferFileSummary(t.Files), ui.font(9, true), ink, rect{X: x, Y: r.Y + 9, Width: w, Height: 22}, oneLine)
	label(c, sub, ui.font(8, false), muted, rect{X: x, Y: r.Y + 31, Width: w, Height: 20}, oneLine)
}

// ---------------------------------------------------------------- ajustes

func (ui *nativeDesktopUI) paintSettings(c *painter, l canvasLayout) {
	x, y, w := l.left, l.top, l.width
	label(c, "Ajustes", ui.font(16, true), ink, rect{X: x, Y: y, Width: 300, Height: 34}, oneLine)
	ui.button(c, "home", "Voltar", glyphBack, rect{X: x + w - 116, Y: y, Width: 116, Height: 36}, buttonSubtle, true)
	y += 48
	gap := 16
	colW := (w - gap) / 2
	settings := ui.snapshot.Settings

	identity := rect{X: x, Y: y, Width: colW, Height: 256}
	ui.card(c, identity)
	label(c, "Este computador", ui.font(10, true), ink, rect{X: identity.X + 20, Y: identity.Y + 12, Width: 240, Height: 22}, oneLine)
	rowW := identity.Width - 40
	row := func(index int) int { return identity.Y + 40 + index*54 }
	divider := func(index int) {
		fill(c, line, rect{X: identity.X + 20, Y: row(index) - 4, Width: rowW, Height: 1})
	}
	buttonRight := identity.X + identity.Width - 20
	nameButtons := ui.smallButton(c, "rename", "Renomear", buttonRight, row(0)+6, buttonSubtle)
	label(c, "Nome que aparece no celular", ui.font(8, false), muted, rect{X: identity.X + 20, Y: row(0), Width: nameButtons - identity.X - 24, Height: 20}, oneLine)
	label(c, settings.Device.DisplayName, ui.font(11, true), ink, rect{X: identity.X + 20, Y: row(0) + 20, Width: nameButtons - identity.X - 24, Height: 26}, oneLine)
	divider(1)
	folderButtons := ui.smallButton(c, "folder-change", "Alterar", buttonRight, row(1)+6, buttonSubtle)
	folderButtons = ui.smallButton(c, "folder-open", "Abrir", folderButtons, row(1)+6, buttonSubtle)
	label(c, "Pasta de recebimento", ui.font(8, false), muted, rect{X: identity.X + 20, Y: row(1), Width: folderButtons - identity.X - 24, Height: 20}, oneLine)
	label(c, ui.downloadDirectory(), ui.font(9, true), ink, rect{X: identity.X + 20, Y: row(1) + 20, Width: folderButtons - identity.X - 24, Height: 24}, dtVCenter|dtSingleLine|dtPathEllipsis)
	divider(2)
	toggleX := identity.X + identity.Width - 66
	ui.toggle(c, "auto", settings.AutoAcceptTrusted, rect{X: toggleX, Y: row(2) + 10, Width: 46, Height: 26})
	label(c, "Aceitar automaticamente", ui.font(9, true), ink, rect{X: identity.X + 20, Y: row(2) + 2, Width: rowW - 70, Height: 22}, oneLine)
	label(c, "Arquivos chegam sem pedir confirmação.", ui.font(8, false), muted, rect{X: identity.X + 20, Y: row(2) + 24, Width: rowW - 70, Height: 20}, oneLine)
	divider(3)
	ui.toggle(c, "autostart", ui.autostartOn, rect{X: toggleX, Y: row(3) + 10, Width: 46, Height: 26})
	label(c, "Abrir junto com o Windows", ui.font(9, true), ink, rect{X: identity.X + 20, Y: row(3) + 2, Width: rowW - 70, Height: 22}, oneLine)
	label(c, "Fica pronto na bandeja ao ligar o PC.", ui.font(8, false), muted, rect{X: identity.X + 20, Y: row(3) + 24, Width: rowW - 70, Height: 20}, oneLine)

	phone := rect{X: x, Y: identity.Y + identity.Height + gap, Width: colW, Height: 112}
	ui.card(c, phone)
	label(c, "Acesso pelo celular", ui.font(10, true), ink, rect{X: phone.X + 20, Y: phone.Y + 12, Width: 240, Height: 22}, oneLine)
	label(c, ui.addressURL(), ui.font(11, true), ink, rect{X: phone.X + 20, Y: phone.Y + 34, Width: phone.Width - 40, Height: 26}, oneLine)
	ui.button(c, "connect", "Mostrar QR", glyphQR, rect{X: phone.X + 20, Y: phone.Y + 68, Width: 140, Height: 32}, buttonPrimary, true)
	ui.button(c, "copy-url", "Copiar endereço", "", rect{X: phone.X + 170, Y: phone.Y + 68, Width: 156, Height: 32}, buttonSubtle, true)

	trustedHeight := l.bottom - y - 96
	trusted := rect{X: x + colW + gap, Y: y, Width: w - colW - gap, Height: trustedHeight}
	ui.card(c, trusted)
	label(c, "Meus aparelhos", ui.font(10, true), ink, rect{X: trusted.X + 20, Y: trusted.Y + 12, Width: 240, Height: 22}, oneLine)
	if len(ui.peers) == 0 {
		label(c, "Nenhum aparelho conectado ainda.", ui.font(10, true), ink, rect{X: trusted.X + 20, Y: trusted.Y + 44, Width: trusted.Width - 40, Height: 24}, oneLine)
		label(c, "Use Conectar celular para adicionar o primeiro.", ui.font(9, false), muted, rect{X: trusted.X + 20, Y: trusted.Y + 70, Width: trusted.Width - 40, Height: 24}, oneLine)
	}
	rows := (trusted.Height - 50) / 54
	for i, peer := range ui.peers {
		if i >= rows {
			label(c, fmt.Sprintf("e mais %d…", len(ui.peers)-rows), ui.font(8, false), muted, rect{X: trusted.X + 20, Y: trusted.Y + trusted.Height - 28, Width: 200, Height: 20}, oneLine)
			break
		}
		rr := rect{X: trusted.X + 12, Y: trusted.Y + 40 + i*54, Width: trusted.Width - 24, Height: 54}
		circle := rect{X: rr.X + 8, Y: rr.Y + 9, Width: 36, Height: 36}
		dot(c, subtleBG, circle)
		g := glyphDesktop
		if isMobileType(peer.DeviceType) {
			g = glyphPhone
		}
		ui.glyph(c, g, 11, ink, circle)
		buttonX := ui.smallButton(c, "revoke|"+peer.DeviceID, "Remover", rr.X+rr.Width-8, rr.Y+11, buttonDanger)
		label(c, peer.DisplayName, ui.font(9, true), ink, rect{X: rr.X + 56, Y: rr.Y + 6, Width: buttonX - rr.X - 60, Height: 22}, oneLine)
		state, stateColor := availability(peer)
		label(c, state, ui.font(8, false), stateColor, rect{X: rr.X + 56, Y: rr.Y + 28, Width: buttonX - rr.X - 60, Height: 20}, oneLine)
	}

	storageY := trusted.Y + trusted.Height + 14
	storageText := "Calculando o espaço usado…"
	if ui.storageBytes >= 0 {
		storageText = "Espaço usado pelo LocalDrop: " + formatNativeBytes(ui.storageBytes)
	}
	storageButton := ui.smallButton(c, "storage-clean", "Limpar", trusted.X+trusted.Width, storageY, buttonSubtle)
	label(c, storageText, ui.font(9, true), ink, rect{X: trusted.X + 4, Y: storageY - 4, Width: storageButton - trusted.X - 8, Height: 22}, oneLine)
	label(c, "Seus arquivos salvos não são apagados.", ui.font(8, false), muted, rect{X: trusted.X + 4, Y: storageY + 16, Width: storageButton - trusted.X - 8, Height: 20}, oneLine)

	quit := rect{X: trusted.X + trusted.Width - 200, Y: l.bottom - 38, Width: 200, Height: 36}
	ui.button(c, "quit", "Encerrar o LocalDrop", "", quit, buttonDanger, true)
	infoRight := quit.X - 12
	if releasesURL != "" {
		infoRight = ui.smallButton(c, "releases", "Ver novidades", infoRight, quit.Y+2, buttonSubtle)
	}
	label(c, "Versão "+version, ui.font(8, false), muted,
		rect{X: trusted.X + 4, Y: quit.Y, Width: infoRight - trusted.X - 8, Height: quit.Height}, oneLine)
}

func (ui *nativeDesktopUI) paintModal(c *painter, b rect) {
	fill(c, rgb(222, 224, 233), b)
	p := strings.SplitN(ui.modal, "|", 2)
	kind, msg := p[0], ""
	if len(p) > 1 {
		msg = p[1]
	}
	r := rect{X: (b.Width - 480) / 2, Y: (b.Height - 230) / 2, Width: 480, Height: 230}
	rounded(c, panel, r, 18)
	title, col := "Aviso", primary
	if kind == "error" {
		title, col = "Algo não saiu como esperado", danger
	} else if kind == "confirm" {
		title, col = "Confirmar", warning
	}
	fill(c, col, rect{X: r.X, Y: r.Y + 18, Width: 5, Height: r.Height - 36})
	label(c, title, ui.font(14, true), ink, rect{X: r.X + 28, Y: r.Y + 24, Width: r.Width - 56, Height: 32}, oneLine)
	label(c, msg, ui.font(10, false), muted, rect{X: r.X + 28, Y: r.Y + 66, Width: r.Width - 56, Height: 92}, wrapped)
	if kind == "confirm" {
		ui.button(c, "modal-cancel", "Cancelar", "", rect{X: r.X + r.Width - 252, Y: r.Y + r.Height - 58, Width: 106, Height: 38}, buttonSubtle, true)
		ui.button(c, "modal-confirm", "Confirmar", "", rect{X: r.X + r.Width - 136, Y: r.Y + r.Height - 58, Width: 110, Height: 38}, buttonPrimary, true)
	} else {
		ui.button(c, "modal-close", "Entendi", "", rect{X: r.X + r.Width - 130, Y: r.Y + r.Height - 58, Width: 104, Height: 38}, buttonPrimary, true)
	}
}

// ---------------------------------------------------------------- estado

func (ui *nativeDesktopUI) alive() bool {
	select {
	case <-ui.closed:
		return false
	default:
		return true
	}
}

func (ui *nativeDesktopUI) synchronize(action func()) {
	if !ui.alive() {
		return
	}
	ui.queue.post(func() {
		if ui.alive() {
			action()
		}
	})
}

func (ui *nativeDesktopUI) setStatus(s string) {
	ui.synchronize(func() { ui.status = s; ui.invalidate() })
}

func (ui *nativeDesktopUI) showError(action string, e error) {
	log.Printf("interface nativa: %s: %v", action, e)
	explained := explainError(e)
	ui.synchronize(func() { ui.modal = "error|" + explained.Title + "\n" + explained.Hint; ui.invalidate() })
}

func (ui *nativeDesktopUI) peerName(id string) string {
	for _, d := range ui.snapshot.Devices {
		if d.DeviceID == id {
			return d.DisplayName
		}
	}
	return "Aparelho removido"
}

func (ui *nativeDesktopUI) downloadDirectory() string {
	if ui.snapshot.Settings.DownloadDirectory != nil {
		return *ui.snapshot.Settings.DownloadDirectory
	}
	return ui.server.downloadDirectory()
}

func (ui *nativeDesktopUI) findTransfer(id string) (TransferView, bool) {
	for _, t := range ui.snapshot.Transfers {
		if t.ID == id {
			return t, true
		}
	}
	return TransferView{}, false
}

func (ui *nativeDesktopUI) clampActivity() {
	maxOffset := len(ui.snapshot.Transfers) - ui.activityRows
	if maxOffset < 0 {
		maxOffset = 0
	}
	if ui.activityOffset > maxOffset {
		ui.activityOffset = maxOffset
	}
	if ui.activityOffset < 0 {
		ui.activityOffset = 0
	}
}

// shown informa se a janela está visível. Escondida na bandeja ou minimizada,
// ela não consulta nem redesenha nada.
func (ui *nativeDesktopUI) shown() bool {
	if ui.main == nil || ui.main.hwnd == 0 {
		return false
	}
	return call(procIsWindowVisible, ui.main.hwnd) != 0 && call(procIsIconic, ui.main.hwnd) == 0
}

// requestRefresh agrupa rajadas de eventos em uma única leitura do estado.
func (ui *nativeDesktopUI) requestRefresh() {
	ui.refreshMu.Lock()
	if ui.refreshPending {
		ui.refreshMu.Unlock()
		return
	}
	ui.refreshPending = true
	ui.refreshMu.Unlock()
	time.AfterFunc(150*time.Millisecond, func() {
		ui.refreshMu.Lock()
		ui.refreshPending = false
		ui.refreshMu.Unlock()
		if !ui.alive() {
			return
		}
		ui.synchronize(func() {
			if !ui.shown() {
				ui.refreshMu.Lock()
				ui.stale = true
				ui.refreshMu.Unlock()
				return
			}
			ui.applySnapshot(ui.client.snapshot())
		})
	})
}

func (ui *nativeDesktopUI) resumeIfStale() {
	ui.refreshMu.Lock()
	stale := ui.stale
	ui.stale = false
	ui.refreshMu.Unlock()
	if stale {
		ui.requestRefresh()
	}
}

func (ui *nativeDesktopUI) applySnapshot(s nativeSnapshot) {
	ui.snapshot = s
	self := ui.client.session.DeviceID
	ui.peers = ui.peers[:0]
	selectedExists := false
	for _, d := range s.Devices {
		if d.DeviceID == self {
			continue
		}
		ui.peers = append(ui.peers, d)
		if d.DeviceID == ui.selectedPeer {
			selectedExists = true
		}
	}
	if !selectedExists {
		ui.selectedPeer = ""
		for _, d := range ui.peers {
			if d.Online {
				ui.selectedPeer = d.DeviceID
				break
			}
		}
		if ui.selectedPeer == "" && len(ui.peers) > 0 {
			ui.selectedPeer = ui.peers[0].DeviceID
		}
	}
	if ui.paired != nil {
		known := false
		for _, d := range ui.peers {
			if d.DeviceID == ui.paired.deviceID {
				known = true
			}
		}
		if !known || time.Since(ui.paired.at) > 2*time.Minute {
			ui.paired = nil
		}
	}
	ui.incomingOffers = ui.incomingOffers[:0]
	now := time.Now()
	if ui.speeds == nil {
		ui.speeds = map[string]transferSpeed{}
	}
	for _, t := range s.Transfers {
		if t.ReceiverID == self && t.State == "WAITING_ACCEPTANCE" {
			ui.incomingOffers = append(ui.incomingOffers, t)
		}
		if t.State != "TRANSFERRING" {
			delete(ui.speeds, t.ID)
			continue
		}
		previous, ok := ui.speeds[t.ID]
		current := transferSpeed{bytes: t.ReceivedBytes, at: now, rate: previous.rate}
		if ok {
			if elapsed := now.Sub(previous.at).Seconds(); elapsed > 0.1 && t.ReceivedBytes >= previous.bytes {
				measured := float64(t.ReceivedBytes-previous.bytes) / elapsed
				if previous.rate == 0 {
					current.rate = measured
				} else {
					current.rate = previous.rate*0.7 + measured*0.3
				}
			} else {
				current = previous
			}
		}
		ui.speeds[t.ID] = current
	}
	online := 0
	for _, d := range ui.peers {
		if d.Online {
			online++
		}
	}
	switch {
	case ui.health.problem():
		ui.headerStatus = "O Windows está bloqueando o celular · veja o aviso abaixo"
	case len(ui.peers) == 0:
		ui.headerStatus = "Nenhum celular conectado ainda"
	case online == 0:
		ui.headerStatus = "Pronto para receber · nenhum aparelho disponível agora"
	case online == 1:
		ui.headerStatus = "Pronto para receber · 1 aparelho disponível"
	default:
		ui.headerStatus = fmt.Sprintf("Pronto para receber · %d aparelhos disponíveis", online)
	}
	ui.clampActivity()
	ui.tray.setTooltip("LocalDrop · " + ui.headerStatus)
	ui.invalidate()
}

// onServerEvent roda na goroutine do servidor: apenas agenda trabalho.
func (ui *nativeDesktopUI) onServerEvent(eventType string, payload any) {
	ui.requestRefresh()
	switch eventType {
	case "PAIRING_REQUESTED", "TRANSFER_OFFERED", "TRANSFER_COMPLETED":
		ui.synchronize(func() { ui.notify(eventType, payload) })
	case "DEVICE_PAIRED":
		data, _ := payload.(map[string]any)
		id, _ := data["deviceId"].(string)
		name, _ := data["displayName"].(string)
		ui.synchronize(func() {
			ui.paired = &pairedNotice{deviceID: id, name: name, at: time.Now()}
			ui.selectedPeer = id
			ui.addressIndex = 0
			if ui.qrPopup != nil && !ui.qrPopup.closed {
				ui.qrPopup.closed = true
				call(procDestroyWindow, ui.qrPopup.hwnd)
			}
			ui.status = name + " foi conectado. Já pode arrastar arquivos para a janela."
			if !ui.shown() || call(procGetForegroundWindow) != ui.main.hwnd {
				ui.tray.balloon("Celular conectado", name+" já pode trocar arquivos com este computador.")
			}
			ui.invalidate()
		})
	}
}

// notify mostra um aviso do Windows quando a janela não está em primeiro plano.
func (ui *nativeDesktopUI) notify(eventType string, payload any) {
	if ui.shown() && call(procGetForegroundWindow) == ui.main.hwnd {
		return
	}
	data, _ := payload.(map[string]any)
	self := ui.client.session.DeviceID
	switch eventType {
	case "PAIRING_REQUESTED":
		name, _ := data["displayName"].(string)
		ui.tray.balloon("Pedido de conexão", name+" quer se conectar. Clique para conferir o código.")
	case "TRANSFER_OFFERED":
		t, ok := data["transfer"].(TransferView)
		if !ok || t.ReceiverID != self || ui.snapshot.Settings.AutoAcceptTrusted {
			return
		}
		ui.tray.balloon("Arquivos chegando", ui.peerName(t.SenderID)+" quer enviar "+transferFileSummary(t.Files)+". Clique para aceitar.")
	case "TRANSFER_COMPLETED":
		t, ok := data["transfer"].(TransferView)
		if !ok || t.ReceiverID != self {
			return
		}
		ui.tray.balloon("Arquivo recebido", transferFileSummary(t.Files)+" foi salvo em "+filepath.Base(ui.downloadDirectory())+".")
	}
}

// checkHealth confere firewall e perfil de rede em segundo plano.
func (ui *nativeDesktopUI) checkHealth(force bool) {
	if ui.healthChecking || (!force && time.Since(ui.healthAt) < 30*time.Second) {
		return
	}
	ui.healthChecking = true
	address := ui.addressURL()
	host := strings.TrimPrefix(address, "http://")
	if index := strings.LastIndex(host, ":"); index > 0 {
		host = host[:index]
	}
	go func() {
		health := checkNetworkHealth(host)
		ui.synchronize(func() {
			ui.healthChecking = false
			ui.healthAt = time.Now()
			ui.health = health
			ui.applySnapshot(ui.snapshot)
			if ui.qrPopup != nil {
				ui.qrPopup.invalidate()
			}
		})
	}()
}

func (ui *nativeDesktopUI) fixNetwork() {
	if ui.fixing {
		return
	}
	ui.fixing = true
	health := ui.health
	ui.invalidate()
	go func() {
		err := fixNetworkAccess(health)
		ui.synchronize(func() {
			ui.fixing = false
			switch {
			case err == errUserDeclined:
				ui.status = "Tudo bem. Sem essa permissão, o celular continua bloqueado."
			case err != nil:
				ui.showError("liberar a rede", err)
			default:
				ui.status = "Pronto. O LocalDrop foi liberado nesta rede."
			}
			ui.checkHealth(true)
			ui.invalidate()
		})
	}()
}

// ---------------------------------------------------------------- ações

func (ui *nativeDesktopUI) chooseFiles() {
	if ui.selectedPeer == "" {
		ui.showMobileConnection()
		return
	}
	paths, ok, err := showOpenDialog(ui.main.hwnd, "Escolher arquivos para enviar", "", false, true)
	if err != nil {
		ui.showError("abrir o seletor de arquivos", err)
	} else if ok {
		ui.sendPaths(paths)
	}
}

// expandDropped transforma pastas soltas na janela nos arquivos que elas contêm.
func expandDropped(paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			result = append(result, path)
			continue
		}
		err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type().IsRegular() {
				result = append(result, current)
			}
			if len(result) > 1000 {
				return fmt.Errorf("a seleção passa de 1000 arquivos")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (ui *nativeDesktopUI) dropFiles(paths []string) {
	if ui.selectedPeer == "" {
		ui.modal = "info|Conecte um celular antes de enviar. Clique em Conectar celular e aponte a câmera do celular para o código."
		ui.invalidate()
		return
	}
	ui.sendPaths(paths)
}

func (ui *nativeDesktopUI) sendPaths(paths []string) {
	receiver := ui.selectedPeer
	name := ui.peerName(receiver)
	go func() {
		files, err := expandDropped(paths)
		if err == nil && len(files) == 0 {
			err = fmt.Errorf("nenhum arquivo foi encontrado na seleção")
		}
		if err != nil {
			ui.showError("preparar o envio", err)
			return
		}
		if _, err := ui.client.offer(receiver, files); err != nil {
			ui.showError("oferecer os arquivos", err)
			return
		}
		count := "1 arquivo pronto"
		if len(files) > 1 {
			count = fmt.Sprintf("%d arquivos prontos", len(files))
		}
		ui.setStatus(count + " para " + name + ". No celular, toque em Aceitar para baixar.")
	}()
}

func (ui *nativeDesktopUI) sendTextValue(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if len(text) > 1<<20 {
		ui.modal = "info|O texto passa de 1 MB. Salve-o em um arquivo e arraste o arquivo para a janela."
		ui.invalidate()
		return
	}
	receiver, name := ui.selectedPeer, ui.peerName(ui.selectedPeer)
	go func() {
		if _, err := ui.client.sendText(receiver, text); err != nil {
			ui.showError("enviar o texto", err)
			return
		}
		ui.setStatus("Texto enviado para " + name + ". No celular, toque em Aceitar para ver.")
	}()
}

func (ui *nativeDesktopUI) pasteText() {
	if ui.selectedPeer == "" {
		return
	}
	text, err := clipboardText(ui.main.hwnd)
	if err != nil || strings.TrimSpace(text) == "" {
		ui.status = "A área de transferência não tem texto. Use Enviar texto para escrever um."
		ui.invalidate()
		return
	}
	ui.sendTextValue(text)
}

func (ui *nativeDesktopUI) runTransfer(id, action string) {
	go func() {
		if e := ui.client.action(id, action); e != nil {
			ui.showError("responder ao envio", e)
		}
	}()
}

func (ui *nativeDesktopUI) savedPath(id string) string {
	t, ok := ui.findTransfer(id)
	if !ok || len(t.Files) == 0 || t.Files[0].SavedPath == nil {
		return ""
	}
	return *t.Files[0].SavedPath
}

func (ui *nativeDesktopUI) copyReceivedText(id string) {
	data, err := os.ReadFile(ui.savedPath(id))
	if err != nil {
		ui.showError("ler o texto recebido", err)
		return
	}
	if !utf8.Valid(data) {
		ui.modal = "info|Esse arquivo não contém texto comum. Use Mostrar na pasta para abri-lo."
		return
	}
	ui.copy(string(data), "Texto recebido copiado. Já pode colar onde quiser.")
}

func (ui *nativeDesktopUI) pair(requestID string, approve bool) {
	var pairing pairingView
	for _, p := range ui.snapshot.Pairings {
		if p.RequestID == requestID {
			pairing = p
		}
	}
	if pairing.RequestID == "" {
		return
	}
	go func() {
		var e error
		if approve {
			e = ui.client.approvePairing(pairing)
		} else {
			e = ui.client.rejectPairing(pairing.RequestID)
		}
		if e != nil {
			ui.showError("responder ao pedido de conexão", e)
			return
		}
		if approve {
			ui.setStatus(pairing.DisplayName + " agora está conectado.")
		}
		ui.requestRefresh()
	}()
}

func (ui *nativeDesktopUI) updateSettings(input settingsUpdate, done string) {
	go func() {
		if _, e := ui.client.updateSettings(input); e != nil {
			ui.showError("salvar o ajuste", e)
		} else {
			ui.setStatus(done)
		}
		ui.requestRefresh()
	}()
}

func (ui *nativeDesktopUI) chooseFolder() {
	v, ok, e := pickDirectory(ui.main.hwnd, ui.downloadDirectory())
	if e != nil {
		ui.showError("escolher a pasta", e)
	} else if ok {
		clean := filepath.Clean(v)
		ui.updateSettings(settingsUpdate{DownloadDirectory: &clean}, "Pasta de recebimento atualizada.")
	}
}

func (ui *nativeDesktopUI) confirmRevoke(id string) {
	name := ui.peerName(id)
	ui.modal = "confirm|Remover " + name + " dos seus aparelhos? Para trocar arquivos de novo, será preciso conectar outra vez pelo QR Code."
	ui.confirmAction = func() { ui.revoke(id, name+" foi removido.") }
}

func (ui *nativeDesktopUI) revoke(id, done string) {
	go func() {
		if e := ui.client.revoke(id); e != nil {
			ui.showError("remover o aparelho", e)
			return
		}
		ui.setStatus(done)
		ui.requestRefresh()
	}()
}

func (ui *nativeDesktopUI) copy(s, msg string) {
	if e := setClipboardText(ui.main.hwnd, s); e != nil {
		ui.showError("copiar o texto", e)
		return
	}
	ui.status = msg
	ui.invalidate()
}

func (ui *nativeDesktopUI) setAutostartChoice(enabled bool) {
	ui.server.markAutostartAsked()
	if err := setAutostart(enabled); err != nil {
		ui.showError("mudar a abertura com o Windows", err)
		return
	}
	ui.autostartOn = autostartEnabled()
	if ui.autostartOn {
		ui.status = "O LocalDrop vai abrir sozinho na bandeja quando o Windows iniciar."
	} else {
		ui.status = "O LocalDrop não vai mais abrir com o Windows."
	}
}

func (ui *nativeDesktopUI) refreshStorage() {
	ui.storageBytes = -1
	go func() {
		size := ui.server.storageUsage()
		ui.synchronize(func() { ui.storageBytes = size; ui.invalidate() })
	}()
}

func splitHit(id string) (string, string) {
	kind, arg, _ := strings.Cut(id, "|")
	return kind, arg
}

func (ui *nativeDesktopUI) activate(id string) {
	kind, arg := splitHit(id)
	switch kind {
	case "home":
		ui.page = "home"
	case "settings":
		ui.page = "settings"
		ui.autostartOn = autostartEnabled()
		ui.refreshStorage()
	case "connect", "connect-chip", "connect-help":
		ui.showMobileConnection()
	case "peer":
		ui.selectedPeer = arg
	case "drop":
		ui.chooseFiles()
	case "send-text":
		ui.showSendText()
	case "pair-approve":
		ui.pair(arg, true)
	case "pair-reject":
		ui.pair(arg, false)
	case "net-fix":
		ui.fixNetwork()
	case "net-settings":
		_ = shellOpen("ms-settings:network-status")
	case "paired-ok":
		ui.paired = nil
	case "paired-undo":
		if ui.paired != nil {
			ui.revoke(ui.paired.deviceID, ui.paired.name+" foi desconectado.")
			ui.paired = nil
		}
	case "autostart-yes":
		ui.setAutostartChoice(true)
	case "autostart-no", "autostart-never":
		ui.server.markAutostartAsked()
	case "autostart":
		ui.setAutostartChoice(!ui.autostartOn)
	case "t-accept":
		ui.runTransfer(arg, "accept")
	case "t-reject":
		ui.runTransfer(arg, "reject")
	case "t-pause":
		ui.runTransfer(arg, "pause")
	case "t-cancel":
		ui.runTransfer(arg, "cancel")
	case "t-open":
		if err := openPath(ui.savedPath(arg)); err != nil {
			ui.showError("abrir o arquivo", err)
		}
	case "t-folder":
		if err := revealPath(ui.savedPath(arg)); err != nil {
			ui.showError("abrir a pasta", err)
		}
	case "t-copy":
		ui.copyReceivedText(arg)
	case "clear-list":
		if cleared := ui.client.clearFinished(); cleared > 0 {
			ui.status = fmt.Sprintf("%d itens tirados da lista. Os arquivos salvos continuam na pasta.", cleared)
			ui.activityOffset = 0
		}
		ui.requestRefresh()
	case "scroll-up":
		ui.activityOffset -= ui.activityRows
		ui.clampActivity()
	case "scroll-down":
		ui.activityOffset += ui.activityRows
		ui.clampActivity()
	case "rename":
		ui.showRename()
	case "folder-change":
		ui.chooseFolder()
	case "folder-open":
		if err := openPath(ui.downloadDirectory()); err != nil {
			ui.showError("abrir a pasta", err)
		}
	case "auto":
		value := !ui.snapshot.Settings.AutoAcceptTrusted
		ui.snapshot.Settings.AutoAcceptTrusted = value
		message := "Arquivos dos seus aparelhos vão pedir confirmação."
		if value {
			message = "Arquivos dos seus aparelhos vão entrar sem perguntar."
		}
		ui.updateSettings(settingsUpdate{AutoAcceptTrusted: &value}, message)
	case "revoke":
		ui.confirmRevoke(arg)
	case "copy-url":
		ui.copy(ui.addressURL(), "Endereço copiado. Cole no navegador do celular.")
	case "storage-clean":
		go func() {
			ui.server.cleanStorage()
			ui.synchronize(func() {
				ui.status = "Cópias temporárias apagadas."
				ui.refreshStorage()
				ui.requestRefresh()
			})
		}()
	case "releases":
		_ = shellOpen(releasesURL)
	case "quit":
		ui.quit()
	case "modal-close", "modal-cancel":
		ui.modal, ui.confirmAction = "", nil
	case "modal-confirm":
		action := ui.confirmAction
		ui.modal, ui.confirmAction = "", nil
		if action != nil {
			action()
		}
	}
	ui.invalidate()
}

func (ui *nativeDesktopUI) wheel(delta int) {
	if ui.page != "home" || ui.modal != "" {
		return
	}
	if delta < 0 {
		ui.activityOffset++
	} else if delta > 0 {
		ui.activityOffset--
	}
	ui.clampActivity()
	ui.invalidate()
}

func (ui *nativeDesktopUI) key(vk int, ctrl, shift bool) bool {
	if ui.modal != "" {
		switch vk {
		case vkEscape:
			ui.activate("modal-cancel")
			return true
		case vkReturn:
			if ui.main.focus == "" || !ui.main.focusVisible {
				if strings.HasPrefix(ui.modal, "confirm|") {
					ui.activate("modal-confirm")
				} else {
					ui.activate("modal-close")
				}
				return true
			}
		}
		return false
	}
	if ctrl {
		switch vk {
		case 'V':
			ui.pasteText()
			return true
		case 'O':
			ui.chooseFiles()
			return true
		}
		return false
	}
	switch vk {
	case vkEscape:
		if ui.page == "settings" {
			ui.activate("home")
			return true
		}
	case vkNext, vkDown:
		if vk == vkNext {
			ui.activityOffset += ui.activityRows
		} else {
			ui.activityOffset++
		}
		ui.clampActivity()
		ui.invalidate()
		return true
	case vkPrior, vkUp:
		if vk == vkPrior {
			ui.activityOffset -= ui.activityRows
		} else {
			ui.activityOffset--
		}
		ui.clampActivity()
		ui.invalidate()
		return true
	}
	return false
}

// ---------------------------------------------------------------- janelas modais

// popupButtons desenha os botões de rodapé de uma janela modal, da direita para
// a esquerda, e devolve onde o primeiro ficou.
type popupButton struct {
	id, text string
	kind     buttonKind
}

func (ui *nativeDesktopUI) paintPopupButtons(c *painter, buttons []popupButton, right, y int) int {
	for index := len(buttons) - 1; index >= 0; index-- {
		button := buttons[index]
		w := ui.textWidth(c, button.text, ui.font(9, true)) + 36
		if w < 100 {
			w = 100
		}
		ui.button(c, button.id, button.text, "", rect{X: right - w, Y: y, Width: w, Height: 38}, button.kind, true)
		right -= w + 10
	}
	return right
}

func (ui *nativeDesktopUI) newPopup(title string, width, height int) (*canvasWindow, error) {
	popup := &canvasWindow{cache: ui.cache, background: canvasBG}
	style := uint32(wsPopup | wsCaption | wsSysMenu)
	if err := createCanvasWindow(popup, popupWindowClass, title+" · LocalDrop", style, wsExDlgModalFrame, ui.main.hwnd, width, height); err != nil {
		return nil, err
	}
	applyLocalDropWindowFrame(popup.hwnd)
	return popup, nil
}

func (ui *nativeDesktopUI) showMobileConnection() {
	if ui.qrPopup != nil && !ui.qrPopup.closed {
		call(procSetForegroundWindow, ui.qrPopup.hwnd)
		return
	}
	popup, err := ui.newPopup("Conectar celular", 500, 690)
	if err != nil {
		ui.showError("abrir a conexão do celular", err)
		return
	}
	ui.qrPopup = popup
	opened := time.Now()
	ui.checkHealth(false)
	popup.paint = func(c *painter, b rect) {
		ui.target = popup
		r := rect{X: 18, Y: 16, Width: b.Width - 36, Height: b.Height - 32}
		rounded(c, panel, r, 18)
		label(c, "Conectar celular", ui.font(17, true), ink, rect{X: r.X + 28, Y: r.Y + 20, Width: r.Width - 56, Height: 34}, oneLine)
		label(c, "Abra a câmera do celular e aponte para o código. O celular precisa estar no mesmo Wi-Fi deste computador.", ui.font(9, false), muted, rect{X: r.X + 28, Y: r.Y + 56, Width: r.Width - 56, Height: 46}, wrapped)
		qr := rect{X: (b.Width - 280) / 2, Y: r.Y + 108, Width: 280, Height: 280}
		outline(c, line, qr, 14)
		ui.paintQR(c, qr)
		_, expires := ui.inviteURL()
		remaining := time.Until(expires).Round(time.Minute)
		validity := "Este código funciona uma única vez."
		if remaining >= time.Minute {
			validity = fmt.Sprintf("Este código funciona uma única vez e vale por mais %d min.", int(remaining.Minutes()))
		}
		label(c, validity, ui.font(8, false), muted, rect{X: r.X + 20, Y: qr.Y + qr.Height + 8, Width: r.Width - 40, Height: 20}, centeredLine)
		label(c, "Sem câmera? Digite no navegador do celular:", ui.font(8, false), muted, rect{X: r.X + 20, Y: qr.Y + qr.Height + 34, Width: r.Width - 40, Height: 20}, centeredLine)
		label(c, ui.addressURL(), ui.font(11, true), primary, rect{X: r.X + 20, Y: qr.Y + qr.Height + 54, Width: r.Width - 40, Height: 26}, centeredLine)

		box := rect{X: r.X + 24, Y: qr.Y + qr.Height + 90, Width: r.Width - 48, Height: 104}
		switch {
		case ui.health.problem() || ui.fixing:
			rounded(c, warningSoft, box, 12)
			title := "O Windows está bloqueando o celular"
			detail := "O firewall não liberou o LocalDrop. Clique em Corrigir e confirme o aviso do Windows."
			if ui.health.PublicNetwork {
				detail = "Este Wi-Fi está como rede pública. Clique em Corrigir para deixá-lo como rede particular e liberar o LocalDrop."
			}
			if ui.fixing {
				title, detail = "Aguardando a permissão do Windows…", "Confirme no aviso do Windows que apareceu na tela."
			}
			label(c, title, ui.font(9, true), warning, rect{X: box.X + 16, Y: box.Y + 10, Width: box.Width - 32, Height: 22}, oneLine)
			label(c, detail, ui.font(8, false), ink, rect{X: box.X + 16, Y: box.Y + 34, Width: box.Width - 150, Height: 62}, wrapped)
			if !ui.fixing {
				ui.button(c, "net-fix", "Corrigir", "", rect{X: box.X + box.Width - 120, Y: box.Y + box.Height - 46, Width: 104, Height: 34}, buttonPrimary, true)
			}
		case time.Since(opened) > 60*time.Second && ui.server.lastRemoteRequest().Before(opened):
			rounded(c, subtleBG, box, 12)
			label(c, "Não está conectando? Confira:", ui.font(9, true), ink, rect{X: box.X + 16, Y: box.Y + 8, Width: box.Width - 32, Height: 22}, oneLine)
			tips := "• O celular está no mesmo Wi-Fi (não na rede de convidados nem nos dados móveis).\n• Se o computador ou o celular tiver VPN ligada, desligue.\n• Ainda não deu? Clique em Outro endereço e escaneie de novo."
			label(c, tips, ui.font(8, false), ink, rect{X: box.X + 16, Y: box.Y + 32, Width: box.Width - 32, Height: 70}, wrapped)
		}

		buttons := []popupButton{{"copy", "Copiar endereço", buttonSubtle}}
		if len(ui.server.addresses()) > 1 {
			buttons = append(buttons, popupButton{"other", "Outro endereço", buttonSubtle})
		}
		buttons = append(buttons, popupButton{"close", "Pronto", buttonPrimary})
		ui.paintPopupButtons(c, buttons, r.X+r.Width-24, r.Y+r.Height-60)
	}
	popup.activate = func(id string) {
		switch id {
		case "copy":
			ui.copy(ui.addressURL(), "Endereço copiado. Cole no navegador do celular.")
		case "other":
			ui.addressIndex++
			ui.checkHealth(true)
		case "net-fix":
			ui.fixNetwork()
		case "close":
			popup.closed = true
			call(procDestroyWindow, popup.hwnd)
			return
		}
		popup.invalidate()
	}
	popup.key = func(vk int, _, _ bool) bool {
		if vk == vkEscape {
			popup.activate("close")
			return true
		}
		return false
	}
	popup.message = func(message uint32, _, _ uintptr) (uintptr, bool) {
		if message == wmTimer {
			popup.invalidate()
			return 0, true
		}
		return 0, false
	}
	call(procSetTimer, popup.hwnd, timerPopup, 1000, 0)
	runModal(ui.main.hwnd, popup, nil)
	ui.qrPopup = nil
	ui.invalidate()
}

// textPrompt abre uma janela com um campo de texto e devolve o que foi digitado.
func (ui *nativeDesktopUI) textPrompt(title, prompt, initial, accept string, multiline bool, limit int) (string, bool) {
	width, height := 440, 210
	if multiline {
		width, height = 560, 420
	}
	popup, err := ui.newPopup(title, width, height)
	if err != nil {
		ui.showError("abrir a janela", err)
		return "", false
	}
	editArea := rect{X: 32, Y: 70, Width: width - 64, Height: 34}
	if multiline {
		editArea.Height = height - 70 - 82
	}
	accepted := false
	var edit *editField
	finish := func(ok bool) {
		if popup.closed {
			return
		}
		accepted = ok
		popup.closed = true
	}
	popup.paint = func(c *painter, b rect) {
		ui.target = popup
		label(c, prompt, ui.font(10, true), ink, rect{X: 32, Y: 26, Width: b.Width - 64, Height: 30}, oneLine)
		frame := rect{X: editArea.X - 4, Y: editArea.Y - 4, Width: editArea.Width + 8, Height: editArea.Height + 8}
		rounded(c, white, frame, 10)
		border := line
		if edit != nil && call(procGetFocus) == edit.hwnd {
			border = primary
		}
		outline(c, border, frame, 10)
		popup.add("edit", frame)
		hint := ""
		if multiline {
			hint = "Ctrl+Enter também envia."
		}
		label(c, hint, ui.font(8, false), muted, rect{X: 32, Y: b.Height - 52, Width: 200, Height: 38}, oneLine)
		ui.paintPopupButtons(c, []popupButton{{"cancel", "Cancelar", buttonSubtle}, {"accept", accept, buttonPrimary}}, b.Width-32, b.Height-56)
	}
	popup.activate = func(id string) {
		switch id {
		case "cancel":
			finish(false)
		case "accept":
			finish(true)
		case "edit":
			edit.focus(false)
		}
	}
	popup.key = func(vk int, _, shift bool) bool {
		if vk == vkEscape {
			finish(false)
			return true
		}
		if vk == vkTab {
			popup.moveFocus(shift)
			if popup.focus == "edit" {
				popup.focusVisible = false
				edit.focus(false)
			}
			popup.invalidate()
			return true
		}
		return false
	}
	popup.message = func(message uint32, wParam, _ uintptr) (uintptr, bool) {
		switch message {
		case wmCtlColorEdit:
			call(procSetBkColor, wParam, uintptr(white))
			call(procSetTextColor, wParam, uintptr(ink))
			return ui.cache.brush(white), true
		case wmCommand:
			popup.invalidate()
		}
		return 0, false
	}
	edit = newEditField(popup, editArea, initial, multiline, limit, ui.font(10, false))
	if edit == nil {
		popup.closed = true
		call(procDestroyWindow, popup.hwnd)
		return "", false
	}
	filter := func(m *msg) bool {
		if m.Hwnd != edit.hwnd || m.Message != wmKeyDown {
			return false
		}
		switch int(m.WParam) {
		case vkEscape:
			finish(false)
			return true
		case vkReturn:
			if !multiline || keyDown(vkControl) {
				finish(true)
				return true
			}
		case vkTab:
			call(procSetFocus, popup.hwnd)
			popup.focus = "edit"
			popup.moveFocus(keyDown(vkShift))
			if popup.focus == "edit" {
				popup.moveFocus(false)
			}
			return true
		}
		return false
	}
	call(procShowWindow, popup.hwnd, swShowNormal)
	edit.focus(true)
	var value string
	popupLoop(ui.main.hwnd, popup, filter, func() { value = edit.text() })
	return value, accepted
}

// popupLoop roda a janela modal e lê o resultado antes de ela ser destruída.
func popupLoop(owner uintptr, popup *canvasWindow, filter func(m *msg) bool, read func()) {
	call(procEnableWindow, owner, 0)
	var m msg
	for !popup.closed {
		result := int32(call(procGetMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0))
		if result == 0 {
			call(procPostQuitMessage, m.WParam)
			break
		}
		if result < 0 {
			break
		}
		if filter != nil && filter(&m) {
			continue
		}
		call(procTranslateMessage, uintptr(unsafe.Pointer(&m)))
		call(procDispatchMessageW, uintptr(unsafe.Pointer(&m)))
	}
	if read != nil {
		read()
	}
	call(procEnableWindow, owner, 1)
	popup.closed = true
	if windowHandlers[popup.hwnd] != nil {
		call(procDestroyWindow, popup.hwnd)
	}
	call(procSetForegroundWindow, owner)
}

func (ui *nativeDesktopUI) showRename() {
	current := ui.snapshot.Settings.Device.DisplayName
	name, ok := ui.textPrompt("Renomear este computador", "Nome que aparece no celular:", current, "Salvar", false, 60)
	name = strings.TrimSpace(name)
	if ok && name != "" && name != current {
		ui.updateSettings(settingsUpdate{DisplayName: &name}, "Nome atualizado.")
	}
	ui.invalidate()
}

func (ui *nativeDesktopUI) showSendText() {
	if ui.selectedPeer == "" {
		ui.showMobileConnection()
		return
	}
	name := ui.peerName(ui.selectedPeer)
	text, ok := ui.textPrompt("Enviar texto", "Texto ou link para "+name+":", "", "Enviar", true, 1_000_000)
	if ok {
		ui.sendTextValue(strings.ReplaceAll(text, "\r\n", "\n"))
	}
	ui.invalidate()
}

// ---------------------------------------------------------------- janela e bandeja

func (ui *nativeDesktopUI) showWindow() {
	if ui.main == nil {
		return
	}
	hwnd := ui.main.hwnd
	call(procShowWindow, hwnd, swShow)
	if call(procIsIconic, hwnd) != 0 {
		call(procShowWindow, hwnd, swRestore)
	}
	call(procSetForegroundWindow, hwnd)
	ui.resumeIfStale()
	ui.requestRefresh()
	ui.checkHealth(false)
}

func (ui *nativeDesktopUI) hideToTray() {
	call(procShowWindow, ui.main.hwnd, swHide)
	ui.server.dropInvite()
	marker := filepath.Join(ui.server.cfg.StorageRoot, ".tray-hint")
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		_ = os.WriteFile(marker, []byte("1"), 0o600)
		ui.tray.balloon("O LocalDrop continua aberto", "Ele segue pronto para receber arquivos aqui na bandeja. Para fechar de vez, clique com o botão direito no ícone.")
	}
	// Sem nada para desenhar, o buffer da tela e a memória livre voltam ao Windows.
	ui.main.buffer.release()
	trimMemory()
}

func (ui *nativeDesktopUI) quit() {
	ui.quitting = true
	call(procDestroyWindow, ui.main.hwnd)
}

func (ui *nativeDesktopUI) trayMessage(lParam uintptr) {
	switch uint32(lParam) {
	case wmLButtonUp, ninBalloonClk:
		ui.showWindow()
	case wmRButtonUp, wmContextMenu:
		switch popupMenu(ui.main.hwnd, []menuItem{{"Abrir o LocalDrop", 1}, {"Conectar celular", 2}, {"", 0}, {"Encerrar", 3}}) {
		case 1:
			ui.showWindow()
		case 2:
			ui.showWindow()
			ui.showMobileConnection()
		case 3:
			ui.quit()
		}
	}
}

func (ui *nativeDesktopUI) mainMessage(message uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch message {
	case msgSynchronize:
		ui.queue.drain()
		return 0, true
	case msgTray:
		ui.trayMessage(lParam)
		return 0, true
	case wmActivate:
		if loword(wParam) != 0 {
			ui.resumeIfStale()
		}
	case wmSize:
		if wParam != 1 /* SIZE_MINIMIZED */ {
			ui.resumeIfStale()
		}
	case wmDestroy:
		ui.closeOnce.Do(func() { close(ui.closed) })
		ui.tray.remove()
		call(procPostQuitMessage, 0)
	}
	if message == msgTaskbarCreate && msgTaskbarCreate != 0 {
		ui.tray.add()
		return 0, true
	}
	if message == msgShowRequest && msgShowRequest != 0 {
		ui.showWindow()
		return 0, true
	}
	return 0, false
}

func (ui *nativeDesktopUI) build(startHidden bool) error {
	ui.page = "home"
	ui.status = "Arraste arquivos para a janela, ou use Enviar texto para mandar um recado."
	ui.closed = make(chan struct{})
	ui.cache = newGDICache()
	ui.storageBytes = -1
	ui.appIcon = loadAppIcon(32)
	ui.appIconSmall = loadAppIcon(16)
	if err := registerClasses(ui.appIcon, ui.appIconSmall); err != nil {
		return err
	}
	ui.main = &canvasWindow{cache: ui.cache, background: canvasBG, minW: 764, minH: 561}
	ui.target = ui.main
	ui.main.paint = ui.paint
	ui.main.activate = ui.activate
	ui.main.key = ui.key
	ui.main.wheel = ui.wheel
	ui.main.message = ui.mainMessage
	ui.main.dropped = ui.dropFiles
	ui.main.close = func() bool {
		if !ui.quitting && ui.tray.added {
			ui.hideToTray()
			return false
		}
		return true
	}
	if err := createCanvasWindow(ui.main, mainWindowClass, nativeWindowTitle, wsOverlappedWindow, wsExAcceptFiles, 0, 984, 681); err != nil {
		return err
	}
	ui.queue.mu.Lock()
	ui.queue.hwnd = ui.main.hwnd
	ui.queue.mu.Unlock()
	call(procDragAcceptFiles, ui.main.hwnd, 1)
	applyLocalDropWindowFrame(ui.main.hwnd)
	ui.tray = trayIcon{hwnd: ui.main.hwnd, icon: ui.appIconSmall, tooltip: "LocalDrop"}
	if !ui.tray.add() {
		log.Printf("ícone da bandeja indisponível")
	}
	ui.autostartOn = autostartEnabled()
	refreshAutostart()
	ui.applySnapshot(ui.client.snapshot())
	if !startHidden || !ui.tray.added {
		call(procShowWindow, ui.main.hwnd, swShowDefault)
		call(procUpdateWindow, ui.main.hwnd)
		call(procSetForegroundWindow, ui.main.hwnd)
	} else {
		ui.stale = true
		trimMemory()
	}
	// A primeira conferência de firewall espera a janela aparecer.
	time.AfterFunc(3*time.Second, func() { ui.synchronize(func() { ui.checkHealth(true) }) })
	return nil
}

func (ui *nativeDesktopUI) run() {
	ui.server.hub.SetLocalListener(ui.client.session.DeviceID, ui.onServerEvent)
	defer ui.server.hub.SetLocalListener("", nil)
	go func() {
		// Rede de segurança: estados sem evento próprio (aparelho que deixou de
		// responder, horários relativos) são revistos a cada 30 segundos.
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ui.closed:
				return
			case <-t.C:
				ui.requestRefresh()
			}
		}
	}()
	runMessageLoop()
}

func (ui *nativeDesktopUI) dispose() {
	ui.tray.remove()
	for _, icons := range ui.navIcons {
		disposeNativeNavIcons(icons)
	}
	if ui.cache != nil {
		ui.cache.dispose()
	}
	if ui.appIcon != 0 {
		call(procDestroyIcon, ui.appIcon)
	}
	if ui.appIconSmall != 0 {
		call(procDestroyIcon, ui.appIconSmall)
	}
}

func showDesktopPanel(server *Server, startHidden bool) bool {
	lockUIThread()
	client, e := newNativeHostClient(server)
	if e != nil {
		log.Printf("criar sessão da janela: %v", e)
		return false
	}
	ui := &nativeDesktopUI{server: server, client: client}
	if e = ui.build(startHidden); e != nil {
		ui.dispose()
		log.Printf("criar janela: %v", e)
		return false
	}
	ui.run()
	ui.dispose()
	return true
}
