package processor

import (
	"reflect"
	"testing"
)

func TestParseHeader(t *testing.T) {
	out := []byte("/work/in.heic: 4032x3024 uchar, 3 bands, srgb, heifload\nwidth: 4032\n")
	w, h, err := ParseHeader(out)
	if err != nil || w != 4032 || h != 3024 {
		t.Fatalf("got %dx%d err %v", w, h, err)
	}
	for _, bad := range []string{
		"", "no dimensions here", "x: 0x5 ", "vipsheader: VipsForeignLoad: file missing",
	} {
		if _, _, err := ParseHeader([]byte(bad)); err == nil {
			t.Errorf("ParseHeader(%q): expected error", bad)
		}
	}
}

func TestRotateChain(t *testing.T) {
	cases := []struct {
		o    int
		want [][]string
	}{
		{1, nil},
		{2, [][]string{{"flip", "horizontal"}}},
		{3, [][]string{{"rot", "d180"}}},
		{4, [][]string{{"flip", "vertical"}}},
		{5, [][]string{{"rot", "d90"}, {"flip", "horizontal"}}},
		{6, [][]string{{"rot", "d90"}}},
		{7, [][]string{{"rot", "d270"}, {"flip", "horizontal"}}},
		{8, [][]string{{"rot", "d270"}}},
		{0, nil}, {9, nil},
	}
	for _, c := range cases {
		if got := rotateChain(c.o); !reflect.DeepEqual(got, c.want) {
			t.Errorf("rotateChain(%d) = %v, want %v", c.o, got, c.want)
		}
	}
}

func TestVariantNamesMatchSchema(t *testing.T) {
	// asset_variants CHECK: name IN ('thumb','medium','display','original')
	want := []string{"thumb", "medium", "display", "original"}
	var got []string
	for _, v := range Variants {
		got = append(got, v.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("variant names %v must match the frozen CHECK set %v", got, want)
	}
}
