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

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	qrcode "github.com/skip2/go-qrcode"
)

const nativeWindowTitle = "LocalDrop"

type nativeHit struct {
	id string
	r  walk.Rectangle
}

// canvasLayout descreve a coluna de conteúdo abaixo do cabeçalho.
type canvasLayout struct {
	bounds              walk.Rectangle
	header              int
	left, width         int
	top, bottom, status int
}

// computeCanvasLayout é pura: toda a geometria parte daqui, então redimensionar,
// maximizar ou mudar o DPI nunca reaproveita medidas antigas.
func computeCanvasLayout(b walk.Rectangle) canvasLayout {
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
	header := 72
	top := header + 20
	bottom := b.Height - 34
	if bottom < top {
		bottom = top
	}
	return canvasLayout{bounds: b, header: header, left: left, width: width, top: top, bottom: bottom, status: b.Height - 26}
}

type homeLayout struct {
	banners  []walk.Rectangle
	devices  walk.Rectangle
	drop     walk.Rectangle
	activity walk.Rectangle
}

// computeHomeLayout empilha avisos, destinos, área de envio e atividade. A
// atividade ocupa o que sobrar até o rodapé.
func computeHomeLayout(l canvasLayout, banners int, onboarding bool) homeLayout {
	var result homeLayout
	y := l.top
	for index := 0; index < banners; index++ {
		result.banners = append(result.banners, walk.Rectangle{X: l.left, Y: y, Width: l.width, Height: 64})
		y += 76
	}
	devicesHeight := 82
	if onboarding {
		devicesHeight = 216
	}
	result.devices = walk.Rectangle{X: l.left, Y: y, Width: l.width, Height: devicesHeight}
	y += devicesHeight + 16
	dropHeight := 116
	if l.bottom-y < dropHeight+16+180 {
		dropHeight = 84
	}
	result.drop = walk.Rectangle{X: l.left, Y: y, Width: l.width, Height: dropHeight}
	y += dropHeight + 16
	height := l.bottom - y
	if height < 0 {
		height = 0
	}
	result.activity = walk.Rectangle{X: l.left, Y: y, Width: l.width, Height: height}
	return result
}

func edgeToEdgeCanvasLayout() VBox {
	return VBox{MarginsZero: true, SpacingZero: true}
}

var (
	canvasBG    = walk.RGB(243, 245, 250)
	ink         = walk.RGB(27, 31, 51)
	muted       = walk.RGB(104, 112, 139)
	sidebar     = walk.RGB(25, 28, 49)
	sidebarSoft = walk.RGB(43, 47, 79)
	headerMuted = walk.RGB(164, 170, 208)
	primary     = walk.RGB(100, 86, 231)
	primaryDark = walk.RGB(79, 66, 197)
	primarySoft = walk.RGB(238, 235, 255)
	panel       = walk.RGB(255, 255, 255)
	line        = walk.RGB(228, 231, 241)
	success     = walk.RGB(27, 148, 105)
	successSoft = walk.RGB(225, 245, 236)
	warning     = walk.RGB(204, 128, 26)
	warningSoft = walk.RGB(253, 242, 224)
	danger      = walk.RGB(213, 72, 85)
	dangerSoft  = walk.RGB(252, 233, 235)
	subtleBG    = walk.RGB(236, 238, 247)
	white       = walk.RGB(255, 255, 255)
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
)

type buttonKind int

const (
	buttonPrimary buttonKind = iota
	buttonSubtle
	buttonDanger
	buttonHeader
)

type transferSpeed struct {
	bytes int64
	at    time.Time
	rate  float64
}

type nativeDesktopUI struct {
	server   *Server
	client   *nativeClient
	window   *walk.MainWindow
	canvas   *walk.CustomWidget
	tray     *walk.NotifyIcon
	appIcon  *walk.Icon
	navIcons map[string]nativeNavIcon
	qr       *walk.Bitmap
	qrSmall  *walk.Bitmap
	qrURL    string
	fonts    map[int]*walk.Font
	glyphs   map[int]*walk.Font

	snapshot       nativeSnapshot
	peers          []DeviceView
	incomingOffers []TransferView
	selectedPeer   string
	page, status   string
	headerStatus   string
	activityOffset int
	activityRows   int
	speeds         map[string]transferSpeed

	hits                  []nativeHit
	hover, pressed, modal string
	confirmAction         func()

	refreshMu      sync.Mutex
	refreshPending bool
	stale          bool

	closed    chan struct{}
	closeOnce sync.Once
	quitting  bool
}

func translateTransferState(s string) string {
	m := map[string]string{"CREATED": "Criada", "WAITING_ACCEPTANCE": "Aguardando", "ACCEPTED": "Aceita", "TRANSFERRING": "Transferindo", "PAUSED": "Pausada", "VERIFYING": "Verificando", "COMPLETED": "Concluída", "REJECTED": "Recusada", "CANCELLED": "Cancelada", "FAILED": "Falhou"}
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

// ---------------------------------------------------------------- desenho

func (ui *nativeDesktopUI) font(size int, bold bool) *walk.Font {
	k := size
	if bold {
		k = -k
	}
	if f := ui.fonts[k]; f != nil {
		return f
	}
	style := walk.FontStyle(0)
	if bold {
		style = walk.FontBold
	}
	f, e := walk.NewFont("Segoe UI", size, style)
	if e == nil {
		ui.fonts[k] = f
	}
	return f
}

func (ui *nativeDesktopUI) glyphFont(size int) *walk.Font {
	if f := ui.glyphs[size]; f != nil {
		return f
	}
	f, e := walk.NewFont("Segoe MDL2 Assets", size, 0)
	if e == nil {
		ui.glyphs[size] = f
	}
	return f
}

func fill(c *walk.Canvas, col walk.Color, r walk.Rectangle) {
	b, e := walk.NewSolidColorBrush(col)
	if e == nil {
		defer b.Dispose()
		_ = c.FillRectangle(b, r)
	}
}

func rounded(c *walk.Canvas, col walk.Color, r walk.Rectangle, rad int) {
	b, e := walk.NewSolidColorBrush(col)
	if e == nil {
		defer b.Dispose()
		_ = c.FillRoundedRectangle(b, r, walk.Size{Width: rad, Height: rad})
	}
}

func outline(c *walk.Canvas, col walk.Color, r walk.Rectangle, rad int) {
	p, e := walk.NewCosmeticPen(walk.PenSolid, col)
	if e == nil {
		defer p.Dispose()
		_ = c.DrawRoundedRectangle(p, r, walk.Size{Width: rad, Height: rad})
	}
}

func dashedOutline(c *walk.Canvas, col walk.Color, r walk.Rectangle, rad int) {
	p, e := walk.NewCosmeticPen(walk.PenDash, col)
	if e == nil {
		defer p.Dispose()
		_ = c.DrawRoundedRectangle(p, r, walk.Size{Width: rad, Height: rad})
	}
}

func dot(c *walk.Canvas, col walk.Color, r walk.Rectangle) {
	b, e := walk.NewSolidColorBrush(col)
	if e == nil {
		defer b.Dispose()
		_ = c.FillEllipse(b, r)
	}
}

func label(c *walk.Canvas, s string, f *walk.Font, col walk.Color, r walk.Rectangle, format walk.DrawTextFormat) {
	if f != nil && r.Width > 0 && r.Height > 0 {
		_ = c.DrawText(s, f, col, r, format)
	}
}

const (
	oneLine      = walk.TextVCenter | walk.TextSingleLine | walk.TextEndEllipsis
	centeredLine = walk.TextCenter | walk.TextVCenter | walk.TextSingleLine
)

func inset(r walk.Rectangle, l, t, rr, b int) walk.Rectangle {
	return walk.Rectangle{X: r.X + l, Y: r.Y + t, Width: r.Width - l - rr, Height: r.Height - t - b}
}

func inside(r walk.Rectangle, x, y int) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.Width && y < r.Y+r.Height
}

