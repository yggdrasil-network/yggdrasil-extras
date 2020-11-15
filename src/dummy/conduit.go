package dummy

type Conduit struct {
	recv chan []byte
	send chan []byte
}

type ConduitEndpoint struct {
	recv chan []byte
	send chan []byte
}

func CreateConduit() *Conduit {
	return &Conduit{
		recv: make(chan []byte),
		send: make(chan []byte),
	}
}

func CreateConduitEndpoint(c *Conduit) *ConduitEndpoint {
	return &ConduitEndpoint{
		recv: c.send,
		send: c.recv,
	}
}

func (c *ConduitEndpoint) Send(p []byte) {
	c.send <- append([]byte{}, p...)
}

func (c *ConduitEndpoint) Recv() []byte {
	return <-c.recv
}

func (c *Conduit) Write(p []byte) (n int, err error) {
	c.send <- p
	return len(p), nil
}

func (c *Conduit) Read(p []byte) (n int, err error) {
	b := <-c.recv
	copy(p[:], b)
	return len(b), nil
}

func (c *Conduit) Close() error {
	close(c.recv)
	close(c.send)
	return nil
}
