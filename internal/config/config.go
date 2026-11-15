// Package config loads gl-dash's configuration and resolves the GitLab hosts
// and tokens to use.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Section is one tab of the dashboard, in gh-dash's section shape.
type Section struct {
	Title   string `yaml:"title"`
	Filters string `yaml:"filters"`
	Limit   int    `yaml:"limit"`
}

// HostConfig is a host entry in gl-dash's own config file.
type HostConfig struct {
	Host        string `yaml:"host"`
	APIHost     string `yaml:"api_host"`
	APIProtocol string `yaml:"api_protocol"`
	Token       string `yaml:"token"`
}

// File is gl-dash's config file.
type File struct {
	Hosts      []HostConfig `yaml:"hosts"`
	MRSections []Section    `yaml:"mrSections"`
	Defaults   struct {
		MRsLimit int `yaml:"mrsLimit"`
	} `yaml:"defaults"`
}

// DefaultSections are named after the groups on GitLab's merge request homepage.
var DefaultSections = []Section{
	{Title: "Review requested", Filters: "reviewer:@me my-review-state:unreviewed,review_started,unapproved"},
	{Title: "Returned to you", Filters: "author:@me draft:false review-state:requested_changes,reviewed"},
	{Title: "Your merge requests", Filters: "author:@me"},
}

const defaultLimit = 20

// Load reads gl-dash's config file. A missing file at the default path is
// not an error; a missing file named explicitly is.
func Load(path string, explicit bool) (File, error) {
	var f File
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !explicit {
		return f.withDefaults(), nil
	}
	if err != nil {
		return f, err
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	for i, h := range f.Hosts {
		if h.Host == "" {
			return f, fmt.Errorf("%s: hosts[%d]: host is required", path, i)
		}
	}
	for i, s := range f.MRSections {
		if s.Title == "" {
			return f, fmt.Errorf("%s: mrSections[%d]: title is required", path, i)
		}
	}
	return f.withDefaults(), nil
}

func (f File) withDefaults() File {
	if len(f.MRSections) == 0 {
		f.MRSections = append([]Section(nil), DefaultSections...)
	}
	if f.Defaults.MRsLimit <= 0 {
		f.Defaults.MRsLimit = defaultLimit
	}
	for i := range f.MRSections {
		if f.MRSections[i].Limit <= 0 {
			f.MRSections[i].Limit = f.Defaults.MRsLimit
		}
		if f.MRSections[i].Limit > 100 {
			f.MRSections[i].Limit = 100
		}
	}
	return f
}

// DefaultPath returns the config path: $GL_DASH_CONFIG, then
// $XDG_CONFIG_HOME/gl-dash/config.yml, then ~/.config/gl-dash/config.yml.
func DefaultPath() string {
	if p := os.Getenv("GL_DASH_CONFIG"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "gl-dash", "config.yml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gl-dash", "config.yml")
}

// EnvTokenName returns the per-host token variable, e.g.
// GL_DASH_TOKEN_GITLAB_EXAMPLE_COM for gitlab.example.com.
func EnvTokenName(host string) string {
	var b strings.Builder
	b.WriteString("GL_DASH_TOKEN_")
	for _, r := range strings.ToUpper(host) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
