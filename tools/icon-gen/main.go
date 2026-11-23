package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/png"
	"os"
	"path/filepath"

	"image/png"
)

func main() {
	input := flag.String("input", "build/appicon.png", "source PNG icon")
	output := flag.String("output", "build/windows/icon.ico", "output ICO file")
	flag.Parse()

	if err := generate(*input, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(input, output string) error {
	file, err := os.Open(input)
	if err != nil {
		return fmt.Errorf("open source icon: %w", err)
	}
	defer file.Close()

	source, _, err := image.Decode(file)
	if err != nil {
		return fmt.Errorf("decode source icon: %w", err)
	}

	bounds := source.Bounds()
	side := bounds.Dx()
	if bounds.Dy() < side {
		side = bounds.Dy()
	}
	if side <= 0 {
		return fmt.Errorf("source icon has invalid dimensions")
	}
	crop := image.Rect(
		bounds.Min.X+(bounds.Dx()-side)/2,
		bounds.Min.Y+(bounds.Dy()-side)/2,
		bounds.Min.X+(bounds.Dx()-side)/2+side,
		bounds.Min.Y+(bounds.Dy()-side)/2+side,
	)

	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	images := make([][]byte, 0, len(sizes))
	for _, size := range sizes {
		scaled := image.NewRGBA(image.Rect(0, 0, size, size))
		draw.Draw(scaled, scaled.Bounds(), &image.Uniform{C: color.Transparent}, image.Point{}, draw.Src)
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				sx := crop.Min.X + x*side/size
				sy := crop.Min.Y + y*side/size
				scaled.Set(x, y, source.At(sx, sy))
			}
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, scaled); err != nil {
			return fmt.Errorf("encode %dx%d icon: %w", size, size, err)
		}
		images = append(images, encoded.Bytes())
	}

	var ico bytes.Buffer
	_ = binary.Write(&ico, binary.LittleEndian, uint16(0))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(len(sizes)))

	offset := uint32(6 + 16*len(sizes))
	for i, size := range sizes {
		dimension := byte(size)
		if size == 256 {
			dimension = 0
		}
		ico.WriteByte(dimension)
		ico.WriteByte(dimension)
		ico.WriteByte(0)
		ico.WriteByte(0)
		_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
		_ = binary.Write(&ico, binary.LittleEndian, uint16(32))
		_ = binary.Write(&ico, binary.LittleEndian, uint32(len(images[i])))
		_ = binary.Write(&ico, binary.LittleEndian, offset)
		offset += uint32(len(images[i]))
	}
	for _, encoded := range images {
		_, _ = ico.Write(encoded)
	}

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(output, ico.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write ICO file: %w", err)
	}
	fmt.Printf("Generated %s from %s (%d sizes)\n", output, input, len(sizes))
	return nil
}
