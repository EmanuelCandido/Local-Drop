//go:build windows

package main

import (
	"image"
	"math"
	"unsafe"

	qrcode "github.com/skip2/go-qrcode"
)

// rect usa coordenadas lógicas (96 DPI). O painter converte para pixels.
type rect struct{ X, Y, Width, Height int }

type color uint32 // COLORREF: 0x00BBGGRR

func rgb(r, g, b uint8) color { return color(uint32(r) | uint32(g)<<8 | uint32(b)<<16) }

func (c color) components() (uint8, uint8, uint8) {
	return uint8(c), uint8(c >> 8), uint8(c >> 16)
}

type fontKey struct {
	tenths int // tamanho em décimos de ponto
	bold   bool
	glyph  bool
	dpi    int
}

type penKey struct {
	c      color
	width  int
	dashed bool
}

// gdiCache guarda pincéis, canetas e fontes entre uma pintura e outra. Antes,
// cada pintura criava e destruía dezenas de objetos GDI.
type gdiCache struct {
	brushes map[color]uintptr
	pens    map[penKey]uintptr
	fonts   map[fontKey]uintptr
}

func newGDICache() *gdiCache {
	return &gdiCache{brushes: map[color]uintptr{}, pens: map[penKey]uintptr{}, fonts: map[fontKey]uintptr{}}
}

func (g *gdiCache) brush(c color) uintptr {
	if handle, ok := g.brushes[c]; ok {
		return handle
	}
	handle := call(procCreateSolidBrush, uintptr(c))
	g.brushes[c] = handle
	return handle
}

func (g *gdiCache) pen(c color, width int, dashed bool) uintptr {
	key := penKey{c, width, dashed}
	if handle, ok := g.pens[key]; ok {
		return handle
	}
	var handle uintptr
	if dashed {
		brush := logBrush{Style: 0, Color: uint32(c)}
		handle = call(procExtCreatePen, psGeometric|psDash|psEndcapFlat|psJoinMiter, uintptr(width), uintptr(unsafe.Pointer(&brush)), 0, 0)
	} else {
		handle = call(procCreatePen, psSolid, uintptr(width), uintptr(c))
	}
	g.pens[key] = handle
	return handle
}

func (g *gdiCache) font(points float64, bold, glyph bool, dpi int) uintptr {
	key := fontKey{int(math.Round(points * 10)), bold, glyph, dpi}
	if handle, ok := g.fonts[key]; ok {
		return handle
	}
	height := -int(math.Round(points * float64(dpi) / 72))
	weight := 400
	if bold {
		weight = 700
	}
	face := "Segoe UI"
	if glyph {
		face = "Segoe MDL2 Assets"
	}
	handle := call(procCreateFontW, uintptr(height), 0, 0, 0, uintptr(weight), 0, 0, 0,
		1 /* DEFAULT_CHARSET */, 0, 0, 5 /* CLEARTYPE_QUALITY */, 0, uintptr(unsafe.Pointer(utf16Ptr(face))))
	g.fonts[key] = handle
	return handle
}

// dropFonts descarta as fontes ao mudar o DPI da tela.
func (g *gdiCache) dropFonts() {
	for key, handle := range g.fonts {
		call(procDeleteObject, handle)
		delete(g.fonts, key)
	}
}

func (g *gdiCache) dispose() {
	for _, handle := range g.brushes {
		call(procDeleteObject, handle)
	}
	for _, handle := range g.pens {
		call(procDeleteObject, handle)
	}
	g.dropFonts()
	g.brushes, g.pens = map[color]uintptr{}, map[penKey]uintptr{}
}

// font descreve uma fonte da interface; o painter escolhe o objeto GDI certo
// para o DPI atual.
type font struct {
	points float64
	bold   bool
	glyph  bool
}

type painter struct {
	dc    uintptr
	dpi   int
	scale float64
	cache *gdiCache
}

func newPainter(dc uintptr, dpi int, cache *gdiCache) *painter {
	if dpi <= 0 {
		dpi = 96
	}
	call(procSetBkMode, dc, transparentBk)
	return &painter{dc: dc, dpi: dpi, scale: float64(dpi) / 96, cache: cache}
}

func (p *painter) px(value int) int32 { return int32(math.Round(float64(value) * p.scale)) }

func (p *painter) device(r rect) rect32 {
	return rect32{Left: p.px(r.X), Top: p.px(r.Y), Right: p.px(r.X + r.Width), Bottom: p.px(r.Y + r.Height)}
}

func (p *painter) fill(c color, r rect) {
	if r.Width <= 0 || r.Height <= 0 {
		return
	}
	area := p.device(r)
	call(procFillRect, p.dc, uintptr(unsafe.Pointer(&area)), p.cache.brush(c))
}

