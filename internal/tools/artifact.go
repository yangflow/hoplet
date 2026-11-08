package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/yangflow/hoplet/internal/artifact"
	"github.com/yangflow/hoplet/proto"
)

// published is what artifact_publish returns.
type published struct {
	ID      proto.ArtifactID   `json:"id"`
	Name    string             `json:"name"`
	Kind    proto.ArtifactKind `json:"kind"`
	Version int                `json:"version"`
}

func artifactTools(src Source) []tool {
	return []tool{{
		info: proto.ToolInfo{
			Name: "artifact_publish",
			Description: "Publish a result as a Hoplet artifact the user can open on their computer and phone: an HTML page, " +
				"Markdown (.md), an image (.png, .jpg, .gif, .webp), CSV or plain text (.txt). Publishing again under the same " +
				"name adds a new version. Paths are relative to your working directory and must stay inside it. A page loads " +
				"nothing from the network: list every script, style, font and image it uses in files, under the entry's directory.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{` +
				`"name":{"type":"string","description":"The artifact's name in this session; the same name makes a new version"},` +
				`"entry":{"type":"string","description":"The file to show: .html, .htm, .md, .markdown, .png, .jpg, .jpeg, .gif, .webp, .csv or .txt"},` +
				`"files":{"type":"array","items":{"type":"string"},"description":"For an HTML entry only: the other files the page uses, under the entry's directory"}` +
				`},"required":["name","entry"],"additionalProperties":false}`),
		},
		visible: func(proto.Session) bool { return true },
		call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
			var p struct {
				Name  string   `json:"name"`
				Entry string   `json:"entry"`
				Files []string `json:"files"`
			}
			if err := decode(args, &p); err != nil {
				return nil, err
			}
			if p.Name == "" || p.Entry == "" {
				return nil, failf("name and entry are required")
			}
			dir, err := src.WorkDir(ctx, caller.ID)
			if err != nil {
				return nil, err
			}
			entry, err := inside(dir, p.Entry)
			if err != nil {
				return nil, err
			}
			files := make([]string, 0, len(p.Files))
			for _, f := range p.Files {
				abs, err := inside(dir, f)
				if err != nil {
					return nil, err
				}
				files = append(files, abs)
			}
			a, err := src.Publish(ctx, artifact.Request{
				Session: caller.ID, Name: p.Name, Entry: entry, Files: files, By: proto.ArtifactByHarness,
			})
			if err != nil {
				return nil, err
			}
			return published{ID: a.ID, Name: a.Name, Kind: a.Kind, Version: a.Version}, nil
		},
	}}
}

// inside returns p, taken relative to dir unless it is absolute, as an
// absolute path, or fails when it leads outside dir, through ".." or a
// symbolic link.
func inside(dir, p string) (string, error) {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(dir, abs)
	}
	abs = filepath.Clean(abs)
	outside := failf("%s is outside your working directory %s; write the files there and publish them from it", p, dir)
	if rel, err := filepath.Rel(dir, abs); err != nil || !filepath.IsLocal(rel) {
		return "", outside
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("tools: working directory: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", failf("%s: %v", p, err)
	}
	if rel, err := filepath.Rel(root, real); err != nil || !filepath.IsLocal(rel) {
		return "", outside
	}
	return abs, nil
}
