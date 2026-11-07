package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/trick77/almanach/internal/messaging"
)

// TopicsCommit is the commit a source repository's messaging was last read
// at by this messaging.Version; false when it never was, or by an older
// one.
func (s *Store) TopicsCommit(ctx context.Context, repoID int64) (string, bool, error) {
	var commit string
	err := s.db.QueryRowContext(ctx, `SELECT "commit" FROM repo_topics WHERE repo_id = ? AND reader = ?`, repoID,
		messaging.Version).Scan(&commit)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return commit, err == nil, err
}

// SaveTopics stores what a source repository's code says of its messages
// at commit, in place of what it said before. ErrNoRepo when the row went
// meanwhile.
func (s *Store) SaveTopics(ctx context.Context, repoID int64, commit string, read messaging.Read) error {
	if read.Topics == nil {
		read.Topics = []messaging.Topic{}
	}
	if read.Schemas == nil {
		read.Schemas = []messaging.Schema{}
	}
	topics, err := jsonOf(read.Topics)
	if err != nil {
		return err
	}
	schemas, err := jsonOf(read.Schemas)
	if err != nil {
		return err
	}
	n, err := affected(s.db.ExecContext(ctx, `
		INSERT INTO repo_topics (repo_id, "commit", topics, schemas, reader, read_at)
		SELECT sr.repo_id, ?, ?, ?, ?, ? FROM source_repos sr WHERE sr.repo_id = ? AND `+activeRow+`
		ON CONFLICT (repo_id) DO UPDATE SET "commit" = excluded."commit", topics = excluded.topics,
		       schemas = excluded.schemas, reader = excluded.reader, read_at = excluded.read_at`,
		commit, topics, schemas, messaging.Version, nowStamp(), repoID))
	if isForeignKey(err) || err == nil && n == 0 {
		return ErrNoRepo
	}
	return err
}

// Messages is what every source repository's code says of its messages,
// read at once; who talks to whom over a topic is joined from it (Flow,
// RepoTopics), never stored. A topic is one name: two repositories that
// name the same topic talk over it, whatever else they share.
type Messages struct {
	read map[RepoRef]messaging.Read
	// commit is the commit each repository's was read at.
	commit map[RepoRef]string
}

// NewMessages is Messages over reads already at hand.
func NewMessages(read map[RepoRef]messaging.Read) Messages { return Messages{read: read} }

// NewMessagesAt is NewMessages with the commit each was read at.
func NewMessagesAt(read map[RepoRef]messaging.Read, commit map[RepoRef]string) Messages {
	return Messages{read: read, commit: commit}
}

// Commit is the commit a repository's messages were read at, "" when they
// never were (or were handed in by NewMessages).
func (m Messages) Commit(ref RepoRef) string { return m.commit[ref] }

// TopicFlow is a topic with every source repository on each side:
// producers, consumers, and those that name it without code saying which
// way. Each list sorted.
type TopicFlow struct {
	Name      string
	Producers []RepoRef
	Consumers []RepoRef
	Declared  []RepoRef
}

// RepoTopic is one topic a repository touches one way, and the other
// repositories on the far side: the consumers of what it produces, the
// producers of what it consumes; none for a topic it only names.
type RepoTopic struct {
	messaging.Topic
	Peers []RepoRef
}

// Messaging reads every active source repository's messages. A repository
// missing from its listing, or deactivated, counts for nothing.
func (s *Store) Messaging(ctx context.Context) (Messages, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, r.slug, t."commit", t.topics, t.schemas
		  FROM repo_topics t
		  JOIN source_repos sr ON sr.repo_id = t.repo_id
		  JOIN repos r ON r.id = t.repo_id
		  JOIN projects p ON p.id = r.project_id
		 WHERE sr.state <> 'missing' AND `+activeRow)
	if err != nil {
		return Messages{}, err
	}
	defer func() { _ = rows.Close() }()
	m := Messages{read: map[RepoRef]messaging.Read{}, commit: map[RepoRef]string{}}
	for rows.Next() {
		var ref RepoRef
		var commit, topics, schemas string
		if err := rows.Scan(&ref.ProjectKey, &ref.Slug, &commit, &topics, &schemas); err != nil {
			return Messages{}, err
		}
		var r messaging.Read
		if err := json.Unmarshal([]byte(topics), &r.Topics); err != nil {
			return Messages{}, err
		}
		if err := json.Unmarshal([]byte(schemas), &r.Schemas); err != nil {
			return Messages{}, err
		}
		m.read[ref], m.commit[ref] = r, commit
	}
	return m, rows.Err()
}

// Of is what a repository's code says of its messages; empty lists when
// it was never read.
func (m Messages) Of(ref RepoRef) messaging.Read {
	r := m.read[ref]
	if r.Topics == nil {
		r.Topics = []messaging.Topic{}
	}
	if r.Schemas == nil {
		r.Schemas = []messaging.Schema{}
	}
	return r
}

// flows is every topic by name with its sides.
func (m Messages) flows() map[string]*TopicFlow {
	out := map[string]*TopicFlow{}
	for ref, r := range m.read {
		for _, t := range r.Topics {
			f := out[t.Name]
			if f == nil {
				f = &TopicFlow{Name: t.Name, Producers: []RepoRef{}, Consumers: []RepoRef{}, Declared: []RepoRef{}}
				out[t.Name] = f
			}
			switch t.Direction {
			case messaging.Produces:
				f.Producers = append(f.Producers, ref)
			case messaging.Consumes:
				f.Consumers = append(f.Consumers, ref)
			default:
				f.Declared = append(f.Declared, ref)
			}
		}
	}
	for _, f := range out {
		for _, l := range [][]RepoRef{f.Producers, f.Consumers, f.Declared} {
			sort.Slice(l, func(i, j int) bool { return less(l[i], l[j]) })
		}
	}
	return out
}

// RepoTopics is a repository's topics, each with the repositories on its
// far side, in the repository's order (by name, then direction).
func (m Messages) RepoTopics(ref RepoRef) []RepoTopic {
	flows := m.flows()
	out := []RepoTopic{}
	for _, t := range m.Of(ref).Topics {
		rt := RepoTopic{Topic: t, Peers: []RepoRef{}}
		f := flows[t.Name]
		var far []RepoRef
		switch t.Direction {
		case messaging.Produces:
			far = f.Consumers
		case messaging.Consumes:
			far = f.Producers
		}
		for _, p := range far {
			if p != ref {
				rt.Peers = append(rt.Peers, p)
			}
		}
		out = append(out, rt)
	}
	return out
}

// ProjectFlows is the topics a project's repositories touch, each with
// every repository on its sides, another project's included: the system's
// messages and where they go beyond it. By name.
func (m Messages) ProjectFlows(key string) []TopicFlow {
	flows := m.flows()
	names := map[string]bool{}
	for ref, r := range m.read {
		if ref.ProjectKey != key {
			continue
		}
		for _, t := range r.Topics {
			names[t.Name] = true
		}
	}
	out := make([]TopicFlow, 0, len(names))
	for n := range names {
		out = append(out, *flows[n])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