func (ui *nativeDesktopUI) add(id string, r walk.Rectangle) {
	ui.hits = append(ui.hits, nativeHit{id, r})
}

func (ui *nativeDesktopUI) hit(x, y int) string {
	for i := len(ui.hits) - 1; i >= 0; i-- {
		if inside(ui.hits[i].r, x, y) {
			return ui.hits[i].id
		}
	}
	return ""
}

func (ui *nativeDesktopUI) invalidate() {
	if ui.canvas != nil {
		ui.canvas.Invalidate()
	}
}

func (ui *nativeDesktopUI) textWidth(c *walk.Canvas, s string, f *walk.Font) int {
	if f == nil {
		return len(s) * 7
	}
	// Sem TextCalcRect o walk devolve a própria largura de entrada.
	measured, _, err := c.MeasureText(s, f, walk.Rectangle{Width: 2000, Height: 100}, walk.TextSingleLine|walk.TextCalcRect)
	if err != nil {
		return len(s) * 7
	}
	return measured.Width
}

func (ui *nativeDesktopUI) glyph(c *walk.Canvas, g string, size int, col walk.Color, r walk.Rectangle) {
	label(c, g, ui.glyphFont(size), col, r, centeredLine)
}

func (ui *nativeDesktopUI) card(c *walk.Canvas, r walk.Rectangle) {
	rounded(c, walk.RGB(232, 235, 245), walk.Rectangle{X: r.X, Y: r.Y + 2, Width: r.Width, Height: r.Height}, 16)
	rounded(c, panel, r, 16)
}

// button desenha um botão com texto e glifo opcional e devolve a largura usada.
func (ui *nativeDesktopUI) button(c *walk.Canvas, id, text, g string, r walk.Rectangle, kind buttonKind, enabled bool) {
	bg, fg, hover := subtleBG, ink, walk.RGB(224, 227, 242)
	switch kind {
	case buttonPrimary:
		bg, fg, hover = primary, white, primaryDark
	case buttonDanger:
		bg, fg, hover = dangerSoft, danger, walk.RGB(248, 215, 220)
	case buttonHeader:
		bg, fg, hover = sidebarSoft, white, walk.RGB(56, 61, 99)
	}
	if !enabled {
		bg, fg = walk.RGB(234, 236, 243), walk.RGB(160, 165, 182)
		if kind == buttonHeader {
			bg, fg = sidebarSoft, headerMuted
		}
	} else if ui.hover == id || ui.pressed == id {
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
		label(c, g, ui.glyphFont(10), fg, walk.Rectangle{X: x, Y: r.Y, Width: 16, Height: r.Height}, centeredLine)
		label(c, text, f, fg, walk.Rectangle{X: x + 24, Y: r.Y, Width: tw + 4, Height: r.Height}, walk.TextVCenter|walk.TextSingleLine)
	}
	if enabled {
		ui.add(id, r)
	}
}

// smallButton ajusta a largura ao texto e é desenhado da direita para a esquerda.
func (ui *nativeDesktopUI) smallButton(c *walk.Canvas, id, text string, right, y int, kind buttonKind) int {
	w := ui.textWidth(c, text, ui.font(9, true)) + 26
	ui.button(c, id, text, "", walk.Rectangle{X: right - w, Y: y, Width: w, Height: 30}, kind, true)
	return right - w - 8
}

func (ui *nativeDesktopUI) pill(c *walk.Canvas, text string, fg, bg walk.Color, right, y int) int {
	f := ui.font(8, true)
	w := ui.textWidth(c, text, f) + 22
	r := walk.Rectangle{X: right - w, Y: y, Width: w, Height: 24}
	rounded(c, bg, r, 12)
	label(c, text, f, fg, r, centeredLine)
	return r.X
}

func (ui *nativeDesktopUI) toggle(c *walk.Canvas, id string, on bool, r walk.Rectangle) {
	track := walk.RGB(204, 208, 222)
	if on {
		track = primary
	}
	rounded(c, track, r, r.Height)
	knob := walk.Rectangle{X: r.X + 3, Y: r.Y + 3, Width: r.Height - 6, Height: r.Height - 6}
	if on {
		knob.X = r.X + r.Width - r.Height + 3
	}
	dot(c, white, knob)
	ui.add(id, r)
}

func (ui *nativeDesktopUI) paint(c *walk.Canvas, _ walk.Rectangle) error {
	ui.paintBounds(c, ui.canvas.ClientBounds())
	return nil
}

func (ui *nativeDesktopUI) paintBounds(c *walk.Canvas, b walk.Rectangle) {
	l := computeCanvasLayout(b)
	ui.hits = ui.hits[:0]
	fill(c, canvasBG, b)
	ui.paintHeader(c, l)
	if ui.page == "settings" {
		ui.paintSettings(c, l)
	} else {
		ui.paintHome(c, l)
	}
	label(c, ui.status, ui.font(8, false), muted, walk.Rectangle{X: l.left, Y: l.status, Width: l.width, Height: 18}, oneLine)
	if ui.modal != "" {
		ui.hits = ui.hits[:0]
		ui.paintModal(c, b)
	}
}

func (ui *nativeDesktopUI) paintHeader(c *walk.Canvas, l canvasLayout) {
	fill(c, sidebar, walk.Rectangle{Width: l.bounds.Width, Height: l.header})
	logo := walk.Rectangle{X: l.left, Y: 17, Width: 38, Height: 38}
	rounded(c, primary, logo, 12)
	if icon := ui.navIcons["nav-history"].active; icon != nil {
		_ = c.DrawImageStretched(icon, inset(logo, 8, 8, 8, 8))
	}
	label(c, "LocalDrop", ui.font(13, true), white, walk.Rectangle{X: logo.X + 50, Y: 14, Width: 260, Height: 24}, oneLine)
	statusColor := success
	if len(ui.peers) == 0 {
		statusColor = warning
	}
	dot(c, statusColor, walk.Rectangle{X: logo.X + 51, Y: 43, Width: 8, Height: 8})
	label(c, ui.headerStatus, ui.font(8, false), headerMuted, walk.Rectangle{X: logo.X + 65, Y: 37, Width: l.width/2 - 60, Height: 20}, oneLine)

	right := l.left + l.width
	gear := walk.Rectangle{X: right - 40, Y: 16, Width: 40, Height: 40}
	gearID, bg := "settings", sidebarSoft
	if ui.page == "settings" {
		gearID, bg = "home", primary
	} else if ui.hover == gearID {
		bg = walk.RGB(56, 61, 99)
	}
	rounded(c, bg, gear, 10)
	if icon := ui.navIcons["nav-settings"].active; icon != nil {
		_ = c.DrawImageStretched(icon, inset(gear, 10, 10, 10, 10))
	}
	ui.add(gearID, gear)
	ui.button(c, "connect", "Conectar celular", glyphQR, walk.Rectangle{X: gear.X - 186, Y: 16, Width: 176, Height: 40}, buttonPrimary, true)
}

// ---------------------------------------------------------------- início

func (ui *nativeDesktopUI) paintHome(c *walk.Canvas, l canvasLayout) {
	pairings := ui.snapshot.Pairings
	if len(pairings) > 2 {
		pairings = pairings[:2]
	}
	offers := ui.incomingOffers
	if len(offers) > 2-len(pairings) {
		offers = offers[:2-len(pairings)]
	}
	layout := computeHomeLayout(l, len(pairings)+len(offers), len(ui.peers) == 0)
	index := 0
	for _, pairing := range pairings {
		ui.paintPairingBanner(c, layout.banners[index], pairing)
		index++
	}
	for _, offer := range offers {
		ui.paintOfferBanner(c, layout.banners[index], offer)
		index++
	}
	if len(ui.peers) == 0 {
		ui.paintOnboarding(c, layout.devices)
	} else {
		ui.paintDevices(c, layout.devices)
	}
	ui.paintDrop(c, layout.drop)
	ui.paintActivity(c, layout.activity)
}

