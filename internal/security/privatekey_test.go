package security

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const testKey = "aabbccdd11223344aabbccdd11223344aabbccdd11223344aabbccdd11223344"

func TestPrivateKey_ValueReturnsRaw(t *testing.T) {
	pk := NewPrivateKey(testKey)
	if pk.Value() != testKey {
		t.Fatalf("Value() = %q, want %q", pk.Value(), testKey)
	}
}

func TestPrivateKey_StringRedacted(t *testing.T) {
	pk := NewPrivateKey(testKey)
	s := pk.String()
	if s != "[REDACTED]" {
		t.Fatalf("String() = %q, want [REDACTED]", s)
	}
	if strings.Contains(s, testKey) {
		t.Fatal("String() leaked the raw key")
	}
}

func TestPrivateKey_MarshalJSON_Redacted(t *testing.T) {
	pk := NewPrivateKey(testKey)
	data, err := json.Marshal(pk)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	if string(data) != `"[REDACTED]"` {
		t.Fatalf("MarshalJSON = %s, want \"[REDACTED]\"", data)
	}
	if strings.Contains(string(data), testKey) {
		t.Fatal("MarshalJSON leaked the raw key")
	}
}

func TestPrivateKey_StructJSON_Redacted(t *testing.T) {
	type wrapper struct {
		Name string     `json:"name"`
		Key  PrivateKey `json:"key"`
	}
	w := wrapper{Name: "test", Key: NewPrivateKey(testKey)}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if strings.Contains(string(data), testKey) {
		t.Fatalf("struct JSON leaked the raw key: %s", data)
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("struct JSON missing [REDACTED]: %s", data)
	}
}

func TestPrivateKey_Sprintf_NeverLeaks(t *testing.T) {
	pk := NewPrivateKey(testKey)

	formats := []struct {
		format string
		fn     func() string
	}{
		{"%s", func() string { return fmt.Sprintf("%s", pk) }},
		{"%v", func() string { return fmt.Sprintf("%v", pk) }},
		{"%+v", func() string { return fmt.Sprintf("%+v", pk) }},
		{"%#v", func() string { return fmt.Sprintf("%#v", pk) }},
		{"%q", func() string { return fmt.Sprintf("%q", pk) }},
	}

	for _, tc := range formats {
		t.Run(tc.format, func(t *testing.T) {
			result := tc.fn()
			if strings.Contains(result, testKey) {
				t.Fatalf("fmt.Sprintf(%q) leaked raw key: %s", tc.format, result)
			}
		})
	}
}

func TestPrivateKey_ErrorFormatting_NeverLeaks(t *testing.T) {
	pk := NewPrivateKey(testKey)
	err := fmt.Errorf("failed to deploy with key %s", pk)
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("error formatting leaked raw key: %s", err.Error())
	}
}

func TestPrivateKey_MarshalText_Redacted(t *testing.T) {
	pk := NewPrivateKey(testKey)
	text, err := pk.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText error: %v", err)
	}
	if string(text) != "[REDACTED]" {
		t.Fatalf("MarshalText = %q, want [REDACTED]", text)
	}
}
