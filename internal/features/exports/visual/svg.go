package visual

import (
	"bytes"
	"fmt"
	"html"
)

const svgMIMEType = "image/svg+xml"

func renderSVG(value scene) ([]byte, error) {
	var output bytes.Buffer
	_, _ = fmt.Fprintf(
		&output,
		"<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%.0f\" height=\"%.0f\" viewBox=\"0 0 %.0f %.0f\" role=\"img\" aria-label=\"%s\">\n",
		value.Width,
		value.Height,
		value.Width,
		value.Height,
		html.EscapeString(value.Title),
	)
	output.WriteString("<defs><linearGradient id=\"paper\" x1=\"0\" y1=\"0\" x2=\"0\" y2=\"1\"><stop offset=\"0\" stop-color=\"#fbf6e8\"/><stop offset=\"1\" stop-color=\"#f1e7cf\"/></linearGradient></defs>\n")
	_, _ = fmt.Fprintf(&output, "<rect width=\"%.0f\" height=\"%.0f\" fill=\"url(#paper)\"/>\n", value.Width, value.Height)
	_, _ = fmt.Fprintf(&output, "<rect x=\"18\" y=\"18\" width=\"%.0f\" height=\"%.0f\" rx=\"8\" fill=\"none\" stroke=\"#6b2431\" stroke-width=\"2\"/>\n", value.Width-36, value.Height-36)
	_, _ = fmt.Fprintf(&output, "<rect x=\"25\" y=\"25\" width=\"%.0f\" height=\"%.0f\" rx=\"6\" fill=\"none\" stroke=\"#b29255\" stroke-width=\"1\"/>\n", value.Width-50, value.Height-50)
	writeSVGText(&output, value.Width/2, 66, 36, "#3a2522", "700", value.Title)
	writeSVGText(&output, value.Width/2, 102, 14, "#7b5c42", "600", value.Subtitle)
	_, _ = fmt.Fprintf(&output, "<line x1=\"%.1f\" y1=\"122\" x2=\"%.1f\" y2=\"122\" stroke=\"#b29255\" stroke-width=\"2\"/>\n", value.Width/2-150, value.Width/2+150)
	for _, edge := range value.ParentEdges {
		writeSVGPolyline(&output, edge.Points, "#9b7a3f", 2, "")
	}
	for _, edge := range value.UnionEdges {
		writeSVGPolyline(&output, edge.Points, "#6b2431", 2, "8 6")
		if len(edge.Points) > 0 {
			middle := edge.Points[len(edge.Points)/2]
			_, _ = fmt.Fprintf(&output, "<circle cx=\"%.1f\" cy=\"%.1f\" r=\"4\" fill=\"#b29255\" stroke=\"#6b2431\"/>\n", middle.X, middle.Y)
		}
	}
	for _, node := range value.Nodes {
		_, _ = fmt.Fprintf(&output, "<rect x=\"%.1f\" y=\"%.1f\" width=\"%.1f\" height=\"%.1f\" rx=\"12\" fill=\"#fffaf0\" stroke=\"#6b2431\" stroke-width=\"2\"/>\n", node.X, node.Y, nodeWidth, nodeHeight)
		_, _ = fmt.Fprintf(&output, "<rect x=\"%.1f\" y=\"%.1f\" width=\"%.1f\" height=\"%.1f\" rx=\"9\" fill=\"none\" stroke=\"#d6bd83\" stroke-width=\"1\"/>\n", node.X+5, node.Y+5, nodeWidth-10, nodeHeight-10)
		_, _ = fmt.Fprintf(&output, "<line x1=\"%.1f\" y1=\"%.1f\" x2=\"%.1f\" y2=\"%.1f\" stroke=\"#b29255\" stroke-width=\"3\"/>\n", node.X+82, node.Y+12, node.X+150, node.Y+12)
		nameY := node.Y + 42
		if len(node.NameLines) == 2 {
			nameY = node.Y + 34
		}
		for index, line := range node.NameLines {
			writeSVGText(&output, node.X+nodeWidth/2, nameY+float64(index)*21, 17, "#3a2522", "700", line)
		}
		writeSVGText(&output, node.X+nodeWidth/2, node.Y+80, 12, "#765e4c", "400", node.Detail)
	}
	if value.EmptyText != "" {
		writeSVGText(&output, value.Width/2, headerHeight+60, 20, "#765e4c", "400", value.EmptyText)
	}
	writeSVGText(&output, value.Width/2, value.Height-29, 11, "#8a7258", "400", value.Footer)
	output.WriteString("</svg>\n")
	return output.Bytes(), nil
}

func writeSVGText(
	output *bytes.Buffer,
	x float64,
	y float64,
	size int,
	color string,
	weight string,
	text string,
) {
	_, _ = fmt.Fprintf(
		output,
		"<text x=\"%.1f\" y=\"%.1f\" text-anchor=\"middle\" font-family=\"Georgia, 'Times New Roman', serif\" font-size=\"%d\" font-weight=\"%s\" fill=\"%s\">%s</text>\n",
		x,
		y,
		size,
		weight,
		color,
		html.EscapeString(text),
	)
}

func writeSVGPolyline(output *bytes.Buffer, points []point, color string, width int, dash string) {
	if len(points) < 2 {
		return
	}
	output.WriteString("<polyline points=\"")
	for index, item := range points {
		if index > 0 {
			output.WriteByte(' ')
		}
		_, _ = fmt.Fprintf(output, "%.1f,%.1f", item.X, item.Y)
	}
	_, _ = fmt.Fprintf(output, "\" fill=\"none\" stroke=\"%s\" stroke-width=\"%d\" stroke-linejoin=\"round\"", color, width)
	if dash != "" {
		_, _ = fmt.Fprintf(output, " stroke-dasharray=\"%s\"", dash)
	}
	output.WriteString("/>\n")
}
