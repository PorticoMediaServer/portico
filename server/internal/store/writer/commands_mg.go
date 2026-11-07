package writer

// MG owns explicit command IDs 800–899. Descriptors declare compiled purposes;
// no public driver, authority, recipe, SQL registration or runtime enablement.
var mgCommands = commandRegistration{owner: "MG", descriptors: mgDomainDescriptors()}

func mgDomainDescriptors() []Descriptor {
	out := mgGuideDescriptors()
	out = append(out, mgTunerDescriptors()...)
	out = append(out, mgDVRPolicyDescriptors()...)
	out = append(out, mgRecordingIntentDescriptors()...)
	out = append(out, mgPersonalDescriptors()...)
	out = append(out, mgSourceDescriptors()...)
	out = append(out, mgCaptureDescriptors()...)
	return append(out, mgLCDescriptors()...)
}
