package streaming

type deliveryPlanWire DeliveryPlan

func (v DeliveryPlan) MarshalJSON() ([]byte, error) {
	return MarshalDurations(deliveryPlanWire(v), map[string]string{"duration": "durationMs"})
}
func (v *DeliveryPlan) UnmarshalJSON(raw []byte) error {
	return UnmarshalDurations(raw, (*deliveryPlanWire)(v), map[string]string{"duration": "durationMs"})
}
