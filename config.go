package quic

func (c *Config) Clone() *Config {
	copy := *c
	return &copy
}
