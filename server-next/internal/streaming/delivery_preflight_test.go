package streaming

type originalDeliverySettings struct {
	cfg   DeliveryConfiguration
	reads int
}

func (s *originalDeliverySettings) Delivery() DeliveryConfiguration {
	panic("cached delivery used for accepting proof")
}
