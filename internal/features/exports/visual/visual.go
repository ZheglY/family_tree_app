package visual

import (
	"fmt"

	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
)

func Render(
	value manifest.Manifest,
	format string,
	maxNodes int,
	maxPixels int64,
) ([]byte, string, error) {
	prepared, err := buildScene(value, maxNodes, maxPixels)
	if err != nil {
		return nil, "", err
	}
	var body []byte
	var mimeType string
	switch format {
	case domain.FormatSVG:
		body, err = renderSVG(prepared)
		mimeType = svgMIMEType
	case domain.FormatPNG:
		body, err = renderPNG(prepared)
		mimeType = pngMIMEType
	case domain.FormatPDF:
		body, err = renderPDF(prepared)
		mimeType = pdfMIMEType
	default:
		return nil, "", domain.ErrInvalidExport
	}
	if err != nil {
		return nil, "", fmt.Errorf("render %s family tree: %w", format, err)
	}
	if len(body) == 0 {
		return nil, "", fmt.Errorf("render %s family tree: empty result", format)
	}
	return body, mimeType, nil
}
