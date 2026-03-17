package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: go run ./scripts/gen_default_icon.go <output.(ico|png)>")
		os.Exit(1)
	}
	outPath := os.Args[1]
	const size = 64

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{18, 18, 18, 255}}, image.Point{}, draw.Src)
	for y := 6; y < size-6; y++ {
		for x := 6; x < size-6; x++ {
			img.Set(x, y, color.RGBA{0, 212, 170, 255})
		}
	}
	for y := 14; y < size-14; y++ {
		for x := 14; x < size-14; x++ {
			img.Set(x, y, color.RGBA{15, 17, 21, 255})
		}
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		panic(err)
	}
	pngBytes := pngBuf.Bytes()

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		panic(err)
	}
	switch strings.ToLower(filepath.Ext(outPath)) {
	case ".png":
		if err := os.WriteFile(outPath, pngBytes, 0o644); err != nil {
			panic(err)
		}
	default:
		var ico bytes.Buffer
		_ = binary.Write(&ico, binary.LittleEndian, uint16(0))
		_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
		_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
		ico.WriteByte(byte(size))
		ico.WriteByte(byte(size))
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
