// Package messaging reads which message topics a repository produces to
// and consumes from, and which message schemas it defines: Kafka listeners
// and templates, Kafka Streams, plain clients, Spring Cloud Stream
// bindings, kafkajs, topic resources and AsyncAPI files, Avro and Protobuf
// schemas. Pure: bytes in, facts out, every rule a table test. A topic
// name comes from a literal, a constant or a property code can resolve;
// one it cannot (a method parameter, a property set per stage) is left
// out, never guessed. Which repository talks to which is joined from these
// at read time (store), never by a model.
package messaging

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
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
func Config(p string) bool { return baseConfig.MatchString(path.Base(p)) }

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
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	r := resolver{config: map[string]map[string]string{}, consts: map[string]string{}}
	// YAML first, .properties over it: Spring's order for one location.
	learnOrder := append([]string{}, paths...)
	sort.SliceStable(learnOrder, func(i, j int) bool {
		return !strings.HasSuffix(learnOrder[i], ".properties") && strings.HasSuffix(learnOrder[j], ".properties")
	})
	for _, p := range learnOrder {
		r.learn(p, files[p])
	}
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
	for _, mod := range sortedKeys(r.config) {
		cfg := r.config[mod]
		for _, k := range sortedKeys(cfg) {
			v := cfg[k]
			if b, ok := strings.CutPrefix(k, "spring.cloud.stream.bindings."); ok && strings.HasSuffix(b, ".destination") {
				dir := bindingDirection(strings.TrimSuffix(b, ".destination"))
				for _, t := range strings.Split(v, ",") {
					add(strings.TrimSpace(t), dir, r.keyFile[mod+"\x00"+k])
				}
				continue
			}
			if topicKey(k) && !used[k] && !strings.Contains(v, "${") {
				for _, t := range strings.Split(v, ",") {
					if t = strings.TrimSpace(t); !setting.MatchString(t) {
						add(t, Declares, r.keyFile[mod+"\x00"+k])
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
		out.Topics = append(out.Topics, Topic{Name: k[0], Direction: k[1], Files: sortedKeys(fs)})
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

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// resolver turns an expression in code into a topic name: a literal, a
// constant, a property, or their concatenation.
type resolver struct {
	// config is each module's application properties, flattened; a module
	// is the path before src/main/ ("" at the root). keyFile is the file
	// each module's key came from, by module NUL key.
	config  map[string]map[string]string
	keyFile map[string]string
	// consts are the String constants by simple name; one named twice with
	// two values is none. fileConsts are each file's own, read first: a
	// per-class TOPIC is the commonest constant and would collide.
	consts     map[string]string
	fileConsts map[string]map[string]string
	// getters are what a method without parameters returns, by its name:
	// every override's (a base class sends to getTopic(), each subclass
	// names its own), all of them the repository's, each read in its file.
	getters map[string][]fileExpr
	// file is the file an expression is read in.
	file string
}

// fileExpr is an expression and the file it stands in.
type fileExpr struct{ file, expr string }

var (
	javaGetter   = regexp.MustCompile(`String\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*\)\s*\{\s*return\s+([^;{}]+);\s*\}`)
	kotlinGetter = regexp.MustCompile(`fun\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*\)\s*(?::\s*String\s*)?=\s*([^\n;]+)`)
	getterCall   = regexp.MustCompile(`^(?:this\.)?([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*\)$`)
)

// moduleOf is the module a path sits in: what comes before src/main/.
func moduleOf(p string) string {
	if i := strings.Index(p, "src/main/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// baseConfig is the application properties without a profile: the topic
// a stage overrides is no one name.
var baseConfig = regexp.MustCompile(`^(application|bootstrap)\.(ya?ml|properties)$`)

var (
	javaConst   = regexp.MustCompile(`(?:static\s+final|final\s+static)\s+String\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"\\]*)"\s*;`)
	kotlinConst = regexp.MustCompile(`const\s+val\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*String\s*)?=\s*"([^"\\$]*)"`)
	scriptConst = regexp.MustCompile(`(?:export\s+)?const\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*string\s*)?=\s*['"]([^'"\\$]*)['"]`)
)

func (r *resolver) learn(p string, body []byte) {
	if baseConfig.MatchString(path.Base(p)) {
		mod := moduleOf(p)
		if r.config[mod] == nil {
			r.config[mod] = map[string]string{}
		}
		if r.keyFile == nil {
			r.keyFile = map[string]string{}
		}
		one := map[string]string{}
		if strings.HasSuffix(p, ".properties") {
			properties(body, one)
		} else {
			flatYAML(body, one)
		}
		for k, v := range one {
			r.config[mod][k] = v
			r.keyFile[mod+"\x00"+k] = p
		}
		return
	}
	var re, getter *regexp.Regexp
	switch strings.ToLower(path.Ext(p)) {
	case ".java", ".groovy", ".scala":
		re, getter = javaConst, javaGetter
	case ".kt":
		re, getter = kotlinConst, kotlinGetter
	case ".ts", ".js", ".mjs":
		re = scriptConst
	default:
		return
	}
	src := stripComments(string(body))
	own := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		own[m[1]] = m[2]
		if old, ok := r.consts[m[1]]; ok && old != m[2] {
			r.consts[m[1]] = ""
			continue
		}
		r.consts[m[1]] = m[2]
	}
	if r.fileConsts == nil {
		r.fileConsts = map[string]map[string]string{}
	}
	r.fileConsts[p] = own
	if getter == nil {
		return
	}
	if r.getters == nil {
		r.getters = map[string][]fileExpr{}
	}
	for _, m := range getter.FindAllStringSubmatch(src, -1) {
		r.getters[m[1]] = append(r.getters[m[1]], fileExpr{file: p, expr: strings.TrimSpace(m[2])})
	}
}

// in is the resolver reading expressions in file p.
func (r resolver) in(p string) resolver {
	r.file = p
	return r
}

// property is a key's value for the module: its own, else the one value
// every module that sets the key agrees on.
func (r *resolver) property(mod, key string) (string, bool) {
	if v, ok := r.config[mod][key]; ok {
		return v, true
	}
	var found string
	seen := false
	for _, cfg := range r.config {
		v, ok := cfg[key]
		if !ok {
			continue
		}
		if seen && v != found {
			return "", false
		}
		found, seen = v, true
	}
	return found, found != ""
}

var placeholder = regexp.MustCompile(`\$\{([^}:]+)(?::([^}]*))?\}`)

// fill replaces every ${key} or ${key:default} in s; false when one has
// no value.
func (r *resolver) fill(mod, s string, used map[string]bool) (string, bool) {
	ok := true
	out := placeholder.ReplaceAllStringFunc(s, func(m string) string {
		sub := placeholder.FindStringSubmatch(m)
		if v, found := r.property(mod, sub[1]); found {
			if used != nil {
				used[sub[1]] = true
			}
			return v
		}
		if strings.Contains(m, ":") {
			return sub[2]
		}
		ok = false
		return ""
	})
	return out, ok && !strings.Contains(out, "${")
}

// value is an expression's name: string literals, constants (by their
// simple name) and properties in them, joined by +; false for anything
// else (a call, a parameter, a SpEL expression).
func (r *resolver) value(mod, expr string, used map[string]bool) (string, bool) {
	var b strings.Builder
	for _, part := range splitTop(expr, '+') {
		part = strings.TrimSpace(part)
		switch {
		case len(part) >= 2 && (part[0] == '"' || part[0] == '\'' || part[0] == '`') && part[len(part)-1] == part[0]:
			// Kotlin writes a Spring placeholder "\${key}".
			s, ok := r.fill(mod, strings.ReplaceAll(part[1:len(part)-1], `\$`, "$"), used)
			if !ok || strings.Contains(s, "#{") {
				return "", false
			}
			b.WriteString(s)
		case identifier.MatchString(part):
			name := part[strings.LastIndex(part, ".")+1:]
			v, ok := r.fileConsts[r.file][name]
			if !ok || strings.Contains(part, ".") {
				v, ok = r.consts[name]
			}
			if !ok || v == "" {
				return "", false
			}
			s, ok := r.fill(mod, v, used)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		default:
			return "", false
		}
	}
	return b.String(), b.Len() > 0
}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// values is a list expression's names: a single expression, an array
// literal ({a, b}, [a, b], arrayOf(a, b)) or a collection factory
// (List.of, Arrays.asList, listOf, ...). Each name resolved on its own;
// one that does not resolve is left out.
func (r *resolver) values(mod, expr string, used map[string]bool) []string {
	expr = strings.TrimSpace(expr)
	for _, f := range []string{"List.of", "Set.of", "Arrays.asList", "Collections.singletonList", "Collections.singleton",
		"listOf", "setOf", "arrayOf", "Pattern.compile"} {
		if strings.HasPrefix(expr, f+"(") && strings.HasSuffix(expr, ")") {
			if f == "Pattern.compile" {
				return nil
			}
			expr = expr[len(f)+1 : len(expr)-1]
			break
		}
	}
	if n := len(expr); n >= 2 && (expr[0] == '{' && expr[n-1] == '}' || expr[0] == '[' && expr[n-1] == ']') {
		expr = expr[1 : n-1]
	}
	var out []string
	for _, e := range splitTop(expr, ',') {
		if m := getterCall.FindStringSubmatch(strings.TrimSpace(e)); m != nil {
			for _, ret := range r.getters[m[1]] {
				in := r.in(ret.file)
				if v, ok := in.value(mod, ret.expr, used); ok {
					out = append(out, v)
				}
			}
			continue
		}
		if v, ok := r.value(mod, e, used); ok {
			out = append(out, v)
		}
	}
	return out
}

// inString steps a scan through string literals: true while s[*i] is in
// one (or opens or closes it), the index moved past an escape.
func inString(s string, i *int, quote *byte) bool {
	c := s[*i]
	if *quote != 0 {
		switch {
		case c == '\\':
			*i++
		case c == *quote:
			*quote = 0
		case c == '\n' && *quote != '`':
			// A quote left open (a Scala symbol, a stray apostrophe)
			// ends with its line, never swallows the file.
			*quote = 0
			return false
		}
		return true
	}
	if c == '"' || c == '\'' || c == '`' {
		*quote = c
		return true
	}
	return false
}

// splitTop splits s at sep outside brackets and string literals.
func splitTop(s string, sep byte) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		if inString(s, &i, &quote) {
			continue
		}
		switch c := s[i]; c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		default:
			if c == sep && depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// args is the text between the parenthesis at open and its match.
func args(s string, open int) (string, bool) {
	depth := 0
	var quote byte
	for i := open; i < len(s); i++ {
		if inString(s, &i, &quote) {
			continue
		}
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : i], true
			}
		}
	}
	return "", false
}

// call finds every call matching re (its match ends at the opening
// parenthesis) and hands its argument list on.
func call(src string, re *regexp.Regexp, each func(args string)) {
	for _, m := range re.FindAllStringIndex(src, -1) {
		if a, ok := args(src, m[1]-1); ok {
			each(a)
		}
	}
}

// named is the value of the argument name= in an annotation's arguments.
func named(a, name string) (string, bool) {
	for _, part := range splitTop(a, ',') {
		k, v, ok := strings.Cut(part, "=")
		if ok && strings.TrimSpace(k) == name {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
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
func jvm(p, src string, r resolver, add func(name, dir, file string), used map[string]bool) {
	src = stripComments(src)
	r = r.in(p)
	mod := moduleOf(p)
	put := func(dir string) func(string) {
		return func(expr string) {
			for _, v := range r.values(mod, expr, used) {
				add(v, dir, p)
			}
		}
	}
	first := func(dir string) func(string) {
		return func(a string) { put(dir)(splitTop(a, ',')[0]) }
	}
	call(src, kafkaListener, func(a string) {
		if v, ok := named(a, "topics"); ok {
			put(Consumes)(v)
		}
	})
	call(src, producerRecord, first(Produces))
	// A JmsTemplate's or RabbitTemplate's send is no Kafka topic.
	if strings.Contains(strings.ToLower(src), "kafkatemplate") {
		call(src, templateSend, func(a string) {
			// send(topic, ...) names one; send(message) carries it in a header.
			if parts := splitTop(a, ','); len(parts) > 1 {
				put(Produces)(parts[0])
			}
		})
	}
	call(src, defaultTopic, first(Produces))
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
			if a, ok := args(src, m[1]-1); ok {
				first(Consumes)(a)
			}
		}
		call(src, streamsOut, first(Produces))
	}
	if strings.Contains(src, "org.apache.kafka.clients.consumer") {
		call(src, clientSub, first(Consumes))
	}
}

var (
	jsSend      = regexp.MustCompile(`\.send\s*\(`)
	jsSubscribe = regexp.MustCompile(`\.subscribe\s*\(`)
	jsTopic     = regexp.MustCompile(`\btopics?\s*:\s*`)
)

// script reads a kafkajs producer's send and consumer's subscribe.
func script(p, src string, r resolver, add func(name, dir, file string)) {
	if !strings.Contains(src, "kafkajs") {
		return
	}
	src = stripComments(src)
	r = r.in(p)
	each := func(dir string) func(string) {
		return func(a string) {
			m := jsTopic.FindStringIndex(a)
			if m == nil {
				return
			}
			rest := a[m[1]:]
			if v := splitTop(rest, ','); len(v) > 0 {
				expr := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(v[0]), "}"))
				for _, name := range r.values("", expr, nil) {
					add(name, dir, p)
				}
			}
		}
	}
	call(src, jsSend, each(Produces))
	call(src, jsSubscribe, each(Consumes))
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

// flatYAML flattens a YAML file's scalars into dotted keys: its documents
// without a profile, as baseConfig its files; one for a profile
// (spring.config.activate.on-profile, spring.profiles) is a stage's.
func flatYAML(body []byte, out map[string]string) {
	dec := yaml.NewDecoder(bytes.NewReader(body))
	for {
		var doc any
		if err := dec.Decode(&doc); err != nil {
			return
		}
		one := map[string]string{}
		flatten("", doc, one)
		if _, ok := one["spring.config.activate.on-profile"]; ok {
			continue
		}
		if _, ok := one["spring.profiles"]; ok {
			continue
		}
		for k, v := range one {
			out[k] = v
		}
	}
}

// stripComments blanks // and /* */ comments outside string literals,
// keeping every offset: an apostrophe in a comment would otherwise open a
// literal for the scan of a call's arguments.
func stripComments(src string) string {
	b := []byte(src)
	var quote byte
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote || c == '\n' && quote != '`' {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			for ; i < len(b) && (b[i] != '*' || i+1 >= len(b) || b[i+1] != '/'); i++ {
				if b[i] != '\n' {
					b[i] = ' '
				}
			}
			if i+1 < len(b) {
				b[i], b[i+1] = ' ', ' '
				i++
			}
		}
	}
	return string(b)
}

func flatten(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten(key, x, out)
		}
	case string:
		out[prefix] = t
	case []any:
		var parts []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			out[prefix] = strings.Join(parts, ",")
		}
	}
}

// properties reads key=value lines of a .properties file.
func properties(body []byte, out map[string]string) {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			k, v, ok = strings.Cut(line, ":")
		}
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
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
