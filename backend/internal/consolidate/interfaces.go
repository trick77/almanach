package consolidate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/endpoints"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/logging"
)

// kindName is an interface's kind as a prompt names it.
var kindName = map[string]string{endpoints.HTTP: "HTTP API", endpoints.GRPC: "gRPC service", endpoints.SOAP: "SOAP service"}

// interfaceHead is an interface's line: its kind, name, version, base
// path, whether the code serves it, its files.
func interfaceHead(a store.RepoAPI) string {
	name := "routes declared in code, no spec"
	if a.Name != "" {
		name = "`" + a.Name + "`"
	}
	line := fmt.Sprintf("- %s %s", kindName[a.Kind], name)
	if a.Version != "" {
		line += " " + a.Version
	}
	if a.BasePath != "" {
		line += ", base path `" + a.BasePath + "`"
	}
	if a.Direction == endpoints.Declares {
		line += ", declared only: its contract is here, no code here serves it (likely another service's, for a client)"
	}
	if len(a.Files) > 0 {
		line += " (`" + strings.Join(a.Files, "`, `") + "`)"
	}
	return line
}

// operationLine is one operation under its interface.
func operationLine(e endpoints.Endpoint) string {
	line := "  - `" + e.Method + " " + e.Path + "`"
	if e.Summary != "" {
		line += ": " + e.Summary
	}
	return line
}

// foldedOperations is an interface's operations by the first segment of
// their path below the base path, each with its count and methods: what a
// step reads of an interface too large to list.
func foldedOperations(a store.RepoAPI) []string {
	type fold struct {
		n       int
		methods map[string]bool
	}
	folds := map[string]*fold{}
	for _, e := range a.Endpoints {
		rel := strings.TrimPrefix(e.Path, a.BasePath)
		seg := "/" + strings.SplitN(strings.TrimPrefix(rel, "/"), "/", 2)[0]
		if a.Kind != endpoints.HTTP {
			seg = a.Name
		}
		f := folds[seg]
		if f == nil {
			f = &fold{methods: map[string]bool{}}
			folds[seg] = f
		}
		f.n++
		f.methods[e.Method] = true
	}
	keys := make([]string, 0, len(folds))
	for k := range folds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		var ms []string
		for m := range folds[k].methods {
			ms = append(ms, m)
		}
		sort.Strings(ms)
		out = append(out, fmt.Sprintf("  - `%s…`: %d operations (%s)", k, folds[k].n, strings.Join(ms, ", ")))
	}
	return out
}

// interfacesShown is the interfaces a repository's code offers or
// declares, read by code (store.Interfaces), each with its operations:
// what a describe step's interface statements name. Within
// InterfacesMaxBytes every operation; past it, the rest's operations
// folded by path (foldedOperations). No interface is ever left out for
// size. Never read, or read at another commit than the run's: none
// shown, as topicsShown, and said so: no list is no "none".
func interfacesShown(in store.Interfaces, ref store.RepoRef, commit string) string {
	if !in.Read(ref) {
		return "_Not read from the code yet: name only the interfaces the files below show._"
	}
	if c := in.Commit(ref); c != "" && c != commit {
		return "_Not read at this run's commit: name only the interfaces the files below show._"
	}
	apis := in.Query(store.InterfaceFilter{Project: ref.ProjectKey, Slug: ref.Slug})
	if len(apis) == 0 {
		return "_None._"
	}
	var b strings.Builder
	folding := false
	for _, a := range apis {
		full := []string{interfaceHead(a)}
		for _, e := range a.Endpoints {
			full = append(full, operationLine(e))
		}
		block := strings.Join(full, "\n") + "\n"
		if folding || b.Len()+len(block) > baseline.InterfacesMaxBytes {
			folding = true
			block = strings.Join(append([]string{interfaceHead(a)}, foldedOperations(a)...), "\n") + "\n"
		}
		b.WriteString(block)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// systemInterfacesShown is the interfaces a project's repositories offer,
// by repository, each with its count of operations: what the project
// page's interface statements name. One line each, every one: the prompt
// says none is left out.
func systemInterfacesShown(in store.Interfaces, key string) string {
	apis := in.Query(store.InterfaceFilter{Project: key, Direction: endpoints.Offers})
	if len(apis) == 0 {
		return "_None._"
	}
	var b strings.Builder
	for _, a := range apis {
		ops := fmt.Sprintf(", %d operations\n", len(a.Endpoints))
		if len(a.Endpoints) == 1 {
			ops = ", 1 operation\n"
		}
		b.WriteString(strings.Replace(interfaceHead(a), "- ", "- `"+a.Repo.Slug+"`: ", 1) + ops)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// systemInterfaces reads the project's offered interfaces for its step;
// a store that does not answer pauses the drain, as every read a step
// rests on.
func (w *Worker) systemInterfaces(ctx context.Context, key string) (string, error) {
	in, err := w.base.Store.Interfaces(ctx)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: interfaces read failed, drain paused", logging.Err(err))
		return "", errStop
	}
	return systemInterfacesShown(in, key), nil
}
