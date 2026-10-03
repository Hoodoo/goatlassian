// Package services checks whether the sibling tools' web UIs are up and
// starts the missing ones as detached background processes.
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Hoodoo/goatlassian/internal/config"
)

// Service is one sibling web UI.
type Service struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Up      bool   `json:"up"`
	Problem string `json:"problem,omitempty"`
	Start   string `json:"start"` // the command that starts it
}

var client = &http.Client{Timeout: 1500 * time.Millisecond}

func reachable(u string) error {
	resp, err := client.Get(u)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// KataWebURL asks the kata CLI for the daemon's web address.
func KataWebURL(ctx context.Context, bin string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, "daemon", "status", "--json").Output()
	if err != nil {
		return "", err
	}
	var st struct {
		Daemons []struct {
			WebURL string `json:"web_url"`
		} `json:"daemons"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return "", err
	}
	if len(st.Daemons) == 0 || st.Daemons[0].WebURL == "" {
		return "", fmt.Errorf("no kata daemon running")
	}
	return st.Daemons[0].WebURL, nil
}

// Status checks every sibling UI.
func Status(ctx context.Context, cfg config.Config) []Service {
	kata := Service{Name: "kata", Start: cfg.Bin.Kata + " daemon start"}
	if u, err := KataWebURL(ctx, cfg.Bin.Kata); err != nil {
		kata.Problem = err.Error()
	} else {
		kata.URL = u + "/kata"
		if err := reachable(u); err != nil {
			kata.Problem = err.Error()
		} else {
			kata.Up = true
		}
	}
	owcli := Service{Name: "owcli", URL: cfg.Services.OwcliURL, Start: fmt.Sprintf("%s serve --no-open --port %s", cfg.Bin.Owcli, port(cfg.Services.OwcliURL))}
	if err := reachable(cfg.Services.OwcliURL + "/api/wikis"); err != nil {
		owcli.Problem = err.Error()
	} else {
		owcli.Up = true
	}
	bossman := Service{Name: "bossman", URL: cfg.Services.BossmanURL, Start: fmt.Sprintf("%s serve --addr %s", cfg.Bin.Bossman, hostPort(cfg.Services.BossmanURL))}
	if err := reachable(cfg.Services.BossmanURL + "/api/facets"); err != nil {
		bossman.Problem = err.Error()
	} else {
		bossman.Up = true
	}
	return []Service{kata, owcli, bossman}
}

func hostPort(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Host
}

func port(raw string) string {
	_, p, err := net.SplitHostPort(hostPort(raw))
	if err != nil {
		return "80"
	}
	return p
}

// StartMissing starts every service that is down. Viewers run detached
// with their output in home/logs, so they outlive goatlassian.
func StartMissing(ctx context.Context, cfg config.Config, home string) ([]Service, error) {
	logs := filepath.Join(home, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return nil, err
	}
	for _, s := range Status(ctx, cfg) {
		if s.Up {
			continue
		}
		var args []string
		var bin string
		switch s.Name {
		case "kata":
			bin, args = cfg.Bin.Kata, []string{"daemon", "start"}
		case "owcli":
			bin, args = cfg.Bin.Owcli, []string{"serve", "--no-open", "--port", port(cfg.Services.OwcliURL)}
		case "bossman":
			bin, args = cfg.Bin.Bossman, []string{"serve", "--addr", hostPort(cfg.Services.BossmanURL)}
		}
		if err := spawn(bin, args, home, filepath.Join(logs, s.Name+".log")); err != nil {
			return nil, fmt.Errorf("start %s: %w", s.Name, err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := Status(ctx, cfg)
		all := true
		for _, s := range st {
			all = all && s.Up
		}
		if all || time.Now().After(deadline) {
			return st, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func spawn(bin string, args []string, dir, logPath string) error {
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer log.Close()
	fmt.Fprintf(log, "--- %s %s %v\n", time.Now().Format(time.RFC3339), bin, args)
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
