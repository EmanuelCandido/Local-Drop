//go:build windows

package main

import (
	"embed"
	"encoding/xml"
	"fmt"
	"image"
	imagecolor "image/color"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// The supplied SVG files stay as the source of truth. They are parsed and
// rasterized locally at startup, so the native desktop never needs a browser,
// WebView or external image service.
//
//go:embed assets/setting.svg assets/arrow-transfer2.svg
var nativeNavIconFiles embed.FS

type nativeNavIcon struct {
	mask   *image.Alpha
	active *alphaIcon // branco, sobre fundos de cor
	ink    *alphaIcon // escuro, sobre fundos claros
}

type svgPoint struct {
	x float64
	y float64
}

type svgSubpath struct {
	points []svgPoint
	closed bool
}

type svgStyle struct {
	fill          string
	stroke        string
	opacity       float64
	fillOpacity   float64
	strokeOpacity float64
	strokeWidth   float64
}

type svgShape struct {
	paths []svgSubpath
	style svgStyle
}

type svgDocument struct {
	minX, minY, width, height float64
	shapes                    []svgShape
}

// loadNativeNavIcons rasteriza os SVGs no tamanho exato em pixels da tela.
func loadNativeNavIcons(pixels int) (map[string]nativeNavIcon, error) {
	files := map[string]string{
		"nav-history":  "assets/arrow-transfer2.svg",
		"nav-settings": "assets/setting.svg",
	}
	icons := make(map[string]nativeNavIcon, len(files))
	for id, name := range files {
		data, err := nativeNavIconFiles.ReadFile(name)
		if err != nil {
			disposeNativeNavIcons(icons)
			return nil, fmt.Errorf("ler %s: %w", name, err)
		}
		mask, err := rasterizeSVGMask(data, pixels)
		if err != nil {
			disposeNativeNavIcons(icons)
			return nil, fmt.Errorf("renderizar %s: %w", name, err)
		}
		icons[id] = nativeNavIcon{mask: mask, active: newAlphaIcon(mask, 255, 255, 255), ink: newAlphaIcon(mask, 27, 31, 51)}
	}
	return icons, nil
}

func disposeNativeNavIcons(icons map[string]nativeNavIcon) {
	for _, icon := range icons {
		icon.active.dispose()
		if icon.ink != nil {
			icon.ink.dispose()
		}
	}
}

func rasterizeSVGMask(data []byte, size int) (*image.Alpha, error) {
	doc, err := parseSVGDocument(data)
	if err != nil {
		return nil, err
	}
	if size <= 0 || doc.width <= 0 || doc.height <= 0 {
		return nil, fmt.Errorf("dimensões SVG inválidas")
	}
	mask := image.NewAlpha(image.Rect(0, 0, size, size))
	const samples = 3
	const sampleCount = samples * samples
	for _, shape := range doc.shapes {
		fill := shape.style.fill != "none"
		stroke := shape.style.stroke != "none" && shape.style.strokeWidth > 0
		if !fill && !stroke {
			continue
		}
		for py := 0; py < size; py++ {
			for px := 0; px < size; px++ {
				var alphaSum float64
				for sy := 0; sy < samples; sy++ {
					for sx := 0; sx < samples; sx++ {
						x := doc.minX + (float64(px)+(float64(sx)+0.5)/samples)/float64(size)*doc.width
						y := doc.minY + (float64(py)+(float64(sy)+0.5)/samples)/float64(size)*doc.height
						sampleAlpha := 0.0
						if fill && pointInSVGShape(shape.paths, svgPoint{x: x, y: y}) {
							sampleAlpha = shape.style.fillOpacity
						}
						if stroke && pointOnSVGStroke(shape.paths, svgPoint{x: x, y: y}, shape.style.strokeWidth/2) && shape.style.strokeOpacity > sampleAlpha {
							sampleAlpha = shape.style.strokeOpacity
						}
						alphaSum += sampleAlpha * shape.style.opacity
					}
				}
				sourceAlpha := clampByte(alphaSum / sampleCount * 255)
				if sourceAlpha == 0 {
					continue
				}
				destinationAlpha := int(mask.AlphaAt(px, py).A)
				combined := int(sourceAlpha) + destinationAlpha*(255-int(sourceAlpha))/255
				mask.SetAlpha(px, py, imagecolor.Alpha{A: uint8(combined)})
			}
		}
	}
	return mask, nil
}

func clampByte(value float64) uint8 {
	if value <= 0 {
		return 0
	}
	if value >= 255 {
		return 255
	}
	return uint8(math.Round(value))
}

func parseSVGDocument(data []byte) (svgDocument, error) {
	doc := svgDocument{width: 24, height: 24}
	styles := []svgStyle{{fill: "black", stroke: "none", opacity: 1, fillOpacity: 1, strokeOpacity: 1, strokeWidth: 1}}
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return svgDocument{}, err
		}
		switch element := token.(type) {
		case xml.StartElement:
			style := styles[len(styles)-1]
			applySVGStyle(&style, element.Attr)
			styles = append(styles, style)
			if element.Name.Local == "svg" {
				if viewBox := svgAttribute(element.Attr, "viewBox"); viewBox != "" {
					values, parseErr := svgNumberList(viewBox)
					if parseErr != nil || len(values) != 4 {
						return svgDocument{}, fmt.Errorf("viewBox inválido: %q", viewBox)
					}
					doc.minX, doc.minY, doc.width, doc.height = values[0], values[1], values[2], values[3]
				}
			}
			if element.Name.Local == "path" {
				d := svgAttribute(element.Attr, "d")
				paths, parseErr := parseSVGPath(d)
				if parseErr != nil {
					return svgDocument{}, parseErr
				}
				doc.shapes = append(doc.shapes, svgShape{paths: paths, style: style})
			}
		case xml.EndElement:
			if len(styles) > 1 {
				styles = styles[:len(styles)-1]
			}
		}
	}
	if len(doc.shapes) == 0 {
		return svgDocument{}, fmt.Errorf("SVG sem paths")
	}
	return doc, nil
}

