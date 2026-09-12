package objects

import (
	"errors"
	"fmt"
)

// PNG predictor filters, ported from lopdf src/filters/png.rs.

type pngFilterType uint8

const (
	pngFilterNone pngFilterType = iota
	pngFilterSub
	pngFilterUp
	pngFilterAvg
	pngFilterPaeth
)

func paethPredict(left, above, upperleft byte) byte {
	estimate := int16(left) + int16(above) - int16(upperleft)
	distLeft := abs16(estimate - int16(left))
	distAbove := abs16(estimate - int16(above))
	distUpperleft := abs16(estimate - int16(upperleft))
	if distLeft <= distAbove && distLeft <= distUpperleft {
		return left
	} else if distAbove <= distUpperleft {
		return above
	}
	return upperleft
}

func abs16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}

func decodePNGRow(filter pngFilterType, bpp int, previous, current []byte) {
	length := len(current)
	if bpp > length {
		bpp = length
	}
	switch filter {
	case pngFilterNone:
	case pngFilterSub:
		for i := bpp; i < length; i++ {
			current[i] += current[i-bpp]
		}
	case pngFilterUp:
		for i := 0; i < length; i++ {
			current[i] += previous[i]
		}
	case pngFilterAvg:
		for i := 0; i < bpp; i++ {
			current[i] += previous[i] / 2
		}
		for i := bpp; i < length; i++ {
			current[i] += byte(int16(current[i-bpp]) + int16(previous[i])/2)
		}
	case pngFilterPaeth:
		for i := 0; i < bpp; i++ {
			current[i] += paethPredict(0, previous[i], 0)
		}
		for i := bpp; i < length; i++ {
			current[i] += paethPredict(current[i-bpp], previous[i], previous[i-bpp])
		}
	}
}

// decodePNGFrame mirrors png::decode_frame. Returns an IO-style error on an
// invalid filter byte or a short row (wrapped as KindIO by the caller path).
func decodePNGFrame(content []byte, bytesPerPixel, pixelsPerRow int) ([]byte, error) {
	bytesPerRow := bytesPerPixel * pixelsPerRow
	previous := make([]byte, bytesPerRow)
	current := make([]byte, bytesPerRow)
	decoded := make([]byte, 0, len(content))
	pos := 0
	for pos < len(content) {
		filter := content[pos]
		if filter > 4 {
			return nil, errIO(fmt.Errorf("invalid PNG filter type (%d)", filter))
		}
		pos++
		end := pos + bytesPerRow
		if end > len(content) {
			return nil, errIO(errors.New("failed to fill whole buffer"))
		}
		copy(current, content[pos:end])
		pos = end
		decodePNGRow(pngFilterType(filter), bytesPerPixel, previous, current)
		decoded = append(decoded, current...)
		previous, current = current, previous
	}
	return decoded, nil
}
