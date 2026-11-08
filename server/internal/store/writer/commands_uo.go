package writer

// UO owns explicit CommandID constants 600–699.
const (
	SetPersonalFlags CommandID = 600
	PublishNotice    CommandID = 620
	WithdrawNotice   CommandID = 621
)

var uoCommands = commandRegistration{owner: "UO", descriptors: []Descriptor{
	{ID: SetPersonalFlags, Name: "uo_set_personal_flags", Durability: Normal},
	{ID: PublishNotice, Name: "uo_publish_notice", Durability: Normal},
	{ID: WithdrawNotice, Name: "uo_withdraw_notice", Durability: Normal},
}}
