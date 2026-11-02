package brand

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/image/vector"
)

// icon is one parsed SVG in the small subset the brand artwork uses: an svg
// root, optional g elements for inherited fill, and filled path, rect, and
// circle shapes. Strokes, transforms, gradients, and arcs are deliberately not
// supported; keeping the subset tiny keeps the Client free of an SVG
// dependency and the artwork reviewable as plain text.
type icon struct {
	width, height float64
	minX, minY    float64
	shapes        []shape
}

type shape struct {
	fill    string
	opacity float64
	path    []pathCommand
}

// pathCommand is one absolute path instruction. op is one of M, L, Q, C, Z;
// args holds (x,y) for M/L, (cx,cy,x,y) for Q, and (c1x,c1y,c2x,c2y,x,y) for C.
type pathCommand struct {
	op   byte
	args [6]float64
}

func (s shape) emit(r *vector.Rasterizer, minX, minY, scaleX, scaleY float64) {
	x := func(v float64) float32 { return float32((v - minX) * scaleX) }
	y := func(v float64) float32 { return float32((v - minY) * scaleY) }
	for _, c := range s.path {
		switch c.op {
		case 'M':
			r.MoveTo(x(c.args[0]), y(c.args[1]))
		case 'L':
			r.LineTo(x(c.args[0]), y(c.args[1]))
		case 'Q':
			r.QuadTo(x(c.args[0]), y(c.args[1]), x(c.args[2]), y(c.args[3]))
		case 'C':
			r.CubeTo(
				x(c.args[0]), y(c.args[1]),
				x(c.args[2]), y(c.args[3]),
				x(c.args[4]), y(c.args[5]),
			)
		case 'Z':
			r.ClosePath()
		}
	}
}

// png rasterises the icon at size×size and encodes it. A nil colours map means
// the SVG's literal colours are used as-is.
func (ic *icon) png(size int, colours map[string]color.NRGBA) []byte {
	img := ic.render(size, colours)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// render rasterises the icon into a size×size RGBA image. Every shape gets its
// own antialiased coverage mask, so rounded corners and the fox silhouette stay
// smooth at any size instead of being sampled at pixel centres.
func (ic *icon) render(size int, colours map[string]color.NRGBA) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	scaleX := float64(size) / ic.width
	scaleY := float64(size) / ic.height
	for _, shape := range ic.shapes {
		fill, ok := resolveColour(shape.fill, colours)
		if !ok {
			continue
		}
		if shape.opacity < 1 {
			fill.A = uint8(float64(fill.A) * shape.opacity)
		}
		if fill.A == 0 {
			continue
		}
		var raster vector.Rasterizer
		raster.Reset(size, size)
		shape.emit(&raster, ic.minX, ic.minY, scaleX, scaleY)
		raster.Draw(dst, dst.Bounds(), image.NewUniform(fill), image.Point{})
	}
	return dst
}

// resolveColour turns an SVG fill into a colour. Palette references are written
// as var(--name); everything else is a literal #rrggbb or #rgb value.
func resolveColour(value string, colours map[string]color.NRGBA) (color.NRGBA, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == "none" || value == "transparent" {
		return color.NRGBA{}, false
	}
	if name, ok := paletteReference(value); ok {
		c, found := colours[name]
		return c, found
	}
	if strings.HasPrefix(value, "#") {
		return parseHex(value)
	}
	return color.NRGBA{}, false
}

func paletteReference(value string) (string, bool) {
	if !strings.HasPrefix(value, "var(--") || !strings.HasSuffix(value, ")") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(value, "var(--"), ")")
	return name, name != ""
}

func parseHex(value string) (color.NRGBA, bool) {
	hex := strings.TrimPrefix(value, "#")
	switch len(hex) {
	case 3:
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	case 6:
	default:
		return color.NRGBA{}, false
	}
	parsed, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	return color.NRGBA{
		R: uint8(parsed >> 16),
		G: uint8(parsed >> 8),
		B: uint8(parsed),
		A: 0xff,
	}, true
}

