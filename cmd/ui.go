package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yithcai/flow/internal/errs"
	"github.com/yithcai/flow/internal/ui"
)

func init() {
	var addr string
	var noOpen bool

	uiCmd := &cobra.Command{
		Use:   "ui",
		Short: "Start a local web UI to manage projects/scripts/cmds/greps/finds/schedules",
		Long: `Starts a small HTTP server (loopback only) and serves a single
HTML page that lets you browse, edit and run all flow resources from
your browser.

Hit Ctrl-C to stop the server. The config file is the same one used by
all other flow commands; changes made in the UI are visible immediately
on the CLI and vice versa.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			srv, err := ui.New(s, addr)
			if err != nil {
				return errs.User(err.Error(),
					"use a loopback address, e.g. 127.0.0.1:7777")
			}

			// Bind first so if the port is busy we fail before forking the browser.
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return errs.User(
					fmt.Sprintf("could not listen on %s: %v", addr, err),
					"pick another port:  flow ui --addr 127.0.0.1:7788",
				)
			}
			http.DefaultClient.Timeout = 0

			httpSrv := &http.Server{
				Handler:           srv.Handler(),
				ReadHeaderTimeout: 10 * time.Second,
				// No Write/Idle timeout on purpose: run-stream (SSE) responses
				// are intentionally long-lived.
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go func() {
				sigs := make(chan os.Signal, 1)
				signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
				<-sigs
				fmt.Fprintln(os.Stderr, "\nshutting down…")
				ctx2, c2 := context.WithTimeout(context.Background(), 2*time.Second)
				defer c2()
				_ = httpSrv.Shutdown(ctx2)
				cancel()
			}()

			url := "http://" + addr + "/"
			fmt.Printf("flow UI → %s   (config: %s)\n", url, s.Path)
			fmt.Println("press Ctrl-C to stop")
			if !noOpen {
				_ = openBrowser(url)
			}
			if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
				return errs.System("ui server failed", err)
			}
			<-ctx.Done()
			return nil
		},
	}
	uiCmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7777", "loopback address to bind")
	uiCmd.Flags().BoolVar(&noOpen, "no-open", false, "do not auto-open the browser")
	rootCmd.AddCommand(uiCmd)
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	c := exec.Command(cmd, args...)
	c.Stdout = nil
	c.Stderr = nil
	return c.Start()
}
