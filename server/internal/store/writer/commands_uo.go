package writer

// UO owns explicit CommandID constants 600–699.
const (
	SetPersonalFlags   CommandID = 600
	PublishNotice      CommandID = 620
	WithdrawNotice     CommandID = 621
	AcknowledgeNotices CommandID = 622
	ReadAllNotices     CommandID = 623
	// AdvanceNoticeOperation is allocated to UO-14's shared broadcast operation.
	// This slice reads one operation's completion point and reclaims expired
	// work; it adds no advance body, and a descriptor alone never admits a turn.
	// The operation writer/dispatcher that owns 624 must supply that body.
	AdvanceNoticeOperation CommandID = 624
	ReclaimNotices         CommandID = 625
)

var uoCommands = commandRegistration{owner: "UO", descriptors: []Descriptor{
	{ID: SetPersonalFlags, Name: "uo_set_personal_flags", Durability: Normal},
	{ID: PublishNotice, Name: "uo_publish_notice", Durability: Normal},
	{ID: WithdrawNotice, Name: "uo_withdraw_notice", Durability: Normal},
	{ID: AcknowledgeNotices, Name: "uo_acknowledge_notices", Durability: Normal},
	{ID: ReadAllNotices, Name: "uo_read_all_notices", Durability: Normal},
	{ID: AdvanceNoticeOperation, Name: "uo_advance_notice_operation", Durability: Normal},
	{ID: ReclaimNotices, Name: "uo_reclaim_notices", Durability: Normal},
}}
