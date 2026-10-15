package core

import "testing"

func TestExecutionSnapshotDoesNotInterpretNativeJSONAsMetadata(t *testing.T) {
	native := `{"qcontrolhub_deployment":1,"content":"different","tls_frontends":[]}`
	decoded, frontends, err := DecodeExecutionSnapshot(native)
	if err != nil || decoded != native || len(frontends) != 0 {
		t.Fatal("native user configuration interpreted as trusted task metadata")
	}
	encoded, err := EncodeExecutionSnapshot(native, nil)
	if err != nil {
		t.Fatal(err)
	}
	decoded, frontends, err = DecodeExecutionSnapshot(encoded)
	if err != nil || decoded != native || len(frontends) != 0 {
		t.Fatal("snapshot was decoded recursively")
	}
}
