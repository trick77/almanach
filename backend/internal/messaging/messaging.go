// Package messaging reads which message topics a repository produces to
// and consumes from, and which message schemas it defines: Kafka listeners
// and templates, Kafka Streams, plain clients, Spring Cloud Stream
// bindings, kafkajs, topic resources and AsyncAPI files, Avro and Protobuf
// schemas. Pure: bytes in, facts out, every rule a table test. A topic
// name comes from a literal, a constant or a property code can resolve
// (coderead); one it cannot (a method parameter, a property set per stage)
// is left out, never guessed. Which repository talks to which is joined
// from these at read time (store), never by a model.
package messaging

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/trick77/almanach/internal/coderead"
)

// Version is this package's reading: raise it with any change to what
// Parse makes of a file, and every repository is read again at the next
// sync, not only those whose head moved.
const Version = 1

// Directions: what a repository does with a topic. Declares is a topic it
// names without code saying which way (a topic resource, a property no
// code was seen to use, an AsyncAPI channel).
const (
	Produces = "produces"
	Consumes = "consumes"
	Declares = "declares"
)

// Topic is one topic a repository touches one way, and the files that say
// so.
type Topic struct {
	Name      string   `json:"name"`
	Direction string   `json:"direction"`
	Files     []string `json:"files"`
}

// Schema is a message schema a repository defines: its full name and
// kind (avro, proto).
type Schema struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	File string `json:"file"`
}

// Read is what Parse found: sorted, each topic and direction once.
type Read struct {
	Topics  []Topic  `json:"topics"`
	Schemas []Schema `json:"schemas"`
}

// GrepPattern is what a file must contain, case aside, to be worth
// parsing (git grep -i -E): the rest of a tree is never read.
// A file of constants alone is read too when one looks like a topic name
// (a lower-case dotted or dashed string): `Destinations.ORDERS` names a
// constant whose file says neither kafka nor topic.
const GrepPattern = `kafka|topic|asyncapi|(static final String|const val) +[a-z0-9_]+ *= *"[a-z0-9_]+[.-][a-z0-9._-]+"`

// Source reports whether a path is a file Parse reads for topics: JVM,
// JavaScript and TypeScript sources and YAML or properties config.
func Source(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".java", ".kt", ".scala", ".groovy", ".ts", ".js", ".mjs", ".yml", ".yaml", ".properties":
		return true
	}
	return false
}

// Config reports whether a path is application properties without a
// profile: read whatever it holds, since a listener's ${key} is resolved
// against it and the key need not say topic.
func Config(p string) bool { return coderead.Config(p) }

// SchemaFile reports whether a path is a schema Parse reads by its name.
func SchemaFile(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".avsc", ".avdl", ".proto":
		return true
	}
	return false
}

