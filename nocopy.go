package fasthttp

type noCopy struct{}

func (*noCopy) Lock() {}
