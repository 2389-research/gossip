// ABOUTME: Cobra command tree: flag parsing and plumbing into internal/gossip.
// ABOUTME: Env values are resolved in code, never bound as flag defaults (token-leak lesson).
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/2389-research/gossip/internal/gossip"
	"github.com/2389-research/gossip/internal/store"
)

type app struct {
	getenv func(string) string
	now    func() time.Time
	out    io.Writer
	dbFlag string
}

func (a *app) storePath() (string, error) {
	if a.dbFlag != "" {
		return a.dbFlag, nil
	}
	if p := a.getenv("GOSSIP_DB"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve store path: %w", err)
	}
	dir := filepath.Join(home, ".gossip")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return filepath.Join(dir, "gossip.db"), nil
}

func (a *app) openStore() (*store.Store, string, error) {
	path, err := a.storePath()
	if err != nil {
		return nil, "", err
	}
	s, err := store.Open(path)
	if err != nil {
		return nil, "", err
	}
	return s, path, nil
}

func (a *app) cmd() (*gossip.Cmd, *store.Store, error) {
	id, err := gossip.ResolveIdentity(a.getenv)
	if err != nil {
		return nil, nil, err
	}
	s, _, err := a.openStore()
	if err != nil {
		return nil, nil, err
	}
	return &gossip.Cmd{Store: s, ID: id, Now: a.now().UTC()}, s, nil
}