// validName is a topic name Kafka accepts.
var validName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,249}$`)

// Parse reads the facts out of files, path to body.
func Parse(files map[string][]byte) Read {
	paths := coderead.SortedKeys(files)
	r := coderead.NewResolver(files)
	found := map[[2]string]map[string]bool{}
	add := func(name, dir, file string) {
		if !validName.MatchString(name) {
			return
		}
		k := [2]string{name, dir}
		if found[k] == nil {
			found[k] = map[string]bool{}
		}
		found[k][file] = true
	}
	used := map[string]bool{}
	var schemas []Schema
	for _, p := range paths {
		body := files[p]
		switch strings.ToLower(path.Ext(p)) {
		case ".avsc":
			schemas = append(schemas, avro(p, body)...)
		case ".avdl":
			schemas = append(schemas, avdl(p, body)...)
		case ".proto":
			schemas = append(schemas, proto(p, body)...)
		case ".yml", ".yaml":
			yamlTopics(p, body, add)
		case ".java", ".kt", ".scala", ".groovy":
			jvm(p, string(body), r, add, used)
		case ".ts", ".js", ".mjs":
			script(p, string(body), r, add)
		}
	}
	// Spring Cloud Stream bindings, then the topic properties no code was
	// seen to use: named, direction unknown.
	for _, mod := range r.Modules() {
		cfg := r.Settings(mod)
		for _, k := range coderead.SortedKeys(cfg) {
			v := cfg[k]
			if b, ok := strings.CutPrefix(k, "spring.cloud.stream.bindings."); ok && strings.HasSuffix(b, ".destination") {
				dir := bindingDirection(strings.TrimSuffix(b, ".destination"))
				for _, t := range strings.Split(v, ",") {
					add(strings.TrimSpace(t), dir, r.KeyFile(mod, k))
				}
				continue
			}
			if topicKey(k) && !used[k] && !strings.Contains(v, "${") {
				for _, t := range strings.Split(v, ",") {
					if t = strings.TrimSpace(t); !setting.MatchString(t) {
						add(t, Declares, r.KeyFile(mod, k))
					}
				}
			}
		}
	}
	out := Read{Topics: []Topic{}, Schemas: []Schema{}}
	directed := map[string]bool{}
	for k := range found {
		if k[1] != Declares {
			directed[k[0]] = true
		}
	}
	for k, fs := range found {
		if k[1] == Declares && directed[k[0]] {
			continue
		}
		out.Topics = append(out.Topics, Topic{Name: k[0], Direction: k[1], Files: coderead.SortedKeys(fs)})
	}
	sort.Slice(out.Topics, func(i, j int) bool {
		a, b := out.Topics[i], out.Topics[j]
		return a.Name < b.Name || a.Name == b.Name && a.Direction < b.Direction
	})
	seen := map[string]bool{}
	for _, s := range schemas {
		if !seen[s.Kind+":"+s.Name] {
			seen[s.Kind+":"+s.Name] = true
			out.Schemas = append(out.Schemas, s)
		}
	}
	sort.Slice(out.Schemas, func(i, j int) bool { return out.Schemas[i].Name < out.Schemas[j].Name })
	return out
}

// topicKey reports whether a property names topics by its last segment:
// topic, topics, or one ending so (order-topic, orderTopic); a setting
// about topics (auto-create-topics) names none, its value is no name.
func topicKey(k string) bool {
	last := strings.ToLower(k[strings.LastIndex(k, ".")+1:])
	return (strings.HasSuffix(last, "topic") || strings.HasSuffix(last, "topics")) && !strings.Contains(last, "create")
}

// setting is a value that is no topic name: a flag or a number.
var setting = regexp.MustCompile(`^(?i:true|false|\d+)$`)

// bindingDirection is a Spring Cloud Stream binding's way by its name:
// process-in-0 consumes, process-out-0 produces, input and output as the
// older names said.
func bindingDirection(b string) string {
	l := strings.ToLower(b)
	switch {
	case strings.Contains(l, "-in-") || strings.HasSuffix(l, "-in") || l == "input":
		return Consumes
	case strings.Contains(l, "-out-") || strings.HasSuffix(l, "-out") || l == "output":
		return Produces
	}
	return Declares
}

var (
	kafkaListener  = regexp.MustCompile(`@KafkaListener\s*\(`)
	producerRecord = regexp.MustCompile(`\bProducerRecord\s*(?:<[^()]*>)?\s*\(`)
	templateSend   = regexp.MustCompile(`(?i)template\s*\.\s*send\s*\(`)
	defaultTopic   = regexp.MustCompile(`\.setDefaultTopic\s*\(`)
	headerTopic    = regexp.MustCompile(`KafkaHeaders\.TOPIC\s*,`)
	// A StreamsBuilder's source: its receiver is no collection helper
	// (Arrays.stream, StreamSupport.stream), which a topology file uses too.
	streamsIn  = regexp.MustCompile(`(\w+)\s*\.\s*(?:stream|table|globalTable)\s*\(`)
	streamsOut = regexp.MustCompile(`\.to\s*\(`)
	clientSub  = regexp.MustCompile(`\.subscribe\s*\(`)
)

// notStreams are receivers of a stream( call that is no Kafka Streams
// source.
var notStreams = map[string]bool{"Arrays": true, "Stream": true, "StreamSupport": true, "IntStream": true, "LongStream": true,
	"Optional": true, "Collections": true, "Flux": true, "Mono": true}

// jvm reads a Java, Kotlin, Scala or Groovy file.
func jvm(p, src string, r coderead.Resolver, add func(name, dir, file string), used map[string]bool) {
	src = coderead.StripComments(src)
	r = r.In(p)
	mod := coderead.ModuleOf(p)
	put := func(dir string) func(string) {
		return func(expr string) {
			for _, v := range r.Values(mod, expr, used) {
				add(v, dir, p)
			}
		}
	}
	first := func(dir string) func(string) {
		return func(a string) { put(dir)(coderead.SplitTop(a, ',')[0]) }
	}
	coderead.Call(src, kafkaListener, func(a string) {
		if v, ok := coderead.Named(a, "topics"); ok {
			put(Consumes)(v)
		}
	})
	coderead.Call(src, producerRecord, first(Produces))
	// A JmsTemplate's or RabbitTemplate's send is no Kafka topic.
	if strings.Contains(strings.ToLower(src), "kafkatemplate") {
		coderead.Call(src, templateSend, func(a string) {
			// send(topic, ...) names one; send(message) carries it in a header.
			if parts := coderead.SplitTop(a, ','); len(parts) > 1 {
				put(Produces)(parts[0])
			}
		})
	}
	coderead.Call(src, defaultTopic, first(Produces))
	for _, m := range headerTopic.FindAllStringIndex(src, -1) {
		rest := src[m[1]:]
		if end := strings.IndexAny(rest, ")\n"); end > 0 {
			put(Produces)(rest[:end])
		}
	}
	if strings.Contains(src, "org.apache.kafka.streams") {
		for _, m := range streamsIn.FindAllStringSubmatchIndex(src, -1) {
			if notStreams[src[m[2]:m[3]]] {
				continue
			}
			if a, ok := coderead.Args(src, m[1]-1); ok {
				first(Consumes)(a)
			}
		}
		coderead.Call(src, streamsOut, first(Produces))
	}
	if strings.Contains(src, "org.apache.kafka.clients.consumer") {
		coderead.Call(src, clientSub, first(Consumes))
	}
}

var (
	jsSend      = regexp.MustCompile(`\.send\s*\(`)
	jsSubscribe = regexp.MustCompile(`\.subscribe\s*\(`)
	jsTopic     = regexp.MustCompile(`\btopics?\s*:\s*`)
)

// script reads a kafkajs producer's send and consumer's subscribe.
func script(p, src string, r coderead.Resolver, add func(name, dir, file string)) {
	if !strings.Contains(src, "kafkajs") {
		return
	}
	src = coderead.StripComments(src)
	r = r.In(p)
	each := func(dir string) func(string) {
		return func(a string) {
			m := jsTopic.FindStringIndex(a)
			if m == nil {
				return
			}
			rest := a[m[1]:]
			if v := coderead.SplitTop(rest, ','); len(v) > 0 {
				expr := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(v[0]), "}"))
				for _, name := range r.Values("", expr, nil) {
					add(name, dir, p)
				}
			}
		}
	}
	coderead.Call(src, jsSend, each(Produces))
	coderead.Call(src, jsSubscribe, each(Consumes))
}

// yamlTopics reads Kubernetes KafkaTopic resources and AsyncAPI channels:
// named, the way unknown.
func yamlTopics(p string, body []byte, add func(name, dir, file string)) {
	dec := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			return
		}
		if doc["kind"] == "KafkaTopic" {
			name := ""
			if spec, ok := doc["spec"].(map[string]any); ok {
				name, _ = spec["topicName"].(string)
			}
			if meta, ok := doc["metadata"].(map[string]any); ok && name == "" {
				name, _ = meta["name"].(string)
			}
			add(name, Declares, p)
		}
		if _, ok := doc["asyncapi"]; ok {
			if ch, ok := doc["channels"].(map[string]any); ok {
				for name, c := range ch {
					// AsyncAPI 3 keys a channel by an id, its topic the address.
					if m, ok := c.(map[string]any); ok {
						if addr, ok := m["address"].(string); ok && addr != "" {
							name = addr
						}
					}
					add(name, Declares, p)
				}
			}
		}
	}
}

// avro reads an .avsc: its record, or the records of a union file.
func avro(p string, body []byte) []Schema {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		list = []any{v}
	}
	var out []Schema
	for _, x := range list {
		m, ok := x.(map[string]any)
		if !ok || m["type"] != "record" {
			continue
		}
		name, _ := m["name"].(string)
		ns, _ := m["namespace"].(string)
		if name == "" {
			continue
		}
		if ns != "" && !strings.Contains(name, ".") {
			name = ns + "." + name
		}
		// A name goes into prompts and pages: Avro's own name rule, nothing else.
		if !schemaName.MatchString(name) {
			continue
		}
		out = append(out, Schema{Name: name, Kind: "avro", File: p})
	}
	return out
}

var schemaName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

var (
	avdlNamespace = regexp.MustCompile(`@namespace\s*\(\s*"([^"]+)"\s*\)\s*protocol`)
	avdlRecord    = regexp.MustCompile(`(?m)^\s*record\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	protoPackage  = regexp.MustCompile(`(?m)^package\s+([A-Za-z0-9_.]+)\s*;`)
	protoMessage  = regexp.MustCompile(`(?m)^message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
)

// avdl reads an Avro IDL's records, under its protocol's namespace.
func avdl(p string, body []byte) []Schema {
	ns := ""
	if m := avdlNamespace.FindSubmatch(body); m != nil {
		ns = string(m[1]) + "."
	}
	var out []Schema
	for _, m := range avdlRecord.FindAllSubmatch(body, -1) {
		if name := ns + string(m[1]); schemaName.MatchString(name) {
			out = append(out, Schema{Name: name, Kind: "avro", File: p})
		}
	}
	return out
}

// proto reads a .proto's top-level messages, under its package.
func proto(p string, body []byte) []Schema {
	pkg := ""
	if m := protoPackage.FindSubmatch(body); m != nil {
		pkg = string(m[1]) + "."
	}
	var out []Schema
	for _, m := range protoMessage.FindAllSubmatch(body, -1) {
		if name := pkg + string(m[1]); schemaName.MatchString(name) {
			out = append(out, Schema{Name: name, Kind: "proto", File: p})
		}
	}
	return out
}
