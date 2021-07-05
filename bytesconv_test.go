package fasthttp

import (
    "testing"
)

func TestParseUintSuccess(t *testing.T) {
    testParseUintSuccess(t, "0", 0)
    testParseUintSuccess(t, "123", 123)
    testParseUintSuccess(t, "123456789012345678", 123456789012345678)
}

func testParseUintSuccess(t *testing.T, s string, expectedN int) {
    n, err := parseUint([]byte(s))
    if err != nil {
        t.Fatalf("Unexpected error when parsing %q: %s", s, err)
    }
    if n != expectedN {
        t.Fatalf("Unexpected value %d. Expected %d. num=%q", n, expectedN, s)
    }
}

func TestParseUintError(t *testing.T) {
    // empty string
    testParseUintError(t, "")

    // negative value
    testParseUintError(t, "-123")

    // non-num
    testParseUintError(t, "foobar234")

    // non-num chars at the end
    testParseUintError(t, "123w")

    // floating point num
    testParseUintError(t, "1234.545")

    // too big num
    testParseUintError(t, "12345678901234567890")
}

func testParseUintError(t *testing.T, s string) {
    n, err := parseUint([]byte(s))
    if err == nil {
        t.Fatalf("Expecting error when parsing %q. obtained %d", s, n)
    }
    if n >= 0 {
        t.Fatalf("Unexpected n=%d when parsing %q. Expected negative num", n, s)
    }
}
