package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/dashboard"
)

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Open a live web-based dashboard in your browser",
	Long: `Start an embedded HTTP server and open a live web-based dashboard.

The dashboard provides a Kanban board with real-time updates, dependency graph,
ready work queue, and activity feed — all powered by the same storage backend
used by the CLI.

The server binds to localhost only and stops when you press Ctrl-C.`,
	Example: `  bd dashboard                      # Open on default port 7700
  bd dashboard --port 8080           # Custom port
  bd dashboard --no-open             # Start server without opening browser
  bd dashboard --read-only           # Disable mutations from the UI`,
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		host, _ := cmd.Flags().GetString("host")
		noOpen, _ := cmd.Flags().GetBool("no-open")
		readOnly, _ := cmd.Flags().GetBool("read-only")
		pollMs, _ := cmd.Flags().GetInt("poll-interval")
		staticDir, _ := cmd.Flags().GetString("static-dir")

		cfg := dashboard.Config{
			Port:         port,
			Host:         host,
			NoOpen:       noOpen,
			ReadOnly:     readOnly,
			PollInterval: time.Duration(pollMs) * time.Millisecond,
			StaticDir:    staticDir,
			DefaultActor: getActorWithGit(),
		}

		srv := dashboard.New(store, cfg)

		// Resolve actual address first so we can print it.
		addr := srv.ListenAddr()
		url := fmt.Sprintf("http://%s", addr)

		fmt.Fprintf(cmd.OutOrStderr(), "bd dashboard  %s\n", url)
		if noOpen {
			fmt.Fprintln(cmd.OutOrStderr(), "  (browser auto-open disabled, use --no-open to suppress this message)")
		}

		if err := srv.Run(rootCtx); err != nil {
			return fmt.Errorf("dashboard: %w", err)
		}
		return nil
	},
}

func init() {
	dashboardCmd.Flags().Int("port", 7700, "Port to listen on (auto-increments if busy)")
	dashboardCmd.Flags().String("host", "127.0.0.1", "Bind address")
	dashboardCmd.Flags().Bool("no-open", false, "Do not open browser automatically")
	dashboardCmd.Flags().Bool("read-only", false, "Disable mutations from the UI")
	dashboardCmd.Flags().Int("poll-interval", 250, "Storage poll interval in milliseconds")
	dashboardCmd.Flags().String("static-dir", "", "Dev mode: serve frontend assets from this directory on disk instead of the embedded build (e.g. internal/dashboard/static)")

	rootCmd.AddCommand(dashboardCmd)
}
