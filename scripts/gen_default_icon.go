package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

const sourceIconPath = "codexsess.png"

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: go run ./scripts/gen_default_icon.go <output.(ico|png)>")
		os.Exit(1)
	}
	outPath := os.Args[1]

	srcBytes, err := os.ReadFile(sourceIconPath)
	if err != nil {
		panic(fmt.Errorf("read %s: %w", sourceIconPath, err))
	}
	srcImg, err := png.Decode(bytes.NewReader(srcBytes))
	if err != nil {
		panic(fmt.Errorf("decode %s: %w", sourceIconPath, err))
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		panic(err)
	}

	switch strings.ToLower(filepath.Ext(outPath)) {
	case ".png":
		if err := os.WriteFile(outPath, srcBytes, 0o644); err != nil {
			panic(err)
		}
	default:
		icoPNG := resizeNearest(srcImg, 256, 256)
		var pngBuf bytes.Buffer
		if err := png.Encode(&pngBuf, icoPNG); err != nil {
			panic(err)
		}
		pngBytes := pngBuf.Bytes()

		var ico bytes.Buffer
		_ = binary.Write(&ico, binary.LittleEndian, uint16(0))
		_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
		_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
		// 0 means 256x256 in ICO header.
		ico.WriteByte(0)
		ico.WriteByte(0)
		ico.WriteByte(0)
		ico.WriteByte(0)
		_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
		_ = binary.Write(&ico, binary.LittleEndian, uint16(32))
		_ = binary.Write(&ico, binary.LittleEndian, uint32(len(pngBytes)))
		_ = binary.Write(&ico, binary.LittleEndian, uint32(6+16))
		_, _ = ico.Write(pngBytes)

		if err := os.WriteFile(outPath, ico.Bytes(), 0o644); err != nil {
			panic(err)
		}
	}
}

func resizeNearest(src image.Image, width, height int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	sb := src.Bounds()
	sw := sb.Dx()
	sh := sb.Dy()
	for y := 0; y < height; y++ {
		sy := sb.Min.Y + (y*sh)/height
		for x := 0; x < width; x++ {
			sx := sb.Min.X + (x*sw)/width
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}
