// Package deploy reads a workly.yaml, which declares queues and schedules,
// and works out the changes that make a server match it. The CLI's
// validate, plan and deploy commands are built on it.
package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/worklydev/workly/internal/api"
	"github.com/worklydev/workly/internal/config"
	"github.com/worklydev/workly/internal/store"
)

// Version is the file format version this package reads.
const Version = 1

// File is a valid workly.yaml. A section left out of the file isn't managed
// by it (HasQueues, HasSchedules); an empty one declares nothing.
type File struct {
	Path                    string
	Queues                  []Queue
	Schedules               []Schedule
	HasQueues, HasSchedules bool
}

// Queue is a declared queue: Config is the PUT body, settings what it
// becomes with defaults applied.
type Queue struct {
	Name     string
	Config   api.QueueConfig
	settings store.QueueConfig
}

type Schedule struct {
	Name     string
	Config   api.ScheduleConfig
	settings store.ScheduleConfig
}

// Problem is one thing wrong with a file: where, which field (a path such as
// schedules.nightly.cron) and what.
type Problem struct {
	Line    int
	Field   string
	Message string
}

// InvalidError lists every problem found in a file.
type InvalidError struct {
	Path     string
	Problems []Problem
}

func (e *InvalidError) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		loc := e.Path
		if p.Line > 0 {
			loc = fmt.Sprintf("%s:%d", e.Path, p.Line)
		}
		if p.Field != "" {
			loc += ": " + p.Field
		}
		lines[i] = loc + ": " + p.Message
	}
	return strings.Join(lines, "\n")
}

// Load reads and validates the file at path, replacing ${NAME} in values
// with environment variables looked up by getenv (os.LookupEnv in the CLI).
func Load(path string, getenv func(string) (string, bool)) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, data, getenv)
}

// Parse is Load for a file already read.
func Parse(path string, data []byte, getenv func(string) (string, bool)) (*File, error) {
	p := &parser{getenv: getenv}
	f := p.file(data)
	if len(p.problems) > 0 {
		slices.SortStableFunc(p.problems, func(a, b Problem) int { return a.Line - b.Line })
		return nil, &InvalidError{path, p.problems}
	}
	f.Path = path
	return f, nil
}

type parser struct {
	getenv   func(string) (string, bool)
	problems []Problem
}

func (p *parser) add(n *yaml.Node, field, format string, args ...any) {
	p.problems = append(p.problems, Problem{n.Line, field, fmt.Sprintf(format, args...)})
}

// hints answer keys people reach for that Workly spells differently or
// doesn't have.
var hints = map[string]string{
	"concurrency": `use max_concurrency`,
	"rate_limit":  `use rate_per_second`,
	"rate":        `use rate_per_second`,
	"retries":     `use max_attempts, which counts the first attempt too`,
	"timeout":     `use timeout_seconds`,
	"queue":       `schedules call their url directly; they don't use a queue`,
	"destructive": `workly deploy never deletes anything; remove queues and schedules with workly queues rm and workly schedules rm`,
	"paused":      `deploying doesn't pause or resume; use workly queues pause or workly schedules pause`,
}

func (p *parser) file(data []byte) *File {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		p.problems = append(p.problems, Problem{Message: strings.TrimPrefix(err.Error(), "yaml: ")})
		return nil
	}
	if len(doc.Content) == 0 {
		p.problems = append(p.problems, Problem{Message: "the file is empty: start it with version: 1"})
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		p.add(root, "", "expected a mapping with version, queues and schedules")
		return nil
	}
	f := &File{}
	var version *yaml.Node
	for _, kv := range p.mapping(root, "") {
		switch key := kv[0].Value; key {
		case "version":
			version = kv[1]
		case "queues":
			f.HasQueues = true
			p.queues(f, kv[1])
		case "schedules":
			f.HasSchedules = true
			p.schedules(f, kv[1])
		default:
			p.unknown(kv[0], key, []string{"version", "queues", "schedules"})
		}
	}
	var v int
	switch {
	case version == nil:
		p.problems = append(p.problems, Problem{Line: root.Line, Field: "version", Message: "required: add version: 1"})
	case version.Decode(&v) != nil || v != Version:
		p.add(version, "version", "%s isn't supported: this version of workly reads version %d", version.Value, Version)
	}
	return f
}

func (p *parser) queues(f *File, n *yaml.Node) {
	for _, kv := range p.mapping(n, "queues") {
		q := Queue{Name: kv[0].Value}
		path := "queues." + q.Name
		if err := api.ValidateName("queue", q.Name); err != nil {
			p.add(kv[0], path, "%v", err)
			continue
		}
		given, ok := p.fields(kv[1], path, &q.Config)
		if !ok {
			continue
		}
		var err error
		if q.settings, err = api.QueueSettings(q.Config); err != nil {
			p.settingsError(kv[0], path, given, err)
			continue
		}
		f.Queues = append(f.Queues, q)
	}
}

func (p *parser) schedules(f *File, n *yaml.Node) {
	for _, kv := range p.mapping(n, "schedules") {
		s := Schedule{Name: kv[0].Value}
		path := "schedules." + s.Name
		if err := api.ValidateName("schedule", s.Name); err != nil {
			p.add(kv[0], path, "%v", err)
			continue
		}
		given, ok := p.fields(kv[1], path, &s.Config)
		if !ok {
			continue
		}
		var err error
		if s.settings, err = api.ScheduleSettings(s.Config, config.Default().MaxPayloadBytes); err != nil {
			p.settingsError(kv[0], path, given, err)
			continue
		}
		f.Schedules = append(f.Schedules, s)
	}
}

