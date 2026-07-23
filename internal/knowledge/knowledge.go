// Package knowledge loads the RootCause knowledge base: markdown articles
// with a small YAML front-matter block (id, title, tags) followed by the
// concept/trade-off/production-example content that Rules link to.
package knowledge

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Doc struct {
	ID    string
	Title string
	Tags  []string
	Body  string
	Path  string
}

type frontMatter struct {
	ID    string   `yaml:"id"`
	Title string   `yaml:"title"`
	Tags  []string `yaml:"tags"`
}

// List returns every knowledge article under dir, sorted by ID.
func List(fsys fs.FS, dir string) ([]Doc, error) {
	var docs []Doc

	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(p) != ".md" {
			return nil
		}

		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}

		doc, err := parseDoc(p, string(data))
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		docs = append(docs, doc)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	return docs, nil
}

// Lookup finds a single article by ID (case-insensitive).
func Lookup(fsys fs.FS, dir, topic string) (*Doc, error) {
	docs, err := List(fsys, dir)
	if err != nil {
		return nil, err
	}

	topic = strings.ToLower(strings.TrimSpace(topic))
	for i := range docs {
		if docs[i].ID == topic {
			return &docs[i], nil
		}
	}
	return nil, fmt.Errorf("no knowledge article found for %q", topic)
}

func parseDoc(p, content string) (Doc, error) {
	doc := Doc{Path: p}

	if strings.HasPrefix(content, "---\n") {
		rest := content[len("---\n"):]
		if idx := strings.Index(rest, "\n---"); idx >= 0 {
			fm := frontMatter{}
			if err := yaml.Unmarshal([]byte(rest[:idx]), &fm); err != nil {
				return Doc{}, err
			}
			body := strings.TrimPrefix(rest[idx+len("\n---"):], "\n")

			doc.ID = strings.ToLower(fm.ID)
			doc.Title = fm.Title
			doc.Tags = fm.Tags
			doc.Body = strings.TrimSpace(body)
			return doc, nil
		}
	}

	base := path.Base(p)
	id := strings.TrimSuffix(base, path.Ext(base))
	doc.ID = strings.ToLower(id)
	doc.Title = id
	doc.Body = strings.TrimSpace(content)
	return doc, nil
}
