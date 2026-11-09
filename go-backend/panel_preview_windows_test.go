//go:build windows

package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lxn/walk"
)

// TestRenderDesktopPreview desenha a janela fora da tela e salva PNGs em
// LOCALDROP_PREVIEW_DIR. Serve para revisar o visual sem abrir o programa.
func TestRenderDesktopPreview(t *testing.T) {
	directory := os.Getenv("LOCALDROP_PREVIEW_DIR")
	if directory == "" {
		t.Skip("defina LOCALDROP_PREVIEW_DIR para gerar as prévias")
	}
	// O walk só inicializa pincéis e canetas internos ao criar a primeira janela.
	host, err := walk.NewMainWindow()
	if err != nil {
		t.Fatal(err)
	}
	defer host.Dispose()
	server, err := NewServer(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	client, err := newNativeHostClient(server)
	if err != nil {
		t.Fatal(err)
	}
	ui := &nativeDesktopUI{server: server, client: client, closed: make(chan struct{}), fonts: map[int]*walk.Font{}, glyphs: map[int]*walk.Font{}, page: "home"}
	ui.navIcons, err = loadNativeNavIcons()
	if err != nil {
		t.Fatal(err)
	}
	defer ui.dispose()

	self := client.session.DeviceID
	now := time.Now().UTC()
	directoryPath := `C:\Users\voce\Downloads\LocalDrop`
	saved := directoryPath + `\relatorio.pdf`
	settings := settingsResponse{Device: DeviceView{DeviceID: self, DisplayName: "DESKTOP-EMANOEL", Host: true}, DownloadDirectory: &directoryPath}
	peers := []DeviceView{
		{DeviceID: self, DisplayName: "DESKTOP-EMANOEL", Host: true, Online: true},
		{DeviceID: "phone", DisplayName: "Galaxy S23", DeviceType: "MOBILE", Online: true, LastSeenAt: now},
		{DeviceID: "tablet", DisplayName: "iPad da sala", DeviceType: "TABLET", LastSeenAt: now.Add(-3 * time.Hour)},
	}
	file := func(name string, size int64, mime string) TransferFileView {
		return TransferFileView{ID: name, Name: name, Size: size, MimeType: mime, DownloadAvailable: true}
	}
	text := file("Mensagem LocalDrop.txt", 320, "text/plain; charset=utf-8")
	text.SavedPath = &saved
	pdf := file("relatorio.pdf", 2_400_000, "application/pdf")
	pdf.SavedPath = &saved
	transfers := []TransferView{
		{ID: "t1", SenderID: "phone", ReceiverID: self, State: "TRANSFERRING", TotalBytes: 1_800_000_000, ReceivedBytes: 1_130_000_000, CreatedAt: now.Add(-2 * time.Minute), Files: []TransferFileView{file("VID_20261022_1840.mp4", 1_800_000_000, "video/mp4")}},
		{ID: "t2", SenderID: self, ReceiverID: "phone", State: "WAITING_ACCEPTANCE", TotalBytes: 52_000_000, CreatedAt: now.Add(-5 * time.Minute), Files: []TransferFileView{file("fotos-viagem.zip", 52_000_000, "application/zip")}},
		{ID: "t3", SenderID: "phone", ReceiverID: self, State: "COMPLETED", TotalBytes: 320, CreatedAt: now.Add(-40 * time.Minute), Files: []TransferFileView{text}},
		{ID: "t4", SenderID: self, ReceiverID: "tablet", State: "COMPLETED", TotalBytes: 8_400_000, CreatedAt: now.Add(-26 * time.Hour), Files: []TransferFileView{file("apresentacao.pptx", 8_400_000, "application/vnd.ms-powerpoint"), file("notas.docx", 30_000, "application/msword")}},
		{ID: "t5", SenderID: "phone", ReceiverID: self, State: "COMPLETED", TotalBytes: 2_400_000, CreatedAt: now.Add(-28 * time.Hour), Files: []TransferFileView{pdf}},
	}
	ui.speeds = map[string]transferSpeed{"t1": {rate: 38_500_000}}

	render := func(name string, size walk.Size, prepare func()) {
		prepare()
		bitmap, err := walk.NewBitmapForDPI(size, 96)
		if err != nil {
			t.Fatal(err)
		}
		defer bitmap.Dispose()
		canvas, err := walk.NewCanvasFromImage(bitmap)
		if err != nil {
			t.Fatal(err)
		}
		ui.paintBounds(canvas, walk.Rectangle{Width: size.Width, Height: size.Height})
		canvas.Dispose()
		image, err := bitmap.ToImage()
		if err != nil {
			t.Fatal(err)
		}
		output, err := os.Create(filepath.Join(directory, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer output.Close()
		if err := png.Encode(output, image); err != nil {
			t.Fatal(err)
		}
	}
	full := nativeSnapshot{Devices: peers, Transfers: transfers, Settings: settings, Pairings: []pairingView{{RequestID: "p1", DisplayName: "Moto G84", Code: "482 913"}}}
	render("inicio", walk.Size{Width: 984, Height: 681}, func() { ui.page = "home"; ui.applySnapshot(full) })
	render("inicio-minimo", walk.Size{Width: 764, Height: 561}, func() { ui.page = "home"; ui.applySnapshot(full) })
	render("primeiro-uso", walk.Size{Width: 984, Height: 681}, func() {
		ui.applySnapshot(nativeSnapshot{Devices: peers[:1], Settings: settings})
	})
	render("ajustes", walk.Size{Width: 984, Height: 681}, func() { ui.page = "settings"; ui.applySnapshot(full) })
}
