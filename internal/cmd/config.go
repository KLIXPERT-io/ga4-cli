package cmd

import (
	"github.com/KLIXPERT-io/ga4-cli/internal/config"
	"github.com/KLIXPERT-io/ga4-cli/internal/errs"
	"github.com/KLIXPERT-io/ga4-cli/internal/output"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	c := &cobra.Command{Use: "config", Short: "Read/write the ga4 config file"}
	c.AddCommand(newConfigGetCmd(), newConfigSetCmd(), newConfigPathCmd(), newConfigListCmd())
	return c
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Read a config value by dotted key (e.g. defaults.property)",
		Long: `Examples:
  ga4 config get defaults.property
  ga4 config get cache.dir`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			val, ok := cfg.Get(args[0])
			if !ok {
				return errs.New(errs.CodeInvalidArgs, "unknown key: "+args[0]).WithHint("Try `ga4 config list`.")
			}
			return emit(cmd, map[string]any{"key": args[0], "value": val}, output.Meta{}, nil, nil)
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Write a config value",
		Long: `Examples:
  ga4 config set auth.service_account_path ~/secrets/ga4-sa.json
  ga4 config set defaults.property properties/123456789
  ga4 config set defaults.range last-7d
  ga4 config set logging.format json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			// A default property that cannot be resolved would fail every later
			// command with a confusing error, so validate it at write time.
			if args[0] == "defaults.property" && args[1] != "" {
				normalized, err := normalizeProperty(args[1])
				if err != nil {
					return err
				}
				args[1] = normalized
			}
			if err := cfg.Set(args[0], args[1]); err != nil {
				return errs.New(errs.CodeInvalidArgs, err.Error())
			}
			return emit(cmd, map[string]any{"ok": true, "key": args[0], "value": args[1]}, output.Meta{}, nil, nil)
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the path of the config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := config.Path()
			if err != nil {
				return err
			}
			return emit(cmd, map[string]any{"path": p}, output.Meta{}, nil, nil)
		},
	}
}

func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all known config keys and current values",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			out := map[string]any{}
			cols := []string{"key", "value"}
			rows := []output.Row{}
			for _, k := range config.Keys() {
				v, _ := cfg.Get(k)
				out[k] = v
				rows = append(rows, output.Row{"key": k, "value": v})
			}
			return emit(cmd, out, output.Meta{}, cols, rows)
		},
	}
}
