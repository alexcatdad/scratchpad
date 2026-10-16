package localcapture

import "testing"

func TestOnlineRequestAllowlist(t *testing.T) {
	for _, path := range []string{"/records?projectId=p", "/records/r", "/projects/p/context?version=2", "/ai/jobs", "/suggestions"} {
		if !ValidOnlineRequest(OnlineRequest{Method: "GET", Path: path}) {
			t.Errorf("rejected %s", path)
		}
	}
	for _, path := range []string{"/projects/resolve", "/projects/resolve-explicit", "/rag/retrieve", "/search/semantic", "/ai/jobs", "/summaries/export"} {
		if !ValidOnlineRequest(OnlineRequest{Method: "POST", Path: path}) {
			t.Errorf("rejected %s", path)
		}
	}
	for _, r := range []OnlineRequest{{Method: "POST", Path: "/records"}, {Method: "GET", Path: "https://evil.test/records"}, {Method: "GET", Path: "//evil.test/records"}, {Method: "POST", Path: "/auth/mcp/challenge"}, {Method: "DELETE", Path: "/records/r"}, {Method: "GET", Path: "/records/%2e%2e"}, {Method: "GET", Path: "/records/r%2fmirror"}, {Method: "POST", Path: "/ai/jobs?redirect=x"}} {
		if ValidOnlineRequest(r) {
			t.Errorf("accepted bypass %+v", r)
		}
	}
}
