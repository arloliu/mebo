package section

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNumericFlag_IsValidMagicNumber(t *testing.T) {
	tests := []struct {
		name  string
		magic uint16
		want  bool
	}{
		{name: "V1 magic", magic: MagicNumericV1Opt, want: true},
		{name: "V2 compact magic", magic: MagicNumericV2Opt, want: true},
		{name: "V2 extended magic", magic: MagicNumericV2ExtOpt, want: true},
		{name: "text magic", magic: MagicTextV1Opt, want: false},
		{name: "zero", magic: 0x0000, want: false},
		{name: "random", magic: 0xFFFF, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NumericFlag{Options: tt.magic}
			require.Equal(t, tt.want, f.IsValidMagicNumber())
		})
	}
}

func TestNumericFlag_IsV2(t *testing.T) {
	tests := []struct {
		name  string
		magic uint16
		want  bool
	}{
		{name: "V1 magic", magic: MagicNumericV1Opt, want: false},
		{name: "V2 compact magic", magic: MagicNumericV2Opt, want: true},
		{name: "V2 extended magic", magic: MagicNumericV2ExtOpt, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NumericFlag{Options: tt.magic}
			require.Equal(t, tt.want, f.IsV2())
		})
	}
}

func TestNumericFlag_IsV2Ext(t *testing.T) {
	tests := []struct {
		name  string
		magic uint16
		want  bool
	}{
		{name: "V1 magic", magic: MagicNumericV1Opt, want: false},
		{name: "V2 compact magic", magic: MagicNumericV2Opt, want: false},
		{name: "V2 extended magic", magic: MagicNumericV2ExtOpt, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NumericFlag{Options: tt.magic}
			require.Equal(t, tt.want, f.IsV2Ext())
		})
	}
}

func TestNumericFlag_IndexEntrySize(t *testing.T) {
	tests := []struct {
		name  string
		magic uint16
		want  int
	}{
		{name: "V1 magic", magic: MagicNumericV1Opt, want: NumericIndexEntrySize},
		{name: "V2 compact magic", magic: MagicNumericV2Opt, want: NumericIndexEntrySize},
		{name: "V2 extended magic", magic: MagicNumericV2ExtOpt, want: NumericExtIndexEntrySize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NumericFlag{Options: tt.magic}
			require.Equal(t, tt.want, f.IndexEntrySize())
		})
	}
}
