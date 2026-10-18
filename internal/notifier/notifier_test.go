package notifier

import (
	"strings"
	"testing"
)

func TestPlainBodyKeyLines(t *testing.T) {
	e := testEvent()
	e.KeyAlgorithm = "RSA"
	e.KeyFingerprint = "SHA256:abc"

	body := plainBody(e)
	keyIdx := strings.Index(body, "Key:          RSA")
	fpIdx := strings.Index(body, "Fingerprint:  SHA256:abc")
	if keyIdx < 0 || fpIdx < 0 {
		t.Fatalf("key lines missing:\n%s", body)
	}
	if keyIdx > strings.Index(body, "Time:") || fpIdx > strings.Index(body, "Time:") {
		t.Errorf("key lines should precede Time:\n%s", body)
	}

	e.Method = "password"
	if body := plainBody(e); strings.Contains(body, "Key:") || strings.Contains(body, "SHA256") {
		t.Errorf("password login must not show key lines:\n%s", body)
	}
}