func applySVGStyle(style *svgStyle, attributes []xml.Attr) {
	for _, attribute := range attributes {
		value := strings.TrimSpace(attribute.Value)
		switch attribute.Name.Local {
		case "fill":
			style.fill = strings.ToLower(value)
		case "stroke":
			style.stroke = strings.ToLower(value)
		case "opacity":
			if opacity, err := strconv.ParseFloat(value, 64); err == nil {
				style.opacity *= opacity
			}
		case "fill-opacity":
			if opacity, err := strconv.ParseFloat(value, 64); err == nil {
				style.fillOpacity *= opacity
			}
		case "stroke-opacity":
			if opacity, err := strconv.ParseFloat(value, 64); err == nil {
				style.strokeOpacity *= opacity
			}
		case "stroke-width":
			if width, err := strconv.ParseFloat(value, 64); err == nil {
				style.strokeWidth = width
			}
		}
	}
}

func svgAttribute(attributes []xml.Attr, name string) string {
	for _, attribute := range attributes {
		if attribute.Name.Local == name {
			return attribute.Value
		}
	}
	return ""
}

func svgNumberList(value string) ([]float64, error) {
	tokens := tokenizeSVGPath(value)
	numbers := make([]float64, 0, len(tokens))
	for _, token := range tokens {
		if isSVGCommand(token) {
			return nil, fmt.Errorf("comando inesperado %q", token)
		}
		number, err := strconv.ParseFloat(token, 64)
		if err != nil {
			return nil, err
		}
		numbers = append(numbers, number)
	}
	return numbers, nil
}

