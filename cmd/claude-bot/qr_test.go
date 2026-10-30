package main

import (
	"slices"
	"strings"
	"testing"

	"rsc.io/qr"
)

func TestRenderQR(t *testing.T) {
	t.Parallel()

	const link = "https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=abc&user_code=ABCD-EFGH"

	out, width, err := renderQR(link)
	if err != nil {
		t.Fatal(err)
	}

	code, err := qr.Encode(link, qr.L)
	if err != nil {
		t.Fatal(err)
	}

	if want := code.Size + 2*qrQuiet; width != want {
		t.Fatalf("width = %d, want %d", width, want)
	}

	got := unpackQR(out)
	if len(got) < width {
		t.Fatalf("%d module rows, want at least %d", len(got), width)
	}

	for y := range width {
		want := make([]bool, width)
		for x := range want {
			mx, my := x-qrQuiet, y-qrQuiet
			want[x] = mx >= 0 && my >= 0 && mx < code.Size && my < code.Size && code.Black(mx, my)
		}

		if !slices.Equal(got[y], want) {
			t.Fatalf("module row %d differs from the code", y)
		}
	}
}

// unpackQR turns half-block output back into rows of modules, true = black.
func unpackQR(out string) [][]bool {
	var rows [][]bool

	for line := range strings.Lines(out) {
		line = strings.TrimSuffix(strings.TrimPrefix(line, "\x1b[30;107m"), "\x1b[0m\n")

		var top, bottom []bool
		for _, c := range line {
			top = append(top, c == '█' || c == '▀')
			bottom = append(bottom, c == '█' || c == '▄')
		}

		rows = append(rows, top, bottom)
	}

	return rows
}
