package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/yithcai/flow/internal/agentsession"
	"github.com/yithcai/flow/internal/errs"
)

func init() {
	var asJSON bool
	root := &cobra.Command{Use: "agent-session", Short: "Associate TAPD requirements with Codex sessions and fork trees"}
	root.PersistentFlags().BoolVar(&asJSON, "json", false, "print structured JSON")
	rootCmd.AddCommand(root)
	service := func() (*agentsession.Service, error) {
		s, e := loadStore()
		if e != nil {
			return nil, e
		}
		return agentsession.New(s), nil
	}
	output := func(v any) error { enc := json.NewEncoder(os.Stdout); enc.SetIndent("", "  "); return enc.Encode(v) }
	wrap := func(fn func(*cobra.Command, []string, *agentsession.Service) error) func(*cobra.Command, []string) error {
		return func(c *cobra.Command, args []string) error {
			s, e := service()
			if e != nil {
				return e
			}
			if e = fn(c, args, s); e != nil {
				return errs.UserWrap(e, "agent-session operation failed", "run flow agent-session --help; use full IDs when prefixes are ambiguous")
			}
			return nil
		}
	}
	tapd := &cobra.Command{Use: "tapd", Short: "Manage TAPD requirement links"}
	root.AddCommand(tapd)
	for _, verb := range []string{"add", "edit"} {
		verb := verb
		var title, notes, rawURL string
		use := verb + " <url>"
		if verb == "edit" {
			use = verb + " <requirement-id>"
		}
		c := &cobra.Command{Use: use, Args: cobra.ExactArgs(1), RunE: wrap(func(c *cobra.Command, args []string, s *agentsession.Service) error {
			id := ""
			u := args[0]
			if verb == "edit" {
				id = args[0]
				cfg, e := s.Store.Load()
				if e != nil {
					return e
				}
				old := agentsession.FindRequirement(cfg, id)
				if old == nil {
					return fmt.Errorf("requirement not found")
				}
				u = old.URL
				if c.Flags().Changed("url") {
					u = rawURL
				}
				if !c.Flags().Changed("title") {
					title = old.Title
				}
				if !c.Flags().Changed("notes") {
					notes = old.Notes
				}
			}
			v, e := s.SaveRequirement(id, u, title, notes)
			if e != nil {
				return e
			}
			if asJSON {
				return output(v)
			}
			fmt.Printf("%s  %s  %s\n", v.ID, v.Title, v.URL)
			return nil
		})}
		c.Flags().StringVar(&title, "title", "", "display title")
		c.Flags().StringVar(&notes, "notes", "", "requirement notes")
		if verb == "edit" {
			c.Flags().StringVar(&rawURL, "url", "", "updated TAPD URL")
		}
		tapd.AddCommand(c)
	}
	tapd.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: wrap(func(c *cobra.Command, args []string, s *agentsession.Service) error {
		v, e := s.Snapshot()
		if e != nil {
			return e
		}
		if asJSON {
			return output(v.Requirements)
		}
		rows := [][]string{}
		for _, r := range v.Requirements {
			rows = append(rows, []string{r.ID, r.Title, r.URL})
		}
		printTable([]string{"ID", "TITLE", "URL"}, rows)
		return nil
	})}, &cobra.Command{Use: "rm <requirement-id>", Args: cobra.ExactArgs(1), RunE: wrap(func(c *cobra.Command, args []string, s *agentsession.Service) error {
		return s.DeleteRequirement(args[0])
	})})
	var filter string
	var local bool
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: wrap(func(c *cobra.Command, args []string, s *agentsession.Service) error {
		v, e := s.Snapshot()
		if e != nil {
			return e
		}
		sessions := v.Sessions
		if local {
			if filter != "" {
				return fmt.Errorf("--local and --tapd cannot be combined")
			}
			sessions, e = s.Discover(c.Context(), false)
			if e != nil {
				return e
			}
		}
		if filter != "" {
			found := false
			sessions = sessions[:0:0]
			for _, t := range v.Trees {
				if t.Requirement.ID == filter {
					found = true
					for _, n := range t.Nodes {
						if !n.ContextOnly {
							sessions = append(sessions, n.AgentSession)
						}
					}
				}
			}
			if !found {
				return fmt.Errorf("requirement not found")
			}
		}
		if asJSON {
			return output(sessions)
		}
		rows := [][]string{}
		for _, x := range sessions {
			title := x.Label
			if title == "" {
				title = x.Title
			}
			rows = append(rows, []string{x.ID, title, x.ForkedFromID, x.Cwd, fmt.Sprint(x.Available)})
		}
		printTable([]string{"ID", "TITLE", "FORKED FROM", "CWD", "AVAILABLE"}, rows)
		return nil
	})}
	list.Flags().StringVar(&filter, "tapd", "", "filter by requirement ID")
	list.Flags().BoolVar(&local, "local", false, "discover local Codex sessions")
	root.AddCommand(list)
	root.AddCommand(&cobra.Command{Use: "show <session-id-or-prefix>", Args: cobra.ExactArgs(1), RunE: wrap(func(c *cobra.Command, args []string, s *agentsession.Service) error {
		v, e := s.Snapshot()
		if e != nil {
			return e
		}
		id, e := agentsession.Resolve(args[0], v.Sessions)
		if e != nil {
			return e
		}
		for _, x := range v.Sessions {
			if x.ID == id {
				return output(x)
			}
		}
		return fmt.Errorf("session not found")
	})})
	for _, verb := range []string{"link", "unlink"} {
		verb := verb
		var requirement string
		c := &cobra.Command{Use: verb + " <session-id-or-prefix>", Args: cobra.ExactArgs(1), RunE: wrap(func(c *cobra.Command, args []string, s *agentsession.Service) error {
			if verb == "unlink" {
				return s.Unlink(requirement, args[0])
			}
			id, e := s.Link(c.Context(), requirement, args[0])
			if e != nil {
				return e
			}
			if asJSON {
				return output(map[string]string{"id": id})
			}
			fmt.Println(id)
			return nil
		})}
		c.Flags().StringVar(&requirement, "tapd", "", "requirement ID")
		_ = c.MarkFlagRequired("tapd")
		root.AddCommand(c)
	}
	var requestID string
	fork := &cobra.Command{Use: "fork <session-id-or-prefix>", Args: cobra.ExactArgs(1), RunE: wrap(func(c *cobra.Command, args []string, s *agentsession.Service) error {
		if requestID == "" {
			requestID = agentsession.NewID()
		}
		v, e := s.Fork(c.Context(), args[0], requestID)
		if asJSON {
			_ = output(v)
		} else {
			fmt.Printf("Request: %s (%s)\n", requestID, v.State)
			if v.Session != nil {
				fmt.Println("codex resume " + v.Session.ID)
			}
		}
		return e
	})}
	fork.Flags().StringVar(&requestID, "request-id", "", "reuse an existing fork request UUID to recover a recorded result")
	root.AddCommand(fork)
	root.AddCommand(&cobra.Command{Use: "sync", Args: cobra.NoArgs, RunE: wrap(func(c *cobra.Command, args []string, s *agentsession.Service) error {
		if e := s.Sync(c.Context()); e != nil {
			return e
		}
		v, e := s.Snapshot()
		if e != nil {
			return e
		}
		if asJSON {
			return output(v)
		}
		fmt.Printf("Synchronized %d tracked sessions\n", len(v.Sessions))
		return nil
	})}, &cobra.Command{Use: "operations", Short: "Inspect recoverable or uncertain fork operations", Args: cobra.NoArgs, RunE: wrap(func(c *cobra.Command, args []string, s *agentsession.Service) error {
		v, e := s.Operations()
		if e != nil {
			return e
		}
		return output(v)
	})})
}