func (p *painter) rounded(c color, r rect, radius int) {
	if r.Width <= 0 || r.Height <= 0 {
		return
	}
	area := p.device(r)
	oldPen := call(procSelectObject, p.dc, call(procGetStockObject, nullPen))
	oldBrush := call(procSelectObject, p.dc, p.cache.brush(c))
	diameter := p.px(radius)
	call(procRoundRect, p.dc, uintptr(area.Left), uintptr(area.Top), uintptr(area.Right+1), uintptr(area.Bottom+1), uintptr(diameter), uintptr(diameter))
	call(procSelectObject, p.dc, oldBrush)
	call(procSelectObject, p.dc, oldPen)
}

func (p *painter) strokeWidth(width int) int {
	value := int(math.Round(float64(width) * p.scale))
	if value < 1 {
		value = 1
	}
	return value
}

func (p *painter) outlineWidth(c color, r rect, radius, width int, dashed bool) {
	if r.Width <= 0 || r.Height <= 0 {
		return
	}
	area := p.device(r)
	stroke := p.strokeWidth(width)
	half := int32(stroke / 2)
	oldPen := call(procSelectObject, p.dc, p.cache.pen(c, stroke, dashed))
	oldBrush := call(procSelectObject, p.dc, call(procGetStockObject, nullBrush))
	diameter := p.px(radius)
	call(procRoundRect, p.dc, uintptr(area.Left+half), uintptr(area.Top+half), uintptr(area.Right-half+1-int32(stroke%2)), uintptr(area.Bottom-half+1-int32(stroke%2)), uintptr(diameter), uintptr(diameter))
	call(procSelectObject, p.dc, oldBrush)
	call(procSelectObject, p.dc, oldPen)
}

func (p *painter) outline(c color, r rect, radius int) { p.outlineWidth(c, r, radius, 1, false) }

func (p *painter) dashed(c color, r rect, radius int) { p.outlineWidth(c, r, radius, 1, true) }

func (p *painter) dot(c color, r rect) {
	area := p.device(r)
	oldPen := call(procSelectObject, p.dc, call(procGetStockObject, nullPen))
	oldBrush := call(procSelectObject, p.dc, p.cache.brush(c))
	call(procEllipse, p.dc, uintptr(area.Left), uintptr(area.Top), uintptr(area.Right+1), uintptr(area.Bottom+1))
	call(procSelectObject, p.dc, oldBrush)
	call(procSelectObject, p.dc, oldPen)
}

func (p *painter) selectFont(f font) uintptr {
	return call(procSelectObject, p.dc, p.cache.font(f.points, f.bold, f.glyph, p.dpi))
}

func (p *painter) text(s string, f font, c color, r rect, format uint32) {
	if s == "" || r.Width <= 0 || r.Height <= 0 {
		return
	}
	area := p.device(r)
	old := p.selectFont(f)
	call(procSetTextColor, p.dc, uintptr(c))
	text := utf16Ptr(s)
	call(procDrawTextW, p.dc, uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(&area)), uintptr(format|dtNoPrefix))
	call(procSelectObject, p.dc, old)
}

// measure devolve a largura lógica de uma linha de texto.
func (p *painter) measure(s string, f font) int {
	if s == "" {
		return 0
	}
	area := rect32{Right: 4000, Bottom: 400}
	old := p.selectFont(f)
	text := utf16Ptr(s)
	call(procDrawTextW, p.dc, uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(&area)), dtSingleLine|dtCalcRect|dtNoPrefix)
	call(procSelectObject, p.dc, old)
	return int(math.Ceil(float64(area.Right-area.Left) / p.scale))
}

// measureHeight devolve a altura lógica de um texto quebrado em várias linhas.
func (p *painter) measureHeight(s string, f font, width int) int {
	area := rect32{Right: p.px(width), Bottom: 4000}
	old := p.selectFont(f)
	text := utf16Ptr(s)
	call(procDrawTextW, p.dc, uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(&area)), dtWordBreak|dtCalcRect|dtNoPrefix)
	call(procSelectObject, p.dc, old)
	return int(math.Ceil(float64(area.Bottom-area.Top) / p.scale))
}

