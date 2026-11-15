package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const glabYAML = `git_protocol: ssh
hosts:
    gitlab.example.dev:
        token: ""
        use_keyring: "true"
        api_host: gitlab.example.dev
        api_protocol: https
        user: alex
    gitlab.com:
        token: ""
        api_host: gitlab.com
    localhost.test:
        api_host: 127.0.0.1:8081
        api_protocol: http
`

func testEnv(t *testing.T, vars map[string]string, glabTokens map[string]string) Env {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(p, []byte(glabYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return Env{
		Getenv:     func(k string) string { return vars[k] },
		GlabToken:  func(h string) (string, error) { return glabTokens[h], nil },
		GlabConfig: p,
	}
}

func TestResolveHostsFromGlab(t *testing.T) {
	env := testEnv(t,
		map[string]string{"GL_DASH_TOKEN_GITLAB_COM": "env-com"},
		map[string]string{"gitlab.example.dev": "keyring-dev", "gitlab.com": "glab-com"},
	)
	hosts, err := ResolveHosts(File{}, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []Host{
		{Name: "gitlab.example.dev", APIHost: "gitlab.example.dev", APIProtocol: "https", Token: "keyring-dev", TokenSource: "glab"},
		{Name: "gitlab.com", APIHost: "gitlab.com", APIProtocol: "https", Token: "env-com", TokenSource: "env"},
		{Name: "localhost.test", APIHost: "127.0.0.1:8081", APIProtocol: "http"},
	}
	if !reflect.DeepEqual(hosts, want) {
		t.Errorf("hosts =\n%+v\nwant\n%+v", hosts, want)
	}
}

func TestResolveHostsConfigListWins(t *testing.T) {
	env := testEnv(t, nil, map[string]string{"gitlab.example.dev": "keyring-dev"})
	f := File{Hosts: []HostConfig{
		{Host: "gitlab.example.dev"},
		{Host: "git.internal.test", APIProtocol: "http", Token: "cfg-tok"},
	}}
	hosts, err := ResolveHosts(f, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []Host{
		{Name: "gitlab.example.dev", APIHost: "gitlab.example.dev", APIProtocol: "https", Token: "keyring-dev", TokenSource: "glab"},
		{Name: "git.internal.test", APIHost: "git.internal.test", APIProtocol: "http", Token: "cfg-tok", TokenSource: "config"},
	}
	if !reflect.DeepEqual(hosts, want) {
		t.Errorf("hosts =\n%+v\nwant\n%+v", hosts, want)
	}
}

func TestTokenPrecedence(t *testing.T) {
	glab := map[string]string{"gitlab.com": "glab"}
	cases := []struct {
		name       string
		vars       map[string]string
		cfg        string
		want, from string
	}{
		{"per-host env beats all", map[string]string{"GL_DASH_TOKEN_GITLAB_COM": "a", "GITLAB_TOKEN": "b"}, "c", "a", "env"},
		{"GITLAB_TOKEN applies to gitlab.com by default", map[string]string{"GITLAB_TOKEN": "b"}, "c", "b", "env"},
		{"GITLAB_TOKEN follows GITLAB_HOST", map[string]string{"GITLAB_TOKEN": "b", "GITLAB_HOST": "https://other.test"}, "c", "c", "config"},
		{"config beats glab", nil, "c", "c", "config"},
		{"glab last", nil, "", "glab", "glab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := Env{Getenv: func(k string) string { return c.vars[k] }, GlabToken: func(h string) (string, error) { return glab[h], nil }}
			tok, from := resolveToken("gitlab.com", c.cfg, env)
			if tok != c.want || from != c.from {
				t.Errorf("got (%q, %q), want (%q, %q)", tok, from, c.want, c.from)
			}
		})
	}
}

func TestResolveHostsNone(t *testing.T) {
	env := Env{Getenv: func(string) string { return "" }}
	_, err := ResolveHosts(File{}, env)
	if err == nil || !strings.Contains(err.Error(), "glab auth login") {
		t.Fatalf("err = %v, want a hint naming glab auth login", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	f, err := Load(filepath.Join(dir, "missing.yml"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(titles(f.MRSections), []string{"Review requested", "Returned to you", "Your merge requests"}) || f.MRSections[0].Limit != 20 {
		t.Errorf("defaults not applied: %+v", f.MRSections)
	}

	if _, err := Load(filepath.Join(dir, "missing.yml"), true); err == nil {
		t.Error("explicit missing config: want error")
	}

	p := filepath.Join(dir, "config.yml")
	os.WriteFile(p, []byte("defaults:\n  mrsLimit: 5\nmrSections:\n  - title: Mine\n    filters: author:@me\n  - title: Big\n    filters: reviewer:@me\n    limit: 500\n"), 0o600)
	f, err = Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if f.MRSections[0].Limit != 5 || f.MRSections[1].Limit != 100 {
		t.Errorf("limits = %d, %d; want 5, 100", f.MRSections[0].Limit, f.MRSections[1].Limit)
	}

	os.WriteFile(p, []byte("mrSections:\n  - filters: author:@me\n"), 0o600)
	if _, err := Load(p, true); err == nil || !strings.Contains(err.Error(), "title is required") {
		t.Errorf("err = %v, want title is required", err)
	}
}

func TestEnvTokenName(t *testing.T) {
	if got := EnvTokenName("gitlab.example-corp.dev:8443"); got != "GL_DASH_TOKEN_GITLAB_EXAMPLE_CORP_DEV_8443" {
		t.Errorf("EnvTokenName = %q", got)
	}
}

func titles(ss []Section) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Title)
	}
	return out
}