func (ui *nativeDesktopUI) banner(c *walk.Canvas, r walk.Rectangle, accent, soft walk.Color, g, title, detail string) {
	ui.card(c, r)
	fill(c, accent, walk.Rectangle{X: r.X, Y: r.Y + 12, Width: 4, Height: r.Height - 24})
	circle := walk.Rectangle{X: r.X + 18, Y: r.Y + 14, Width: 36, Height: 36}
	dot(c, soft, circle)
	ui.glyph(c, g, 12, accent, circle)
	label(c, title, ui.font(10, true), ink, walk.Rectangle{X: r.X + 66, Y: r.Y + 11, Width: r.Width - 300, Height: 22}, oneLine)
	label(c, detail, ui.font(8, false), muted, walk.Rectangle{X: r.X + 66, Y: r.Y + 33, Width: r.Width - 300, Height: 20}, oneLine)
}

func (ui *nativeDesktopUI) paintPairingBanner(c *walk.Canvas, r walk.Rectangle, p pairingView) {
	ui.banner(c, r, warning, warningSoft, glyphShield, p.DisplayName+" quer se conectar",
		"Confira se o celular mostra o código "+p.Code+" antes de confiar.")
	x := r.X + r.Width - 16
	x = ui.smallButton(c, "pair-approve|"+p.RequestID, "Confiar", x, r.Y+17, buttonPrimary)
	ui.smallButton(c, "pair-reject|"+p.RequestID, "Recusar", x, r.Y+17, buttonSubtle)
	label(c, p.Code, ui.font(14, true), warning, walk.Rectangle{X: x - 220, Y: r.Y, Width: 110, Height: r.Height}, walk.TextRight|walk.TextVCenter|walk.TextSingleLine)
}

func (ui *nativeDesktopUI) paintOfferBanner(c *walk.Canvas, r walk.Rectangle, t TransferView) {
	files := fmt.Sprintf("%d arquivos", len(t.Files))
	if len(t.Files) == 1 {
		files = t.Files[0].Name
	}
	ui.banner(c, r, primary, primarySoft, glyphDownload, ui.peerName(t.SenderID)+" quer enviar "+files,
		formatNativeBytes(t.TotalBytes)+" · os arquivos vão para "+ui.downloadDirectory())
	x := r.X + r.Width - 16
	x = ui.smallButton(c, "t-accept|"+t.ID, "Aceitar", x, r.Y+17, buttonPrimary)
	ui.smallButton(c, "t-reject|"+t.ID, "Recusar", x, r.Y+17, buttonSubtle)
}

func (ui *nativeDesktopUI) paintOnboarding(c *walk.Canvas, r walk.Rectangle) {
	ui.card(c, r)
	ui.ensureQR()
	qr := walk.Rectangle{X: r.X + 20, Y: r.Y + 18, Width: 180, Height: 180}
	if ui.qrSmall != nil {
		_ = c.DrawImageStretched(ui.qrSmall, qr)
	}
	x := qr.X + qr.Width + 28
	w := r.X + r.Width - x - 24
	label(c, "Conecte seu celular", ui.font(15, true), ink, walk.Rectangle{X: x, Y: r.Y + 22, Width: w, Height: 30}, oneLine)
	steps := []string{
		"Aponte a câmera do celular para o código ao lado.",
		"Abra o link e toque em Parear com este computador.",
		"Confira o código de 6 dígitos e toque em Confiar aqui.",
	}
	for i, step := range steps {
		y := r.Y + 64 + i*30
		circle := walk.Rectangle{X: x, Y: y, Width: 22, Height: 22}
		dot(c, primarySoft, circle)
		label(c, fmt.Sprint(i+1), ui.font(8, true), primary, circle, centeredLine)
		label(c, step, ui.font(9, false), ink, walk.Rectangle{X: x + 32, Y: y, Width: w - 32, Height: 22}, oneLine)
	}
	label(c, ui.server.localURL(), ui.font(9, true), primary, walk.Rectangle{X: x, Y: r.Y + 164, Width: w - 170, Height: 30}, oneLine)
	ui.button(c, "copy-url", "Copiar endereço", "", walk.Rectangle{X: r.X + r.Width - 184, Y: r.Y + 162, Width: 160, Height: 34}, buttonSubtle, true)
}

func (ui *nativeDesktopUI) paintDevices(c *walk.Canvas, r walk.Rectangle) {
	label(c, "ENVIAR PARA", ui.font(8, true), muted, walk.Rectangle{X: r.X + 2, Y: r.Y, Width: 200, Height: 18}, oneLine)
	chipY, chipH, gap := r.Y+24, 56, 12
	available := r.Width - chipH - gap
	n := len(ui.peers)
	chipW := 220
	if n > 0 && (available-(n-1)*gap)/n < chipW {
		chipW = (available - (n-1)*gap) / n
	}
	if chipW < 160 {
		chipW = 160
	}
	x := r.X
	for _, peer := range ui.peers {
		if x+chipW > r.X+available {
			break
		}
		chip := walk.Rectangle{X: x, Y: chipY, Width: chipW, Height: chipH}
		selected := peer.DeviceID == ui.selectedPeer
		bg, border := panel, line
		if selected {
			bg, border = primarySoft, primary
		} else if ui.hover == "peer|"+peer.DeviceID {
			border = walk.RGB(200, 194, 245)
		}
		rounded(c, bg, chip, 14)
		outline(c, border, chip, 14)
		if selected {
			outline(c, border, inset(chip, 1, 1, 1, 1), 13)
		}
		circle := walk.Rectangle{X: chip.X + 10, Y: chip.Y + 10, Width: 36, Height: 36}
		circleBG, glyphColor := subtleBG, ink
		if selected {
			circleBG, glyphColor = primary, white
		}
		dot(c, circleBG, circle)
		g := glyphPhone
		if !strings.Contains(strings.ToUpper(peer.DeviceType), "MOBILE") && !strings.Contains(strings.ToUpper(peer.DeviceType), "TABLET") {
			g = glyphDesktop
		}
		ui.glyph(c, g, 12, glyphColor, circle)
		label(c, peer.DisplayName, ui.font(9, true), ink, walk.Rectangle{X: chip.X + 56, Y: chip.Y + 9, Width: chipW - 66, Height: 20}, oneLine)
		state, stateColor := "Offline · "+relativeTime(peer.LastSeenAt), muted
		if peer.Online {
			state, stateColor = "Online", success
		}
		dot(c, stateColor, walk.Rectangle{X: chip.X + 57, Y: chip.Y + 36, Width: 7, Height: 7})
		label(c, state, ui.font(8, false), stateColor, walk.Rectangle{X: chip.X + 70, Y: chip.Y + 29, Width: chipW - 80, Height: 20}, oneLine)
		ui.add("peer|"+peer.DeviceID, chip)
		x += chipW + gap
	}
	add := walk.Rectangle{X: x, Y: chipY, Width: chipH, Height: chipH}
	bg := panel
	if ui.hover == "connect-chip" {
		bg = primarySoft
	}
	rounded(c, bg, add, 14)
	dashedOutline(c, walk.RGB(190, 184, 240), add, 14)
	ui.glyph(c, glyphAdd, 12, primary, add)
	ui.add("connect-chip", add)
}

