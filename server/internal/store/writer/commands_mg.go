package writer

// MG owns explicit CommandID constants 800–899. Add its descriptors here.
var mgCommands = commandRegistration{owner: "MG", descriptors: mgDomainDescriptors()}

func mgDomainDescriptors() []Descriptor {
	out := append(mgSourceDescriptors(), mgDVRPolicyDescriptors()...)
	out = append(out, mgRuleDescriptors()...)
	return append(out, mgGuideDescriptors()...)
}
