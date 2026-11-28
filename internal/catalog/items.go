package catalog

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Kind-file names and limits from section 3.10. The file name is the kind.
const (
	WritingFileName  = "writing.yaml"
	QuizFileName     = "quiz.yaml"
	StoryFileName    = "story.yaml"
	FeedbackFileName = "feedback.yaml"

	MaxPromptBytes    = 8192
	MaxSceneBytes     = 4096
	MinCriteria       = 2
	MaxCriteria       = 6
	MaxCriterionChars = 120
	maxLocalIDLen     = 32
	MinQuestions      = 3
	MaxQuestions      = 10
	MaxQuestionBytes  = 1000
	MinChoices        = 2
	MaxQuizChoices    = 5
	MaxChoiceChars    = 200
	MaxNoteChars      = 300
	MaxExplainBytes   = 1000
	MaxFromChars      = 40
	MaxStoryChoices   = 4
	MaxReplyChars     = 300
	MaxReactionBytes  = 1000
)

// Story verdicts, authored and drawn as a badge.
const (
	VerdictGood = "good"
	VerdictOkay = "okay"
	VerdictPoor = "poor"
)

// reservedSlugs are the item-route action words, refused as the second part of any id.
var reservedSlugs = map[string]bool{"draft": true, "submit": true, "answers": true, "choice": true}

func reservedSlug(id string) bool {
	_, second, _ := strings.Cut(id, "/")
	return reservedSlugs[second]
}

// Item holds the fields every non-ticket kind file shares with ticket.yaml.
type Item struct {
	ID         string   `yaml:"id" json:"-"`
	Title      string   `yaml:"title" json:"-"`
	Role       string   `yaml:"role" json:"-"`
	Order      int      `yaml:"order" json:"-"`
	EstMinutes int      `yaml:"est_minutes" json:"-"`
	Tags       []string `yaml:"tags" json:"-"`
}

// Criterion is one rubric line of a writing or feedback item.
type Criterion struct {
	ID   string `yaml:"id" json:"id"`
	Text string `yaml:"text" json:"text"`
}

// From says who sends the scene of a story or feedback item.
type From struct {
	Name string `yaml:"name" json:"name"`
	Role string `yaml:"role" json:"role"`
}

// Writing is a writing task (writing.yaml).
type Writing struct {
	Item       `yaml:",inline"`
	Prompt     string      `yaml:"prompt" json:"prompt"`
	Rubric     []Criterion `yaml:"rubric" json:"rubric"`
	AwardsMark string      `yaml:"awards_mark" json:"-"`
}

// QuizChoice is one answer of a question; Note says what that choice does.
type QuizChoice struct {
	Text string `yaml:"text" json:"text"`
	Note string `yaml:"note" json:"note"`
}

// Question is one quiz question; Answer is the 0-based index of the right choice.
type Question struct {
	ID      string       `yaml:"id" json:"id"`
	Text    string       `yaml:"text" json:"text"`
	Choices []QuizChoice `yaml:"choices" json:"choices"`
	Answer  *int         `yaml:"answer" json:"answer"`
	Explain string       `yaml:"explain" json:"explain,omitempty"`
}

// Quiz is a quiz (quiz.yaml).
type Quiz struct {
	Item      `yaml:",inline"`
	Questions []Question `yaml:"questions" json:"questions"`
}

// StoryChoice is one reply of a story moment, with its authored verdict and reaction.
type StoryChoice struct {
	ID       string `yaml:"id" json:"id"`
	Text     string `yaml:"text" json:"text"`
	Verdict  string `yaml:"verdict" json:"verdict"`
	Reaction string `yaml:"reaction" json:"reaction"`
}

// Story is a story moment (story.yaml).
type Story struct {
	Item    `yaml:",inline"`
	From    From          `yaml:"from" json:"from"`
	Scene   string        `yaml:"scene" json:"scene"`
	Choices []StoryChoice `yaml:"choices" json:"choices"`
}

// Feedback is the feedback round (feedback.yaml): a scene answered in writing and reviewed.
type Feedback struct {
	Item       `yaml:",inline"`
	From       From        `yaml:"from" json:"from"`
	Scene      string      `yaml:"scene" json:"scene"`
	Rubric     []Criterion `yaml:"rubric" json:"rubric"`
	AwardsMark string      `yaml:"awards_mark" json:"-"`
}

// ParseWriting decodes and validates the bytes of a writing.yaml; see ParseTicket for the contract.
func ParseWriting(file string, raw []byte) (*Writing, error) {
	var w Writing
	return parseKind(file, raw, &w, func(is *issues) {
		w.validateShared(is)
		checkMarkdown(is, "prompt", w.Prompt, MaxPromptBytes)
		checkRubric(is, w.Rubric)
		checkMarkSlug(is, w.AwardsMark)
	})
}

// ParseQuiz decodes and validates the bytes of a quiz.yaml.
func ParseQuiz(file string, raw []byte) (*Quiz, error) {
	var q Quiz
	return parseKind(file, raw, &q, func(is *issues) {
		q.validateShared(is)
		if n := len(q.Questions); n < MinQuestions || n > MaxQuestions {
			is.add("questions", "needs %d to %d questions, got %d", MinQuestions, MaxQuestions, n)
		}
		ids := map[string]bool{}
		for i, qu := range q.Questions {
			checkQuestion(is, fmt.Sprintf("questions[%d]", i), qu, ids)
		}
	})
}

