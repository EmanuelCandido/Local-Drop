package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lxn/walk"
)

func TestCanvasLayoutAndHomeStayInsideClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		width      int
		height     int
		banners    int
		onboarding bool
	}{
		{name: "minimum", width: 764, height: 561},
		{name: "minimum-onboarding", width: 764, height: 561, onboarding: true},
		{name: "default", width: 984, height: 681, banners: 1},
		{name: "full-hd", width: 1920, height: 1040, banners: 2},
		{name: "ultrawide", width: 2560, height: 1040},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := walk.Rectangle{Width: test.width, Height: test.height}
			layout := computeCanvasLayout(client)

			if layout.width <= 0 || layout.left < 0 || layout.left+layout.width > client.Width {
				t.Fatalf("conteúdo horizontal fora do cliente: layout=%+v client=%+v", layout, client)
			}
			if layout.top <= layout.header || layout.bottom > client.Height || layout.bottom <= layout.top {
				t.Fatalf("conteúdo vertical fora do cliente: layout=%+v client=%+v", layout, client)
			}
			if layout.width > 1080 {
				t.Fatalf("a coluna deve ter largura confortável de leitura: %d", layout.width)
			}

			home := computeHomeLayout(layout, test.banners, test.onboarding)
			if len(home.banners) != test.banners {
				t.Fatalf("avisos = %d, esperado %d", len(home.banners), test.banners)
			}
			sections := append(append([]walk.Rectangle{}, home.banners...), home.devices, home.drop)
			previousBottom := layout.top
			for _, section := range sections {
				if section.Y < previousBottom || section.X != layout.left || section.Width != layout.width {
					t.Fatalf("seção sobreposta ou desalinhada: %+v (anterior termina em %d)", section, previousBottom)
				}
				previousBottom = section.Y + section.Height
			}
			if home.activity.Y < previousBottom || home.activity.Y+home.activity.Height != layout.bottom {
				t.Fatalf("atividade deve ocupar o espaço restante até o rodapé: activity=%+v layout=%+v", home.activity, layout)
			}
		})
	}
}

func TestCanvasLayoutHasNoOuterGutters(t *testing.T) {
	t.Parallel()

	layout := edgeToEdgeCanvasLayout()
	if !layout.MarginsZero {
		t.Fatal("a margem zero precisa ser explícita para impedir o fallback de 9 px do Walk")
	}
	if !layout.SpacingZero {
		t.Fatal("o espaçamento zero precisa ser explícito no container raiz")
	}
	if layout.Margins.Left != 0 || layout.Margins.Top != 0 || layout.Margins.Right != 0 || layout.Margins.Bottom != 0 {
		t.Fatalf("margens externas inesperadas: %+v", layout.Margins)
	}
}

func TestExpandDroppedFlattensFolders(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWrite := func(path string) {
		t.Helper()
		if err := writeTestFile(path); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(root + `\a.txt`)
	mustWrite(root + `\pasta\b.txt`)
	mustWrite(root + `\pasta\sub\c.txt`)
	files, err := expandDropped([]string{root + `\a.txt`, root + `\pasta`})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("arquivos = %v", files)
	}
}

func writeTestFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("x"), 0o600)
}