// parseSVG reads the brand subset from data.
func parseSVG(data []byte) (*icon, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	ic := &icon{width: 64, height: 64}
	fills := []string{""}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("brand: parse SVG: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "svg":
				if viewBox := attr(element, "viewBox"); viewBox != "" {
					if err := ic.readViewBox(viewBox); err != nil {
						return nil, err
					}
				} else {
					ic.width = attrFloat(element, "width", ic.width)
					ic.height = attrFloat(element, "height", ic.height)
				}
				fills[0] = attr(element, "fill")
			case "g":
				fill := attr(element, "fill")
				if fill == "" {
					fill = fills[len(fills)-1]
				}
				fills = append(fills, fill)
			case "path":
				d := attr(element, "d")
				if d == "" {
					continue
				}
				path, err := parsePath(d)
				if err != nil {
					return nil, err
				}
				ic.addShape(fills[len(fills)-1], element, path)
			case "rect":
				path := roundedRect(
					attrFloat(element, "x", 0),
					attrFloat(element, "y", 0),
					attrFloat(element, "width", 0),
					attrFloat(element, "height", 0),
					attrFloat(element, "rx", 0),
					attrFloat(element, "ry", 0),
				)
				ic.addShape(fills[len(fills)-1], element, path)
			case "circle":
				path := ellipsePath(
					attrFloat(element, "cx", 0),
					attrFloat(element, "cy", 0),
					attrFloat(element, "r", 0),
					attrFloat(element, "r", 0),
				)
				ic.addShape(fills[len(fills)-1], element, path)
			}
		case xml.EndElement:
			if element.Name.Local == "g" && len(fills) > 1 {
				fills = fills[:len(fills)-1]
			}
		}
	}
	if ic.width <= 0 || ic.height <= 0 {
		return nil, fmt.Errorf("brand: SVG has no usable viewBox")
	}
	return ic, nil
}

func (ic *icon) addShape(inheritedFill string, element xml.StartElement, path []pathCommand) {
	fill := attr(element, "fill")
	if fill == "" {
		fill = inheritedFill
	}
	ic.shapes = append(ic.shapes, shape{
		fill:    fill,
		opacity: clamp01(attrFloat(element, "fill-opacity", 1)),
		path:    path,
	})
}

func (ic *icon) readViewBox(value string) error {
	fields := strings.Fields(value)
	if len(fields) != 4 {
		return fmt.Errorf("brand: viewBox %q is not four numbers", value)
	}
	values := make([]float64, 4)
	for i, field := range fields {
		parsed, err := strconv.ParseFloat(field, 64)
		if err != nil {
			return fmt.Errorf("brand: viewBox %q: %w", value, err)
		}
		values[i] = parsed
	}
	ic.minX, ic.minY = values[0], values[1]
	ic.width, ic.height = values[2], values[3]
	return nil
}

