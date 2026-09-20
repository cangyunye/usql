package completer

import (
	"strings"

	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"
)

// The `\d`-style listings below complete through the catalog cache: a scope
// bucket (the visible listing, or a named schema's objects) is served from
// memory and reloaded in the background, so Tab never blocks on the database
// — a miss returns nothing and the kick re-renders once the load lands. The
// loader filters (types and the like) are part of the cache key, so `\dt`,
// `\dv` and `\dm` keep their exact server-side semantics.

// completeWithSelectablesFull completes `\d`-style listings with tables,
// functions and sequences (see completeListObjects for the tiering).
func (c completer) completeWithSelectablesFull(text []rune) []rline.Cand {
	return c.completeListObjects(text, "", nil)
}

// completeListObjects completes a `\d`-style object listing in three tiers.
// A dotless word first completes schema/owner names — every schema the login
// can reach, not just the current one, so superusers and users granted
// across schemas can pick the namespace before the object (L1 cache). When
// no schema matches the word, it falls back to the visible-scope object
// listing, so bare object names keep completing as before. A schema-qualified
// word completes that namespace's objects — no OnlyVisible restriction
// applies: the objects of a named schema are exactly the ones the login can
// query there.
//
// kindName selects the listing family ("" = selectables: tables, functions
// and sequences); types narrows the reader query (\dt vs \dv).
func (c completer) completeListObjects(text []rune, kindName string, types []string) []rline.Cand {
	// a family the reader does not serve completes nothing at all — not
	// even the schema tier (\di on a database without index metadata)
	if kindName != "" && !c.supportsList(kindName) {
		return nil
	}
	catalog, schema, object := splitListPattern(string(text))
	if schema == "" {
		// tier 1: schema names — L1 cache when installed, a direct query
		// otherwise (cacheless harnesses)
		if matches := completePrefixFull(object, c.namespaceListCands()); len(matches) > 0 {
			return matches
		}
		// tier 2: the visible-scope object listing
		return completeFuzzyFull(object, c.listingCands(kindName, metadata.Filter{OnlyVisible: true, Types: types}))
	}
	return completeFuzzyFull(string(text), c.listingCands(kindName, metadata.Filter{Catalog: catalog, Schema: schema, WithSystem: true, Types: types}))
}

// namespaceListCands lists every schema the login can reach: the L1 cache
// when installed (nil while its load is running — the kick re-renders), a
// direct query without one.
func (c completer) namespaceListCands() []rline.Cand {
	if objs, ok := c.schemas(); ok {
		return candsObjs(objs)
	}
	if c.cache != nil {
		return nil
	}
	return c.querySchemaCands(metadata.Filter{})
}

// supportsList reports whether the reader serves a listing family. The ""
// family (selectables) is a union: any of its readers suffices, matching the
// old per-kind appends.
func (c completer) supportsList(kindName string) bool {
	switch kindName {
	case "tables":
		_, ok := c.reader.(metadata.TableReader)
		return ok
	case "functions":
		_, ok := c.reader.(metadata.FunctionReader)
		return ok
	case "sequences":
		_, ok := c.reader.(metadata.SequenceReader)
		return ok
	case "indexes":
		_, ok := c.reader.(metadata.IndexReader)
		return ok
	}
	return false
}

// listingCands serves one listing bucket: cache hit answers from memory, a
// miss starts the asynchronous load (kick re-renders). Without a cache the
// load runs synchronously — the pre-cache behavior for harnesses.
func (c completer) listingCands(kindName string, f metadata.Filter) []rline.Cand {
	load := func() ([]obj, error) {
		return c.loadListObjs(kindName, f)
	}
	if c.cache == nil {
		objs, err := load()
		if err != nil {
			return nil
		}
		return candsObjs(objs)
	}
	objs, _ := c.cache.get(bucketObject, listKey(kindName, f), load)
	return candsObjs(objs)
}

