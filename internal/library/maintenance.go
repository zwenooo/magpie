package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/steady"
)

// MaintenanceLocation separates update provenance from the local directory
// the maintainer explicitly chose. Only WritePath, when requested, is a file.
type MaintenanceLocation struct {
	Kind            string         `json:"kind"`
	Name            string         `json:"name"`
	Mode            string         `json:"mode"`
	MaintenancePath string         `json:"maintenancePath"`
	RepoSource      *MCPRepoSource `json:"repoSource"`
	WritePath       string         `json:"writePath,omitempty"`
}

type maintenanceItem struct {
	installed    string
	libraryOwned bool
	repo         *MCPRepoSource
}

func localMaintenanceItem(kind, name string) (maintenanceItem, error) {
	var item maintenanceItem
	if err := checkName(kind, name); err != nil {
		return item, err
	}
	if !filepath.IsLocal(name) {
		return item, fmt.Errorf("invalid item name %q", name)
	}
	lib, err := load()
	if err != nil {
		return item, err
	}
	switch kind {
	case "skill":
		s := lib.skill(name)
		if s == nil {
			return item, fmt.Errorf("no library skill called %s", name)
		}
		item.installed = skillDir(name)
		item.libraryOwned = s.Bundle == "" && s.Source == nil
		if src := s.Source; src != nil && (src.Kind == "git" || src.Kind == "github") {
			item.repo = &MCPRepoSource{Repo: src.Repo, Ref: src.Ref, Path: src.Path}
		}
	case "mcp":
		s := lib.server(name)
		if s == nil {
			return item, fmt.Errorf("no library MCP called %s", name)
		}
		item.installed = programDir(name)
		item.repo = s.RepoSource
		item.libraryOwned = s.Transport == "stdio" && s.names(programRef) && s.RepoSource == nil && s.Bundle == "" && s.Plugin == ""
	default:
		return item, errors.New("item kind must be skill or mcp")
	}
	return item, nil
}

func (item maintenanceItem) root(mode, path string) (string, error) {
	// Trust the configured Magpie directory, not links below it to another
	// skill, an installed snapshot, private data or a different checkout.
	config, err := maintenanceDir(settings.Dir())
	if err != nil {
		return "", err
	}
	switch mode {
	case "library":
		if !item.libraryOwned {
			return "", errors.New("item has no independently editable library directory; register a checkout")
		}
		rel, err := filepath.Rel(settings.Dir(), item.installed)
		if err != nil {
			return "", err
		}
		root, err := maintenanceDir(item.installed)
		if err != nil {
			return "", err
		}
		if !sameDir(root, filepath.Join(config, rel)) {
			return "", errors.New("library source directory is redirected")
		}
		if path != "" {
			supplied, err := maintenanceDir(path)
			if err != nil {
				return "", err
			}
			if !sameDir(supplied, root) {
				return "", errors.New("library path must match the item's library directory")
			}
		}
		return root, nil
	case "checkout":
		if path == "" {
			return "", errors.New("checkout requires a path")
		}
		if err := maintenancePathOK(path); err != nil {
			return "", err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		root, err := maintenanceDir(abs)
		if err != nil {
			return "", err
		}
		if within(abs, settings.Dir()) || within(root, config) || within(config, root) {
			return "", errors.New("checkout must be separate from the Magpie configuration and installed files")
		}
		if err := maintenanceCheckout(root); err != nil {
			return "", err
		}
		return root, nil
	default:
		return "", errors.New("maintenance mode must be library or checkout")
	}
}

func maintenanceFile() string { return filepath.Join(settings.Dir(), "library-maintainers.json") }

func maintenanceEntries() (map[string]MaintenanceLocation, error) {
	file := maintenanceFile()
	st, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]MaintenanceLocation{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a local registration file: %s", file)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var entries map[string]MaintenanceLocation
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if entries == nil {
		return nil, fmt.Errorf("invalid null registration file: %s", file)
	}
	return entries, nil
}

// Maintain registers a source on this computer only; it does not call Sync.
func Maintain(kind, name, mode, path string) (*MaintenanceLocation, error) {
	item, err := localMaintenanceItem(kind, name)
	if err != nil {
		return nil, err
	}
	root, err := item.root(mode, path)
	if err != nil {
		return nil, err
	}
	v := MaintenanceLocation{Kind: kind, Name: name, Mode: mode, MaintenancePath: root, RepoSource: item.repo}
	file := maintenanceFile()
	unlock, held := filelock.Lock(file+".lock", filelock.Wait)
	if !held {
		return nil, errors.New("cannot lock local maintenance records")
	}
	defer unlock()
	entries, err := maintenanceEntries()
	if err != nil {
		return nil, err
	}
	entries[kind+"/"+name] = v
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := steady.WriteFile(file, append(b, '\n'), 0600); err != nil {
		return nil, err
	}
	return &v, nil
}

// Where revalidates both the registration and an optional spelling of the
// file the caller intends to write. It does not create or modify that file.
func Where(kind, name, request string) (*MaintenanceLocation, error) {
	item, err := localMaintenanceItem(kind, name)
	if err != nil {
		return nil, err
	}
	entries, err := maintenanceEntries()
	if err != nil {
		return nil, err
	}
	v, ok := entries[kind+"/"+name]
	if !ok {
		return nil, fmt.Errorf("no local maintenance registration for %s %s", kind, name)
	}
	if v.Kind != kind || v.Name != name || !filepath.IsAbs(v.MaintenancePath) || v.WritePath != "" {
		return nil, errors.New("invalid maintenance registration")
	}
	if (v.RepoSource == nil) != (item.repo == nil) || (item.repo != nil && *v.RepoSource != *item.repo) {
		return nil, errors.New("repository source changed; register the maintenance directory again")
	}
	root, err := item.root(v.Mode, v.MaintenancePath)
	if err != nil {
		return nil, err
	}
	if !sameDir(root, v.MaintenancePath) {
		return nil, errors.New("registered directory has been redirected")
	}
	if request != "" {
		v.WritePath, err = maintenanceWritePath(root, request)
		if err != nil {
			return nil, err
		}
	}
	return &v, nil
}
