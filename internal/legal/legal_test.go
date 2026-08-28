package legal

import (
	"strings"
	"testing"
)

// An empty embed compiles and produces a binary that claims to carry notices
// while carrying none, which is the failure mode worth a test.
func TestNoticesAreEmbedded(t *testing.T) {
	got := ThirdPartyNotices()
	if len(got) < 1000 {
		t.Fatalf("embedded notices are %d bytes, which cannot be a full set", len(got))
	}
	for _, want := range []string{
		"arex - third-party notices",
		"Apache License",                                    // client_golang, common, procfs
		"Redistribution and use in source and binary forms", // the BSD deps: protobuf, x/sys
		"github.com/prometheus/",                            // the direct dependency
		"google/go-licenses",                                // provenance of the file
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notices do not mention %q", want)
		}
	}
}