func (ui *nativeDesktopUI) paintDrop(c *walk.Canvas, r walk.Rectangle) {
	enabled := ui.selectedPeer != ""
	bg, border := panel, walk.RGB(196, 190, 242)
	if enabled && ui.hover == "drop" {
		bg, border = walk.RGB(247, 246, 255), primary
	}
	rounded(c, bg, r, 16)
	dashedOutline(c, border, r, 16)
	circle := walk.Rectangle{X: r.X + 24, Y: r.Y + (r.Height-52)/2, Width: 52, Height: 52}
	if r.Height < 100 {
		circle = walk.Rectangle{X: r.X + 20, Y: r.Y + (r.Height-44)/2, Width: 44, Height: 44}
	}
	dot(c, primarySoft, circle)
	ui.glyph(c, glyphUpload, 16, primary, circle)
	x := circle.X + circle.Width + 18
	w := r.X + r.Width - x - 20
	if !enabled {
		label(c, "Arraste arquivos para cá", ui.font(11, true), muted, walk.Rectangle{X: x, Y: r.Y + r.Height/2 - 24, Width: w, Height: 24}, oneLine)
		label(c, "Primeiro conecte um celular — o envio fica disponível assim que ele for aprovado.", ui.font(9, false), muted, walk.Rectangle{X: x, Y: r.Y + r.Height/2, Width: w, Height: 22}, oneLine)
		return
	}
	label(c, "Arraste arquivos para cá ou clique para escolher", ui.font(11, true), ink, walk.Rectangle{X: x, Y: r.Y + r.Height/2 - 25, Width: w, Height: 24}, oneLine)
	label(c, "Vão para "+ui.peerName(ui.selectedPeer)+", sem cópias extras  ·  Ctrl+V envia o texto copiado", ui.font(9, false), muted, walk.Rectangle{X: x, Y: r.Y + r.Height/2 + 1, Width: w, Height: 22}, oneLine)
	ui.add("drop", r)
}

func (ui *nativeDesktopUI) paintActivity(c *walk.Canvas, r walk.Rectangle) {
	ui.activityRows = 0
	if r.Height < 96 {
		return
	}
	ui.card(c, r)
	label(c, "ATIVIDADE", ui.font(8, true), primary, walk.Rectangle{X: r.X + 20, Y: r.Y + 14, Width: 200, Height: 20}, oneLine)
	transfers := ui.snapshot.Transfers
	if len(transfers) == 0 {
		label(c, "Nada por aqui ainda", ui.font(11, true), ink, walk.Rectangle{X: r.X + 20, Y: r.Y + 48, Width: r.Width - 40, Height: 24}, oneLine)
		label(c, "Arquivos enviados e recebidos aparecem nesta lista, com progresso e atalhos para abrir.", ui.font(9, false), muted, walk.Rectangle{X: r.X + 20, Y: r.Y + 74, Width: r.Width - 40, Height: 22}, oneLine)
		return
	}
	rowH := 58
	visible := (r.Height - 52) / rowH
	if visible < 1 {
		visible = 1
	}
	ui.activityRows = visible
	ui.clampActivity()
	if len(transfers) > visible {
		end := ui.activityOffset + visible
		if end > len(transfers) {
			end = len(transfers)
		}
		label(c, fmt.Sprintf("%d–%d de %d · role para ver mais", ui.activityOffset+1, end, len(transfers)), ui.font(8, false), muted,
			walk.Rectangle{X: r.X + r.Width - 320, Y: r.Y + 14, Width: 300, Height: 20}, walk.TextRight|walk.TextVCenter|walk.TextSingleLine)
	}
	for row := 0; row < visible && ui.activityOffset+row < len(transfers); row++ {
		rr := walk.Rectangle{X: r.X + 12, Y: r.Y + 44 + row*rowH, Width: r.Width - 24, Height: rowH}
		if row > 0 {
			fill(c, line, walk.Rectangle{X: rr.X + 56, Y: rr.Y, Width: rr.Width - 64, Height: 1})
		}
		ui.paintTransferRow(c, rr, transfers[ui.activityOffset+row])
	}
}

