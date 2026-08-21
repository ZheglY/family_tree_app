package visual

import (
	"fmt"
	"image/color"

	"github.com/signintech/gopdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

const pdfMIMEType = "application/pdf"

const (
	pdfRegularFont = "GoRegular"
	pdfBoldFont    = "GoBold"
)

func renderPDF(value scene) ([]byte, error) {
	const (
		baseScale     = 0.75
		pagePadding   = 16.0
		minimumWidth  = 842.0
		minimumHeight = 595.0
		maximumPage   = 14400.0
	)
	pageWidth := min(maximumPage, max(minimumWidth, value.Width*baseScale+2*pagePadding))
	pageHeight := min(maximumPage, max(minimumHeight, value.Height*baseScale+2*pagePadding))
	scale := min(
		(pageWidth-2*pagePadding)/value.Width,
		(pageHeight-2*pagePadding)/value.Height,
	)
	offsetX := (pageWidth - value.Width*scale) / 2
	offsetY := (pageHeight - value.Height*scale) / 2
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: pageWidth, H: pageHeight}})
	if err := pdf.AddTTFFontData(pdfRegularFont, goregular.TTF); err != nil {
		return nil, fmt.Errorf("add PDF regular font: %w", err)
	}
	if err := pdf.AddTTFFontData(pdfBoldFont, gobold.TTF); err != nil {
		return nil, fmt.Errorf("add PDF bold font: %w", err)
	}
	pdf.SetInfo(gopdf.PdfInfo{
		Title: value.Title, Subject: "Семейное древо", Creator: "Family Tree",
		Producer: "Family Tree visual export", CreationDate: value.CreatedAt,
	})
	pdf.AddPage()
	pdf.SetFillColor(paperColor.R, paperColor.G, paperColor.B)
	pdf.RectFromUpperLeftWithStyle(0, 0, pageWidth, pageHeight, "F")
	mapX := func(x float64) float64 { return offsetX + x*scale }
	mapY := func(y float64) float64 { return offsetY + y*scale }
	pdf.SetStrokeColor(burgundyColor.R, burgundyColor.G, burgundyColor.B)
	pdf.SetLineWidth(max(0.6, 2*scale))
	pdf.RectFromUpperLeftWithStyle(mapX(18), mapY(18), (value.Width-36)*scale, (value.Height-36)*scale, "D")
	pdf.SetStrokeColor(goldColor.R, goldColor.G, goldColor.B)
	pdf.SetLineWidth(max(0.4, scale))
	pdf.RectFromUpperLeftWithStyle(mapX(25), mapY(25), (value.Width-50)*scale, (value.Height-50)*scale, "D")
	if err := writePDFText(pdf, pdfBoldFont, 36*scale, inkColor, mapX(0), mapY(43), value.Width*scale, 40*scale, value.Title); err != nil {
		return nil, err
	}
	if err := writePDFText(pdf, pdfBoldFont, max(8, 14*scale), mutedInkColor, mapX(0), mapY(88), value.Width*scale, 22*scale, value.Subtitle); err != nil {
		return nil, err
	}
	pdf.SetStrokeColor(goldColor.R, goldColor.G, goldColor.B)
	pdf.SetLineWidth(max(0.6, 2*scale))
	pdf.Line(mapX(value.Width/2-150), mapY(122), mapX(value.Width/2+150), mapY(122))
	for _, edge := range value.ParentEdges {
		drawPDFPolyline(pdf, edge.Points, scale, offsetX, offsetY, false)
	}
	for _, edge := range value.UnionEdges {
		drawPDFPolyline(pdf, edge.Points, scale, offsetX, offsetY, true)
		if len(edge.Points) > 0 {
			middle := edge.Points[len(edge.Points)/2]
			pdf.SetFillColor(goldColor.R, goldColor.G, goldColor.B)
			pdf.RectFromUpperLeftWithStyle(mapX(middle.X)-2*scale, mapY(middle.Y)-2*scale, 4*scale, 4*scale, "F")
		}
	}
	for _, node := range value.Nodes {
		x, y := mapX(node.X), mapY(node.Y)
		width, height := nodeWidth*scale, nodeHeight*scale
		pdf.SetFillColor(cardColor.R, cardColor.G, cardColor.B)
		pdf.SetStrokeColor(burgundyColor.R, burgundyColor.G, burgundyColor.B)
		pdf.SetLineWidth(max(0.6, 2*scale))
		if err := pdf.Rectangle(x, y, x+width, y+height, "DF", 10*scale, 8); err != nil {
			return nil, fmt.Errorf("draw PDF person card: %w", err)
		}
		pdf.SetStrokeColor(lightGoldColor.R, lightGoldColor.G, lightGoldColor.B)
		pdf.SetLineWidth(max(0.4, scale))
		if err := pdf.Rectangle(x+5*scale, y+5*scale, x+width-5*scale, y+height-5*scale, "D", 7*scale, 8); err != nil {
			return nil, fmt.Errorf("draw PDF person card border: %w", err)
		}
		pdf.SetStrokeColor(goldColor.R, goldColor.G, goldColor.B)
		pdf.SetLineWidth(max(0.8, 3*scale))
		pdf.Line(x+82*scale, y+12*scale, x+150*scale, y+12*scale)
		nameY := node.Y + 25
		if len(node.NameLines) == 1 {
			nameY = node.Y + 34
		}
		for index, line := range node.NameLines {
			if err := writePDFText(
				pdf, pdfBoldFont, max(9, 17*scale), inkColor,
				x, mapY(nameY+float64(index)*21), width, 20*scale, line,
			); err != nil {
				return nil, err
			}
		}
		if err := writePDFText(
			pdf, pdfRegularFont, max(7, 12*scale), mutedInkColor,
			x, mapY(node.Y+67), width, 18*scale, node.Detail,
		); err != nil {
			return nil, err
		}
	}
	if value.EmptyText != "" {
		if err := writePDFText(
			pdf, pdfRegularFont, max(10, 20*scale), mutedInkColor,
			mapX(0), mapY(headerHeight+38), value.Width*scale, 30*scale, value.EmptyText,
		); err != nil {
			return nil, err
		}
	}
	if err := writePDFText(
		pdf, pdfRegularFont, max(7, 11*scale), mutedInkColor,
		mapX(0), mapY(value.Height-43), value.Width*scale, 20*scale, value.Footer,
	); err != nil {
		return nil, err
	}
	body, err := pdf.GetBytesPdfReturnErr()
	if err != nil {
		return nil, fmt.Errorf("encode visual PDF: %w", err)
	}
	return body, nil
}

