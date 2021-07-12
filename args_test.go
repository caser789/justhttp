package fasthttp

import (
	"fmt"
	"strings"
	"testing"
)

func TestArgsCopyTo(t *testing.T) {
	var a Args

	// empty args
	testCopyTo(t, &a)

	a.Set("foo", "bar")
	testCopyTo(t, &a)

	a.Set("xxx", "yyy")
	testCopyTo(t, &a)

	a.Del("foo")
	testCopyTo(t, &a)
}

func testCopyTo(t *testing.T, a *Args) {
	keys := make(map[string]struct{})
	a.VisitAll(func(k, v []byte) {
		keys[string(k)] = struct{}{}
	})

	var b Args
	a.CopyTo(&b)

	b.VisitAll(func(k, v []byte) {
		if _, ok := keys[string(k)]; !ok {
			t.Fatalf("unexpected key %q after copying from %q", k, a.String())
		}
		delete(keys, string(k))
	})
	if len(keys) > 0 {
		t.Fatalf("missing keys %#v after copying from %q", keys, a.String())
	}
}

func TestArgsVisitAll(t *testing.T) {
	var a Args
	a.Set("foo", "bar")

	i := 0
	a.VisitAll(func(k, v []byte) {
		if string(k) != "foo" {
			t.Fatalf("unexpected key %q. Expected %q", k, "foo")
		}
		if string(v) != "bar" {
			t.Fatalf("unexpected value %q. Expected %q", v, "bar")
		}
		i++
	})
	if i != 1 {
		t.Fatalf("unexpected number of VisitAll calls: %d. Expected %d", i, 1)
	}
}

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
	testArgsString(t, &a, "f%20o=x.x/x%D0%BF%D1%80%D0%B8%D0%B2%D0%B5aaa&sdf=ss")
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
	a.Parse(s)
	if a.Len() != expectedLen {
		t.Fatalf("Unexpected args len %d. Expected %d. s=%q", a.Len(), expectedLen, s)
	}
	for _, xx := range expectedArgs {
		tmp := strings.SplitN(xx, "=", 2)
		k := tmp[0]
		v := tmp[1]
		buf := a.Peek(k)
		if string(buf) != v {
			t.Fatalf("Unexpected value for key=%q: %q. Expected %q. s=%q", k, buf, v, s)
		}
	}
}

func TestArgsHas(t *testing.T) {
	var a Args

	// single arg
	testArgsHas(t, &a, "foo", "foo")
	testArgsHasNot(t, &a, "foo", "bar", "baz", "")

	// multi args without values
	testArgsHas(t, &a, "foo&bar", "foo", "bar")
	testArgsHasNot(t, &a, "foo&bar", "", "aaaa")

	// multi args
	testArgsHas(t, &a, "b=xx&=aaa&c=", "", "c", "b")
	testArgsHasNot(t, &a, "b=xx&=aaa&c=", "xx", "aaa", "foo")

	// encoded args
	testArgsHas(t, &a, "a+b=c+d%20%20e", "a b")
	testArgsHasNot(t, &a, "a+b=c+d", "a+b", "c+d")
}

func testArgsHas(t *testing.T, a *Args, s string, expectedKeys ...string) {
	a.Parse(s)
	for _, key := range expectedKeys {
		if !a.Has(key) {
			t.Fatalf("Missing key %q in %q", key, s)
		}
	}
}

func testArgsHasNot(t *testing.T, a *Args, s string, unexpectedKeys ...string) {
	a.Parse(s)
	for _, key := range unexpectedKeys {
		if a.Has(key) {
			t.Fatalf("Unexpected key %q in %q", key, s)
		}
	}
}

func TestArgsSetGetDel(t *testing.T) {
	var a Args

	if len(a.Peek("foo")) > 0 {
		t.Fatalf("Unexpected value: %q", a.Peek("foo"))
	}
	if len(a.Peek("")) > 0 {
		t.Fatalf("Unexpected value: %q", a.Peek(""))
	}
	a.Del("xxx")

	for j := 0; j < 3; j++ {
		for i := 0; i < 10; i++ {
			k := fmt.Sprintf("foo%d", i)
			v := fmt.Sprintf("bar_%d", i)
			a.Set(k, v)
			if string(a.Peek(k)) != v {
				t.Fatalf("Unexpected value: %q. Expected %q", a.Peek(k), v)
			}
		}
	}
	for i := 0; i < 10; i++ {
		k := fmt.Sprintf("foo%d", i)
		v := fmt.Sprintf("bar_%d", i)
		if string(a.Peek(k)) != v {
			t.Fatalf("Unexpected value: %q. Expected %q", a.Peek(k), v)
		}
		a.Del(k)
		if string(a.Peek(k)) != "" {
			t.Fatalf("Unexpected value: %q. Expected %q", a.Peek(k), "")
		}
	}

	a.Parse("aaa=xxx&bb=aa")
	if string(a.Peek("foo0")) != "" {
		t.Fatalf("Unexpected value %q", a.Peek("foo0"))
	}
	if string(a.Peek("aaa")) != "xxx" {
		t.Fatalf("Unexpected value %q. Expected %q", a.Peek("aaa"), "xxx")
	}
	if string(a.Peek("bb")) != "aa" {
		t.Fatalf("Unexpected value %q. Expected %q", a.Peek("bb"), "aa")
	}
	for i := 0; i < 10; i++ {
		k := fmt.Sprintf("xx%d", i)
		v := fmt.Sprintf("yy%d", i)
		a.Set(k, v)
		if string(a.Peek(k)) != v {
			t.Fatalf("Unexpected value %q. Expected %q", a.Peek(k), v)
		}
	}
	for i := 5; i < 10; i++ {
		k := fmt.Sprintf("xx%d", i)
		v := fmt.Sprintf("yy%d", i)
		if string(a.Peek(k)) != v {
			t.Fatalf("Unexpected value %q. Expected %q", a.Peek(k), v)
		}
		a.Del(k)
		if string(a.Peek(k)) != "" {
			t.Fatalf("Unexpected value %q. Expected %q", a.Peek(k), "")
		}
	}
}
