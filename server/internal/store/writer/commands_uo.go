package writer

// UO owns explicit CommandID constants 600–699.
const SetPersonalFlags CommandID = 600

var uoCommands = commandRegistration{owner: "UO", descriptors: []Descriptor{
	{ID: SetPersonalFlags, Name: "uo_set_personal_flags", Durability: Normal},
}}
