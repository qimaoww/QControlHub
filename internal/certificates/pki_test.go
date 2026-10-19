package certificates

import (
	"strings"
	"testing"
	"time"
)

func TestCertificateImportAndHostnameBoundaries(t *testing.T) {
	pair, err := SelfSigned([]string{"example.test", "*.example.test"}, "ecdsa256", 90)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Inspect(pair, time.Now())
	if err != nil || len(info.Domains) != 2 || info.KeyType != "ecdsa256" {
		t.Fatalf("inspect: %+v %v", info, err)
	}
	for _, host := range []string{"example.test", "a.example.test"} {
		if err = Covers(pair, []string{host}); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []string{"a.b.example.test", "other.test"} {
		if Covers(pair, []string{host}) == nil {
			t.Fatalf("accepted mismatching %s", host)
		}
	}
	other, _ := SelfSigned([]string{"example.test"}, "rsa2048", 1)
	if _, err = Inspect(Pair{pair.Certificate, other.PrivateKey}, time.Now()); err == nil {
		t.Fatal("accepted wrong private key")
	}
	if _, err = Inspect(pair, time.Now().Add(91*24*time.Hour)); err == nil {
		t.Fatal("accepted expired cert")
	}
	if _, err = Inspect(Pair{pair.Certificate + "garbage", pair.PrivateKey}, time.Now()); err == nil {
		t.Fatal("accepted trailing certificate data")
	}
}
func TestDomainsAndKeyTypes(t *testing.T) {
	for _, v := range []string{"127.0.0.1", "foo", "a..test", "a/../../test", "*.*.example.test"} {
		if _, err := Domains([]string{v}); err == nil {
			t.Fatalf("accepted %s", v)
		}
	}
	names, err := Domains([]string{" EXAMPLE.TEST. ", "example.test", "*.example.test"})
	if err != nil || len(names) != 2 {
		t.Fatalf("normalization %v %v", names, err)
	}
	for _, kind := range []string{"ecdsa256", "ecdsa384", "rsa2048"} {
		key, encoded, err := NewKey(kind)
		if err != nil || key == nil || !strings.Contains(encoded, "PRIVATE KEY") {
			t.Fatalf("key %s %v", kind, err)
		}
		if _, err = ParseKey(encoded); err != nil {
			t.Fatal(err)
		}
	}
}