func tokenizeSVGPath(value string) []string {
	var tokens []string
	for index := 0; index < len(value); {
		character := rune(value[index])
		if unicode.IsSpace(character) || character == ',' {
			index++
			continue
		}
		if unicode.IsLetter(character) {
			tokens = append(tokens, value[index:index+1])
			index++
			continue
		}
		start := index
		if value[index] == '+' || value[index] == '-' {
			index++
		}
		dotSeen := false
		for index < len(value) {
			current := value[index]
			if current >= '0' && current <= '9' {
				index++
				continue
			}
			if current == '.' && !dotSeen {
				dotSeen = true
				index++
				continue
			}
			if current == 'e' || current == 'E' {
				index++
				if index < len(value) && (value[index] == '+' || value[index] == '-') {
					index++
				}
				continue
			}
			break
		}
		if start == index {
			index++
			continue
		}
		tokens = append(tokens, value[start:index])
	}
	return tokens
}

func isSVGCommand(token string) bool {
	return len(token) == 1 && ((token[0] >= 'A' && token[0] <= 'Z') || (token[0] >= 'a' && token[0] <= 'z'))
}

func parseSVGPath(value string) ([]svgSubpath, error) {
	tokens := tokenizeSVGPath(value)
	var result []svgSubpath
	var currentPath []svgPoint
	var current, start, cubicControl, quadraticControl svgPoint
	var command byte
	previousCommand := byte(0)
	index := 0

	finishPath := func(closed bool) {
		if len(currentPath) > 0 {
			result = append(result, svgSubpath{points: append([]svgPoint(nil), currentPath...), closed: closed})
		}
		currentPath = nil
	}
	readNumber := func() (float64, error) {
		if index >= len(tokens) || isSVGCommand(tokens[index]) {
			return 0, fmt.Errorf("path SVG incompleto perto do item %d", index)
		}
		value, err := strconv.ParseFloat(tokens[index], 64)
		index++
		return value, err
	}
	readPoint := func(relative bool) (svgPoint, error) {
		x, err := readNumber()
		if err != nil {
			return svgPoint{}, err
		}
		y, err := readNumber()
		if err != nil {
			return svgPoint{}, err
		}
		if relative {
			x += current.x
			y += current.y
		}
		return svgPoint{x: x, y: y}, nil
	}

	for index < len(tokens) {
		if isSVGCommand(tokens[index]) {
			command = tokens[index][0]
			index++
		}
		if command == 0 {
			return nil, fmt.Errorf("path SVG sem comando")
		}
		relative := command >= 'a' && command <= 'z'
		switch command {
		case 'M', 'm':
			point, err := readPoint(relative)
			if err != nil {
				return nil, err
			}
			finishPath(false)
			current, start = point, point
			currentPath = append(currentPath, point)
			if relative {
				command = 'l'
			} else {
				command = 'L'
			}
		case 'L', 'l':
			point, err := readPoint(relative)
			if err != nil {
				return nil, err
			}
			current = point
			currentPath = append(currentPath, point)
		case 'H', 'h':
			x, err := readNumber()
			if err != nil {
				return nil, err
			}
			if relative {
				x += current.x
			}
			current = svgPoint{x: x, y: current.y}
			currentPath = append(currentPath, current)
		case 'V', 'v':
			y, err := readNumber()
			if err != nil {
				return nil, err
			}
			if relative {
				y += current.y
			}
			current = svgPoint{x: current.x, y: y}
			currentPath = append(currentPath, current)
		case 'C', 'c':
			control1, err := readPoint(relative)
			if err != nil {
				return nil, err
			}
			control2, err := readPoint(relative)
			if err != nil {
				return nil, err
			}
			end, err := readPoint(relative)
			if err != nil {
				return nil, err
			}
			currentPath = appendCubicSVG(currentPath, current, control1, control2, end)
			current, cubicControl = end, control2
		case 'S', 's':
			control1 := current
			if previousCommand == 'C' || previousCommand == 'c' || previousCommand == 'S' || previousCommand == 's' {
				control1 = svgPoint{x: 2*current.x - cubicControl.x, y: 2*current.y - cubicControl.y}
			}
			control2, err := readPoint(relative)
			if err != nil {
				return nil, err
			}
			end, err := readPoint(relative)
			if err != nil {
				return nil, err
			}
			currentPath = appendCubicSVG(currentPath, current, control1, control2, end)
			current, cubicControl = end, control2
		case 'Q', 'q':
			control, err := readPoint(relative)
			if err != nil {
				return nil, err
			}
			end, err := readPoint(relative)
			if err != nil {
				return nil, err
			}
			currentPath = appendQuadraticSVG(currentPath, current, control, end)
			current, quadraticControl = end, control
		case 'T', 't':
			control := current
			if previousCommand == 'Q' || previousCommand == 'q' || previousCommand == 'T' || previousCommand == 't' {
				control = svgPoint{x: 2*current.x - quadraticControl.x, y: 2*current.y - quadraticControl.y}
			}
			end, err := readPoint(relative)
			if err != nil {
				return nil, err
			}
			currentPath = appendQuadraticSVG(currentPath, current, control, end)
			current, quadraticControl = end, control
		case 'Z', 'z':
			current = start
			finishPath(true)
			command = 0
		default:
			return nil, fmt.Errorf("comando SVG %q não suportado", string(command))
		}
		if command != 0 {
			previousCommand = command
		}
	}
	finishPath(false)
	return result, nil
}

