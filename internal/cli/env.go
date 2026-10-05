package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hoodoo/goatlassian/internal/services"
	"github.com/Hoodoo/goatlassian/internal/store"
	"github.com/Hoodoo/goatlassian/internal/web"
)

func (a *app) servicesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services [start]",
		Short: "Check the kata, owcli, and bossman web UIs; start the missing ones",
		Long: `services reports whether the sibling web UIs goatlassian links to are
reachable. "services start" starts the missing ones detached (kata daemon
start, owcli serve --no-open, bossman serve) with their output in the logs
directory. Their addresses come from config.toml [services].`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"start"},
		RunE: func(_ *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var st []services.Service
			var err error
			switch {
			case len(args) == 0:
				st = services.Status(ctx, a.cfg)
			case args[0] == "start":
				if st, err = services.StartMissing(ctx, a.cfg, a.home); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown action %q (want start)", args[0])
			}
			if a.json {
				return a.printJSON(st)
			}
			for _, s := range st {
				state := "up"
				if !s.Up {
					state = "down: " + s.Problem + " (start: " + s.Start + ")"
				}
				fmt.Fprintf(a.out, "%-8s %-30s %s\n", s.Name, s.URL, state)
			}
			return nil
		},
	}
	return cmd
}

func (a *app) serveCmd() *cobra.Command {
	var addr string
	var open, startServices bool
	var every time.Duration
	var opts web.Options
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the local web UI",
		Long: `serve starts the portfolio web UI on a loopback address. Pages link into
the kata, owcli, and bossman web UIs; --services starts those first if they
are down. While running, serve records a metrics snapshot every
--snapshot-every (0 disables).

Behind a reverse proxy such as Google IAP, listen where the proxy can reach
the server, accept the public name, and trust the proxy's user header; the
signed-in user is then the actor of every change made in the UI:

  goatlassian serve --addr 0.0.0.0:7799 --allow-host portfolio.example.com \
    --user-header X-Goog-Authenticated-User-Email

With --user-header, requests without that header are refused. Only use it
when nothing but the proxy can reach the address.`,
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7799", "listen address (keep it on loopback)")
	cmd.Flags().BoolVar(&open, "open", false, "open the UI in a browser")
	cmd.Flags().BoolVar(&startServices, "services", false, "start missing sibling web UIs first")
	cmd.Flags().DurationVar(&every, "snapshot-every", 6*time.Hour, "record a metrics snapshot this often")
	cmd.Flags().StringArrayVar(&opts.AllowHosts, "allow-host", nil, "also accept this name in the Host header, e.g. a proxy's public name (repeatable)")
	cmd.Flags().StringVar(&opts.UserHeader, "user-header", "", "trust this request header as the signed-in user (the actor of changes) and refuse requests without it")
	cmd.RunE = a.withStore(func(st *store.Store, _ []string) error {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return fmt.Errorf("--addr %q: %w", addr, err)
		}
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) && opts.UserHeader == "" {
			fmt.Fprintf(os.Stderr, "warning: %s is not a loopback address; anyone who can reach it can read and change your projects\n", host)
		}
		if startServices {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if _, err := services.StartMissing(ctx, a.cfg, a.home); err != nil {
				fmt.Fprintln(os.Stderr, "services:", err)
			}
			cancel()
		}
		srv := web.New(st, a.cfg, a.home, host, opts)
		if every > 0 {
			go srv.SnapshotLoop(every)
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		url := "http://" + ln.Addr().String() + "/"
		fmt.Fprintf(a.out, "goatlassian UI at %s (Ctrl-C to stop)\n", url)
		if open {
			openBrowser(url)
		}
		hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
		if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	return cmd
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
