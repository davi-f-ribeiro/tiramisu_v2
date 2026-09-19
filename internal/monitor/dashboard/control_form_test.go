package dashboard

import (
	"strings"
	"testing"
)

func TestDiskWarmupQuotaBelongsToTiramisuConfigForm(t *testing.T) {
	html := string(controlHTML)
	field := `name="disk_warmup_quota_gb"`
	idx := strings.Index(html, field)
	if idx < 0 {
		t.Fatalf("%s field not found in control page", field)
	}

	tiramisuStart := strings.Index(html, `<form id="tiramisu-form">`)
	if tiramisuStart < 0 {
		t.Fatal("tiramisu-form not found")
	}
	tiramisuEndRel := strings.Index(html[tiramisuStart:], `</form>`)
	if tiramisuEndRel < 0 {
		t.Fatal("tiramisu-form closing tag not found")
	}
	tiramisuEnd := tiramisuStart + tiramisuEndRel

	if idx < tiramisuStart || idx > tiramisuEnd {
		t.Fatalf("disk_warmup_quota_gb must be inside tiramisu-form so /api/config persists it; field offset=%d form range=%d..%d", idx, tiramisuStart, tiramisuEnd)
	}
}
