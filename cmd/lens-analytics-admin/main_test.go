package main

import (
	"strings"
	"testing"
)

func TestDerivePseudonym(t *testing.T) {
	salt := []byte("01234567890123456789012345678901")
	user, err := derivePseudonym("user", "clerk:user_test", salt)
	if err != nil || !strings.HasPrefix(user, "phu_") {
		t.Fatalf("derive user pseudonym = %q, %v", user, err)
	}
	workspace, err := derivePseudonym("workspace", "org_test", salt)
	if err != nil || !strings.HasPrefix(workspace, "phw_") || workspace == user {
		t.Fatalf("derive workspace pseudonym = %q, %v", workspace, err)
	}
}

func TestDerivePseudonymRejectsUnsafeInput(t *testing.T) {
	if _, err := derivePseudonym("user", "subject", []byte("short")); err == nil {
		t.Fatal("short salt was accepted")
	}
	if _, err := derivePseudonym("other", "subject", []byte("01234567890123456789012345678901")); err == nil {
		t.Fatal("unknown identifier kind was accepted")
	}
}