func writePDFText(
	pdf *gopdf.GoPdf,
	fontFamily string,
	fontSize float64,
	textColor color.RGBA,
	x float64,
	y float64,
	width float64,
	height float64,
	text string,
) error {
	if err := pdf.SetFont(fontFamily, "", fontSize); err != nil {
		return fmt.Errorf("set PDF font: %w", err)
	}
	pdf.SetTextColor(textColor.R, textColor.G, textColor.B)
	pdf.SetXY(x, y)
	if err := pdf.CellWithOption(
		&gopdf.Rect{W: width, H: height},
		text,
		gopdf.CellOption{Align: gopdf.Center | gopdf.Middle},
	); err != nil {
		return fmt.Errorf("write PDF text: %w", err)
	}
	return nil
}

func drawPDFPolyline(
	pdf *gopdf.GoPdf,
	points []point,
	scale float64,
	offsetX float64,
	offsetY float64,
	dashed bool,
) {
	if dashed {
		pdf.SetStrokeColor(burgundyColor.R, burgundyColor.G, burgundyColor.B)
		pdf.SetCustomLineType([]float64{8 * scale, 6 * scale}, 0)
	} else {
		pdf.SetStrokeColor(goldColor.R, goldColor.G, goldColor.B)
		pdf.SetLineType("solid")
	}
	pdf.SetLineWidth(max(0.6, 2*scale))
	for index := 1; index < len(points); index++ {
		from, to := points[index-1], points[index]
		pdf.Line(
			offsetX+from.X*scale,
			offsetY+from.Y*scale,
			offsetX+to.X*scale,
			offsetY+to.Y*scale,
		)
	}
	pdf.SetLineType("solid")
}
