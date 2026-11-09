//go:build windows

package main

import "testing"

func TestSuppliedNavigationSVGsRasterize(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"assets/home.svg", "assets/arrow-transfer2.svg", "assets/setting.svg"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := nativeNavIconFiles.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			mask, err := rasterizeSVGMask(data, 48)
			if err != nil {
				t.Fatal(err)
			}
			nonTransparent := 0
			for _, alpha := range mask.Pix {
				if alpha != 0 {
					nonTransparent++
				}
			}
			if nonTransparent < 80 {
				t.Fatalf("ícone vazio ou incompleto: %d pixels visíveis", nonTransparent)
			}
			if nonTransparent > len(mask.Pix)*3/4 {
				t.Fatalf("ícone ocupou área excessiva: %d de %d pixels", nonTransparent, len(mask.Pix))
			}
		})
	}
}
