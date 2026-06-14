package format_test

import (
	"testing"

	"github.com/arloliu/mebo/format"
)

func TestTypeALPString(t *testing.T) {
	if format.TypeALP != 0x6 {
		t.Fatalf("TypeALP must be 0x6, got %#x", byte(format.TypeALP))
	}
	if format.TypeALP.String() != "ALP" {
		t.Fatalf("got %q", format.TypeALP.String())
	}
}
