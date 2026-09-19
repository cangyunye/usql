package completer

import (
	"strings"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"
)

// completeWithSelectablesFull completes `\d`-style listings with tables,
// functions and sequences (see completeListObjects for the tiering).
func (c completer) completeWithSelectablesFull(text []rune) []rline.Cand {
	return c.completeListObjects(text, func(f metadata.Filter) []rline.Cand {
		var names []rline.Cand
		if r, ok := c.reader.(metadata.TableReader); ok {
			names = append(names, c.getCands(
				func() (iterator, error) { return r.Tables(f) },
				func(res interface{}) (string, string) {
					t := res.(*metadata.TableSet).Get()
					return listIdentifier(f, t.Catalog, t.Schema, t.Name), typeKind(t.Type)
				},
			)...)
		}
		if r, ok := c.reader.(metadata.FunctionReader); ok {
			names = append(names, c.getCands(
				func() (iterator, error) { return r.Functions(f) },
				func(res interface{}) (string, string) {
					fn := res.(*metadata.FunctionSet).Get()
					return listIdentifier(f, fn.Catalog, fn.Schema, fn.Name), "function"
				},
			)...)
		}
		if r, ok := c.reader.(metadata.SequenceReader); ok {
			names = append(names, c.getCands(
				func() (iterator, error) { return r.Sequences(f) },
				func(res interface{}) (string, string) {
					s := res.(*metadata.SequenceSet).Get()
					return listIdentifier(f, s.Catalog, s.Schema, s.Name), "sequence"
				},
			)...)
		}
		return names
	})
}

// completeListObjects completes a `\d`-style object listing in three tiers.
// A dotless word first completes schema/owner names — every schema the login
// can reach, not just the current one, so superusers and users granted
// across schemas can pick the namespace before the object. When no schema
// matches the word, it falls back to the visible-scope object listing, so
// bare object names keep completing as before. A schema-qualified word
// completes that namespace's objects through a query scoped to it — no
// OnlyVisible restriction applies: the objects of a named schema are exactly
// the ones the login can query there.
func (c completer) completeListObjects(text []rune, load func(metadata.Filter) []rline.Cand) []rline.Cand {
	catalog, schema, object := splitListPattern(string(text))
	if schema == "" {
		if matches := completePrefixFull(object, c.getNamespaces(metadata.Filter{WithSystem: false})); len(matches) > 0 {
			return matches
		}
		return completeFuzzyFull(object, load(metadata.Filter{OnlyVisible: true}))
	}
	return completeFuzzyFull(string(text), load(metadata.Filter{Catalog: catalog, Schema: schema, WithSystem: true}))
}

// splitListPattern splits a typed `\d`-style pattern into its catalog,
// schema and object-name parts ("remote.default.film", "scott.e", "film").
func splitListPattern(pattern string) (catalog, schema, object string) {
	parts := strings.SplitN(pattern, ".", 3)
	switch len(parts) {
	case 3:
		return parts[0], parts[1], parts[2]
	case 2:
		return "", parts[0], parts[1]
	default:
		return "", "", parts[0]
	}
}

// listIdentifier renders an object of a `\d`-style listing: fully qualified
// for the visible-scope listing, or qualified as typed for a scoped query.
func listIdentifier(f metadata.Filter, catalog, schema, name string) string {
	if f.Schema != "" || f.Catalog != "" {
		return qualifiedIdentifier(f, catalog, schema, name)
	}
	return fullIdentifier(catalog, schema, name)
}

// completeWithTables completes the tables of a `\dt`-style listing, filtered
// to the requested types (see completeListObjects).
func (c completer) completeWithTables(text []rune, types []string) []rline.Cand {
	r, ok := c.reader.(metadata.TableReader)
	if !ok {
		return nil
	}
	return c.completeListObjects(text, func(f metadata.Filter) []rline.Cand {
		f.Types = types
		return c.getCands(
			func() (iterator, error) {
				return r.Tables(f)
			},
			func(res interface{}) (string, string) {
				t := res.(*metadata.TableSet).Get()
				return listIdentifier(f, t.Catalog, t.Schema, t.Name), typeKind(t.Type)
			},
		)
	})
}

// completeWithFunctions completes the functions of a `\df`-style listing.
func (c completer) completeWithFunctions(text []rune, types []string) []rline.Cand {
	r, ok := c.reader.(metadata.FunctionReader)
	if !ok {
		return nil
	}
	return c.completeListObjects(text, func(f metadata.Filter) []rline.Cand {
		f.Types = types
		return c.getCands(
			func() (iterator, error) {
				return r.Functions(f)
			},
			func(res interface{}) (string, string) {
				fn := res.(*metadata.FunctionSet).Get()
				return listIdentifier(f, fn.Catalog, fn.Schema, fn.Name), "function"
			},
		)
	})
}

func (c completer) completeWithIndexes(text []rune) []rline.Cand {
	r, ok := c.reader.(metadata.IndexReader)
	if !ok {
		return nil
	}
	return c.completeListObjects(text, func(f metadata.Filter) []rline.Cand {
		return c.getCands(
			func() (iterator, error) {
				return r.Indexes(f)
			},
			func(res interface{}) (string, string) {
				i := res.(*metadata.IndexSet).Get()
				return listIdentifier(f, i.Catalog, i.Schema, i.Name), "index"
			},
		)
	})
}

func (c completer) completeWithSequences(text []rune) []rline.Cand {
	r, ok := c.reader.(metadata.SequenceReader)
	if !ok {
		return nil
	}
	return c.completeListObjects(text, func(f metadata.Filter) []rline.Cand {
		return c.getCands(
			func() (iterator, error) {
				return r.Sequences(f)
			},
			func(res interface{}) (string, string) {
				s := res.(*metadata.SequenceSet).Get()
				return listIdentifier(f, s.Catalog, s.Schema, s.Name), "sequence"
			},
		)
	})
}

func (c completer) completeWithSchemas(text []rune) []rline.Cand {
	r, ok := c.reader.(metadata.SchemaReader)
	if !ok {
		return nil
	}
	filter := parseIdentifier(string(text))
	names := c.getCands(
		func() (iterator, error) {
			if filter.Schema != "" {
				// name should already have a wildcard appended
				return r.Schemas(metadata.Filter{Catalog: filter.Schema, Name: filter.Name, WithSystem: true})
			}
			// every schema the login can reach, not just the current one
			filter.OnlyVisible = false
			filter.WithSystem = false
			return r.Schemas(filter)
		},
		func(res interface{}) (string, string) {
			s := res.(*metadata.SchemaSet).Get()
			return qualifiedIdentifier(filter, "", s.Catalog, s.Schema), c.schemaKind
		},
	)
	return completeFuzzyFull(string(text), names)
}

func (c completer) completeWithCatalogs(text []rune) []rline.Cand {
	r, ok := c.reader.(metadata.CatalogReader)
	if !ok {
		return nil
	}
	filter := parseIdentifier(string(text))
	names := c.getCands(
		func() (iterator, error) {
			return r.Catalogs(filter)
		},
		func(res interface{}) (string, string) {
			s := res.(*metadata.CatalogSet).Get()
			return s.Catalog, "database"
		},
	)
	return CompleteFromListCands(text, names)
}
