package urlorigin

import (
	"strings"
	"testing"
)

func TestOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://Almanach.example.com:443/api/auth/callback": "https://almanach.example.com",
		"https://almanach.example.com:8443/x":                "https://almanach.example.com:8443",
		"http://localhost:80/x":                              "http://localhost",
		"http://localhost:443/x":                             "http://localhost:443",
		"https://[::1]:443/x":                                "https://[::1]",
		"https://[::1]:8443/x":                               "https://[::1]:8443",
		"https://jan@GitHub.com/acme/repo.git":               "https://github.com",
		"https://bitbucket.example/context":                  "https://bitbucket.example",
		"no-scheme":                                          "",
		"/local/path.git":                                    "",
		"":                                                   "",
	} {
		if got := Of(in); got != want {
			t.Errorf("Of(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSame(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"https://forge.example:443/scm/x.git", "https://forge.example", true},
		{"http://forge.example:80/x", "http://forge.example/y", true},
		{"HTTPS://GitHub.COM/a", "https://github.com", true},
		{"https://[::1]:443/a", "https://[::1]/b", true},
		{"https://forge.example:8443/x", "https://forge.example", false},
		{"http://forge.example/x", "https://forge.example", false},
		{"https://github.com.evil.example/a", "https://github.com", false},
		{"", "", false},
		{"/a", "/b", false},
	} {
		if got := Same(c.a, c.b); got != c.want {
			t.Errorf("Same(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestDistinct(t *testing.T) {
	got := Distinct([]string{"https://gitlab.com/b.git", "https://GitHub.com:443/a.git", "https://github.com/c.git"})
	if strings.Join(got, ",") != "https://github.com,https://gitlab.com" {
		t.Fatalf("Distinct = %v", got)
	}
	if len(Distinct(nil)) != 0 {
		t.Fatal("Distinct(nil) not empty")
	}
}