// qr desenha o QR Code módulo a módulo, sempre nítido em qualquer DPI.
func (p *painter) qr(code *qrcode.QRCode, r rect, dark color) {
	if code == nil {
		return
	}
	modules := code.Bitmap()
	count := len(modules)
	if count == 0 {
		return
	}
	area := p.device(r)
	size := area.Right - area.Left
	if height := area.Bottom - area.Top; height < size {
		size = height
	}
	module := size / int32(count)
	if module < 1 {
		module = 1
	}
	offsetX := area.Left + (area.Right-area.Left-module*int32(count))/2
	offsetY := area.Top + (area.Bottom-area.Top-module*int32(count))/2
	brush := p.cache.brush(dark)
	for y, row := range modules {
		for x := 0; x < count; {
			if !row[x] {
				x++
				continue
			}
			start := x
			for x < count && row[x] {
				x++
			}
			cell := rect32{
				Left: offsetX + int32(start)*module, Top: offsetY + int32(y)*module,
				Right: offsetX + int32(x)*module, Bottom: offsetY + int32(y+1)*module,
			}
			call(procFillRect, p.dc, uintptr(unsafe.Pointer(&cell)), brush)
		}
	}
}

// alphaIcon é um ícone pré-multiplicado, desenhado com AlphaBlend.
type alphaIcon struct {
	dc, bitmap, old uintptr
	size            int
}

func newAlphaIcon(mask *image.Alpha, r, g, b uint8) *alphaIcon {
	width, height := mask.Rect.Dx(), mask.Rect.Dy()
	if width == 0 || height == 0 {
		return nil
	}
	header := bitmapInfoHeader{Width: int32(width), Height: -int32(height), Planes: 1, BitCount: 32, Compression: biRGB}
	header.Size = uint32(unsafe.Sizeof(header))
	var bits unsafe.Pointer
	bitmap := call(procCreateDIBSection, 0, uintptr(unsafe.Pointer(&header)), dibRGBColor, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		return nil
	}
	pixels := unsafe.Slice((*byte)(bits), width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			alpha := uint32(mask.AlphaAt(mask.Rect.Min.X+x, mask.Rect.Min.Y+y).A)
			offset := (y*width + x) * 4
			pixels[offset+0] = byte(uint32(b) * alpha / 255)
			pixels[offset+1] = byte(uint32(g) * alpha / 255)
			pixels[offset+2] = byte(uint32(r) * alpha / 255)
			pixels[offset+3] = byte(alpha)
		}
	}
	dc := call(procCreateCompatibleDC, 0)
	old := call(procSelectObject, dc, bitmap)
	return &alphaIcon{dc: dc, bitmap: bitmap, old: old, size: width}
}

func (icon *alphaIcon) dispose() {
	if icon == nil {
		return
	}
	call(procSelectObject, icon.dc, icon.old)
	call(procDeleteDC, icon.dc)
	call(procDeleteObject, icon.bitmap)
}

func (p *painter) icon(icon *alphaIcon, r rect) {
	if icon == nil {
		return
	}
	area := p.device(r)
	blend := uintptr(acSrcOver) | uintptr(255)<<16 | uintptr(acSrcAlpha)<<24
	call(procAlphaBlend, p.dc, uintptr(area.Left), uintptr(area.Top), uintptr(area.Right-area.Left), uintptr(area.Bottom-area.Top),
		icon.dc, 0, 0, uintptr(icon.size), uintptr(icon.size), blend)
}

// backBuffer é a superfície fora da tela onde cada quadro é montado antes de
// ir para a janela, sem piscar.
type backBuffer struct {
	dc, bitmap, old uintptr
	width, height   int32
	bits            unsafe.Pointer
}

func (b *backBuffer) ensure(reference uintptr, width, height int32) {
	if b.dc != 0 && b.width == width && b.height == height {
		return
	}
	b.release()
	header := bitmapInfoHeader{Width: width, Height: -height, Planes: 1, BitCount: 32, Compression: biRGB}
	header.Size = uint32(unsafe.Sizeof(header))
	b.bitmap = call(procCreateDIBSection, reference, uintptr(unsafe.Pointer(&header)), dibRGBColor, uintptr(unsafe.Pointer(&b.bits)), 0, 0)
	b.dc = call(procCreateCompatibleDC, reference)
	b.old = call(procSelectObject, b.dc, b.bitmap)
	b.width, b.height = width, height
}

func (b *backBuffer) release() {
	if b.dc == 0 {
		return
	}
	call(procSelectObject, b.dc, b.old)
	call(procDeleteDC, b.dc)
	call(procDeleteObject, b.bitmap)
	b.dc, b.bitmap, b.bits = 0, 0, nil
}

// image copia o conteúdo para uma imagem Go (usado nas prévias dos testes).
func (b *backBuffer) image() *image.RGBA {
	call(procGdiFlush)
	result := image.NewRGBA(image.Rect(0, 0, int(b.width), int(b.height)))
	pixels := unsafe.Slice((*byte)(b.bits), int(b.width)*int(b.height)*4)
	for index := 0; index < len(pixels); index += 4 {
		result.Pix[index+0] = pixels[index+2]
		result.Pix[index+1] = pixels[index+1]
		result.Pix[index+2] = pixels[index+0]
		result.Pix[index+3] = 255
	}
	return result
}