func (ui *nativeDesktopUI) paintTransferRow(c *walk.Canvas, r walk.Rectangle, t TransferView) {
	self := ui.client.session.DeviceID
	incoming := t.ReceiverID == self
	circle := walk.Rectangle{X: r.X + 8, Y: r.Y + 11, Width: 36, Height: 36}
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
			right = ui.smallButton(c, "t-copy|"+t.ID, "Copiar texto", right, y, buttonPrimary)
		} else if len(t.Files) == 1 {
			right = ui.smallButton(c, "t-open|"+t.ID, "Abrir", right, y, buttonPrimary)
		}
	case !isTerminalState(t.State):
		right = ui.smallButton(c, "t-cancel|"+t.ID, "Cancelar", right, y, buttonDanger)
		if t.State == "ACCEPTED" || t.State == "TRANSFERRING" {
			right = ui.smallButton(c, "t-pause|"+t.ID, "Pausar", right, y, buttonSubtle)
		}
	}

	active := t.State == "ACCEPTED" || t.State == "TRANSFERRING" || t.State == "VERIFYING" || (t.State == "PAUSED" && t.ReceivedBytes > 0)
	if active && t.TotalBytes > 0 {
		barW := 150
		bar := walk.Rectangle{X: right - barW - 4, Y: r.Y + 22, Width: barW, Height: 6}
		rounded(c, subtleBG, bar, 6)
		progress := bar
		progress.Width = int(int64(barW) * t.ReceivedBytes / t.TotalBytes)
		if progress.Width > 0 {
			color := primary
			if t.State == "PAUSED" {
				color = warning
			}
			rounded(c, color, progress, 6)
		}
		detail := fmt.Sprintf("%d%%", t.ReceivedBytes*100/t.TotalBytes)
		if speed := ui.speeds[t.ID]; speed.rate > 0 && t.State == "TRANSFERRING" {
			detail += " · " + formatNativeBytes(int64(speed.rate)) + "/s"
		} else if t.State != "TRANSFERRING" {
			detail += " · " + translateTransferState(t.State)
		}
		label(c, detail, ui.font(8, false), muted, walk.Rectangle{X: bar.X, Y: r.Y + 30, Width: barW, Height: 18}, oneLine)
		right = bar.X - 12
	} else {
		text, pfg, pbg := translateTransferState(t.State), muted, subtleBG
		switch t.State {
		case "COMPLETED":
			text, pfg, pbg = "Concluída", success, successSoft
			if !incoming {
				text = "Pronta no celular"
				if len(t.Files) > 0 && !t.Files[0].DownloadAvailable {
					text, pfg, pbg = "Link expirado", muted, subtleBG
				}
			}
		case "WAITING_ACCEPTANCE":
			text, pfg, pbg = "Aguardando o celular", warning, warningSoft
			if incoming {
				text = "Aguardando você"
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
	}
	label(c, transferFileSummary(t.Files), ui.font(9, true), ink, walk.Rectangle{X: x, Y: r.Y + 10, Width: w, Height: 20}, oneLine)
	label(c, sub, ui.font(8, false), muted, walk.Rectangle{X: x, Y: r.Y + 30, Width: w, Height: 18}, oneLine)
}

// ---------------------------------------------------------------- ajustes

func (ui *nativeDesktopUI) paintSettings(c *walk.Canvas, l canvasLayout) {
	x, y, w := l.left, l.top, l.width
	label(c, "Ajustes", ui.font(16, true), ink, walk.Rectangle{X: x, Y: y, Width: 300, Height: 32}, oneLine)
	ui.button(c, "home", "Voltar", glyphBack, walk.Rectangle{X: x + w - 110, Y: y, Width: 110, Height: 34}, buttonSubtle, true)
	y += 48
	gap := 16
	colW := (w - gap) / 2
	settings := ui.snapshot.Settings

	identity := walk.Rectangle{X: x, Y: y, Width: colW, Height: 226}
	ui.card(c, identity)
	label(c, "ESTE COMPUTADOR", ui.font(8, true), primary, walk.Rectangle{X: identity.X + 20, Y: identity.Y + 14, Width: 240, Height: 20}, oneLine)
	rowW := identity.Width - 40
	label(c, "Nome que aparece no celular", ui.font(8, false), muted, walk.Rectangle{X: identity.X + 20, Y: identity.Y + 42, Width: rowW - 120, Height: 18}, oneLine)
	label(c, settings.Device.DisplayName, ui.font(11, true), ink, walk.Rectangle{X: identity.X + 20, Y: identity.Y + 60, Width: rowW - 120, Height: 24}, oneLine)
	ui.smallButton(c, "rename", "Renomear", identity.X+identity.Width-20, identity.Y+50, buttonSubtle)
	fill(c, line, walk.Rectangle{X: identity.X + 20, Y: identity.Y + 94, Width: rowW, Height: 1})
	label(c, "Pasta de recebimento", ui.font(8, false), muted, walk.Rectangle{X: identity.X + 20, Y: identity.Y + 104, Width: rowW, Height: 18}, oneLine)
	buttons := ui.smallButton(c, "folder-change", "Alterar", identity.X+identity.Width-20, identity.Y+112, buttonSubtle)
	buttons = ui.smallButton(c, "folder-open", "Abrir", buttons, identity.Y+112, buttonSubtle)
	label(c, ui.downloadDirectory(), ui.font(9, true), ink, walk.Rectangle{X: identity.X + 20, Y: identity.Y + 122, Width: buttons - identity.X - 24, Height: 22}, walk.TextVCenter|walk.TextSingleLine|walk.TextPathEllipsis)
	fill(c, line, walk.Rectangle{X: identity.X + 20, Y: identity.Y + 156, Width: rowW, Height: 1})
	ui.toggle(c, "auto", settings.AutoAcceptTrusted, walk.Rectangle{X: identity.X + identity.Width - 66, Y: identity.Y + 176, Width: 46, Height: 26})
	label(c, "Aceitar automaticamente", ui.font(9, true), ink, walk.Rectangle{X: identity.X + 20, Y: identity.Y + 166, Width: rowW - 70, Height: 20}, oneLine)
	label(c, "Arquivos de aparelhos confiáveis entram sem perguntar.", ui.font(8, false), muted, walk.Rectangle{X: identity.X + 20, Y: identity.Y + 186, Width: rowW - 70, Height: 20}, oneLine)

	phone := walk.Rectangle{X: x, Y: identity.Y + identity.Height + gap, Width: colW, Height: 128}
	ui.card(c, phone)
	label(c, "ACESSO PELO CELULAR", ui.font(8, true), primary, walk.Rectangle{X: phone.X + 20, Y: phone.Y + 14, Width: 240, Height: 20}, oneLine)
	label(c, ui.server.localURL(), ui.font(11, true), ink, walk.Rectangle{X: phone.X + 20, Y: phone.Y + 38, Width: phone.Width - 40, Height: 24}, oneLine)
	label(c, "Abra no navegador do celular, na mesma rede Wi-Fi.", ui.font(8, false), muted, walk.Rectangle{X: phone.X + 20, Y: phone.Y + 62, Width: phone.Width - 40, Height: 18}, oneLine)
	ui.button(c, "connect", "Mostrar QR", glyphQR, walk.Rectangle{X: phone.X + 20, Y: phone.Y + 84, Width: 140, Height: 32}, buttonPrimary, true)
	ui.button(c, "copy-url", "Copiar endereço", "", walk.Rectangle{X: phone.X + 170, Y: phone.Y + 84, Width: 150, Height: 32}, buttonSubtle, true)

	trustedHeight := l.bottom - y - 52
	trusted := walk.Rectangle{X: x + colW + gap, Y: y, Width: w - colW - gap, Height: trustedHeight}
	ui.card(c, trusted)
	label(c, "APARELHOS CONFIÁVEIS", ui.font(8, true), primary, walk.Rectangle{X: trusted.X + 20, Y: trusted.Y + 14, Width: 240, Height: 20}, oneLine)
	if len(ui.peers) == 0 {
		label(c, "Nenhum aparelho conectado ainda.", ui.font(10, true), ink, walk.Rectangle{X: trusted.X + 20, Y: trusted.Y + 46, Width: trusted.Width - 40, Height: 22}, oneLine)
		label(c, "Use Conectar celular para parear o primeiro.", ui.font(9, false), muted, walk.Rectangle{X: trusted.X + 20, Y: trusted.Y + 70, Width: trusted.Width - 40, Height: 22}, oneLine)
	}
	rows := (trusted.Height - 50) / 52
	for i, peer := range ui.peers {
		if i >= rows {
			label(c, fmt.Sprintf("e mais %d…", len(ui.peers)-rows), ui.font(8, false), muted, walk.Rectangle{X: trusted.X + 20, Y: trusted.Y + trusted.Height - 26, Width: 200, Height: 18}, oneLine)
			break
		}
		rr := walk.Rectangle{X: trusted.X + 12, Y: trusted.Y + 42 + i*52, Width: trusted.Width - 24, Height: 52}
		circle := walk.Rectangle{X: rr.X + 8, Y: rr.Y + 9, Width: 34, Height: 34}
		dot(c, subtleBG, circle)
		g := glyphPhone
		if !strings.Contains(strings.ToUpper(peer.DeviceType), "MOBILE") && !strings.Contains(strings.ToUpper(peer.DeviceType), "TABLET") {
			g = glyphDesktop
		}
		ui.glyph(c, g, 11, ink, circle)
		buttonX := ui.smallButton(c, "revoke|"+peer.DeviceID, "Remover", rr.X+rr.Width-8, rr.Y+11, buttonDanger)
		label(c, peer.DisplayName, ui.font(9, true), ink, walk.Rectangle{X: rr.X + 54, Y: rr.Y + 7, Width: buttonX - rr.X - 58, Height: 20}, oneLine)
		state := "Offline · visto " + relativeTime(peer.LastSeenAt)
		if peer.Online {
			state = "Online agora"
		}
		label(c, state, ui.font(8, false), muted, walk.Rectangle{X: rr.X + 54, Y: rr.Y + 27, Width: buttonX - rr.X - 58, Height: 18}, oneLine)
	}
	ui.button(c, "quit", "Encerrar o LocalDrop", "", walk.Rectangle{X: trusted.X + trusted.Width - 190, Y: l.bottom - 38, Width: 190, Height: 34}, buttonDanger, true)
	label(c, "Fechar a janela mantém o LocalDrop pronto na bandeja.", ui.font(8, false), muted, walk.Rectangle{X: x + 4, Y: phone.Y + phone.Height + 12, Width: colW, Height: 18}, oneLine)
	label(c, "Versão "+version, ui.font(8, false), muted, walk.Rectangle{X: x + 4, Y: phone.Y + phone.Height + 30, Width: colW, Height: 18}, oneLine)
}

func (ui *nativeDesktopUI) paintModal(c *walk.Canvas, b walk.Rectangle) {
	fill(c, walk.RGB(214, 217, 232), b)
	p := strings.SplitN(ui.modal, "|", 2)
	kind, msg := p[0], ""
	if len(p) > 1 {
		msg = p[1]
	}
	r := walk.Rectangle{X: (b.Width - 460) / 2, Y: (b.Height - 210) / 2, Width: 460, Height: 210}
	rounded(c, panel, r, 18)
	title, col := "Aviso", primary
	if kind == "error" {
		title, col = "Algo não saiu como esperado", danger
	} else if kind == "confirm" {
		title, col = "Confirmar", warning
	}
	fill(c, col, walk.Rectangle{X: r.X, Y: r.Y + 18, Width: 5, Height: r.Height - 36})
	label(c, title, ui.font(14, true), ink, walk.Rectangle{X: r.X + 28, Y: r.Y + 26, Width: r.Width - 56, Height: 30}, oneLine)
	label(c, msg, ui.font(10, false), muted, walk.Rectangle{X: r.X + 28, Y: r.Y + 64, Width: r.Width - 56, Height: 76}, walk.TextWordbreak|walk.TextEndEllipsis)
	if kind == "confirm" {
		ui.button(c, "modal-cancel", "Cancelar", "", walk.Rectangle{X: r.X + r.Width - 242, Y: r.Y + r.Height - 56, Width: 100, Height: 36}, buttonSubtle, true)
		ui.button(c, "modal-confirm", "Confirmar", "", walk.Rectangle{X: r.X + r.Width - 130, Y: r.Y + r.Height - 56, Width: 104, Height: 36}, buttonPrimary, true)
	} else {
		ui.button(c, "modal-close", "Entendi", "", walk.Rectangle{X: r.X + r.Width - 124, Y: r.Y + r.Height - 56, Width: 98, Height: 36}, buttonPrimary, true)
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
	if ui.window == nil || !ui.alive() {
		return
	}
	ui.window.Synchronize(func() {
		if ui.alive() {
			action()
		}
	})
}

func (ui *nativeDesktopUI) setStatus(s string) {
	ui.synchronize(func() { ui.status = s; ui.invalidate() })
}

func (ui *nativeDesktopUI) showError(a string, e error) {
	log.Printf("interface nativa: %s: %v", a, e)
	ui.synchronize(func() { ui.modal = "error|" + a + "\n" + e.Error(); ui.invalidate() })
}

func (ui *nativeDesktopUI) peerName(id string) string {
	for _, d := range ui.snapshot.Devices {
		if d.DeviceID == id {
			return d.DisplayName
		}
	}
	return "Dispositivo removido"
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
	if ui.window == nil {
		return false
	}
	handle := ui.window.Handle()
	return win.IsWindowVisible(handle) && !win.IsIconic(handle)
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
		if !ui.shown() {
			ui.stale = true
			ui.refreshMu.Unlock()
			return
		}
		ui.refreshMu.Unlock()
		if !ui.alive() {
			return
		}
		snapshot := ui.client.snapshot()
		ui.synchronize(func() { ui.applySnapshot(snapshot) })
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
	case len(ui.peers) == 0:
		ui.headerStatus = "Nenhum celular conectado · " + ui.server.localURL()
	case online == 0:
		ui.headerStatus = "Pronto para receber · nenhum aparelho online agora"
	case online == 1:
		ui.headerStatus = "Pronto para receber · 1 aparelho online"
	default:
		ui.headerStatus = fmt.Sprintf("Pronto para receber · %d aparelhos online", online)
	}
	ui.clampActivity()
	if ui.tray != nil {
		_ = ui.tray.SetToolTip("LocalDrop · " + ui.headerStatus)
	}
	ui.invalidate()
}

// onServerEvent roda na goroutine do servidor: apenas agenda trabalho.
func (ui *nativeDesktopUI) onServerEvent(eventType string, payload any) {
	ui.requestRefresh()
	switch eventType {
	case "PAIRING_REQUESTED", "TRANSFER_OFFERED", "TRANSFER_COMPLETED":
		ui.synchronize(func() { ui.notify(eventType, payload) })
	}
}

// notify mostra um aviso do Windows quando a janela não está em primeiro plano.
func (ui *nativeDesktopUI) notify(eventType string, payload any) {
	if ui.tray == nil || (ui.shown() && win.GetForegroundWindow() == ui.window.Handle()) {
		return
	}
	data, _ := payload.(map[string]any)
	self := ui.client.session.DeviceID
	switch eventType {
	case "PAIRING_REQUESTED":
		name, _ := data["displayName"].(string)
		_ = ui.tray.ShowInfo("Novo pedido de conexão", name+" quer se conectar a este computador. Clique para conferir o código.")
	case "TRANSFER_OFFERED":
		t, ok := data["transfer"].(TransferView)
		if !ok || t.ReceiverID != self || ui.snapshot.Settings.AutoAcceptTrusted {
			return
		}
		_ = ui.tray.ShowInfo("Arquivos chegando", ui.peerName(t.SenderID)+" quer enviar "+transferFileSummary(t.Files)+". Clique para aceitar.")
	case "TRANSFER_COMPLETED":
		t, ok := data["transfer"].(TransferView)
		if !ok || t.ReceiverID != self {
			return
		}
		_ = ui.tray.ShowInfo("Arquivo recebido", transferFileSummary(t.Files)+" foi salvo em "+filepath.Base(ui.downloadDirectory())+".")
	}
}

// ---------------------------------------------------------------- ações

func (ui *nativeDesktopUI) chooseFiles() {
	if ui.selectedPeer == "" {
		ui.showMobileConnection()
		return
	}
	d := new(walk.FileDialog)
	d.Title = "Escolher arquivos para enviar"
	d.Filter = "Todos os arquivos (*.*)|*.*"
	ok, e := d.ShowOpenMultiple(ui.window)
	if e != nil {
		ui.showError("Não foi possível abrir o seletor de arquivos.", e)
	} else if ok {
		ui.sendPaths(d.FilePaths)
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
		ui.modal = "info|Conecte um celular antes de enviar. Use o botão Conectar celular e escaneie o código."
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
			ui.showError("Não foi possível preparar o envio.", err)
			return
		}
		if _, err := ui.client.offer(receiver, files); err != nil {
			ui.showError("Não foi possível oferecer os arquivos.", err)
			return
		}
		count := "1 arquivo"
		if len(files) > 1 {
			count = fmt.Sprintf("%d arquivos", len(files))
		}
		ui.setStatus(count + " oferecido(s) a " + name + ". Toque em Aceitar no celular para baixar.")
	}()
}

