package main

import (
	"testing"
	"time"
)

func TestTimeoutFromEnv(t *testing.T) {
	t.Setenv("SPLUNKTIMEOUT", "")
	val, err := timeoutFromEnv()
	if err != nil {
		t.Fatalf("expected default timeout, got error: %v", err)
	}
	if val != 120*time.Second {
		t.Fatalf("expected default timeout 120s, got %v", val)
	}

	cases := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "valid", value: "30", want: 30 * time.Second},
		{name: "negative", value: "-30", wantErr: true},
		{name: "invalid", value: "abc", wantErr: true},
		{name: "zero", value: "0", wantErr: true},
		{name: "whitespace", value: "  45  ", want: 45 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SPLUNKTIMEOUT", tc.value)
			got, err := timeoutFromEnv()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for value %q", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for value %q: %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("value %q expected %v, got %v", tc.value, tc.want, got)
			}
		})
	}
}

func TestTLSVerifyFromEnv(t *testing.T) {
	t.Setenv("SPLUNKTLSVERIFY", "")
	val, err := tlsVerifyFromEnv()
	if err != nil {
		t.Fatalf("expected default tls verify true, got error: %v", err)
	}
	if !val {
		t.Fatalf("expected default tls verify true, got false")
	}

	cases := []struct {
		name    string
		value   string
		want    bool
		wantErr bool
	}{
		{name: "true", value: "true", want: true},
		{name: "false", value: "false", want: false},
		{name: "1", value: "1", want: true},
		{name: "0", value: "0", want: false},
		{name: "invalid", value: "not-bool", wantErr: true},
		{name: "whitespace", value: "  false  ", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SPLUNKTLSVERIFY", tc.value)
			got, err := tlsVerifyFromEnv()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for value %q", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for value %q: %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("value %q expected %v, got %v", tc.value, tc.want, got)
			}
		})
	}
}
