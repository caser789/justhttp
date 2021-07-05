package fasthttp

import (
    "testing"
    "strings"
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

func TestArgsParse(t *testing.T) {
    var a Args

    // empty args
    testArgsParse(t, &a, "", 0, "foo=", "bar=", "=")

    // arg without value
    testArgsParse(t, &a, "foo1", 1, "foo=", "bar=", "=")

    // arg without value, but with equal sign
    testArgsParse(t, &a, "foo2=", 1, "foo=", "bar=", "=")

    // arg with value
    testArgsParse(t, &a, "foo3=bar1", 1, "foo3=bar1", "bar=", "=")

    // empty key
    testArgsParse(t, &a, "=bar2", 1, "foo=", "=bar2", "bar2=")

    // missing kv
    testArgsParse(t, &a, "&&&&", 0, "foo=", "bar=", "=")

    // multiple value with the same key
    testArgsParse(t, &a, "x=1&x=2&x=3", 3, "x=1")

    // multiple args without values
    testArgsParse(t, &a, "&&a&&b&&bar&baz", 4, "a=", "b=", "bar=", "baz=")

    // values with '='
    testArgsParse(t, &a, "zz=1&k=v=v=a=a=s", 2, "k=v=v=a=a=s", "zz=1")

    // mixed '=' and '&'
    testArgsParse(t, &a, "sss&z=dsf=&df", 3, "sss=", "z=dsf=", "df=")

    // encoded args
    testArgsParse(t, &a, "%E8%96%9B=%E8%9B%9F", 1, "薛=蛟")

    // invalid percent encoding
    testArgsParse(t, &a, "f%=x&qw%z=d%0k%20p&%%20=%%%20x", 3, "f%=x", "qw%z=d%0k p", "% =%% x")
}

func testArgsParse(t *testing.T, a *Args, s string, expectedLen int, expectedArgs ...string) {
    var buf []byte
    a.Parse(s)
    if a.Len() != expectedLen {
        t.Fatalf("Unexpected args len %d. Expected %d. s=%q", a.Len(), expectedLen, s)
    }
    for _, xx := range expectedArgs {
        tmp := strings.SplitN(xx, "=", 2)
        k := tmp[0]
        v := tmp[1]
        buf = a.GetBytes(buf, k)
        if string(buf) != v {
            t.Fatalf("Unexpected value for key=%q: %q. Expected %q. s=%q", k, buf, v, s)
        }
    }
}
