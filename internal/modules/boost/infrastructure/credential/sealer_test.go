package credential

import (
	"strings"
	"testing"
)

const (
	keyA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-1"
	keyB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-2"
)

func TestSealOpenAndRotate(t *testing.T) {
	v1, err := New(map[int]string{1: keyA}, 1)
	if err != nil {
		t.Fatal(err)
	}
	sealed, version, err := v1.Seal("hunter2")
	if err != nil || version != 1 || !strings.HasPrefix(sealed, "v1:") || strings.Contains(sealed, "hunter2") {
		t.Fatalf("sealed=%q version=%d err=%v", sealed, version, err)
	}
	// 轮换：新版本加密，旧密文仍能解
	v2, err := New(map[int]string{1: keyA, 2: keyB}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := v2.Open(sealed); err != nil || plain != "hunter2" {
		t.Fatalf("open old = %q, %v", plain, err)
	}
	newer, version, _ := v2.Seal("x")
	if version != 2 || !strings.HasPrefix(newer, "v2:") {
		t.Fatalf("new cipher = %q", newer)
	}
	// 去掉旧密钥后旧密文打不开，错误里不带密文
	only2, _ := New(map[int]string{2: keyB}, 2)
	if _, err := only2.Open(sealed); err == nil || strings.Contains(err.Error(), sealed[3:]) {
		t.Fatalf("err = %v", err)
	}
	// 篡改
	tampered := sealed[:len(sealed)-2] + "00"
	if _, err := v1.Open(tampered); err == nil {
		t.Fatal("tampered cipher must fail")
	}
}

func TestConfigValidation(t *testing.T) {
	cases := map[string]func() error{
		"short key":    func() error { _, err := New(map[int]string{1: "short"}, 1); return err },
		"missing curr": func() error { _, err := New(map[int]string{1: keyA}, 2); return err },
		"dup keys":     func() error { _, err := New(map[int]string{1: keyA, 2: keyA}, 1); return err },
		"bad version":  func() error { _, err := FromConfig(map[string]string{"x": keyA}, 0); return err },
	}
	for name, fn := range cases {
		if fn() == nil {
			t.Errorf("%s: want error", name)
		}
	}
	s, err := FromConfig(nil, 0)
	if s != nil || err != nil {
		t.Fatal("empty config means binding disabled")
	}
	s, err = FromConfig(map[string]string{"1": keyA}, 0)
	if err != nil || s.KeyVersion() != 1 {
		t.Fatalf("single key should be current: %v", err)
	}
}