func (ui *nativeDesktopUI) pasteText() {
	if ui.selectedPeer == "" {
		return
	}
	text, err := walk.Clipboard().Text()
	if err != nil || strings.TrimSpace(text) == "" {
		ui.status = "A área de transferência não tem texto para enviar."
		ui.invalidate()
		return
	}
	if len(text) > 1<<20 {
		ui.modal = "info|O texto copiado passa de 1 MB. Salve-o em um arquivo e arraste para a janela."
		ui.invalidate()
		return
	}
	receiver, name := ui.selectedPeer, ui.peerName(ui.selectedPeer)
	go func() {
		if _, err := ui.client.sendText(receiver, text); err != nil {
			ui.showError("Não foi possível enviar o texto.", err)
			return
		}
		ui.setStatus("Texto enviado para " + name + ".")
	}()
}

func (ui *nativeDesktopUI) runTransfer(id, action string) {
	go func() {
		if e := ui.client.action(id, action); e != nil {
			ui.showError("A ação não pôde ser concluída.", e)
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
	path := ui.savedPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		ui.showError("Não foi possível ler o texto recebido.", err)
		return
	}
	if !utf8.Valid(data) {
		ui.modal = "info|O arquivo não contém texto UTF-8. Use Mostrar na pasta para abri-lo."
		return
	}
	ui.copy(string(data), "Texto recebido copiado para a área de transferência.")
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
			ui.showError("Não foi possível responder ao pareamento.", e)
			return
		}
		if approve {
			ui.setStatus(pairing.DisplayName + " agora é um aparelho confiável.")
		}
		ui.requestRefresh()
	}()
}

func (ui *nativeDesktopUI) updateSettings(input map[string]any, done string) {
	go func() {
		if _, e := ui.client.updateSettings(input); e != nil {
			ui.showError("Não foi possível salvar o ajuste.", e)
		} else {
			ui.setStatus(done)
		}
		ui.requestRefresh()
	}()
}

