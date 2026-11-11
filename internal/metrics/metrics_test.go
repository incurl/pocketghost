package metrics

import "testing"

func TestStatsAndFlush(t *testing.T) {
	s := New()
	for i := 0; i < 5; i++ {
		s.AddRequest()
		s.AddHost("demo.example.test")
		s.AddIP("203.0.113.5")
		s.AddCountry("US")
	}
	s.AddError()

	snap := s.Snapshot()
	if snap.Requests != 5 || snap.Errors != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if len(snap.Hosts) != 1 || snap.Hosts[0][0] != "demo.example.test" {
		t.Fatalf("hosts = %#v", snap.Hosts)
	}

	s.Flush()
	snap = s.Snapshot()
	if snap.Requests != 0 || len(snap.Hosts) != 0 || len(snap.IPs) != 0 {
		t.Fatalf("flush failed: %+v", snap)
	}
}

func TestTopLimit(t *testing.T) {
	s := New()
	for i := 0; i < 20; i++ {
		s.AddHost(string(rune('a' + i)))
	}
	if got := len(s.Snapshot().Hosts); got != 10 {
		t.Fatalf("top entries = %d", got)
	}
}
