package browserengine

import "testing"

func TestValidateNativeSurfaceBounds(t *testing.T) {
	valid := NativeSurfaceBounds{Width: 800, Height: 600, ViewportWidth: 1280, ViewportHeight: 820}
	if err := validateNativeSurfaceBounds(valid); err != nil {
		t.Fatalf("valid bounds rejected: %v", err)
	}
	for _, bounds := range []NativeSurfaceBounds{
		{Width: 0, Height: 600, ViewportWidth: 1280, ViewportHeight: 820},
		{Width: 800, Height: 600, ViewportWidth: 0, ViewportHeight: 820},
	} {
		if err := validateNativeSurfaceBounds(bounds); err == nil {
			t.Fatalf("invalid bounds accepted: %+v", bounds)
		}
	}
}
