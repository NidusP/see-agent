// Package intent holds response intent identifiers shared by Backend
// persistence and Runtime response validation.  It deliberately contains no
// semantic classifier: normal user turns are routed by the Runtime model with
// bounded conversation context.
package intent

const (
	KnowledgeQuery = "knowledge_query"
	SocialGreeting = "social_greeting"
	SocialThanks   = "social_thanks"
	SocialFarewell = "social_farewell"
	CapabilityHelp = "capability_help"
	OffTopic       = "off_topic"
	OutOfScope     = "out_of_scope"
)
