// Package config resolves goatlassian's data directory and reads its
// optional config.toml.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is config.toml. Every field has a default, so the file is optional.
type Config struct {
	// StaleDays: an active project with no activity for this long is stale.
	StaleDays int `toml:"stale_days" json:"stale_days"`
	// WikiDriftCommits: a wiki this many commits behind HEAD needs an update.
	WikiDriftCommits int `toml:"wiki_drift_commits" json:"wiki_drift_commits"`
	// RecentDays is the window for "recent" cost, sessions, and closes.
	RecentDays int      `toml:"recent_days" json:"recent_days"`
	Services   Services `toml:"services" json:"services"`
	Bin        Bin      `toml:"bin" json:"bin"`
}

// Services are the sibling tools' web UIs that goatlassian links to.
type Services struct {
	OwcliURL   string `toml:"owcli_url" json:"owcli_url"`
	BossmanURL string `toml:"bossman_url" json:"bossman_url"`
}

// Bin names or paths of the sibling tools' executables.
type Bin struct {
	Git     string `toml:"git" json:"git"`
	Kata    string `toml:"kata" json:"kata"`
	Owcli   string `toml:"owcli" json:"owcli"`
	Bossman string `toml:"bossman" json:"bossman"`
}

// Default returns the configuration used when config.toml is absent.
func Default() Config {
	return Config{
		StaleDays:        14,
		WikiDriftCommits: 10,
		RecentDays:       30,
		Services: Services{
			OwcliURL:   "http://127.0.0.1:4321",
			BossmanURL: "http://127.0.0.1:7788",
		},
		Bin: Bin{Git: "git", Kata: "kata", Owcli: "owcli", Bossman: "bossman"},
	}
}

// Home resolves the data directory: the flag, $GOATLASSIAN_HOME, or
// ~/.local/share/goatlassian.
func Home(flag string) (string, error) {
	if flag != "" {
		return filepath.Abs(flag)
	}
	if h := os.Getenv("GOATLASSIAN_HOME"); h != "" {
		return filepath.Abs(h)
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "goatlassian"), nil
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(u, ".local", "share", "goatlassian"), nil
}

// Load reads home/config.toml over the defaults.
func Load(home string) (Config, error) {
	c := Default()
	_, err := toml.DecodeFile(filepath.Join(home, "config.toml"), &c)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return c, err
	}
	d := Default()
	if c.StaleDays <= 0 {
		c.StaleDays = d.StaleDays
	}
	if c.WikiDriftCommits <= 0 {
		c.WikiDriftCommits = d.WikiDriftCommits
	}
	if c.RecentDays <= 0 {
		c.RecentDays = d.RecentDays
	}
	fill := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	fill(&c.Services.OwcliURL, d.Services.OwcliURL)
	fill(&c.Services.BossmanURL, d.Services.BossmanURL)
	fill(&c.Bin.Git, d.Bin.Git)
	fill(&c.Bin.Kata, d.Bin.Kata)
	fill(&c.Bin.Owcli, d.Bin.Owcli)
	fill(&c.Bin.Bossman, d.Bin.Bossman)
	return c, nil
}
