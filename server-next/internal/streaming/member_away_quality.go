package streaming

import "github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"

// narrowMemberTarget puts a member's away-from-home ceilings on a target already made. A height
// ceiling is a box, as a quality is (qualityladder.BoxWidth): the width narrows with it.
func narrowMemberTarget(target QualityTarget, ceiling ReplanCeilings) QualityTarget {
	target.MaxVideoBitrateBPS = narrowCeiling(target.MaxVideoBitrateBPS, ceiling.MaxVideoBitrateBPS)
	target.MaxVideoHeight = narrowCeiling(target.MaxVideoHeight, ceiling.MaxVideoHeight)
	target.MaxVideoWidth = narrowCeiling(target.MaxVideoWidth, qualityladder.BoxWidth(ceiling.MaxVideoHeight))
	return target
}

// ReplanCeilings are the limits access admission places on a member away from
// home. Remote is admission's own view of the connection, not a hint the client
// sent. MaxVideoBitrateBPS is the tightest video bitrate the member and the
// server's upload budget allow there; zero is none. MaxVideoHeight independently
// preserves the account's selected ladder height.
type ReplanCeilings struct {
	Remote             bool
	MaxVideoBitrateBPS int
	MaxVideoHeight     int
}
