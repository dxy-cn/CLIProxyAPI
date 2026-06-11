package util

import "testing"

func TestMaskSensitiveHeaderValueXAPIKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		in   string
		want string
	}{
		{
			name: "sk key keeps prefix first three and last six",
			key:  "X-Api-Key",
			in:   "sk-abc123456789",
			want: "sk-abc...456789",
		},
		{
			name: "case insensitive header name",
			key:  "x-api-key",
			in:   "sk-xyz987654321",
			want: "sk-xyz...654321",
		},
		{
			name: "non sk key keeps first three and last six",
			key:  "X-Api-Key",
			in:   "apikey123456789",
			want: "api...456789",
		},
		{
			name: "short key falls back to existing masking",
			key:  "X-Api-Key",
			in:   "sk-short",
			want: "sk...rt",
		},
		{
			name: "other api key headers keep existing masking",
			key:  "OpenAI-Api-Key",
			in:   "sk-abc123456789",
			want: "sk-a...6789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskSensitiveHeaderValue(tt.key, tt.in); got != tt.want {
				t.Fatalf("MaskSensitiveHeaderValue(%q, %q) = %q, want %q", tt.key, tt.in, got, tt.want)
			}
		})
	}
}
