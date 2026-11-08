package writer

// WC progress, security and receipt reclaim. These own no run lease.
var wcCommandPurposes = commandPurposeRegistration{owner: "wc", purposes: []commandPurpose{
	{id: ProgressAccept, plan: true, memory: wcProgressPartition, clock: clockWithoutLease},
	{id: SecurityAccept, plan: true, memory: wcSecurityPartition, clock: clockWithoutLease},
	{id: ProgressReceiptReclaim, plan: true, memory: wcReclaimPartition, clock: clockWithoutLease},
}}
