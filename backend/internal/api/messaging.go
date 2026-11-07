package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/trick77/almanach/internal/messaging"
	"github.com/trick77/almanach/internal/store"
)

// The messages: which topics a repository produces to and consumes from,
// the schemas it defines, and who is on the far side, joined by the store
// from what the poller read in the code (store.Messaging), never stored
// and never a model's. With the sources off, none.

// repoTopicBody is one topic a repository touches one way: produces,
// consumes, or declares (named, the way unknown); Peers are the source
// repositories on the far side.
// The topic itself is messaging's wire shape, as stored.
type repoTopicBody struct {
	messaging.Topic
	Peers []repoRefBody `json:"peers"`
}

type repoMessagingBody struct {
	Topics  []repoTopicBody    `json:"topics"`
	Schemas []messaging.Schema `json:"schemas"`
}

// topicFlowBody is a topic a project's repositories touch, with every
// source repository on each side, another project's included.
type topicFlowBody struct {
	Name      string        `json:"name"`
	Producers []repoRefBody `json:"producers"`
	Consumers []repoRefBody `json:"consumers"`
	Declared  []repoRefBody `json:"declared"`
}

// messages is every repository's messages; none with the sources off.
func (d Deps) messages(ctx context.Context) (store.Messages, error) {
	if d.Sources == nil {
		return store.Messages{}, nil
	}
	return d.Store.Messaging(ctx)
}

func refBodies(refs []store.RepoRef) []repoRefBody {
	out := make([]repoRefBody, 0, len(refs))
	for _, r := range refs {
		out = append(out, refBody(r))
	}
	return out
}

func repoMessaging(m store.Messages, ref store.RepoRef) repoMessagingBody {
	out := repoMessagingBody{Topics: []repoTopicBody{}, Schemas: m.Of(ref).Schemas}
	for _, t := range m.RepoTopics(ref) {
		if t.Files == nil {
			t.Files = []string{}
		}
		out.Topics = append(out.Topics, repoTopicBody{Topic: t.Topic, Peers: refBodies(t.Peers)})
	}
	return out
}

func projectFlows(m store.Messages, key string) []topicFlowBody {
	out := []topicFlowBody{}
	for _, f := range m.ProjectFlows(key) {
		out = append(out, topicFlowBody{Name: f.Name, Producers: refBodies(f.Producers), Consumers: refBodies(f.Consumers),
			Declared: refBodies(f.Declared)})
	}
	return out
}

// markdownFlows is a project's topics as a section of its export, ""
// without any: each with its producers and consumers.
func markdownFlows(flows []topicFlowBody) string {
	if len(flows) == 0 {
		return ""
	}
	span := func(s string) string { return "`" + strings.ReplaceAll(s, "`", "'") + "`" }
	refs := func(rs []repoRefBody) string {
		if len(rs) == 0 {
			return "–"
		}
		var out []string
		for _, r := range rs {
			out = append(out, span(r.Project+"/"+r.Slug))
		}
		return strings.Join(out, ", ")
	}
	var b strings.Builder
	b.WriteString("\n## Messaging\n\n")
	for _, f := range flows {
		fmt.Fprintf(&b, "- %s: Producer %s; Consumer %s", span(f.Name), refs(f.Producers), refs(f.Consumers))
		if len(f.Declared) > 0 {
			fmt.Fprintf(&b, "; genannt von %s", refs(f.Declared))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// directionWord is a direction as the export writes it.
var directionWord = map[string]string{messaging.Produces: "Producer von", messaging.Consumes: "Consumer von", messaging.Declares: "nennt"}

// markdownMessaging is the repository's topics and schemas as a section of
// the export, "" when it has none. Names are code's text: in code spans.
func markdownMessaging(m repoMessagingBody) string {
	if len(m.Topics) == 0 && len(m.Schemas) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Messaging\n\n")
	span := func(s string) string { return "`" + strings.ReplaceAll(s, "`", "'") + "`" }
	for _, t := range m.Topics {
		fmt.Fprintf(&b, "- %s %s", directionWord[t.Direction], span(t.Name))
		if len(t.Peers) > 0 {
			var peers []string
			for _, p := range t.Peers {
				peers = append(peers, span(p.Project+"/"+p.Slug))
			}
			if t.Direction == messaging.Produces {
				b.WriteString(", Consumer: ")
			} else {
				b.WriteString(", Producer: ")
			}
			b.WriteString(strings.Join(peers, ", "))
		}
		b.WriteString("\n")
	}
	if len(m.Schemas) > 0 {
		if len(m.Topics) > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Schemas:\n\n")
		for _, s := range m.Schemas {
			fmt.Fprintf(&b, "- %s (%s, %s)\n", span(s.Name), s.Kind, span(s.File))
		}
	}
	return b.String()
}
