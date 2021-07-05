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
