package personalagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// api_rules.go: stage D's commands — the legacy-rule ledger.
//
//	assistant rule legacy                    dry run: reconcile, own nothing
//	assistant rule adopt <name>|--all        take dispatch ownership (explicit)
//	assistant rule rollback <name>           hand it back to the old scheduler
//	assistant rule ledger                    every ledger row

func (a *API) loadConfig() (*config.Config, error) {
	if a.Config != nil {
		return a.Config()
	}
	return config.Load()
}

func (a *API) legacyLast() map[string]time.Time {
	if a.LegacyLast != nil {
		return a.LegacyLast()
	}
	return ReadLegacyWatermarks()
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// ReadLegacyWatermarks reads the old scheduler's last-occurrence map from its
// schedule.json (the daemon package owns the file; only this one field of it
// is read, and never written).
func ReadLegacyWatermarks() map[string]time.Time {
	out := map[string]time.Time{}
	raw, err := os.ReadFile(filepath.Join(config.StateDir(), "schedule.json"))
	if err != nil {
		return out
	}
	var s struct {
		Last map[string]time.Time `json:"last_occurrence"`
	}
	if json.Unmarshal(raw, &s) == nil {
		for k, v := range s.Last {
			out[k] = v
		}
	}
	return out
}

// backupDefinitions keeps one copy of automations.yaml from before the first
// adoption, so a person can always get the old file back byte for byte.
func backupDefinitions() {
	src := config.Path()
	dst := src + ".pre-assistants.bak"
	if _, err := os.Stat(dst); err == nil {
		return
	}
	if raw, err := os.ReadFile(src); err == nil {
		_ = os.WriteFile(dst, raw, 0o600)
	}
}

func (a *API) rule(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant rule <legacy|adopt|rollback|ledger>")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "legacy":
		cfg, err := a.loadConfig()
		if err != nil {
			return nil, coded("invalid", "automations.yaml: %v", err)
		}
		c, err := a.Store.DryRunLegacy(cfg, a.legacyLast(), a.now())
		return ok("candidates", c), err
	case "ledger":
		l, err := a.Store.LegacyLedger()
		return ok("ledger", l), err
	case "adopt":
		var in common
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		cfg, err := a.loadConfig()
		if err != nil {
			return nil, coded("invalid", "automations.yaml: %v", err)
		}
		names := rest
		if len(rest) == 1 && rest[0] == "--all" {
			names = nil
			for _, x := range cfg.Automations {
				if okm, _ := migratable(x, cfg.Backend); okm {
					names = append(names, x.Name)
				}
			}
		}
		if len(names) == 0 {
			return nil, coded("invalid", "usage: assistant rule adopt <name>|--all")
		}
		backupDefinitions()
		last := a.legacyLast()
		out := []LegacyRule{}
		replayed := false
		for _, n := range names {
			// One request id, one rule: a batch derives a stable id per name.
			rid := in.RequestID
			if len(names) > 1 && rid != "" {
				rid += ":" + n
			}
			row, rep, err := a.Store.AdoptLegacy(rid, n, cfg, last, a.now())
			if err != nil {
				return nil, err
			}
			replayed = replayed || rep
			out = append(out, row)
		}
		return ok("rules", out, "replayed", replayed), nil
	case "rollback":
		var in common
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant rule rollback <name>")
		}
		row, replayed, err := a.Store.RollbackLegacy(in.RequestID, rest[0])
		return ok("rule", row, "replayed", replayed), err
	}
	return nil, coded("invalid", "unknown rule command %q", cmd)
}
