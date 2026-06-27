package ai

import "testing"

func TestDocumentNameFromMessage(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{
			message: "Tell me about Parwarish Cares Foundation from document 183_ngo_reg_cert.pdf",
			want:    "183_ngo_reg_cert.pdf",
		},
		{
			message: "Use REPORT-2026.PDF and find the CIN",
			want:    "REPORT-2026.PDF",
		},
		{
			message: "What is the weather in Delhi?",
			want:    "",
		},
	}

	for _, test := range tests {
		if got := documentNameFromMessage(test.message); got != test.want {
			t.Fatalf("documentNameFromMessage(%q) = %q; want %q", test.message, got, test.want)
		}
	}
}
