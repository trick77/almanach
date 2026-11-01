package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"github token in a sentence", "Der Token ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8 stand im Code.", "Der Token <redacted> stand im Code."}, // ship:allow-secret: invented fixture
		{"aws key id", "Schluessel AKIAIOSFODNN7EXAMPLE im Test", "Schluessel <redacted> im Test"},                                                 // ship:allow-secret: invented fixture
		{"aws secret under its name", "aws_secret_access_key = wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY", "<redacted>"},                              // ship:allow-secret: invented fixture
		{"url credentials keep host", "Ruft https://svc:hunter2@upstream.example.invalid/x auf", "Ruft https://<redacted>@upstream.example.invalid/x auf"},
		{"jwt", "Header eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0In0.abc gesetzt", "Header <redacted> gesetzt"},
		{"jasypt", "Wert ENC(abc123) bleibt verschluesselt", "Wert <redacted> bleibt verschluesselt"},
		{"basic header", "Authorization: Basic YWJjOmRlZjEyMw==", "Authorization: Basic <redacted>"},
		{"bearer header", "Authorization: Bearer abcdef0123456789.abcdef", "Authorization: Bearer <redacted>"},
		{"pem block whole", "Key:\n-----BEGIN RSA PRIVATE KEY-----\nMIIEinventedAAAAAAAAAAAA\nmore\n-----END RSA PRIVATE KEY-----\nEnde", "Key:\n<redacted>\nEnde"},
		{"pem block cut off", "-----BEGIN PRIVATE KEY-----\nMIIEinventedAAAAAAAAAAAA", "<redacted>"},
		{"pem block in a string literal", `k := "-----BEGIN PRIVATE KEY-----\nMIIEinventedAAAAAAAAAAAA\n-----END PRIVATE KEY-----"`, `k := "<redacted>"`},
		{"pem header named in prose stays", "Der Schluessel muss mit `-----BEGIN PRIVATE KEY-----` beginnen; danach liest der Loader weiter.", "Der Schluessel muss mit `-----BEGIN PRIVATE KEY-----` beginnen; danach liest der Loader weiter."},
		{"pem header in a prefix check stays", `if !strings.HasPrefix(s, "-----BEGIN RSA PRIVATE KEY-----") {`, `if !strings.HasPrefix(s, "-----BEGIN RSA PRIVATE KEY-----") {`},
		{"basic auth words stay", "Unterstuetzt Basic Authentication/Authorization.", "Unterstuetzt Basic Authentication/Authorization."},
		{"bearer words stay", "Prueft das Bearer Token-Authentifizierungsverfahren.", "Prueft das Bearer Token-Authentifizierungsverfahren."},
		{"base64 basic without digits", "Authorization: Basic dXNlcjpwYXNzd29yZA", "Authorization: Basic <redacted>"},
		{"enc placeholder in docs stays", "Werte im Format ENC(...) entschluesselt Jasypt.", "Werte im Format ENC(...) entschluesselt Jasypt."},
		{"npm variable stays", "Setzt npm_config_cache_folder fuer den Build.", "Setzt npm_config_cache_folder fuer den Build."},
		{"npm token", "NPM_TOKEN=npm_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9", "NPM_TOKEN=<redacted>"}, // ship:allow-secret: invented fixture
		{"token mid-value", `CURL_OPTS=-H "Authorization: Bearer abcdef0123456789abcdef"`, `CURL_OPTS=-H "Authorization: Bearer <redacted>"`},
		{"german basic auth stays", "Der Dienst nutzt HTTP Basic Authentifizierung.", "Der Dienst nutzt HTTP Basic Authentifizierung."},
		{"german bearer word stays", "Bearer Authentifizierungsverfahren ueber OIDC.", "Bearer Authentifizierungsverfahren ueber OIDC."},
		{"getenv stays", "`password := os.Getenv(\"DB_PASSWORD\")` liest das Passwort.", "`password := os.Getenv(\"DB_PASSWORD\")` liest das Passwort."},
		{"sha stays", "Commit 3f2a9c1e5b7d4a6f8e0c2b1d9a7f6e5c4b3a2d1e", "Commit 3f2a9c1e5b7d4a6f8e0c2b1d9a7f6e5c4b3a2d1e"},
		{"task-scheduler stays", "Der task-scheduler-deployment startet nachts.", "Der task-scheduler-deployment startet nachts."},
		{"url without credentials stays", "Siehe https://docs.example.invalid/a@b", "Siehe https://docs.example.invalid/a@b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Text(c.in); got != c.want {
				t.Fatalf("Text(%q)\n got: %q\nwant: %q", c.in, got, c.want)
			}
		})
	}
}

func TestFile(t *testing.T) {
	cases := []struct {
		name, path, in, want string
		ok                   bool
	}{
		{"secret manifest out", "k8s/secret.yaml", "apiVersion: v1\nkind: Secret\ndata:\n  x: eQ==\n", "", false},
		{"config value by key", "application.yaml", "db:\n  password: s3cr3t-invented\n", "db:\n  password: <redacted>\n", true},
		{"config token mid-value", ".env", `CURL_OPTS=-H "Authorization: Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0In0.abc"`, `CURL_OPTS=-H "Authorization: Bearer <redacted>"`, true},
		{"url credentials in a Jenkinsfile", "Jenkinsfile", `sh "curl https://ci:hunter2@nexus.example.invalid/repo"`, `sh "curl https://<redacted>@nexus.example.invalid/repo"`, true},
		{"token in source", "leak.go", `const t = "ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"`, `const t = "<redacted>"`, true}, // ship:allow-secret: invented fixture
		{"getenv in source stays", "main.go", `password := os.Getenv("PW")`, `password := os.Getenv("PW")`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := File(c.path, []byte(c.in))
			if ok != c.ok || string(got) != c.want {
				t.Fatalf("File(%q) = %q %v, want %q %v", c.in, got, ok, c.want, c.ok)
			}
		})
	}
	in := []byte("package main\n")
	if got, _ := File("main.go", in); &got[0] != &in[0] {
		t.Error("an untouched file was copied")
	}
}

func TestJSON(t *testing.T) {
	in := json.RawMessage(`{"summary": "Token ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8 entfernt", "n": 3, "list": ["ok", "https://u:p@h.example.invalid/"], "esc": "a \"q\" ä"}`) // ship:allow-secret: invented fixture
	got := string(JSON(in))
	want := `{"summary": "Token <redacted> entfernt", "n": 3, "list": ["ok", "https://<redacted>@h.example.invalid/"], "esc": "a \"q\" ä"}`
	if got != want {
		t.Fatalf("JSON\n got: %s\nwant: %s", got, want)
	}
	if !json.Valid([]byte(got)) {
		t.Fatal("result is not valid JSON")
	}
}

func TestJSON_untouchedIsSameBytes(t *testing.T) {
	in := json.RawMessage(`{"a": "b", "c": [1, 2]}`)
	if got := JSON(in); &got[0] != &in[0] {
		t.Fatal("unchanged document was copied")
	}
	bad := json.RawMessage(`{"a": "ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"`) // ship:allow-secret: invented fixture
	if got := JSON(bad); string(got) != string(bad) {
		t.Fatal("invalid document was rewritten")
	}
	if strings.Contains(string(JSON(json.RawMessage(`"x\\"`))), "redacted") {
		t.Fatal("escaped backslash misread")
	}
}
