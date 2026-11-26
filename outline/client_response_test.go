package outline

import (
	"strings"
	"testing"
)

func TestReadBoundedResponse(t *testing.T) {
	data, err := readBoundedResponse(strings.NewReader("12345"), 5)
	if err != nil || string(data) != "12345" {
		t.Fatalf("expected exact limit to pass, data=%q err=%v", data, err)
	}
	if _, err := readBoundedResponse(strings.NewReader("123456"), 5); err == nil {
		t.Fatal("response over limit should fail")
	}
	if _, err := readBoundedResponse(strings.NewReader(""), 0); err == nil {
		t.Fatal("zero limit should fail")
	}
}
