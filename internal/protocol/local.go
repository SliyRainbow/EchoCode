package protocol

type LocalProvider struct {
	Endpoint string
}

func (p LocalProvider) Name() string {
	return "local"
}
