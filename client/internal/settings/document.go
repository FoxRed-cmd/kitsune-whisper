package settings

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// errUnrepresentable reports a change the YAML document cannot express in
// place, such as editing an aliased Client section. The caller falls back to
// rewriting the document and keeps a backup of the original.
var errUnrepresentable = errors.New("change cannot be represented in place")

// renderDocument applies field updates to the YAML document. Nodes the update
// does not touch keep their comments, order and style, so the "server:"
// section and free-form comments survive a save.
func renderDocument(raw []byte, updates map[string]any) ([]byte, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("invalid YAML: %w", err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode}
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errUnrepresentable
	}
	for _, path := range sortedKeys(updates) {
		parts := strings.Split(path, ".")
		if err := setPath(root, parts, updates[path]); err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderFallback rewrites the whole document from its resolved value. It loses
// comments but keeps every section, and is only used when renderDocument
// cannot express the change.
func renderFallback(raw []byte, section map[string]any) ([]byte, error) {
	document := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &document); err != nil {
			return nil, fmt.Errorf("invalid YAML: %w", err)
		}
	}
	if document == nil {
		document = map[string]any{}
	}
	document["client"] = section
	return yaml.Marshal(document)
}

// setPath writes value at the given key path inside a mapping node, creating
// missing groups. It returns errUnrepresentable when the existing structure
// cannot be edited in place.
func setPath(node *yaml.Node, parts []string, value any) error {
	key := parts[0]
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != key {
			continue
		}
		child := node.Content[i+1]
		if len(parts) == 1 {
			replacement, err := valueNode(value)
			if err != nil {
				return err
			}
			copyComments(child, replacement)
			node.Content[i+1] = replacement
			return nil
		}
		if child.Kind != yaml.MappingNode {
			return errUnrepresentable
		}
		return setPath(child, parts[1:], value)
	}

	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	if len(parts) == 1 {
		replacement, err := valueNode(value)
		if err != nil {
			return err
		}
		node.Content = append(node.Content, keyNode, replacement)
		return nil
	}
	group := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if err := setPath(group, parts[1:], value); err != nil {
		return err
	}
	node.Content = append(node.Content, keyNode, group)
	return nil
}

// valueNode renders a Go value as a YAML node.
func valueNode(value any) (*yaml.Node, error) {
	raw, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode value: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("encode value: %w", err)
	}
	return doc.Content[0], nil
}

// copyComments keeps the comments attached to the value a save replaces.
func copyComments(from, to *yaml.Node) {
	to.HeadComment = from.HeadComment
	to.LineComment = from.LineComment
	to.FootComment = from.FootComment
}
