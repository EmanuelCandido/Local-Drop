//go:build windows

package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRenderDesktopPreview desenha a janela fora da tela e salva PNGs em
// LOCALDROP_PREVIEW_DIR. Serve para revisar o visual sem abrir o programa.
func TestRenderDesktopPreview(t *testing.T) {
	directory := os.Getenv("LOCALDROP_PREVIEW_DIR")
	if directory == "" {
		t.Skip("defina LOCALDROP_PREVIEW_DIR para gerar as prévias")
	}
	server, err := NewServer(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	client, err := newNativeHostClient(server)
	if err != nil {
		t.Fatal(err)
	}
	cache := newGDICache()
	defer cache.dispose()
	ui := &nativeDesktopUI{server: server, client: client, closed: make(chan struct{}), cache: cache, page: "home", storageBytes: 3_400_000}
	ui.main = &canvasWindow{cache: cache, dpi: 96, background: canvasBG}
	ui.target = ui.main
	defer ui.dispose()

	self := client.session.DeviceID
	now := time.Now().UTC()
	directoryPath := `C:\Users\voce\Downloads\LocalDrop`
	saved := directoryPath + `\relatorio.pdf`
	settings := settingsResponse{Device: DeviceView{DeviceID: self, DisplayName: "DESKTOP-EMANOEL", Host: true}, DownloadDirectory: &directoryPath}
	peers := []DeviceView{
		{DeviceID: self, DisplayName: "DESKTOP-EMANOEL", Host: true, Online: true},
		{DeviceID: "phone", DisplayName: "Celular Android", DeviceType: "MOBILE", Online: true, LastSeenAt: now},
		{DeviceID: "tablet", DisplayName: "iPad", DeviceType: "TABLET", LastSeenAt: now.Add(-3 * time.Hour)},
	}
	file := func(name string, size int64, mime string) TransferFileView {
		return TransferFileView{ID: name, Name: name, Size: size, MimeType: mime, DownloadAvailable: true}
	}
	text := file("Mensagem LocalDrop.txt", 320, "text/plain; charset=utf-8")
	text.SavedPath = &saved
	pdf := file("relatorio.pdf", 2_400_000, "application/pdf")
	pdf.SavedPath = &saved
	expired := file("fotos.zip", 9_000_000, "application/zip")
	expired.DownloadAvailable = false
	transfers := []TransferView{
		{ID: "t1", SenderID: "phone", ReceiverID: self, State: "TRANSFERRING", TotalBytes: 1_800_000_000, ReceivedBytes: 1_130_000_000, CreatedAt: now.Add(-2 * time.Minute), Files: []TransferFileView{file("VID_20261022_1840.mp4", 1_800_000_000, "video/mp4")}},
		{ID: "t2", SenderID: self, ReceiverID: "phone", State: "WAITING_ACCEPTANCE", TotalBytes: 52_000_000, CreatedAt: now.Add(-5 * time.Minute), Files: []TransferFileView{file("fotos-viagem.zip", 52_000_000, "application/zip")}},
		{ID: "t3", SenderID: "phone", ReceiverID: self, State: "COMPLETED", TotalBytes: 320, CreatedAt: now.Add(-40 * time.Minute), Files: []TransferFileView{text}},
		{ID: "t4", SenderID: self, ReceiverID: "tablet", State: "COMPLETED", TotalBytes: 9_000_000, CreatedAt: now.Add(-26 * time.Hour), Files: []TransferFileView{expired}},
		{ID: "t5", SenderID: "phone", ReceiverID: self, State: "COMPLETED", TotalBytes: 2_400_000, CreatedAt: now.Add(-28 * time.Hour), Files: []TransferFileView{pdf}},
		{ID: "t6", SenderID: "phone", ReceiverID: self, State: "FAILED", TotalBytes: 2_400_000, CreatedAt: now.Add(-30 * time.Hour), Files: []TransferFileView{file("planilha.xlsx", 80_000, "application/vnd.ms-excel")}},
	}
	ui.speeds = map[string]transferSpeed{"t1": {rate: 38_500_000}}

	render := func(name string, width, height, dpi int, prepare func()) {
		prepare()
		ui.main.dpi = dpi
		scale := float64(dpi) / 96
		var buffer backBuffer
		buffer.ensure(0, int32(float64(width)*scale), int32(float64(height)*scale))
		defer buffer.release()
		p := newPainter(buffer.dc, dpi, cache)
		bounds := rect{Width: width, Height: height}
		ui.main.hits = ui.main.hits[:0]
		p.fill(canvasBG, bounds)
		ui.paint(p, bounds)
		if ui.main.focusVisible {
			if r, ok := ui.main.focusRect(); ok {
				p.outlineWidth(primary, rect{X: r.X - 3, Y: r.Y - 3, Width: r.Width + 6, Height: r.Height + 6}, 13, 2, false)
			}
		}
		output, err := os.Create(filepath.Join(directory, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer output.Close()
		if err := png.Encode(output, buffer.image()); err != nil {
			t.Fatal(err)
		}
	}
	full := nativeSnapshot{Devices: peers, Transfers: transfers, Settings: settings, Pairings: []pairingView{{RequestID: "p1", DisplayName: "Moto G84", Code: "482913"}}}
	render("inicio", 984, 681, 96, func() { ui.page = "home"; ui.applySnapshot(full) })
	render("inicio-minimo", 764, 561, 96, func() { ui.page = "home"; ui.applySnapshot(full) })
	render("inicio-150dpi", 984, 681, 144, func() {
		ui.page = "home"
		ui.applySnapshot(full)
		ui.main.focus, ui.main.focusVisible = "send-text", true
	})
	render("rede-bloqueada", 984, 681, 96, func() {
		ui.main.focusVisible = false
		ui.health = networkHealth{Checked: true, PublicNetwork: true}
		ui.paired = &pairedNotice{deviceID: "phone", name: "Celular Android", at: time.Now()}
		ui.applySnapshot(nativeSnapshot{Devices: peers, Transfers: transfers[:2], Settings: settings})
	})
	render("primeiro-uso", 984, 681, 96, func() {
		ui.health, ui.paired = networkHealth{}, nil
		ui.applySnapshot(nativeSnapshot{Devices: peers[:1], Settings: settings})
	})
	render("ajustes", 984, 681, 96, func() { ui.page = "settings"; ui.applySnapshot(full) })
	render("ajustes-minimo", 764, 561, 96, func() { ui.page = "settings"; ui.applySnapshot(full) })
	render("aviso", 984, 681, 96, func() {
		ui.page = "home"
		ui.applySnapshot(full)
		explained := explainError(problem(422, "INSUFFICIENT_STORAGE", ""))
		ui.modal = "error|" + explained.Title + "\n" + explained.Hint
	})
	ui.modal = ""
}
