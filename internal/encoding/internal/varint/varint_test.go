package varint

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeU64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		data       []byte
		offset     int
		wantValue  uint64
		wantOffset int
		wantOK     bool
	}{
		{
			name:       "single byte",
			data:       []byte{0x7f},
			wantValue:  0x7f,
			wantOffset: 1,
			wantOK:     true,
		},
		{
			name:       "two bytes after prefix",
			data:       []byte{0xff, 0xac, 0x02},
			offset:     1,
			wantValue:  300,
			wantOffset: 3,
			wantOK:     true,
		},
		{
			name:       "maximum unsigned value",
			data:       binary.AppendUvarint(nil, ^uint64(0)),
			wantValue:  ^uint64(0),
			wantOffset: binary.MaxVarintLen64,
			wantOK:     true,
		},
		{
			name:       "empty input",
			wantOffset: 0,
		},
		{
			name:       "offset at end",
			data:       []byte{0x01},
			offset:     1,
			wantOffset: 1,
		},
		{
			name:       "truncated continuation",
			data:       []byte{0x80},
			wantOffset: 0,
		},
		{
			name:       "unterminated maximum length",
			data:       []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80},
			wantOffset: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			value, offset, ok := DecodeU64(tt.data, tt.offset)

			require.Equal(t, tt.wantValue, value)
			require.Equal(t, tt.wantOffset, offset)
			require.Equal(t, tt.wantOK, ok)
		})
	}
}

func TestDecodeZigZag64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value uint64
		want  int64
	}{
		{name: "zero", value: 0, want: 0},
		{name: "positive one", value: 2, want: 1},
		{name: "negative one", value: 1, want: -1},
		{name: "maximum", value: ^uint64(1), want: int64(^uint64(0) >> 1)},
		{name: "minimum", value: ^uint64(0), want: -1 << 63},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, DecodeZigZag64(tt.value))
		})
	}
}

func TestDecodeU64DoesNotAllocate(t *testing.T) {
	data := binary.AppendUvarint(nil, 1<<28)
	allocations := testing.AllocsPerRun(1000, func() {
		_, _, _ = DecodeU64(data, 0)
	})

	require.Zero(t, allocations)
}
