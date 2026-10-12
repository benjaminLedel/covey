// Package workflow holds the rules of covey's workflows (#561): what a
// definition may say, how a case moves from step to step, who gets a step,
// and when a person has to decide.
//
// A workflow is organisation-wide. It addresses roles, not people: who fills
// a role is decided when the step starts. The package has no database and no
// wiring — the colleagues, the task dispatch, target-system actions and the
// chat sit behind the interfaces in engine.go, so the rules can be tested on
// their own.
package workflow

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Kind is a building block's type. The names are the file format's, so they
// stay as a parser reads them.
type Kind string

const (
	KindTrigger   Kind = "trigger"
	KindAgent     Kind = "agent"     // a colleague from a role does the step
	KindAI        Kind = "ai"        // a short model call without a colleague
	KindCondition Kind = "condition" // branches on an earlier step's result
	KindAction    Kind = "action"    // covey itself acts in a target system
	KindApproval  Kind = "approval"  // a person decides, in their chat
	KindWait      Kind = "wait"      // until an event arrives or time runs out
	KindNotify    Kind = "notify"    // a message into a person's chat
	KindEnd       Kind = "end"
)

// Mode is what a workflow does to the cases it sees.
type Mode string

const (
	// ModeOff starts nothing; the colleagues work by their own playbooks.
	ModeOff Mode = "off"
	// ModeWatch records where a case stands, read from the target system,
	// without assigning or acting.
	ModeWatch Mode = "watch"
	// ModeSteer starts the steps, picks colleagues and hands over.
	ModeSteer Mode = "steer"
)

// Exit names with a meaning of their own. Any other exit of a condition is a
// value of the field it checks; of an approval, one of its options.
const (
	ExitOut     = "out"     // the only exit of a step with one way on
	ExitElse    = "else"    // a condition's value that no other exit names
	ExitLimit   = "limit"   // a loop's rounds are used up
	ExitTimeout = "timeout" // a wait or an approval ran past its maximum
)

// Pick is how a colleague is chosen from a role.
type Pick string

const (
	PickLoad  Pick = "load"  // whoever has the fewest cases in work
	PickRound Pick = "round" // in turn
)

// Definition is one workflow as written.
type Definition struct {
	Name  string
	Scope string
	Owner string
	Mode  Mode
	// Roles maps a role to the colleagues who hold it, in the order "in
	// turn" walks them.
	Roles map[string][]string
	Steps map[string]*Step
	// Order is the steps in the order of the file, for stable output.
	Order []string
}

// Step is one building block. Which fields count depends on Kind.
type Step struct {
	ID    string
	Kind  Kind
	Title string
	// Next maps an exit to the step it leads to.
	Next map[string]string

	// colleague and AI steps
	Role        string
	Pick        Pick
	Sticky      bool   // further rounds go to the person who had the case
	MaxParallel int    // per colleague; 0 means no limit
	NotSameAs   string // a colleague step whose person may not take this one
	Optional    bool   // skip when nobody holds the role
	Task        string // with {{step.field}} placeholders
	Result      []string
	QuestionsTo string
	Limit       time.Duration

	// condition
	On        string // "step.field"
	MaxRounds int

	// action
	System  string
	Do      string
	Target  string
	Value   string
	Comment string

	// approval
	Who     string
	Ask     string
	Options []string
	Wait    time.Duration

	// wait
	Until string // the event that ends the wait
	Max   time.Duration

	// notify
	To   string
	Via  string
	Text string
}

// The file as YAML reads it. Durations and the two shapes of `next` are
// converted by hand, so a mistake names the step it is in.
type fileStep struct {
	Type        string    `yaml:"type"`
	Title       string    `yaml:"title"`
	Next        yaml.Node `yaml:"next"`
	Role        string    `yaml:"role"`
	Pick        string    `yaml:"pick"`
	Sticky      bool      `yaml:"sticky"`
	MaxParallel int       `yaml:"max_parallel"`
	NotSameAs   string    `yaml:"not_same_as"`
	Optional    bool      `yaml:"optional"`
	Task        string    `yaml:"task"`
	Prompt      string    `yaml:"prompt"`
	Result      []string  `yaml:"result"`
	QuestionsTo string    `yaml:"questions_to"`
	Limit       string    `yaml:"limit"`
	On          string    `yaml:"on"`
	MaxRounds   int       `yaml:"max_rounds"`
	System      string    `yaml:"system"`
	Do          string    `yaml:"do"`
	Target      string    `yaml:"target"`
	Value       string    `yaml:"value"`
	Comment     string    `yaml:"comment"`
	Who         string    `yaml:"who"`
	Ask         string    `yaml:"ask"`
	Wait        string    `yaml:"wait"`
	Until       string    `yaml:"until"`
	Max         string    `yaml:"max"`
	To          string    `yaml:"to"`
	Via         string    `yaml:"via"`
	Text        string    `yaml:"text"`
}

