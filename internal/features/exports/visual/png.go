package visual

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const pngMIMEType = "image/png"

var (
	paperColor     = color.RGBA{R: 247, G: 239, B: 219, A: 255}
	cardColor      = color.RGBA{R: 255, G: 250, B: 240, A: 255}
	inkColor       = color.RGBA{R: 58, G: 37, B: 34, A: 255}
	mutedInkColor  = color.RGBA{R: 118, G: 94, B: 76, A: 255}
	burgundyColor  = color.RGBA{R: 107, G: 36, B: 49, A: 255}
	goldColor      = color.RGBA{R: 178, G: 146, B: 85, A: 255}
	lightGoldColor = color.RGBA{R: 214, G: 189, B: 131, A: 255}
)

func renderPNG(value scene) ([]byte, error) {
	width, height := int(math.Ceil(value.Width)), int(math.Ceil(value.Height))
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: paperColor}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(0, 0, width, 132), &image.Uniform{
		C: color.RGBA{R: 251, G: 246, B: 232, A: 255},
	}, image.Point{}, draw.Src)
	drawRectStroke(canvas, image.Rect(18, 18, width-18, height-18), burgundyColor, 2)
	drawRectStroke(canvas, image.Rect(25, 25, width-25, height-25), goldColor, 1)
	regularFont, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	boldFont, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	titleFace, err := newFace(boldFont, 36)
	if err != nil {
		return nil, err
	}
	defer titleFace.Close()
	subtitleFace, err := newFace(boldFont, 14)
	if err != nil {
		return nil, err
	}
	defer subtitleFace.Close()
	nameFace, err := newFace(boldFont, 17)
	if err != nil {
		return nil, err
	}
	defer nameFace.Close()
	detailFace, err := newFace(regularFont, 12)
	if err != nil {
		return nil, err
	}
	defer detailFace.Close()
	footerFace, err := newFace(regularFont, 11)
	if err != nil {
		return nil, err
	}
	defer footerFace.Close()
	drawCenteredText(canvas, titleFace, inkColor, value.Title, width/2, 77)
	drawCenteredText(canvas, subtitleFace, mutedInkColor, value.Subtitle, width/2, 104)
	drawLine(canvas, int(value.Width/2-150), 122, int(value.Width/2+150), 122, goldColor, 2)
	for _, edge := range value.ParentEdges {
		drawPolyline(canvas, edge.Points, goldColor, 2, false)
	}
	for _, edge := range value.UnionEdges {
		drawPolyline(canvas, edge.Points, burgundyColor, 2, true)
		if len(edge.Points) > 0 {
			middle := edge.Points[len(edge.Points)/2]
			drawCircle(canvas, int(math.Round(middle.X)), int(math.Round(middle.Y)), 4, goldColor)
		}
	}
	for _, node := range value.Nodes {
		rectangle := image.Rect(
			int(math.Round(node.X)),
			int(math.Round(node.Y)),
			int(math.Round(node.X+nodeWidth)),
			int(math.Round(node.Y+nodeHeight)),
		)
		fillRoundedRect(canvas, rectangle, 12, burgundyColor)
		fillRoundedRect(canvas, rectangle.Inset(2), 10, cardColor)
		drawRoundedRectStroke(canvas, rectangle.Inset(6), 8, lightGoldColor, 1)
		drawLine(canvas, rectangle.Min.X+82, rectangle.Min.Y+12, rectangle.Min.X+150, rectangle.Min.Y+12, goldColor, 3)
		nameY := rectangle.Min.Y + 47
		if len(node.NameLines) == 2 {
			nameY = rectangle.Min.Y + 38
		}
		for index, line := range node.NameLines {
			drawCenteredText(canvas, nameFace, inkColor, line, rectangle.Min.X+rectangle.Dx()/2, nameY+index*21)
		}
		drawCenteredText(canvas, detailFace, mutedInkColor, node.Detail, rectangle.Min.X+rectangle.Dx()/2, rectangle.Min.Y+82)
	}
	if value.EmptyText != "" {
		drawCenteredText(canvas, nameFace, mutedInkColor, value.EmptyText, width/2, int(headerHeight)+60)
	}
	drawCenteredText(canvas, footerFace, mutedInkColor, value.Footer, width/2, height-27)
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func newFace(value *opentype.Font, size float64) (font.Face, error) {
	return opentype.NewFace(value, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

func drawCenteredText(
	destination *image.RGBA,
	face font.Face,
	textColor color.Color,
	text string,
	centerX int,
	baselineY int,
) {
	width := font.MeasureString(face, text).Ceil()
	drawer := font.Drawer{
		Dst: destination, Src: image.NewUniform(textColor), Face: face,
		Dot: fixed.P(centerX-width/2, baselineY),
	}
	drawer.DrawString(text)
}

func drawPolyline(destination *image.RGBA, points []point, lineColor color.RGBA, thickness int, dashed bool) {
	for index := 1; index < len(points); index++ {
		from, to := points[index-1], points[index]
		if dashed {
			drawDashedLine(destination, from, to, lineColor, thickness, 8, 6)
			continue
		}
		drawLine(
			destination,
			int(math.Round(from.X)),
			int(math.Round(from.Y)),
			int(math.Round(to.X)),
			int(math.Round(to.Y)),
			lineColor,
			thickness,
		)
	}
}

func drawDashedLine(
	destination *image.RGBA,
	from point,
	to point,
	lineColor color.RGBA,
	thickness int,
	dashLength float64,
	gapLength float64,
) {
	deltaX, deltaY := to.X-from.X, to.Y-from.Y
	length := math.Hypot(deltaX, deltaY)
	if length == 0 {
		return
	}
	for offset := 0.0; offset < length; offset += dashLength + gapLength {
		endOffset := min(length, offset+dashLength)
		drawLine(
			destination,
			int(math.Round(from.X+deltaX*offset/length)),
			int(math.Round(from.Y+deltaY*offset/length)),
			int(math.Round(from.X+deltaX*endOffset/length)),
			int(math.Round(from.Y+deltaY*endOffset/length)),
			lineColor,
			thickness,
		)
	}
}

func drawLine(
	destination *image.RGBA,
	x0 int,
	y0 int,
	x1 int,
	y1 int,
	lineColor color.RGBA,
	thickness int,
) {
	deltaX := abs(x1 - x0)
	directionX := -1
	if x0 < x1 {
		directionX = 1
	}
	deltaY := -abs(y1 - y0)
	directionY := -1
	if y0 < y1 {
		directionY = 1
	}
	errorValue := deltaX + deltaY
	for {
		drawCircle(destination, x0, y0, max(0, thickness/2), lineColor)
		if x0 == x1 && y0 == y1 {
			break
		}
		twiceError := 2 * errorValue
		if twiceError >= deltaY {
			errorValue += deltaY
			x0 += directionX
		}
		if twiceError <= deltaX {
			errorValue += deltaX
			y0 += directionY
		}
	}
}

func drawCircle(destination *image.RGBA, centerX int, centerY int, radius int, fill color.RGBA) {
	for y := -radius; y <= radius; y++ {
		for x := -radius; x <= radius; x++ {
			if x*x+y*y <= radius*radius {
				destination.SetRGBA(centerX+x, centerY+y, fill)
			}
		}
	}
}

func fillRoundedRect(destination *image.RGBA, rectangle image.Rectangle, radius int, fill color.RGBA) {
	for y := rectangle.Min.Y; y < rectangle.Max.Y; y++ {
		for x := rectangle.Min.X; x < rectangle.Max.X; x++ {
			if insideRoundedRect(x, y, rectangle, radius) {
				destination.SetRGBA(x, y, fill)
			}
		}
	}
}

func drawRoundedRectStroke(
	destination *image.RGBA,
	rectangle image.Rectangle,
	radius int,
	stroke color.RGBA,
	thickness int,
) {
	fillRoundedRect(destination, rectangle, radius, stroke)
	inner := rectangle.Inset(thickness)
	fillRoundedRect(destination, inner, max(0, radius-thickness), cardColor)
}

func insideRoundedRect(x int, y int, rectangle image.Rectangle, radius int) bool {
	if radius <= 0 {
		return true
	}
	left, right := rectangle.Min.X+radius, rectangle.Max.X-radius-1
	top, bottom := rectangle.Min.Y+radius, rectangle.Max.Y-radius-1
	if x >= left && x <= right || y >= top && y <= bottom {
		return true
	}
	centerX := left
	if x > right {
		centerX = right
	}
	centerY := top
	if y > bottom {
		centerY = bottom
	}
	deltaX, deltaY := x-centerX, y-centerY
	return deltaX*deltaX+deltaY*deltaY <= radius*radius
}

func drawRectStroke(destination *image.RGBA, rectangle image.Rectangle, stroke color.RGBA, thickness int) {
	draw.Draw(destination, image.Rect(rectangle.Min.X, rectangle.Min.Y, rectangle.Max.X, rectangle.Min.Y+thickness), &image.Uniform{C: stroke}, image.Point{}, draw.Src)
	draw.Draw(destination, image.Rect(rectangle.Min.X, rectangle.Max.Y-thickness, rectangle.Max.X, rectangle.Max.Y), &image.Uniform{C: stroke}, image.Point{}, draw.Src)
	draw.Draw(destination, image.Rect(rectangle.Min.X, rectangle.Min.Y, rectangle.Min.X+thickness, rectangle.Max.Y), &image.Uniform{C: stroke}, image.Point{}, draw.Src)
	draw.Draw(destination, image.Rect(rectangle.Max.X-thickness, rectangle.Min.Y, rectangle.Max.X, rectangle.Max.Y), &image.Uniform{C: stroke}, image.Point{}, draw.Src)
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
