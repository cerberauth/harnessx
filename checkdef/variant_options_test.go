package checkdef

import (
	"testing"
	"time"

	"github.com/cerberauth/harnessx"
)

func TestNewVariantCheck_VariantLimitOptions(t *testing.T) {
	check := NewVariantCheck(CheckDef{ID: "a"}, nil,
		WithVariants("x", "y"),
		WithVariantMode(harnessx.VariantsParallel),
		WithVariantTimeout(3*time.Second),
		WithVariantConcurrency(2),
	)
	if check.VariantTimeout != 3*time.Second || check.VariantConcurrency != 2 {
		t.Errorf("VariantTimeout/VariantConcurrency = %v/%d, want 3s/2", check.VariantTimeout, check.VariantConcurrency)
	}
	if check.VariantMode != harnessx.VariantsParallel || len(check.Variants) != 2 {
		t.Errorf("VariantMode/Variants = %v/%v", check.VariantMode, check.Variants)
	}
}
