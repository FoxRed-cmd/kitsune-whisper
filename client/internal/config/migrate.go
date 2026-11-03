package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Migration reports what MigrateFile changed in one YAML document.
type Migration struct {
	// Path is the file that was read.
	Path string
	// Written is false when the file already had every key.
	Written bool
	// Added lists the field paths the migration inserted, sorted.
	Added []string
	// Warnings lists deprecated keys that remain in the file.
	Warnings []string
	// BackupPath is the original kept next to the file, or "".
	BackupPath string
}

// migrationKeys are the Client keys introduced after the first public
// configuration. They are added with their built-in defaults; every other
// missing key keeps being resolved at read time, so a user file stays short.
var migrationKeys = []string{
	"hotkey_backend",
	"cancel_hotkey",
	"ui_language",
	"feedback.indicator_anchor",
	"feedback.indicator_offset_x",
	"feedback.indicator_offset_y",
}

// MigrateFile adds the Server-profile keys and the new Client defaults to an
// existing kitsune.yaml without overwriting any user value. The legacy
// single-client.server_url becomes one active profile. Comments and the
// "server:" section are preserved.
//
// A missing file is not an error: there is nothing to migrate.
func MigrateFile(path string) (Migration, error) {
	migration := Migration{Path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return migration, nil
	}
	if err != nil {
		return migration, fmt.Errorf("config: read %s: %w", path, err)
	}
	var document yaml.Node
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &document); err != nil {
			return migration, fmt.Errorf("config: invalid YAML in %s: %w", path, err)
		}
	}
	if document.Kind == 0 {
		document = yaml.Node{Kind: yaml.DocumentNode}
	}
	if len(document.Content) == 0 {
		document.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return migration, fmt.Errorf("config: %s: top-level document must be a mapping", path)
	}
	client := mappingChild(root, "client", true)
	if client == nil {
		return migration, fmt.Errorf("config: %s: 'client' must be a mapping", path)
	}
	var section map[string]any
	if err := client.Decode(&section); err != nil {
		return migration, fmt.Errorf("config: %s: 'client' must be a mapping", path)
	}
	if section == nil {
		section = map[string]any{}
	}

	additions := map[string]any{}
	defaults, err := defaultsMap()
	if err != nil {
		return migration, fmt.Errorf("config: %w", err)
	}
	// The profile list is computed before the other defaults so a legacy
	// server_url becomes exactly one active profile, as Resolve does in memory.
	migrated := migrateServers(section)
	if _, ok := section["servers"]; !ok {
		if servers, present := migrated["servers"]; present {
			additions["servers"] = servers
		} else if servers, present := defaults["servers"]; present {
			additions["servers"] = servers
		}
	}
	if _, ok := section["active_server"]; !ok {
		if active, present := migrated["active_server"]; present {
			additions["active_server"] = active
		} else if _, serversPresent := section["servers"]; !serversPresent {
			// A fresh section gets the Local Server selection. An existing but
			// invalid profile list is left for validation to report.
			if active, present := defaults["active_server"]; present {
				additions["active_server"] = active
			}
		}
	}
	for _, key := range migrationKeys {
		if _, present := flattenedValue(section, key); present {
			continue
		}
		if value, present := flattenedValue(defaults, key); present {
			additions[key] = value
		}
	}
	if len(additions) == 0 {
		migration.Warnings = DeprecationWarnings(section)
		return migration, nil
	}

	keys := make([]string, 0, len(additions))
	for key := range additions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := setNodePath(client, strings.Split(key, "."), additions[key]); err != nil {
			return migration, fmt.Errorf("config: %s: %w", path, err)
		}
		migration.Added = append(migration.Added, "client."+key)
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return migration, fmt.Errorf("config: encode %s: %w", path, err)
	}
	if err := encoder.Close(); err != nil {
		return migration, fmt.Errorf("config: encode %s: %w", path, err)
	}
	perm := fs.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	backup := path + ".bak"
	if err := os.WriteFile(backup, raw, perm); err != nil {
		return migration, fmt.Errorf("config: write backup %s: %w", backup, err)
	}
	if err := writeConfigAtomic(path, buffer.Bytes(), perm); err != nil {
		return migration, err
	}
	migration.Written = true
	migration.BackupPath = backup
	migration.Warnings = DeprecationWarnings(section)
	return migration, nil
}

// mappingChild returns the value node of key inside a mapping, creating it as
// an empty mapping when create is true. It returns nil when the existing value
// is not a mapping.
func mappingChild(node *yaml.Node, key string, create bool) *yaml.Node {
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value != key {
			continue
		}
		child := node.Content[index+1]
		if child.Kind != yaml.MappingNode {
			return nil
		}
		return child
	}
	if !create {
		return nil
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		child,
	)
	return child
}

// flattenedValue looks up a dotted path inside a nested map.
func flattenedValue(section map[string]any, path string) (any, bool) {
	var current any = section
	for _, part := range strings.Split(path, ".") {
		group, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = group[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// setNodePath inserts value at a dotted key path, creating missing groups.
func setNodePath(node *yaml.Node, parts []string, value any) error {
	key := parts[0]
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value != key {
			continue
		}
		child := node.Content[index+1]
		if len(parts) == 1 {
			replacement, err := configValueNode(value)
			if err != nil {
				return err
			}
			replacement.HeadComment = child.HeadComment
			replacement.LineComment = child.LineComment
			replacement.FootComment = child.FootComment
			node.Content[index+1] = replacement
			return nil
		}
		if child.Kind != yaml.MappingNode {
			return fmt.Errorf("client.%s must be a mapping", key)
		}
		return setNodePath(child, parts[1:], value)
	}
	if len(parts) == 1 {
		replacement, err := configValueNode(value)
		if err != nil {
			return err
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			replacement,
		)
		return nil
	}
	group := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if err := setNodePath(group, parts[1:], value); err != nil {
		return err
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		group,
	)
	return nil
}

func configValueNode(value any) (*yaml.Node, error) {
	raw, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	return document.Content[0], nil
}

// writeConfigAtomic replaces path through a temporary file in the same
// directory, then flushes the rename to disk where the platform supports it.
func writeConfigAtomic(path string, data []byte, perm fs.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".kitsune-*.tmp")
	if err != nil {
		return fmt.Errorf("config: create temporary file for %s: %w", path, err)
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("config: replace %s: %w", path, err)
	}
	if handle, err := os.Open(filepath.Dir(path)); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}