func attr(element xml.StartElement, name string) string {
	for _, a := range element.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func attrFloat(element xml.StartElement, name string, fallback float64) float64 {
	value := attr(element, name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// Path data parsing ---------------------------------------------------------

var pathTokenPattern = regexp.MustCompile(`[A-Za-z]|[-+]?(?:[0-9]*\.[0-9]+|[0-9]+\.?)(?:[eE][-+]?[0-9]+)?`)

func parsePath(d string) ([]pathCommand, error) {
	tokens := pathTokenPattern.FindAllString(d, -1)
	var commands []pathCommand
	var cx, cy, startX, startY float64
	var controlX, controlY float64
	var last byte
	i := 0

	number := func() (float64, error) {
		if i >= len(tokens) || !isNumberToken(tokens[i]) {
			return 0, fmt.Errorf("brand: expected a number in path %q", d)
		}
		token := tokens[i]
		i++
		value, err := strconv.ParseFloat(token, 64)
		if err != nil {
			return 0, fmt.Errorf("brand: path number %q: %w", token, err)
		}
		return value, nil
	}
	hasNumber := func() bool { return i < len(tokens) && isNumberToken(tokens[i]) }
	// offset reloads the current point for one relative segment. SVG applies
	// the offset of each repeated segment, not of the command as a whole.
	offset := func(relative bool) (float64, float64) {
		if relative {
			return cx, cy
		}
		return 0, 0
	}
	for i < len(tokens) {
		if !isCommandToken(tokens[i]) {
			return nil, fmt.Errorf("brand: expected a command in path %q, got %q", d, tokens[i])
		}
		command := tokens[i][0]
		i++
		relative := command >= 'a' && command <= 'z'
		upper := command
		if relative {
			upper = command - 'a' + 'A'
		}

		switch upper {
		case 'M':
			ox, oy := offset(relative)
			x, err := number()
			if err != nil {
				return nil, err
			}
			y, err := number()
			if err != nil {
				return nil, err
			}
			x, y = x+ox, y+oy
			commands = append(commands, pathCommand{op: 'M', args: [6]float64{x, y}})
			cx, cy, startX, startY = x, y, x, y
			for hasNumber() {
				ox, oy = offset(relative)
				x, err = number()
				if err != nil {
					return nil, err
				}
				y, err = number()
				if err != nil {
					return nil, err
				}
				x, y = x+ox, y+oy
				commands = append(commands, pathCommand{op: 'L', args: [6]float64{x, y}})
				cx, cy = x, y
			}
		case 'L':
			if !hasNumber() {
				return nil, fmt.Errorf("brand: L needs coordinates in path %q", d)
			}
			for hasNumber() {
				ox, oy := offset(relative)
				x, err := number()
				if err != nil {
					return nil, err
				}
				y, err := number()
				if err != nil {
					return nil, err
				}
				x, y = x+ox, y+oy
				commands = append(commands, pathCommand{op: 'L', args: [6]float64{x, y}})
				cx, cy = x, y
			}
		case 'H':
			for hasNumber() {
				ox, _ := offset(relative)
				x, err := number()
				if err != nil {
					return nil, err
				}
				cx = x + ox
				commands = append(commands, pathCommand{op: 'L', args: [6]float64{cx, cy}})
			}
		case 'V':
			for hasNumber() {
				_, oy := offset(relative)
				y, err := number()
				if err != nil {
					return nil, err
				}
				cy = y + oy
				commands = append(commands, pathCommand{op: 'L', args: [6]float64{cx, cy}})
			}
		case 'C':
			for hasNumber() {
				ox, oy := offset(relative)
				x1, err := number()
				if err != nil {
					return nil, err
				}
				y1, err := number()
				if err != nil {
					return nil, err
				}
				x2, err := number()
				if err != nil {
					return nil, err
				}
				y2, err := number()
				if err != nil {
					return nil, err
				}
				x, err := number()
				if err != nil {
					return nil, err
				}
				y, err := number()
				if err != nil {
					return nil, err
				}
				x1, y1 = x1+ox, y1+oy
				x2, y2 = x2+ox, y2+oy
				x, y = x+ox, y+oy
				commands = append(commands, pathCommand{op: 'C', args: [6]float64{x1, y1, x2, y2, x, y}})
				cx, cy, controlX, controlY = x, y, x2, y2
			}
		case 'S':
			for hasNumber() {
				ox, oy := offset(relative)
				x2, err := number()
				if err != nil {
					return nil, err
				}
				y2, err := number()
				if err != nil {
					return nil, err
				}
				x, err := number()
				if err != nil {
					return nil, err
				}
				y, err := number()
				if err != nil {
					return nil, err
				}
				x1, y1 := cx, cy
				if last == 'C' || last == 'S' {
					x1, y1 = 2*cx-controlX, 2*cy-controlY
				}
				x2, y2 = x2+ox, y2+oy
				x, y = x+ox, y+oy
				commands = append(commands, pathCommand{op: 'C', args: [6]float64{x1, y1, x2, y2, x, y}})
				cx, cy, controlX, controlY = x, y, x2, y2
				last = 'S'
			}
		case 'Q':
			for hasNumber() {
				ox, oy := offset(relative)
				x1, err := number()
				if err != nil {
					return nil, err
				}
				y1, err := number()
				if err != nil {
					return nil, err
				}
				x, err := number()
				if err != nil {
					return nil, err
				}
				y, err := number()
				if err != nil {
					return nil, err
				}
				x1, y1 = x1+ox, y1+oy
				x, y = x+ox, y+oy
				commands = append(commands, pathCommand{op: 'Q', args: [6]float64{x1, y1, x, y}})
				cx, cy, controlX, controlY = x, y, x1, y1
			}
		case 'T':
			for hasNumber() {
				ox, oy := offset(relative)
				x, err := number()
				if err != nil {
					return nil, err
				}
				y, err := number()
				if err != nil {
					return nil, err
				}
				x1, y1 := cx, cy
				if last == 'Q' || last == 'T' {
					x1, y1 = 2*cx-controlX, 2*cy-controlY
				}
				x, y = x+ox, y+oy
				commands = append(commands, pathCommand{op: 'Q', args: [6]float64{x1, y1, x, y}})
				cx, cy, controlX, controlY = x, y, x1, y1
				last = 'T'
			}
		case 'Z':
			commands = append(commands, pathCommand{op: 'Z'})
			cx, cy = startX, startY
		default:
			return nil, fmt.Errorf("brand: unsupported path command %q in path %q", string(command), d)
		}
		last = upper
	}
	return commands, nil
}

func isNumberToken(token string) bool {
	if token == "" {
		return false
	}
	c := token[0]
	return c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9')
}

func isCommandToken(token string) bool {
	return len(token) == 1 && ((token[0] >= 'A' && token[0] <= 'Z') || (token[0] >= 'a' && token[0] <= 'z'))
}

// roundedRect builds a clockwise rounded-rectangle path. A single radius is
// applied to both axes when only rx or ry is given, matching CSS/SVG.
func roundedRect(x, y, w, h, rx, ry float64) []pathCommand {
	if w <= 0 || h <= 0 {
		return nil
	}
	if rx <= 0 && ry > 0 {
		rx = ry
	}
	if ry <= 0 && rx > 0 {
		ry = rx
	}
	rx = math.Min(math.Max(rx, 0), w/2)
	ry = math.Min(math.Max(ry, 0), h/2)
	if rx == 0 || ry == 0 {
		return []pathCommand{
			{op: 'M', args: [6]float64{x, y}},
			{op: 'L', args: [6]float64{x + w, y}},
			{op: 'L', args: [6]float64{x + w, y + h}},
			{op: 'L', args: [6]float64{x, y + h}},
			{op: 'Z'},
		}
	}
	const k = 0.5522847498307936
	kx, ky := k*rx, k*ry
	return []pathCommand{
		{op: 'M', args: [6]float64{x + rx, y}},
		{op: 'L', args: [6]float64{x + w - rx, y}},
		{op: 'C', args: [6]float64{x + w - rx + kx, y, x + w, y + ry - ky, x + w, y + ry}},
		{op: 'L', args: [6]float64{x + w, y + h - ry}},
		{op: 'C', args: [6]float64{x + w, y + h - ry + ky, x + w - rx + kx, y + h, x + w - rx, y + h}},
		{op: 'L', args: [6]float64{x + rx, y + h}},
		{op: 'C', args: [6]float64{x + rx - kx, y + h, x, y + h - ry + ky, x, y + h - ry}},
		{op: 'L', args: [6]float64{x, y + ry}},
		{op: 'C', args: [6]float64{x, y + ry - ky, x + rx - kx, y, x + rx, y}},
		{op: 'Z'},
	}
}

// ellipsePath builds a clockwise ellipse path from four cubic segments.
func ellipsePath(cx, cy, rx, ry float64) []pathCommand {
	if rx <= 0 || ry <= 0 {
		return nil
	}
	const k = 0.5522847498307936
	kx, ky := k*rx, k*ry
	return []pathCommand{
		{op: 'M', args: [6]float64{cx + rx, cy}},
		{op: 'C', args: [6]float64{cx + rx, cy + ky, cx + kx, cy + ry, cx, cy + ry}},
		{op: 'C', args: [6]float64{cx - kx, cy + ry, cx - rx, cy + ky, cx - rx, cy}},
		{op: 'C', args: [6]float64{cx - rx, cy - ky, cx - kx, cy - ry, cx, cy - ry}},
		{op: 'C', args: [6]float64{cx + kx, cy - ry, cx + rx, cy - ky, cx + rx, cy}},
		{op: 'Z'},
	}
}
