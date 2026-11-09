package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/trick77/almanach/internal/endpoints"
	"github.com/trick77/almanach/internal/store"
)

// The interfaces: the HTTP APIs, gRPC and SOAP services a repository's
// code offers or only declares, read from the code by the poller
// (store.Interfaces), never a model's. With the sources off, none.

// interfaceBody is one interface with its operations: whose, read at which
// commit, from which files. Key is stable while the repository and the
// interface are (store.RepoAPI).
type interfaceBody struct {
	Key       string               `json:"key"`
	Repo      repoRefBody          `json:"repo"`
	Commit    string               `json:"commit"`
	Kind      string               `json:"kind"`
	Name      string               `json:"name"`
	Version   string               `json:"version"`
	BasePath  string               `json:"base_path"`
	Direction string               `json:"direction"`
	Files     []string             `json:"files"`
	Endpoints []endpoints.Endpoint `json:"endpoints"`
}

// interfaces is every repository's interfaces; none with the sources off.
func (d Deps) interfaces(ctx context.Context) (store.Interfaces, error) {
	if d.Sources == nil {
		return store.Interfaces{}, nil
	}
	return d.Store.Interfaces(ctx)
}

func interfaceBodies(apis []store.RepoAPI) []interfaceBody {
	out := make([]interfaceBody, 0, len(apis))
	for _, a := range apis {
		b := interfaceBody{Key: a.Key, Repo: refBody(a.Repo), Commit: a.Commit, Kind: a.Kind, Name: a.Name, Version: a.Version,
			BasePath: a.BasePath, Direction: a.Direction, Files: a.Files, Endpoints: a.Endpoints}
		if b.Files == nil {
			b.Files = []string{}
		}
		if b.Endpoints == nil {
			b.Endpoints = []endpoints.Endpoint{}
		}
		for i := range b.Endpoints {
			if b.Endpoints[i].Files == nil {
				b.Endpoints[i].Files = []string{}
			}
		}
		out = append(out, b)
	}
	return out
}

// repoInterfaces is a repository's interfaces, offered and declared.
func repoInterfaces(in store.Interfaces, ref store.RepoRef) []interfaceBody {
	return interfaceBodies(in.Query(store.InterfaceFilter{Project: ref.ProjectKey, Slug: ref.Slug}))
}

// projectInterfaces is what a project's repositories offer: the system's
// interfaces. A contract only declared is its repository's page's.
func projectInterfaces(in store.Interfaces, key string) []interfaceBody {
	return interfaceBodies(in.Query(store.InterfaceFilter{Project: key, Direction: endpoints.Offers}))
}

// kindWord is an interface's kind as the export writes it.
var kindWord = map[string]string{endpoints.HTTP: "HTTP", endpoints.GRPC: "gRPC", endpoints.SOAP: "SOAP"}

// markdownInterface is one interface as an item of the export, its
// operations below it, each whole: nothing left out for size.
func markdownInterface(b *strings.Builder, a interfaceBody, owner string) {
	span := func(s string) string { return "`" + strings.ReplaceAll(s, "`", "'") + "`" }
	name := "Routen im Code"
	if a.Name != "" {
		name = span(a.Name)
	}
	fmt.Fprintf(b, "- %s%s %s", owner, kindWord[a.Kind], name)
	if a.Version != "" {
		fmt.Fprintf(b, " %s", span(a.Version))
	}
	if a.BasePath != "" {
		fmt.Fprintf(b, ", Basis %s", span(a.BasePath))
	}
	if a.Direction == endpoints.Declares {
		b.WriteString(", nur genannt (kein Server im Code)")
	}
	if len(a.Files) > 0 {
		var fs []string
		for _, f := range a.Files {
			fs = append(fs, span(f))
		}
		fmt.Fprintf(b, " (%s)", strings.Join(fs, ", "))
	}
	b.WriteString("\n")
	for _, e := range a.Endpoints {
		fmt.Fprintf(b, "  - %s", span(e.Method+" "+e.Path))
		if e.Summary != "" {
			fmt.Fprintf(b, ": %s", strings.ReplaceAll(e.Summary, "\n", " "))
		}
		b.WriteString("\n")
	}
}

// markdownInterfaces is interfaces as a section of an export, "" without
// any; owners names each one's repository (a project's export).
func markdownInterfaces(apis []interfaceBody, owners bool) string {
	if len(apis) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Schnittstellen\n\n")
	for _, a := range apis {
		owner := ""
		if owners {
			owner = "`" + a.Repo.Project + "/" + a.Repo.Slug + "`: "
		}
		markdownInterface(&b, a, owner)
	}
	return b.String()
}
