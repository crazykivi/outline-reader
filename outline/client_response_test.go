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

func TestPagePayloadSize(t *testing.T) {
	page := []Document{
		{ID: "id", Title: "title", Text: "body", URL: "/doc/x", UrlID: "x", ParentID: "p", CollectionID: "c"},
	}
	if got := pagePayloadSize(page); got != int64(len("idtitlebody/doc/xxpc")) {
		t.Fatalf("unexpected payload size: %d", got)
	}
	if pagePayloadSize(nil) != 0 {
		t.Fatal("empty page should have zero payload")
	}
}
