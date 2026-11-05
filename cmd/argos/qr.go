package main

import (
	"fmt"
	"io"
	"strings"

	"rsc.io/qr"
)

// printQR renders text as a QR code with Unicode half blocks: two module
// rows per line, light modules drawn so it scans on dark terminals.
func printQR(out io.Writer, text string) error {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return fmt.Errorf("encode QR: %w", err)
	}
	const quiet = 2
	size := code.Size
	light := func(x, y int) bool {
		if x < 0 || y < 0 || x >= size || y >= size {
			return true // quiet zone
		}
		return !code.Black(x, y)
	}
	var b strings.Builder
	for y := -quiet; y < size+quiet; y += 2 {
		for x := -quiet; x < size+quiet; x++ {
			top, bottom := light(x, y), light(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\n")
	}
	_, err = io.WriteString(out, b.String())
	return err
}
