package pbbin

import "testing"

func TestParseAndCompare(t *testing.T) {
	a, err := ParseVersion("0.22.3")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseVersion("0.23.0")
	if a.Compare(b) >= 0 {
		t.Fatalf("0.22.3 should sort below 0.23.0")
	}
	pre, _ := ParseVersion("0.22.3-rc1")
	if pre.Compare(a) >= 0 {
		t.Fatalf("prerelease should sort below release")
	}
	if _, err := ParseVersion("nope"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSatisfies(t *testing.T) {
	v, _ := ParseVersion("0.22.4")
	cases := []struct {
		rng  string
		want bool
	}{
		{"<1.0.0", true},
		{"0.22.*", true},
		{"0.23.*", false},
		{">=0.22.0 <0.23.0", true},
		{">0.22.4", false},
		{"~0.22.0", true},
		{"^0.22.0", true},
		{"*", true},
		{"0.22.4", true},
		{"0.22.5", false},
	}
	for _, c := range cases {
		if got := v.Satisfies(c.rng); got != c.want {
			t.Errorf("Satisfies(%q) = %v, want %v", c.rng, got, c.want)
		}
	}
}

func TestMaxSatisfying(t *testing.T) {
	list := []string{"0.20.0", "0.22.1", "0.22.9", "0.23.0", "1.0.0"}
	got, ok := MaxSatisfying(list, "0.22.*")
	if !ok || got != "0.22.9" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	got, ok = MaxSatisfying(list, "<1.0.0")
	if !ok || got != "0.23.0" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	if _, ok := MaxSatisfying(list, "2.*"); ok {
		t.Fatal("expected no match")
	}
}

func TestPlatformNaming(t *testing.T) {
	p := Platform{OS: "linux", Arch: "amd64"}
	if p.Suffix() != "linux_amd64" {
		t.Fatalf("suffix = %q", p.Suffix())
	}
	if p.AssetName("0.22.0") != "pocketbase_0.22.0_linux_amd64.zip" {
		t.Fatalf("asset = %q", p.AssetName("0.22.0"))
	}
	if (Platform{OS: "windows"}).BinaryName() != "pocketbase.exe" {
		t.Fatal("windows binary name")
	}
}