func appendCubicSVG(points []svgPoint, start, control1, control2, end svgPoint) []svgPoint {
	const steps = 20
	for step := 1; step <= steps; step++ {
		t := float64(step) / steps
		inverse := 1 - t
		points = append(points, svgPoint{
			x: inverse*inverse*inverse*start.x + 3*inverse*inverse*t*control1.x + 3*inverse*t*t*control2.x + t*t*t*end.x,
			y: inverse*inverse*inverse*start.y + 3*inverse*inverse*t*control1.y + 3*inverse*t*t*control2.y + t*t*t*end.y,
		})
	}
	return points
}

func appendQuadraticSVG(points []svgPoint, start, control, end svgPoint) []svgPoint {
	const steps = 16
	for step := 1; step <= steps; step++ {
		t := float64(step) / steps
		inverse := 1 - t
		points = append(points, svgPoint{
			x: inverse*inverse*start.x + 2*inverse*t*control.x + t*t*end.x,
			y: inverse*inverse*start.y + 2*inverse*t*control.y + t*t*end.y,
		})
	}
	return points
}

func pointInSVGShape(paths []svgSubpath, point svgPoint) bool {
	winding := 0
	for _, path := range paths {
		if len(path.points) < 3 {
			continue
		}
		for index := range path.points {
			from := path.points[index]
			to := path.points[(index+1)%len(path.points)]
			if from.y <= point.y {
				if to.y > point.y && svgCross(from, to, point) > 0 {
					winding++
				}
			} else if to.y <= point.y && svgCross(from, to, point) < 0 {
				winding--
			}
		}
	}
	return winding != 0
}

func svgCross(from, to, point svgPoint) float64 {
	return (to.x-from.x)*(point.y-from.y) - (point.x-from.x)*(to.y-from.y)
}

func pointOnSVGStroke(paths []svgSubpath, point svgPoint, radius float64) bool {
	radiusSquared := radius * radius
	for _, path := range paths {
		for index := 0; index+1 < len(path.points); index++ {
			if svgSegmentDistanceSquared(point, path.points[index], path.points[index+1]) <= radiusSquared {
				return true
			}
		}
		if path.closed && len(path.points) > 1 && svgSegmentDistanceSquared(point, path.points[len(path.points)-1], path.points[0]) <= radiusSquared {
			return true
		}
	}
	return false
}

func svgSegmentDistanceSquared(point, from, to svgPoint) float64 {
	dx, dy := to.x-from.x, to.y-from.y
	if dx == 0 && dy == 0 {
		dx, dy = point.x-from.x, point.y-from.y
		return dx*dx + dy*dy
	}
	t := ((point.x-from.x)*dx + (point.y-from.y)*dy) / (dx*dx + dy*dy)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	nearestX, nearestY := from.x+t*dx, from.y+t*dy
	dx, dy = point.x-nearestX, point.y-nearestY
	return dx*dx + dy*dy
}