func (ui *nativeDesktopUI) rename() {
	var dialog *walk.Dialog
	var edit *walk.LineEdit
	var accept, cancel *walk.PushButton
	name := ""
	err := Dialog{
		AssignTo: &dialog, Title: "Renomear este computador", DefaultButton: &accept, CancelButton: &cancel,
		MinSize: Size{Width: 380, Height: 160}, Layout: VBox{Margins: Margins{Left: 18, Top: 16, Right: 18, Bottom: 14}, Spacing: 10},
		Children: []Widget{
			Label{Text: "Nome que aparece no celular:"},
			LineEdit{AssignTo: &edit, Text: ui.snapshot.Settings.Device.DisplayName, MaxLength: 60},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &cancel, Text: "Cancelar", OnClicked: func() { dialog.Cancel() }},
				PushButton{AssignTo: &accept, Text: "Salvar", OnClicked: func() { name = strings.TrimSpace(edit.Text()); dialog.Accept() }},
			}},
		},
	}.Create(ui.window)
	if err != nil {
		ui.showError("Não foi possível abrir a edição do nome.", err)
		return
	}
	_ = dialog.SetIcon(ui.appIcon)
	if dialog.Run() == walk.DlgCmdOK && name != "" && name != ui.snapshot.Settings.Device.DisplayName {
		ui.updateSettings(map[string]any{"displayName": name}, "Nome atualizado.")
	}
}

func (ui *nativeDesktopUI) chooseFolder() {
	v, ok, e := pickDirectory(ui.downloadDirectory())
	if e != nil {
		ui.showError("Não foi possível escolher a pasta.", e)
	} else if ok {
		ui.updateSettings(map[string]any{"downloadDirectory": filepath.Clean(v)}, "Pasta de recebimento atualizada.")
	}
}

func (ui *nativeDesktopUI) confirmRevoke(id string) {
	name := ui.peerName(id)
	ui.modal = "confirm|Remover " + name + " dos aparelhos confiáveis? Ele precisará parear de novo para trocar arquivos."
	ui.confirmAction = func() {
		go func() {
			if e := ui.client.revoke(id); e != nil {
				ui.showError("Não foi possível remover o aparelho.", e)
				return
			}
			ui.setStatus(name + " foi removido.")
			ui.requestRefresh()
		}()
	}
}

func (ui *nativeDesktopUI) copy(s, msg string) {
	if e := walk.Clipboard().SetText(s); e != nil {
		ui.showError("Não foi possível copiar o texto.", e)
		return
	}
	ui.status = msg
	ui.invalidate()
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
	case "connect", "connect-chip":
		ui.showMobileConnection()
	case "peer":
		ui.selectedPeer = arg
	case "drop":
		ui.chooseFiles()
	case "pair-approve":
		ui.pair(arg, true)
	case "pair-reject":
		ui.pair(arg, false)
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
			ui.showError("Não foi possível abrir o arquivo.", err)
		}
	case "t-folder":
		if err := revealPath(ui.savedPath(arg)); err != nil {
			ui.showError("Não foi possível abrir a pasta.", err)
		}
	case "t-copy":
		ui.copyReceivedText(arg)
	case "rename":
		ui.rename()
	case "folder-change":
		ui.chooseFolder()
	case "folder-open":
		if err := openPath(ui.downloadDirectory()); err != nil {
			ui.showError("Não foi possível abrir a pasta.", err)
		}
	case "auto":
		ui.snapshot.Settings.AutoAcceptTrusted = !ui.snapshot.Settings.AutoAcceptTrusted
		ui.updateSettings(map[string]any{"autoAcceptTrusted": ui.snapshot.Settings.AutoAcceptTrusted}, "Preferência de aceite salva.")
	case "revoke":
		ui.confirmRevoke(arg)
	case "copy-url":
		ui.copy(ui.server.localURL(), "Endereço copiado. Cole no navegador do celular.")
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

func (ui *nativeDesktopUI) mouseDown(x, y int, b walk.MouseButton) {
	if b != walk.LeftButton {
		return
	}
	point := ui.canvas.PointTo96DPI(walk.Point{X: x, Y: y})
	ui.pressed = ui.hit(point.X, point.Y)
	ui.canvas.SetFocus()
	ui.invalidate()
}

func (ui *nativeDesktopUI) mouseMove(x, y int, _ walk.MouseButton) {
	point := ui.canvas.PointTo96DPI(walk.Point{X: x, Y: y})
	n := ui.hit(point.X, point.Y)
	if n != ui.hover {
		ui.hover = n
		ui.invalidate()
	}
}

func (ui *nativeDesktopUI) mouseUp(x, y int, b walk.MouseButton) {
	if b != walk.LeftButton {
		return
	}
	point := ui.canvas.PointTo96DPI(walk.Point{X: x, Y: y})
	id := ui.hit(point.X, point.Y)
	same := id != "" && id == ui.pressed
	ui.pressed = ""
	if same {
		ui.activate(id)
	} else {
		ui.invalidate()
	}
}

func (ui *nativeDesktopUI) mouseWheel(_, _ int, button walk.MouseButton) {
	if ui.page != "home" || ui.modal != "" {
		return
	}
	delta := int16(uint32(button) >> 16)
	if delta < 0 {
		ui.activityOffset++
	} else if delta > 0 {
		ui.activityOffset--
	}
	ui.clampActivity()
	ui.invalidate()
}

func (ui *nativeDesktopUI) keyDown(k walk.Key) {
	if ui.modal != "" {
		if k == walk.KeyEscape || k == walk.KeyReturn {
			ui.activate("modal-close")
		}
		return
	}
	if !walk.ControlDown() {
		return
	}
	switch k {
	case walk.KeyV:
		ui.pasteText()
	case walk.KeyO:
		ui.chooseFiles()
	}
}

// ---------------------------------------------------------------- janela e bandeja

func (ui *nativeDesktopUI) ensureQR() {
	current := ui.server.localURL()
	if ui.qrURL == current && ui.qr != nil {
		return
	}
	code, err := qrcode.New(current, qrcode.Medium)
	if err != nil {
		return
	}
	code.DisableBorder = true
	large, err := walk.NewBitmapFromImage(code.Image(320))
	if err != nil {
		return
	}
	small, err := walk.NewBitmapFromImage(code.Image(180))
	if err != nil {
		large.Dispose()
		return
	}
	if ui.qr != nil {
		ui.qr.Dispose()
	}
	if ui.qrSmall != nil {
		ui.qrSmall.Dispose()
	}
	ui.qr, ui.qrSmall, ui.qrURL = large, small, current
}