// listKey builds the cache key of a listing bucket: the family plus every
// filter field that changes the reader query.
func listKey(kindName string, f metadata.Filter) string {
	return strings.Join([]string{
		"list", kindName, f.Catalog, f.Schema, f.Parent, f.Reference, f.Name,
		strings.Join(f.Types, ","),
	}, "\x00")
}

// loadListObjs loads one listing family for the filter, mirroring the kinds
// of loadSchemaObjects: unsupported catalogs retire silently for the session
// instead of failing the bucket.
func (c completer) loadListObjs(kindName string, f metadata.Filter) ([]obj, error) {
	if kindName == "" {
		// \d selectables: tables, functions and sequences in one bucket
		tables, err := c.loadListTables(f)
		if err != nil {
			return nil, err
		}
		functions, err := c.loadListFunctions(f)
		if err != nil {
			return nil, err
		}
		sequences, err := c.loadListSequences(f)
		if err != nil {
			return nil, err
		}
		return append(append(tables, functions...), sequences...), nil
	}
	switch kindName {
	case "tables":
		return c.loadListTables(f)
	case "functions":
		return c.loadListFunctions(f)
	case "sequences":
		return c.loadListSequences(f)
	case "indexes":
		return c.loadListIndexes(f)
	}
	return nil, nil
}

func (c completer) loadListTables(f metadata.Filter) ([]obj, error) {
	r, ok := c.reader.(metadata.TableReader)
	if !ok || c.skipKind("tables") {
		return nil, nil
	}
	set, err := r.Tables(f)
	if err != nil {
		c.failKind("tables", err)
		return nil, err
	}
	defer set.Close()
	var out []obj
	for set.Next() {
		t := set.Get()
		out = append(out, obj{name: listIdentifier(f, t.Catalog, t.Schema, t.Name), kind: typeKind(t.Type)})
	}
	return out, nil
}

func (c completer) loadListFunctions(f metadata.Filter) ([]obj, error) {
	r, ok := c.reader.(metadata.FunctionReader)
	if !ok || c.skipKind("functions") {
		return nil, nil
	}
	set, err := r.Functions(f)
	if err != nil {
		c.failKind("functions", err)
		return nil, err
	}
	defer set.Close()
	var out []obj
	for set.Next() {
		fn := set.Get()
		out = append(out, obj{name: listIdentifier(f, fn.Catalog, fn.Schema, fn.Name), kind: "function"})
	}
	return out, nil
}

func (c completer) loadListSequences(f metadata.Filter) ([]obj, error) {
	r, ok := c.reader.(metadata.SequenceReader)
	if !ok || c.skipKind("sequences") {
		return nil, nil
	}
	set, err := r.Sequences(f)
	if err != nil {
		c.failKind("sequences", err)
		return nil, err
	}
	defer set.Close()
	var out []obj
	for set.Next() {
		s := set.Get()
		out = append(out, obj{name: listIdentifier(f, s.Catalog, s.Schema, s.Name), kind: "sequence"})
	}
	return out, nil
}

func (c completer) loadListIndexes(f metadata.Filter) ([]obj, error) {
	r, ok := c.reader.(metadata.IndexReader)
	if !ok || c.skipKind("indexes") {
		return nil, nil
	}
	set, err := r.Indexes(f)
	if err != nil {
		c.failKind("indexes", err)
		return nil, err
	}
	defer set.Close()
	var out []obj
	for set.Next() {
		i := set.Get()
		out = append(out, obj{name: listIdentifier(f, i.Catalog, i.Schema, i.Name), kind: "index"})
	}
	return out, nil
}

// skipKind reports whether a catalog kind retired for the session. The
// cacheless path (harnesses) has no caps bookkeeping.
func (c completer) skipKind(kind string) bool {
	return c.caps != nil && c.caps.skip(kind)
}

// failKind records a listing load failure (see catalogCaps.failed).
func (c completer) failKind(kind string, err error) {
	if c.caps != nil {
		c.caps.failed(kind, err, c.logger)
	}
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
	return c.completeListObjects(text, "tables", types)
}

// completeWithFunctions completes the functions of a `\df`-style listing.
func (c completer) completeWithFunctions(text []rune, types []string) []rline.Cand {
	return c.completeListObjects(text, "functions", types)
}