// ParseStory decodes and validates the bytes of a story.yaml.
func ParseStory(file string, raw []byte) (*Story, error) {
	var s Story
	return parseKind(file, raw, &s, func(is *issues) {
		s.validateShared(is)
		checkFrom(is, s.From)
		checkMarkdown(is, "scene", s.Scene, MaxSceneBytes)
		if n := len(s.Choices); n < MinChoices || n > MaxStoryChoices {
			is.add("choices", "needs %d to %d choices, got %d", MinChoices, MaxStoryChoices, n)
		}
		ids := map[string]bool{}
		for i, c := range s.Choices {
			f := fmt.Sprintf("choices[%d]", i)
			checkLocalID(is, f+".id", c.ID, ids)
			checkText(is, f+".text", c.Text, MaxReplyChars)
			if c.Verdict != VerdictGood && c.Verdict != VerdictOkay && c.Verdict != VerdictPoor {
				is.add(f+".verdict", "%q must be good, okay or poor", c.Verdict)
			}
			checkMarkdown(is, f+".reaction", c.Reaction, MaxReactionBytes)
		}
	})
}

// ParseFeedback decodes and validates the bytes of a feedback.yaml.
func ParseFeedback(file string, raw []byte) (*Feedback, error) {
	var f Feedback
	return parseKind(file, raw, &f, func(is *issues) {
		f.validateShared(is)
		checkFrom(is, f.From)
		checkMarkdown(is, "scene", f.Scene, MaxSceneBytes)
		checkRubric(is, f.Rubric)
		checkMarkSlug(is, f.AwardsMark)
	})
}

// parseKind decodes strictly into v, then runs validate (a closure over v) and joins the problems.
func parseKind[T any](file string, raw []byte, v *T, validate func(*issues)) (*T, error) {
	if err := decodeStrict(file, raw, v); err != nil {
		return nil, err
	}
	is := &issues{file: file}
	validate(is)
	if err := is.err(); err != nil {
		return nil, err
	}
	return v, nil
}

func (i *Item) validateShared(is *issues) {
	validateShared(is, i.ID, i.Title, i.Role, i.Order, i.EstMinutes, i.Tags)
}

// checkMarkdown checks a required markdown text of at most max bytes.
func checkMarkdown(is *issues, field, s string, max int) {
	switch {
	case strings.TrimSpace(s) == "":
		is.add(field, "is required")
	case len(s) > max:
		is.add(field, "at most %d bytes, got %d", max, len(s))
	case !utf8.ValidString(s):
		is.add(field, "is not valid UTF-8")
	}
}

func checkText(is *issues, field, s string, max int) {
	if msg := checkLine(s, max); msg != "" {
		is.add(field, "%s", msg)
	}
}

// ValidLocalID reports whether id is a question, rubric or choice id: a slug of at most 32 characters.
func ValidLocalID(id string) bool {
	return len(id) <= maxLocalIDLen && slugRE.MatchString(id)
}

// checkLocalID checks an id that is a slug of at most 32 characters, distinct within seen.
func checkLocalID(is *issues, field, id string, seen map[string]bool) {
	switch {
	case !slugRE.MatchString(id):
		is.add(field, "%q must be lower-case letters, digits and single hyphens", id)
	case len(id) > maxLocalIDLen:
		is.add(field, "%q is longer than %d characters", id, maxLocalIDLen)
	case seen[id]:
		is.add(field, "%q appears twice", id)
	}
	seen[id] = true
}

func checkRubric(is *issues, rubric []Criterion) {
	if n := len(rubric); n < MinCriteria || n > MaxCriteria {
		is.add("rubric", "needs %d to %d criteria, got %d", MinCriteria, MaxCriteria, n)
	}
	ids := map[string]bool{}
	for i, c := range rubric {
		checkLocalID(is, fmt.Sprintf("rubric[%d].id", i), c.ID, ids)
		checkText(is, fmt.Sprintf("rubric[%d].text", i), c.Text, MaxCriterionChars)
	}
}

// checkMarkSlug checks the form of an optional mark slug; Load checks it is listed.
func checkMarkSlug(is *issues, slug string) {
	if slug != "" {
		checkSlug(is, "awards_mark", slug)
	}
}

func checkFrom(is *issues, f From) {
	checkText(is, "from.name", f.Name, MaxFromChars)
	checkText(is, "from.role", f.Role, MaxFromChars)
}

func checkQuestion(is *issues, f string, q Question, ids map[string]bool) {
	checkLocalID(is, f+".id", q.ID, ids)
	checkMarkdown(is, f+".text", q.Text, MaxQuestionBytes)
	if q.Explain != "" {
		checkMarkdown(is, f+".explain", q.Explain, MaxExplainBytes)
	}
	if n := len(q.Choices); n < MinChoices || n > MaxQuizChoices {
		is.add(f+".choices", "needs %d to %d choices, got %d", MinChoices, MaxQuizChoices, n)
	}
	seen := map[string]bool{}
	for i, c := range q.Choices {
		cf := fmt.Sprintf("%s.choices[%d]", f, i)
		checkText(is, cf+".text", c.Text, MaxChoiceChars)
		checkText(is, cf+".note", c.Note, MaxNoteChars)
		if seen[c.Text] {
			is.add(cf+".text", "%q appears twice in the question", c.Text)
		}
		seen[c.Text] = true
	}
	switch {
	case q.Answer == nil:
		is.add(f+".answer", "is required")
	case *q.Answer < 0 || *q.Answer >= len(q.Choices):
		is.add(f+".answer", "is %d, want an index 0 to %d", *q.Answer, len(q.Choices)-1)
	}
}
