package cryptox

import (
	"bytes"
	"testing"
)

func TestSealOpenAndTamperDetection(t *testing.T) {
	box, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("secret")
	sealed, err := box.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := box.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plain) {
		t.Fatalf("解密结果 %q", opened)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := box.Open(sealed); err == nil {
		t.Fatal("篡改密文未被拒绝")
	}
}
