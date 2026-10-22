package errors

import (
	stderrors "errors"
	"testing"
)

func TestWrapUnwrap(t *testing.T) {
	cause := stderrors.New("root")
	err := Wrap(cause, CodeUnavailable, "meili.EnsureIndex", "request failed")
	var de *DomainError
	if !As(err, &de) {
		t.Fatal("expected DomainError")
	}
	if de.Code != CodeUnavailable {
		t.Fatalf("code %q", de.Code)
	}
	if !stderrors.Is(err, cause) {
		t.Fatal("unwrap chain")
	}
}

func TestCodeOf(t *testing.T) {
	err := New(CodeInvalid, "test", "bad input")
	if CodeOf(err) != CodeInvalid {
		t.Fatalf("got %q", CodeOf(err))
	}
}
