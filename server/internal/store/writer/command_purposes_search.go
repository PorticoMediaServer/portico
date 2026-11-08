package writer

// PB search image stage.
var searchImageCommandPurposes = commandPurposeRegistration{owner: "search_image", purposes: []commandPurpose{
	{id: SearchImageStage, plan: true, memory: searchImagePartition, clock: clockUnderLease},
}}

// PB search publisher jobs.
var searchPublisherCommandPurposes = commandPurposeRegistration{owner: "search_publisher", purposes: []commandPurpose{
	{id: SearchFragmentStage, plan: true, memory: publisherPartition, clock: clockUnderLease},
	{id: SearchHeadPublish, plan: true, memory: publisherPartition, clock: clockUnderLease},
}}
