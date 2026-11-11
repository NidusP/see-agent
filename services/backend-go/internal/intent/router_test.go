package intent

import "testing"

func TestResponseIntentConstantsExcludeInternalRoutingMarker(t *testing.T) {
	values := []string{KnowledgeQuery, SocialGreeting, SocialThanks, SocialFarewell, CapabilityHelp, OffTopic, OutOfScope}
	for _, value := range values {
		if value == "context_routing_required" || value == "controlled_confirmation" {
			t.Fatalf("response intent must not expose internal routing marker: %q", value)
		}
	}
}