func (ui *nativeDesktopUI) showMobileConnection() {
	ui.ensureQR()
	var dialog *walk.Dialog
	var surface *walk.CustomWidget
	var copyR, closeR walk.Rectangle
	hover := ""
	paint := func(c *walk.Canvas, _ walk.Rectangle) error {
		b := surface.ClientBounds()
		fill(c, canvasBG, b)
		r := walk.Rectangle{X: 20, Y: 18, Width: b.Width - 40, Height: b.Height - 36}
		rounded(c, panel, r, 18)
		label(c, "Conectar celular", ui.font(17, true), ink, walk.Rectangle{X: r.X + 28, Y: r.Y + 24, Width: r.Width - 56, Height: 30}, oneLine)
		label(c, "Aponte a câmera do celular para o código. Ele precisa estar no mesmo Wi-Fi.", ui.font(9, false), muted, walk.Rectangle{X: r.X + 28, Y: r.Y + 56, Width: r.Width - 56, Height: 36}, walk.TextWordbreak)
		qr := walk.Rectangle{X: (b.Width - 300) / 2, Y: r.Y + 104, Width: 300, Height: 300}
		if ui.qr != nil {
			_ = c.DrawImageStretched(ui.qr, qr)
		}
		label(c, "ou digite no navegador do celular:", ui.font(8, false), muted, walk.Rectangle{X: r.X + 28, Y: qr.Y + 314, Width: r.Width - 56, Height: 18}, centeredLine)
		label(c, ui.server.localURL(), ui.font(11, true), primary, walk.Rectangle{X: r.X + 28, Y: qr.Y + 334, Width: r.Width - 56, Height: 24}, centeredLine)
		copyR = walk.Rectangle{X: r.X + 28, Y: r.Y + r.Height - 60, Width: 170, Height: 38}
		closeR = walk.Rectangle{X: r.X + r.Width - 128, Y: r.Y + r.Height - 60, Width: 100, Height: 38}
		copyBG, closeBG := subtleBG, primary
		if hover == "copy" {
			copyBG = walk.RGB(224, 227, 242)
		}
		if hover == "close" {
			closeBG = primaryDark
		}
		rounded(c, copyBG, copyR, 10)
		label(c, "Copiar endereço", ui.font(9, true), ink, copyR, centeredLine)
		rounded(c, closeBG, closeR, 10)
		label(c, "Pronto", ui.font(9, true), white, closeR, centeredLine)
		return nil
	}
	hitAt := func(x, y int) string {
		point := surface.PointTo96DPI(walk.Point{X: x, Y: y})
		switch {
		case inside(copyR, point.X, point.Y):
			return "copy"
		case inside(closeR, point.X, point.Y):
			return "close"
		}
		return ""
	}
	move := func(x, y int, _ walk.MouseButton) {
		if current := hitAt(x, y); current != hover {
			hover = current
			surface.Invalidate()
		}
	}
	click := func(x, y int, button walk.MouseButton) {
		if button != walk.LeftButton {
			return
		}
		switch hitAt(x, y) {
		case "copy":
			ui.copy(ui.server.localURL(), "Endereço copiado. Cole no navegador do celular.")
		case "close":
			dialog.Accept()
		}
	}
	size := Size{Width: 480, Height: 640}
	if e := (Dialog{AssignTo: &dialog, Title: "Conectar celular · LocalDrop", Size: size, MinSize: size, MaxSize: size, FixedSize: true, Layout: edgeToEdgeCanvasLayout(), Children: []Widget{CustomWidget{AssignTo: &surface, PaintMode: PaintBuffered, Paint: paint, InvalidatesOnResize: true, StretchFactor: 1, OnMouseMove: move, OnMouseUp: click}}}).Create(ui.window); e != nil {
		ui.showError("Não foi possível abrir a conexão do celular.", e)
		return
	}
	_ = dialog.SetIcon(ui.appIcon)
	applyLocalDropWindowFrame(dialog)
	dialog.Run()
}

func (ui *nativeDesktopUI) showWindow() {
	if ui.window == nil {
		return
	}
	ui.window.Show()
	handle := ui.window.Handle()
	if win.IsIconic(handle) {
		win.ShowWindow(handle, win.SW_RESTORE)
	}
	win.SetForegroundWindow(handle)
	ui.resumeIfStale()
	ui.requestRefresh()
}

func (ui *nativeDesktopUI) hideToTray() {
	ui.window.Hide()
	marker := filepath.Join(ui.server.cfg.StorageRoot, ".tray-hint")
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		_ = os.WriteFile(marker, []byte("1"), 0o600)
		_ = ui.tray.ShowInfo("O LocalDrop continua ativo", "Ele segue pronto para receber arquivos aqui na bandeja. Para encerrar, clique com o botão direito no ícone.")
	}
}

func (ui *nativeDesktopUI) quit() {
	ui.quitting = true
	ui.window.Close()
}

func (ui *nativeDesktopUI) buildTray() error {
	tray, err := walk.NewNotifyIcon(ui.window)
	if err != nil {
		return err
	}
	ui.tray = tray
	_ = tray.SetIcon(ui.appIcon)
	_ = tray.SetToolTip("LocalDrop")
	tray.MouseUp().Attach(func(_, _ int, button walk.MouseButton) {
		if button == walk.LeftButton {
			ui.showWindow()
		}
	})
	tray.MessageClicked().Attach(ui.showWindow)
	items := []struct {
		text   string
		action func()
	}{
		{"Abrir o LocalDrop", ui.showWindow},
		{"Conectar celular", func() { ui.showWindow(); ui.showMobileConnection() }},
		{"", nil},
		{"Encerrar", ui.quit},
	}
	for _, item := range items {
		action := walk.NewSeparatorAction()
		if item.action != nil {
			action = walk.NewAction()
			_ = action.SetText(item.text)
			action.Triggered().Attach(item.action)
		}
		if err := tray.ContextMenu().Actions().Add(action); err != nil {
			return err
		}
	}
	return tray.SetVisible(true)
}

func (ui *nativeDesktopUI) build() error {
	ui.page = "home"
	ui.status = "Arraste arquivos para a janela ou use Ctrl+V para enviar um texto."
	ui.closed = make(chan struct{})
	ui.fonts = map[int]*walk.Font{}
	ui.glyphs = map[int]*walk.Font{}
	var e error
	ui.appIcon, e = walk.NewIconFromResourceId(appIconResourceID)
	if e != nil {
		return fmt.Errorf("carregar ícone do programa: %w", e)
	}
	ui.navIcons, e = loadNativeNavIcons()
	if e != nil {
		return fmt.Errorf("carregar ícones de navegação: %w", e)
	}
	w := MainWindow{
		AssignTo: &ui.window, Title: nativeWindowTitle, Size: Size{Width: 1000, Height: 720}, MinSize: Size{Width: 780, Height: 600},
		Layout: edgeToEdgeCanvasLayout(), OnDropFiles: ui.dropFiles,
		Children: []Widget{CustomWidget{
			AssignTo: &ui.canvas, PaintMode: PaintBuffered, Paint: ui.paint, InvalidatesOnResize: true, StretchFactor: 1,
			OnMouseDown: ui.mouseDown, OnMouseMove: ui.mouseMove, OnMouseUp: ui.mouseUp, OnKeyDown: ui.keyDown,
		}},
	}
	if e = w.Create(); e != nil {
		return e
	}
	if e = ui.window.SetIcon(ui.appIcon); e != nil {
		ui.window.Dispose()
		return fmt.Errorf("aplicar ícone do programa: %w", e)
	}
	applyLocalDropWindowFrame(ui.window)
	ui.canvas.MouseWheel().Attach(ui.mouseWheel)
	if e = ui.buildTray(); e != nil {
		log.Printf("ícone da bandeja indisponível: %v", e)
		ui.tray = nil
	}
	ui.window.Activating().Attach(ui.resumeIfStale)
	ui.window.SizeChanged().Attach(ui.resumeIfStale)
	ui.window.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if !ui.quitting && ui.tray != nil && reason == walk.CloseReasonUser {
			*canceled = true
			ui.hideToTray()
			return
		}
		ui.closeOnce.Do(func() { close(ui.closed) })
	})
	ui.applySnapshot(ui.client.snapshot())
	_ = ui.canvas.SetFocus()
	return nil
}

func (ui *nativeDesktopUI) run() int {
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
	return ui.window.Run()
}

func (ui *nativeDesktopUI) dispose() {
	if ui.tray != nil {
		_ = ui.tray.Dispose()
	}
	disposeNativeNavIcons(ui.navIcons)
	if ui.appIcon != nil {
		ui.appIcon.Dispose()
	}
	if ui.qr != nil {
		ui.qr.Dispose()
	}
	if ui.qrSmall != nil {
		ui.qrSmall.Dispose()
	}
	for _, f := range ui.fonts {
		f.Dispose()
	}
	for _, f := range ui.glyphs {
		f.Dispose()
	}
}

func showDesktopPanel(server *Server, _ string) bool {
	client, e := newNativeHostClient(server)
	if e != nil {
		log.Printf("criar sessão da interface nativa: %v", e)
		return false
	}
	ui := &nativeDesktopUI{server: server, client: client}
	if e = ui.build(); e != nil {
		ui.dispose()
		log.Printf("criar interface nativa: %v", e)
		return false
	}
	ui.run()
	ui.dispose()
	return true
}
