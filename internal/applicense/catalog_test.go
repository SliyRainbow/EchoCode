package applicense

import (
	"strings"
	"testing"
)

func TestCatalogContainsOfflineLicenseTexts(t *testing.T) {
	notices := Catalog()
	if len(notices) < 5 {
		t.Fatalf("license notices = %d", len(notices))
	}
	wanted := map[string]string{
		"excelize":                     "BSD 3-Clause License",
		"etree":                        "Redistribution and use in source and binary forms",
		"extrame/xls and extrame/ole2": "Apache License",
		"python-pptx default template": "The MIT License",
	}
	for _, notice := range notices {
		marker, ok := wanted[notice.Name]
		if !ok {
			continue
		}
		if notice.URL == "" || !strings.Contains(notice.Text, marker) {
			t.Fatalf("incomplete notice for %s", notice.Name)
		}
		delete(wanted, notice.Name)
	}
	if len(wanted) != 0 {
		t.Fatalf("missing notices: %#v", wanted)
	}
}