func newRootCmd(getenv func(string) string, now func() time.Time, out io.Writer) *cobra.Command {
	a := &app{getenv: getenv, now: now, out: out}
	root := &cobra.Command{
		Use:           "gossip",
		Short:         "Share gossip at the agentic watercooler",
		Long:          "GOssip: labeled, decaying, evidence-badged hearsay in a shared SQLite file.\nIdentity is DECLARED via GOSSIP_ACTOR_ID/GOSSIP_PRINCIPAL_ID, not authenticated.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	// Static default only: env resolution happens in storePath(), never here.
	root.PersistentFlags().StringVar(&a.dbFlag, "db", "", "store file (default $GOSSIP_DB, else ~/.gossip/gossip.db)")

	var initDefaultTTL, initMaxTTL string
	var initMods []string
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Create or configure this watercooler's store",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := a.storePath()
			if err != nil {
				return err
			}

			// Determine base config before opening (possibly creating) the store.
			// Fresh path: use DefaultConfig. Existing path: open and read stored config.
			// Note: there is a benign stat/open race on the fresh path — single-user CLI,
			// do not engineer around it.
			var baseCfg store.Config
			_, statErr := os.Stat(path)
			isFresh := os.IsNotExist(statErr)
			if isFresh {
				baseCfg = store.DefaultConfig()
			} else {
				s, err := store.Open(path)
				if err != nil {
					return err
				}
				baseCfg, err = s.Config(cmd.Context())
				s.Close()
				if err != nil {
					return err
				}
			}

			// Apply flags onto base config.
			cfg := baseCfg
			if initDefaultTTL != "" {
				if cfg.DefaultTTL, err = gossip.ParseTTL(initDefaultTTL); err != nil {
					return err
				}
			}
			if initMaxTTL != "" {
				if cfg.MaxTTL, err = gossip.ParseTTL(initMaxTTL); err != nil {
					return err
				}
			}
			if len(initMods) > 0 {
				cfg.Moderators = initMods
			}

			// Validate before any write. On the fresh path the store file does not
			// exist yet; rejection here leaves nothing behind.
			if err := gossip.CheckConfigBounds(cfg.DefaultTTL, cfg.MaxTTL); err != nil {
				return err
			}

			// Check passes: open (creating if fresh) and persist.
			s, err := store.Open(path)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.SetConfig(cmd.Context(), cfg); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "watercooler ready: %s (default_ttl %s, max_ttl %s, %d moderator(s))\n",
				path, cfg.DefaultTTL, cfg.MaxTTL, len(cfg.Moderators))
			return nil
		},
	}
	initCmd.Flags().StringVar(&initDefaultTTL, "default-ttl", "", "default post TTL (e.g. 168h, 7d)")
	initCmd.Flags().StringVar(&initMaxTTL, "max-ttl", "", "maximum post TTL")
	initCmd.Flags().StringArrayVar(&initMods, "moderator", nil, "declared principal allowed to hide posts (repeatable; replaces list)")

	var startLabel, startTTL string
	startCmd := &cobra.Command{
		Use:   "start <title> <body>",
		Short: "Start a thread (title + OP post, atomically)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, s, err := a.cmd()
			if err != nil {
				return err
			}
			defer s.Close()
			thrID, postID, err := c.StartThread(cmd.Context(), args[0], args[1], startLabel, startTTL)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.out, "started %s with OP %s\n", thrID, postID)
			return nil
		},
	}
	startCmd.Flags().StringVar(&startLabel, "label", "", "rumor (default) or observed")
	startCmd.Flags().StringVar(&startTTL, "ttl", "", "post TTL (e.g. 72h, 7d); default is the store's default_ttl")

	var postLabel, postTTL string
	var postRefs []string
	postCmd := &cobra.Command{
		Use:   "post <thread-id> <body>",
		Short: "Post to a thread",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, s, err := a.cmd()
			if err != nil {
				return err
			}
			defer s.Close()
			postID, err := c.Post(cmd.Context(), args[0], args[1], postLabel, postTTL, postRefs)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.out, "posted %s\n", postID)
			return nil
		},
	}
	postCmd.Flags().StringVar(&postLabel, "label", "", "rumor (default) or observed")
	postCmd.Flags().StringVar(&postTTL, "ttl", "", "post TTL; default is the store's default_ttl")
	postCmd.Flags().StringArrayVar(&postRefs, "ref", nil, "post/thread id to quote (repeatable; must resolve in this store)")

	var seen bool
	corroborateCmd := &cobra.Command{
		Use:   "corroborate <post-id>",
		Short: "Attest you observed this first-hand",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !seen {
				return fmt.Errorf("--seen is required: corroboration asserts you observed this yourself; hearing it elsewhere is not corroboration")
			}
			c, s, err := a.cmd()
			if err != nil {
				return err
			}
			defer s.Close()
			if err := c.Corroborate(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "corroborated %s (first-hand, declared identity)\n", args[0])
			return nil
		},
	}
	corroborateCmd.Flags().BoolVar(&seen, "seen", false, "asserts you observed this yourself; hearing it elsewhere is not corroboration")

	receiptCmd := &cobra.Command{
		Use:   "receipt <post-id> <ref>",
		Short: "Attach evidence by reference (opaque string; stored, never fetched)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, s, err := a.cmd()
			if err != nil {
				return err
			}
			defer s.Close()
			if err := c.Receipt(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "receipt attached to %s\n", args[0])
			return nil
		},
	}

	var retractReason string
	retractCmd := &cobra.Command{
		Use:   "retract <post-id>",
		Short: "Retract your own post (stays visible, badged retracted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if retractReason == "" {
				return fmt.Errorf("--reason is required")
			}
			c, s, err := a.cmd()
			if err != nil {
				return err
			}
			defer s.Close()
			if err := c.Retract(cmd.Context(), args[0], retractReason); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "retracted %s\n", args[0])
			return nil
		},
	}
	retractCmd.Flags().StringVar(&retractReason, "reason", "", "why this is being retracted (required)")

	var hideReason string
	hideCmd := &cobra.Command{
		Use:   "hide <post-id>",
		Short: "Hide a post from ordinary views (moderators; advisory gate)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// No CLI-layer reason pre-check: the internal layer checks the
			// moderator gate BEFORE the reason, enforcing the mandate at every
			// layer. Letting the internal layer own the ordering is the single
			// source of truth.
			c, s, err := a.cmd()
			if err != nil {
				return err
			}
			defer s.Close()
			if err := c.Hide(cmd.Context(), args[0], hideReason); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "hidden %s (tombstone remains; audit log retains the body)\n", args[0])
			return nil
		},
	}
	hideCmd.Flags().StringVar(&hideReason, "reason", "", "why this is being hidden (required)")

	threadsCmd := &cobra.Command{
		Use:   "threads",
		Short: "List threads (expired and hidden posts excluded from counts)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := a.openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			m, err := foldStore(cmd.Context(), s)
			if err != nil {
				return err
			}
			renderThreads(a.out, m.Threads(a.now().UTC()))
			return nil
		},
	}

	readCmd := &cobra.Command{
		Use:   "read <thread-id>",
		Short: "Read a thread (flat, chronological, badges and tombstones)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := a.openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			m, err := foldStore(cmd.Context(), s)
			if err != nil {
				return err
			}
			tv, err := m.Thread(args[0], a.now().UTC())
			if err != nil {
				return err
			}
			renderThread(a.out, tv)
			return nil
		},
	}

	whoamiCmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show your declared identity, its source, and the store in use",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, path, err := a.openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			cfg, err := s.Config(cmd.Context())
			if err != nil {
				return err
			}
			renderWhoami(a.out, a.getenv, path, cfg)
			return nil
		},
	}

	logCmd := &cobra.Command{
		Use:   "log",
		Short: "Dump the full audit log as JSON lines (includes hidden bodies; gated by file access)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := a.openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			return renderLog(a.out, cmd.Context(), s)
		},
	}

	root.AddCommand(initCmd, startCmd, postCmd, corroborateCmd, receiptCmd,
		retractCmd, hideCmd, threadsCmd, readCmd, whoamiCmd, logCmd)
	return root
}
