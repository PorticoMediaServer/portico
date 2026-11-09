package writer

// MG owns explicit CommandID constants 800–899. Add its descriptors here.
var mgCommands = commandRegistration{owner: "MG", descriptors: mgDomainDescriptors()}

func mgDomainDescriptors() []Descriptor {
	out := append(mgSourceDescriptors(), mgDVRPolicyDescriptors()...)
	out = append(out, mgTunerDescriptors()...)
	out = append(out, mgTunerRetireDescriptors()...)
	out = append(out, mgRuleDescriptors()...)
	out = append(out, mgGuideDescriptors()...)
	out = append(out, mgGuidePublicationDescriptors()...)
	return append(out, mgGuideReclaimDescriptors()...)
}
