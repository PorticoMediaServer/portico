package writer

import "strings"

func cloneBasicSource(s basicStreamFactStage) basicStreamFactStage {
	s.assetToken = strings.Clone(s.assetToken)
	s.runID = strings.Clone(s.runID)
	s.sourceID = strings.Clone(s.sourceID)
	s.incarnation = strings.Clone(s.incarnation)
	s.objectID = strings.Clone(s.objectID)
	s.objectRevision = strings.Clone(s.objectRevision)
	s.fingerprint = strings.Clone(s.fingerprint)
	return s
}