func (c completer) completeWithIndexes(text []rune) []rline.Cand {
	return c.completeListObjects(text, "indexes", nil)
}

func (c completer) completeWithSequences(text []rune) []rline.Cand {
	return c.completeListObjects(text, "sequences", nil)
}

// completeWithSchemas completes a `\ds`-style schema listing from the L1
// cache: every schema the login can reach, matched client-side (the old
// path re-queried the reader on every keystroke in the argument position).
func (c completer) completeWithSchemas(text []rune) []rline.Cand {
	if _, ok := c.reader.(metadata.SchemaReader); !ok {
		return nil
	}
	filter := parseIdentifier(string(text))
	objs, ok := c.schemas()
	if !ok {
		// no cache (harnesses): query the reader synchronously
		return completeFuzzyFull(string(text), c.querySchemaCands(filter))
	}
	namePart := strings.TrimSuffix(filter.Name, "%")
	cands := make([]rline.Cand, 0, len(objs))
	for _, o := range objs {
		if namePart != "" && !strings.HasPrefix(strings.ToLower(o.name), strings.ToLower(namePart)) {
			continue
		}
		cands = append(cands, rline.Cand{Text: qualifiedIdentifier(filter, "", "", o.name), Kind: c.schemaKind})
	}
	return completeFuzzyFull(string(text), cands)
}

// querySchemaCands is the cacheless fallback of completeWithSchemas.
func (c completer) querySchemaCands(filter metadata.Filter) []rline.Cand {
	r, ok := c.reader.(metadata.SchemaReader)
	if !ok {
		return nil
	}
	set, err := func() (*metadata.SchemaSet, error) {
		if filter.Schema != "" {
			// name should already have a wildcard appended
			return r.Schemas(metadata.Filter{Catalog: filter.Schema, Name: filter.Name, WithSystem: true})
		}
		// every schema the login can reach, not just the current one
		f := filter
		f.OnlyVisible = false
		f.WithSystem = false
		return r.Schemas(f)
	}()
	if err != nil {
		return nil
	}
	defer set.Close()
	var out []rline.Cand
	for set.Next() {
		s := set.Get()
		out = append(out, rline.Cand{Text: qualifiedIdentifier(filter, "", s.Catalog, s.Schema), Kind: c.schemaKind})
	}
	return out
}

// completeWithCatalogs completes a `\l`-style catalog (database) listing.
// Catalogs are few and change rarely: one small bucket, matched client-side.
func (c completer) completeWithCatalogs(text []rune) []rline.Cand {
	if _, ok := c.reader.(metadata.CatalogReader); !ok {
		return nil
	}
	if c.cache == nil {
		return completeFromListCands(text, c.queryCatalogCands())
	}
	objs, _ := c.cache.get(bucketObject, "catalogs", c.loadCatalogs)
	return completeFromListCands(text, candsObjs(objs))
}

func (c completer) loadCatalogs() ([]obj, error) {
	r, ok := c.reader.(metadata.CatalogReader)
	if !ok || c.skipKind("catalogs") {
		return []obj{}, nil
	}
	set, err := r.Catalogs(metadata.Filter{})
	if err != nil {
		c.failKind("catalogs", err)
		return nil, err
	}
	defer set.Close()
	var out []obj
	for set.Next() {
		s := set.Get()
		out = append(out, obj{name: s.Catalog, kind: "database"})
	}
	return out, nil
}

// queryCatalogCands is the cacheless fallback of completeWithCatalogs.
func (c completer) queryCatalogCands() []rline.Cand {
	r, ok := c.reader.(metadata.CatalogReader)
	if !ok {
		return nil
	}
	set, err := r.Catalogs(parseIdentifier(""))
	if err != nil {
		return nil
	}
	defer set.Close()
	var out []rline.Cand
	for set.Next() {
		s := set.Get()
		out = append(out, rline.Cand{Text: s.Catalog, Kind: "database"})
	}
	return out
}
