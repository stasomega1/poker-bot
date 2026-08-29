package service

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestBuildReceiptVariantsCreatesTwoRotatedAttempts(t *testing.T) {
	sourceImage := image.NewRGBA(image.Rect(0, 0, 80, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 80; x++ {
			sourceImage.Set(x, y, color.White)
		}
	}

	var source bytes.Buffer
	if err := jpeg.Encode(&source, sourceImage, nil); err != nil {
		t.Fatalf("encode source image: %v", err)
	}

	variants, err := buildReceiptVariants(source.Bytes())
	if err != nil {
		t.Fatalf("buildReceiptVariants returned error: %v", err)
	}
	if len(variants) != 2 {
		t.Fatalf("expected exactly two OCR attempts, got %d", len(variants))
	}

	wantLabels := []string{"enhanced_rotated_minus_15", "enhanced_rotated_plus_15"}
	for i, want := range wantLabels {
		if variants[i].Label != want {
			t.Errorf("variant %d label: got %q, want %q", i, variants[i].Label, want)
		}
		if len(variants[i].Bytes) == 0 {
			t.Errorf("variant %d is empty", i)
		}
	}
}