type file struct {
	Workflow string              `yaml:"workflow"`
	Scope    string              `yaml:"scope"`
	Owner    string              `yaml:"owner"`
	Mode     string              `yaml:"mode"`
	Roles    map[string][]string `yaml:"roles"`
	Steps    yaml.Node           `yaml:"steps"`
}

// Parse reads a definition. It checks the shape only; what the definition
// means is Check's business.
func Parse(src []byte) (*Definition, error) {
	var f file
	if err := yaml.Unmarshal(src, &f); err != nil {
		return nil, fmt.Errorf("workflow: %w", err)
	}
	d := &Definition{
		Name:  f.Workflow,
		Scope: f.Scope,
		Owner: f.Owner,
		Mode:  Mode(f.Mode),
		Roles: f.Roles,
		Steps: map[string]*Step{},
	}
	if d.Mode == "" {
		d.Mode = ModeOff
	}
	switch d.Mode {
	case ModeOff, ModeWatch, ModeSteer:
	default:
		return nil, fmt.Errorf("workflow: unknown mode %q", d.Mode)
	}
	if f.Steps.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("workflow: steps must be a mapping of step ids")
	}
	for i := 0; i+1 < len(f.Steps.Content); i += 2 {
		id := f.Steps.Content[i].Value
		var fs fileStep
		if err := f.Steps.Content[i+1].Decode(&fs); err != nil {
			return nil, fmt.Errorf("workflow: step %s: %w", id, err)
		}
		s, err := fs.step(id)
		if err != nil {
			return nil, fmt.Errorf("workflow: step %s: %w", id, err)
		}
		if _, dup := d.Steps[id]; dup {
			return nil, fmt.Errorf("workflow: step %s is defined twice", id)
		}
		d.Steps[id] = s
		d.Order = append(d.Order, id)
	}
	return d, nil
}

func (fs fileStep) step(id string) (*Step, error) {
	s := &Step{
		ID:          id,
		Kind:        Kind(fs.Type),
		Title:       fs.Title,
		Role:        fs.Role,
		Pick:        Pick(fs.Pick),
		Sticky:      fs.Sticky,
		MaxParallel: fs.MaxParallel,
		NotSameAs:   fs.NotSameAs,
		Optional:    fs.Optional,
		Task:        fs.Task,
		Result:      fs.Result,
		QuestionsTo: fs.QuestionsTo,
		On:          fs.On,
		MaxRounds:   fs.MaxRounds,
		System:      fs.System,
		Do:          fs.Do,
		Target:      fs.Target,
		Value:       fs.Value,
		Comment:     fs.Comment,
		Who:         fs.Who,
		Ask:         fs.Ask,
		Until:       fs.Until,
		To:          fs.To,
		Via:         fs.Via,
		Text:        fs.Text,
	}
	switch s.Kind {
	case KindTrigger, KindAgent, KindAI, KindCondition, KindAction, KindApproval, KindWait, KindNotify, KindEnd:
	default:
		return nil, fmt.Errorf("unknown type %q", fs.Type)
	}
	if s.Kind == KindAI && s.Task == "" {
		s.Task = fs.Prompt
	}
	if s.Pick == "" {
		s.Pick = PickLoad
	}
	if s.Pick != PickLoad && s.Pick != PickRound {
		return nil, fmt.Errorf("unknown pick %q", fs.Pick)
	}
	var err error
	if s.Limit, err = duration(fs.Limit); err != nil {
		return nil, fmt.Errorf("limit: %w", err)
	}
	if s.Wait, err = duration(fs.Wait); err != nil {
		return nil, fmt.Errorf("wait: %w", err)
	}
	if s.Max, err = duration(fs.Max); err != nil {
		return nil, fmt.Errorf("max: %w", err)
	}
	if s.Next, err = exits(fs.Next); err != nil {
		return nil, fmt.Errorf("next: %w", err)
	}
	// An approval's options are its exits, minus the timeout.
	if s.Kind == KindApproval {
		for _, e := range orderedExits(fs.Next) {
			if e != ExitTimeout {
				s.Options = append(s.Options, e)
			}
		}
	}
	return s, nil
}

// exits reads `next`: a step id for the one way on, or a mapping of exits.
func exits(n yaml.Node) (map[string]string, error) {
	out := map[string]string{}
	switch n.Kind {
	case 0:
		return out, nil
	case yaml.ScalarNode:
		out[ExitOut] = n.Value
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i].Value, n.Content[i+1]
			if v.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("exit %q must name one step", k)
			}
			out[k] = v.Value
		}
	default:
		return nil, fmt.Errorf("must be a step id or a mapping of exits")
	}
	return out, nil
}

func orderedExits(n yaml.Node) []string {
	var keys []string
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			keys = append(keys, n.Content[i].Value)
		}
	}
	return keys
}

// duration reads Go durations and whole days ("14d"), which Go does not.
func duration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%q is not a number of days", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("%q is negative", s)
	}
	return d, nil
}