// mapping returns a mapping's key and value nodes, reporting duplicate keys.
// An empty value (`queues:` with nothing under it) is an empty mapping.
func (p *parser) mapping(n *yaml.Node, path string) [][2]*yaml.Node {
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		p.add(n, path, "expected a mapping")
		return nil
	}
	var out [][2]*yaml.Node
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if seen[key.Value] {
			p.add(key, join(path, key.Value), "defined more than once")
			continue
		}
		seen[key.Value] = true
		out = append(out, [2]*yaml.Node{key, val})
	}
	return out
}

// settingsError reports an error from the API's validation. Its messages
// start with the field they're about ("cron: …", "max_attempts must be …"),
// which then points at that field.
func (p *parser) settingsError(key *yaml.Node, path string, given map[string]*yaml.Node, err error) {
	msg := err.Error()
	for name, n := range given {
		if rest, ok := strings.CutPrefix(msg, name+": "); ok {
			p.add(n, path+"."+name, "%s", rest)
			return
		}
		if rest, ok := strings.CutPrefix(msg, name+" "); ok {
			p.add(n, path+"."+name, "%s", rest)
			return
		}
	}
	p.add(key, path, "%s", msg)
}

// fields decodes a mapping into dst, an API request struct, by its JSON
// field names. It returns the fields given, and whether all were valid.
func (p *parser) fields(n *yaml.Node, path string, dst any) (map[string]*yaml.Node, bool) {
	before := len(p.problems)
	v := reflect.ValueOf(dst).Elem()
	byName := map[string]int{}
	var names, required []string
	for i := range v.NumField() {
		name, opts, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
		byName[name] = i
		names = append(names, name)
		if opts != "omitempty" {
			required = append(required, name)
		}
	}
	given := map[string]*yaml.Node{}
	for _, kv := range p.mapping(n, path) {
		key, field := kv[0].Value, join(path, kv[0].Value)
		i, ok := byName[key]
		if !ok {
			p.unknown(kv[0], field, names)
			continue
		}
		given[key] = kv[1]
		if !p.expand(kv[1], field) {
			continue
		}
		var raw any
		if err := kv[1].Decode(&raw); err != nil {
			p.add(kv[1], field, "%v", err)
			continue
		}
		b, err := json.Marshal(raw)
		if err != nil {
			p.add(kv[1], field, "mapping keys must be strings")
			continue
		}
		ptr := reflect.New(v.Field(i).Type())
		if json.Unmarshal(b, ptr.Interface()) != nil {
			p.add(kv[1], field, "must be %s", describe(v.Field(i).Type()))
			continue
		}
		v.Field(i).Set(ptr.Elem())
	}
	for _, name := range required {
		if given[name] == nil {
			p.add(n, join(path, name), "required")
		}
	}
	return given, len(p.problems) == before
}

func describe(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int:
		return "a whole number"
	case reflect.Float64:
		return "a number"
	case reflect.Bool:
		return "true or false"
	case reflect.Map:
		return "a mapping of names to strings"
	}
	return "a string"
}

func (p *parser) unknown(key *yaml.Node, field string, known []string) {
	if hint, ok := hints[key.Value]; ok && (key.Value != "queue" || strings.HasPrefix(field, "schedules.")) {
		p.add(key, field, "unknown field: %s", hint)
	} else if s := suggest(key.Value, known); s != "" {
		p.add(key, field, "unknown field: did you mean %s?", s)
	} else {
		p.add(key, field, "unknown field: expected one of %s", strings.Join(known, ", "))
	}
}

var envRef = regexp.MustCompile(`\$\{([^}]*)\}`)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// expand replaces ${NAME} in string values, in place. An unquoted value is
// read again afterwards, so `max_concurrency: ${CONCURRENCY}` is a number.
func (p *parser) expand(n *yaml.Node, field string) bool {
	ok := true
	switch n.Kind {
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			ok = p.expand(n.Content[i], field+"."+n.Content[i-1].Value) && ok
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			ok = p.expand(c, fmt.Sprintf("%s[%d]", field, i)) && ok
		}
	case yaml.ScalarNode:
		if n.ShortTag() != "!!str" || !strings.Contains(n.Value, "${") {
			return true
		}
		n.Value = envRef.ReplaceAllStringFunc(n.Value, func(ref string) string {
			name := ref[2 : len(ref)-1]
			if !envName.MatchString(name) {
				p.add(n, field, "%s isn't a valid environment variable reference", ref)
				ok = false
				return ref
			}
			val, set := p.getenv(name)
			if !set {
				p.add(n, field, "environment variable %s isn't set", name)
				ok = false
			}
			return val
		})
		if n.Style == 0 {
			n.Tag = ""
		}
	}
	return ok
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// suggest returns the known name closest to s, if one is close enough to be
// a typo.
func suggest(s string, known []string) string {
	best, bestDist := "", len(s)/3+2
	for _, k := range known {
		short := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(k, "max_"), "min_"), "_seconds")
		if d := min(distance(s, k), distance(s, short)); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
