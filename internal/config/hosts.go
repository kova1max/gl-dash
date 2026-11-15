package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Host is a GitLab instance to query, with its resolved token.
type Host struct {
	Name        string // display name, e.g. gitlab.com
	APIHost     string // host[:port] the API is served from
	APIProtocol string // https or http
	Token       string
	TokenSource string // env, config or glab; empty when no token was found
}

// Env abstracts the process environment and the glab shell-out for tests.
type Env struct {
	Getenv func(string) string
	// GlabToken returns glab's stored token for host, or "" if none.
	GlabToken func(host string) (string, error)
	// GlabConfig is the path of glab's config.yml, or "" if there is none.
	GlabConfig string
}

// SystemEnv uses the real environment and the glab binary on PATH.
func SystemEnv() Env {
	return Env{Getenv: os.Getenv, GlabToken: glabToken, GlabConfig: glabConfigPath()}
}

// glabHost is a host entry in glab's config.yml.
type glabHost struct {
	Name        string
	APIHost     string `yaml:"api_host"`
	APIProtocol string `yaml:"api_protocol"`
}

// ResolveHosts returns the hosts to query. If gl-dash's config lists hosts,
// exactly those are used; otherwise every host in glab's config is used.
func ResolveHosts(f File, env Env) ([]Host, error) {
	glabHosts, err := readGlabHosts(env.GlabConfig)
	if err != nil {
		return nil, err
	}
	byName := map[string]glabHost{}
	for _, g := range glabHosts {
		byName[g.Name] = g
	}

	var hosts []Host
	if len(f.Hosts) > 0 {
		for _, hc := range f.Hosts {
			g := byName[hc.Host]
			h := Host{Name: hc.Host, APIHost: first(hc.APIHost, g.APIHost, hc.Host), APIProtocol: first(hc.APIProtocol, g.APIProtocol, "https")}
			h.Token, h.TokenSource = resolveToken(hc.Host, hc.Token, env)
			hosts = append(hosts, h)
		}
	} else {
		for _, g := range glabHosts {
			h := Host{Name: g.Name, APIHost: first(g.APIHost, g.Name), APIProtocol: first(g.APIProtocol, "https")}
			h.Token, h.TokenSource = resolveToken(g.Name, "", env)
			hosts = append(hosts, h)
		}
	}
	if len(hosts) == 0 {
		return nil, errors.New("no GitLab hosts configured: run `glab auth login`, or add hosts to " + DefaultPath())
	}
	return hosts, nil
}

// resolveToken looks for a token in the environment, then gl-dash's config,
// then glab (which also reads tokens stored in the OS keyring).
func resolveToken(host, configToken string, env Env) (string, string) {
	if t := env.Getenv(EnvTokenName(host)); t != "" {
		return t, "env"
	}
	// glab's own variables apply to GITLAB_HOST, which defaults to gitlab.com.
	if glabHost := first(env.Getenv("GITLAB_HOST"), "gitlab.com"); stripScheme(glabHost) == host {
		for _, v := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN"} {
			if t := env.Getenv(v); t != "" {
				return t, "env"
			}
		}
	}
	if configToken != "" {
		return configToken, "config"
	}
	if env.GlabToken != nil {
		if t, err := env.GlabToken(host); err == nil && t != "" {
			return t, "glab"
		}
	}
	return "", ""
}

// TokenHint is the one-line fix for a host without a working token.
func TokenHint(host string) string {
	return fmt.Sprintf("run `glab auth login --hostname %s` or set %s", host, EnvTokenName(host))
}

func readGlabHosts(path string) ([]glabHost, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Hosts yaml.Node `yaml:"hosts"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("glab config %s: %w", path, err)
	}
	var hosts []glabHost
	// Walk the mapping node to keep glab's host order.
	for i := 0; i+1 < len(doc.Hosts.Content); i += 2 {
		var g glabHost
		if err := doc.Hosts.Content[i+1].Decode(&g); err != nil {
			return nil, fmt.Errorf("glab config %s: %w", path, err)
		}
		g.Name = doc.Hosts.Content[i].Value
		hosts = append(hosts, g)
	}
	return hosts, nil
}

// glabConfigPath finds glab's config.yml the way glab does.
func glabConfigPath() string {
	var dirs []string
	if d := os.Getenv("GLAB_CONFIG_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		dirs = append(dirs, filepath.Join(x, "glab-cli"))
	}
	if d, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(d, "glab-cli"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "glab-cli"))
	}
	for _, d := range dirs {
		p := filepath.Join(d, "config.yml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// glabToken asks glab for a host's token. This covers tokens glab keeps in
// the OS keyring, which are not in its config file.
func glabToken(host string) (string, error) {
	bin, err := exec.LookPath("glab")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "config", "get", "token", "--host", host)
	// glab prefers these variables over the stored token for every host.
	cmd.Env = withoutVars(os.Environ(), "GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func withoutVars(env []string, names ...string) []string {
	var out []string
outer:
	for _, kv := range env {
		for _, n := range names {
			if strings.HasPrefix(kv, n+"=") {
				continue outer
			}
		}
		out = append(out, kv)
	}
	return out
}

func stripScheme(h string) string {
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	return strings.TrimSuffix(h, "/")
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
