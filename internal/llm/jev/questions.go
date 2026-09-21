package jev

import (
	"errors"
	"fmt"
	"reflect"
)

// Answer types, also the value of each question's "type" field.
const (
	// AnswerNoul is a yes/no answer.
	AnswerNoul = "noul"
	// AnswerChoice is a selection between named alternatives.
	AnswerChoice = "choice"
	// AnswerScore is a rating against an ordered rubric.
	AnswerScore = "score"
)

// Question is one question asked about the request state. The implementations
// are Noul, Choice, Score, and RawQuestion.
type Question interface {
	// encodeQuestion returns the question's wire form.
	encodeQuestion() (map[string]any, error)
}

// Questions maps the name each answer comes back under to the question asked.
type Questions map[string]Question

// NoulCriteria describes what counts as a yes (True) or no (False) answer.
// Either field is a string, an object, or an array; leaving one nil leaves that
// outcome undescribed.
type NoulCriteria struct {
	True  any `json:"true,omitempty"`
	False any `json:"false,omitempty"`
}

// Noul is a yes/no question.
type Noul struct {
	// Instructions is the question or statement to evaluate, as a string, an
	// object, or an array. Optional.
	Instructions any
	// Criteria optionally describes either outcome.
	Criteria *NoulCriteria
}

// Choice is a question that selects between named alternatives.
type Choice struct {
	// Instructions is what the model should decide when choosing, as a string,
	// an object, or an array. Optional.
	Instructions any
	// Criteria maps each alternative to a description of when it applies; a nil
	// description is interpreted by the name alone. Required and non-empty.
	Criteria map[string]any
}

// Score is a question that rates the state against an ordered rubric.
type Score struct {
	// Instructions is what the model should rate, as a string, an object, or an
	// array. Optional.
	Instructions any
	// Criteria holds one description per score level, position zero first.
	// Required and non-empty.
	Criteria []any
}

// RawQuestion is a question passed through as a raw dictionary, for question
// types this package does not model. It must carry a nonempty "type" string,
// and a "criteria" when that type is "choice" or "score".
type RawQuestion map[string]any

func (q Noul) encodeQuestion() (map[string]any, error) {
	body := map[string]any{"type": AnswerNoul}
	if q.Instructions != nil {
		body["instructions"] = q.Instructions
	}
	if q.Criteria != nil {
		body["criteria"] = q.Criteria
	}
	return body, nil
}

func (q Choice) encodeQuestion() (map[string]any, error) {
	if len(q.Criteria) == 0 {
		return nil, errors.New(`choice question requires "criteria"`)
	}
	body := map[string]any{"type": AnswerChoice, "criteria": q.Criteria}
	if q.Instructions != nil {
		body["instructions"] = q.Instructions
	}
	return body, nil
}

func (q Score) encodeQuestion() (map[string]any, error) {
	if len(q.Criteria) == 0 {
		return nil, errors.New("score question has no criteria; at least one score is required")
	}
	body := map[string]any{"type": AnswerScore, "criteria": q.Criteria}
	if q.Instructions != nil {
		body["instructions"] = q.Instructions
	}
	return body, nil
}

func (q RawQuestion) encodeQuestion() (map[string]any, error) {
	name, _ := q["type"].(string)
	if name == "" {
		return nil, errors.New(`raw question requires a nonempty string "type"`)
	}
	criteria := q["criteria"]
	switch {
	case name == AnswerChoice && criteria == nil:
		return nil, fmt.Errorf(`%s question requires "criteria"`, name)
	case name == AnswerScore && (criteria == nil || isEmptySlice(criteria)):
		return nil, errors.New("score question has no criteria; at least one score is required")
	}
	// Any other raw type (including primitives this package does not know) is
	// passed through for the server to judge.
	return q, nil
}

// isEmptySlice reports whether value is a slice with no elements.
func isEmptySlice(value any) bool {
	v := reflect.ValueOf(value)
	return v.Kind() == reflect.Slice && v.Len() == 0
}

// encodeQuestions encodes one request's questions, naming the offending
// question in any validation error.
func encodeQuestions(questions Questions) (map[string]any, error) {
	if len(questions) == 0 {
		return nil, errors.New("at least one question is required")
	}
	encoded := make(map[string]any, len(questions))
	for name, question := range questions {
		if isNil(question) {
			return nil, fmt.Errorf("question %q is nil", name)
		}
		body, err := question.encodeQuestion()
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", name, err)
		}
		encoded[name] = body
	}
	return encoded, nil
}

// isNil reports whether a Question holds no value: nil itself, or a nil map or
// pointer whose method call would panic.
func isNil(question Question) bool {
	if question == nil {
		return true
	}
	value := reflect.ValueOf(question)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	}
	return false
}
