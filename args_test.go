package fasthttp

import (
    "testing"
)

func TestArgsStringCompose(t *testing.T) {
    var a Args
    a.Set("foo", "bar")
    a.Set("aa", "bbb")
    a.Set("薛", "蛟")
    a.Set("", "xxxx")
    a.Set("cvx", "")

    expectedS := "foo=bar&aa=bbb&%E8%96%9B=%E8%9B%9F&=xxxx&cvx"

    s := a.String()
    if s != expectedS {
        t.Fatalf("Unexpected string %q. Expected %q", s, expectedS)
    }
}

func TestArgsString(t *testing.T) {
    var a Args

    testArgsString(t, &a, "")
    testArgsString(t, &a, "foobar")
    testArgsString(t, &a, "foo=bar")
    testArgsString(t, &a, "foo=bar&baz=sss")
    testArgsString(t, &a, "")
    testArgsString(t, &a, "foo=bar&aa=bbb&%E8%96%9B=%E8%9B%9F&=xxxx&cvx")
    testArgsString(t, &a, "=asdfsdf")
}

func testArgsString(t *testing.T, a *Args, s string) {
    a.Parse(s)
    s1 := a.String()
    if s != s1 {
        t.Fatalf("Unexpected args %q. Expected %q", s1, s)
    }
}
