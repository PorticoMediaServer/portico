package mediaengine

// ConversionLoad includes converters still running in a previous destination.
func (e *Engine) ConversionLoad() (hardware, processor int) {
	e.mu.Lock()
	places := append([]*place{e.current}, e.retired...)
	e.mu.Unlock()
	for _, p := range places {
		h, c := p.streams.ConversionLoad()
		hardware += h
		processor += c
	}
	return hardware, processor
}
