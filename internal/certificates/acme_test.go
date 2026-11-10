package certificates

import (
	"context"
	"strings"
	"testing"
)

func TestACMERejectsPartialEABBeforeRegistration(t *testing.T) {
	_, key, err := NewKey("ecdsa256")
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range []ACMEAccount{{Key: key, EABID: "id"}, {Key: key, EABKey: "a2V5"}} {
		if _, err := Register(context.Background(), "zerossl", "local@example.test", account); err == nil || !strings.Contains(err.Error(), "同时填写") {
			t.Fatalf("partial EAB accepted: %v", err)
		}
	}
}
