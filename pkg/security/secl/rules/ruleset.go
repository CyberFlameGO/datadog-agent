// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package rules

import (
	"fmt"
	"strings"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"

	"github.com/DataDog/datadog-agent/pkg/security/secl/compiler/eval"
)

// MacroID represents the ID of a macro
type MacroID = string

// MacroDefinition holds the definition of a macro
type MacroDefinition struct {
	ID         MacroID `yaml:"id"`
	Expression string  `yaml:"expression"`
}

// Macro describes a macro of a ruleset
type Macro struct {
	*eval.Macro
	Definition *MacroDefinition
}

// RuleID represents the ID of a rule
type RuleID = string

// RuleDefinition holds the definition of a rule
type RuleDefinition struct {
	ID          RuleID            `yaml:"id"`
	Version     string            `yaml:"version"`
	Expression  string            `yaml:"expression"`
	Description string            `yaml:"description"`
	Tags        map[string]string `yaml:"tags"`
	Policy      *Policy
}

// GetTags returns the tags associated to a rule
func (rd *RuleDefinition) GetTags() []string {
	tags := []string{}
	for k, v := range rd.Tags {
		tags = append(
			tags,
			fmt.Sprintf("%s:%s", k, v))
	}
	return tags
}

// Rule describes a rule of a ruleset
type Rule struct {
	*eval.Rule
	Definition *RuleDefinition
}

// RuleSetListener describes the methods implemented by an object used to be
// notified of events on a rule set.
type RuleSetListener interface {
	RuleMatch(rule *Rule, event eval.Event)
	EventDiscarderFound(rs *RuleSet, event eval.Event, field eval.Field, eventType eval.EventType)
}

// Opts defines rules set options
type Opts struct {
	eval.Opts
	SupportedDiscarders map[eval.Field]bool
	ReservedRuleIDs     []RuleID
	EventTypeEnabled    map[eval.EventType]bool
	Logger              Logger
}

// NewOptsWithParams initializes a new Opts instance with Debug and Constants parameters
func NewOptsWithParams(constants map[string]interface{}, variables map[string]eval.VariableValue, supportedDiscarders map[eval.Field]bool, eventTypeEnabled map[eval.EventType]bool, reservedRuleIDs []RuleID, legacyAttributes map[eval.Field]eval.Field, logger ...Logger) *Opts {
	if len(logger) == 0 {
		logger = []Logger{NullLogger{}}
	}
	return &Opts{
		Opts: eval.Opts{
			Constants:        constants,
			Variables:        variables,
			Macros:           make(map[eval.MacroID]*eval.Macro),
			LegacyAttributes: legacyAttributes,
		},
		SupportedDiscarders: supportedDiscarders,
		ReservedRuleIDs:     reservedRuleIDs,
		EventTypeEnabled:    eventTypeEnabled,
		Logger:              logger[0],
	}
}

// RuleSet holds a list of rules, grouped in bucket. An event can be evaluated
// against it. If the rule matches, the listeners for this rule set are notified
type RuleSet struct {
	opts             *Opts
	loadedPolicies   map[string]string
	eventRuleBuckets map[eval.EventType]*RuleBucket
	rules            map[eval.RuleID]*Rule
	macros           map[eval.RuleID]*Macro
	model            eval.Model
	eventCtor        func() eval.Event
	listeners        []RuleSetListener
	// fields holds the list of event field queries (like "process.uid") used by the entire set of rules
	fields []string
	logger Logger
	pool   *eval.ContextPool
}

// ListRuleIDs returns the list of RuleIDs from the ruleset
func (rs *RuleSet) ListRuleIDs() []RuleID {
	var ids []string
	for ruleID := range rs.rules {
		ids = append(ids, ruleID)
	}
	return ids
}

// GetRules returns the active rules
func (rs *RuleSet) GetRules() map[eval.RuleID]*Rule {
	return rs.rules
}

// ListMacroIDs returns the list of MacroIDs from the ruleset
func (rs *RuleSet) ListMacroIDs() []MacroID {
	var ids []string
	for macroID := range rs.opts.Macros {
		ids = append(ids, macroID)
	}
	return ids
}

// AddMacros parses the macros AST and adds them to the list of macros of the ruleset
func (rs *RuleSet) AddMacros(macros []*MacroDefinition) *multierror.Error {
	var result *multierror.Error

	// Build the list of macros for the ruleset
	for _, macroDef := range macros {
		if _, err := rs.AddMacro(macroDef); err != nil {
			result = multierror.Append(result, err)
		}
	}

	return result
}

// AddMacro parses the macro AST and adds it to the list of macros of the ruleset
func (rs *RuleSet) AddMacro(macroDef *MacroDefinition) (*eval.Macro, error) {
	if _, exists := rs.opts.Macros[macroDef.ID]; exists {
		return nil, &ErrMacroLoad{Definition: macroDef, Err: errors.New("multiple definition with the same ID")}
	}

	macro := &Macro{
		Macro: &eval.Macro{
			ID:         macroDef.ID,
			Expression: macroDef.Expression,
		},
		Definition: macroDef,
	}

	if err := macro.Parse(); err != nil {
		return nil, &ErrMacroLoad{Definition: macroDef, Err: errors.Wrap(err, "syntax error")}
	}

	if err := macro.GenEvaluator(rs.model, &rs.opts.Opts); err != nil {
		return nil, &ErrMacroLoad{Definition: macroDef, Err: errors.Wrap(err, "compilation error")}
	}

	rs.opts.Macros[macro.ID] = macro.Macro

	return macro.Macro, nil
}

// AddRules adds rules to the ruleset and generate their partials
func (rs *RuleSet) AddRules(rules []*RuleDefinition) *multierror.Error {
	var result *multierror.Error

	for _, ruleDef := range rules {
		if _, err := rs.AddRule(ruleDef); err != nil {
			result = multierror.Append(result, err)
		}
	}

	if err := rs.generatePartials(); err != nil {
		result = multierror.Append(result, errors.Wrapf(err, "couldn't generate partials for rule"))
	}

	return result
}

// GetRuleEventType return the rule EventType. Currently rules support only one eventType
func GetRuleEventType(rule *eval.Rule) (eval.EventType, error) {
	eventTypes, err := rule.GetEventTypes()
	if err != nil {
		return "", err
	}

	if len(eventTypes) == 0 {
		return "", ErrRuleWithoutEvent
	}

	// TODO: this contraints could be removed, but currently approver resolution can't handle multiple event type approver
	if len(eventTypes) > 1 {
		return "", ErrRuleWithMultipleEvents
	}

	return eventTypes[0], nil
}

// AddRule creates the rule evaluator and adds it to the bucket of its events
func (rs *RuleSet) AddRule(ruleDef *RuleDefinition) (*eval.Rule, error) {
	for _, id := range rs.opts.ReservedRuleIDs {
		if id == ruleDef.ID {
			return nil, &ErrRuleLoad{Definition: ruleDef, Err: ErrInternalIDConflict}
		}
	}

	if _, exists := rs.rules[ruleDef.ID]; exists {
		return nil, &ErrRuleLoad{Definition: ruleDef, Err: ErrDefinitionIDConflict}
	}

	var tags []string
	for k, v := range ruleDef.Tags {
		tags = append(tags, k+":"+v)
	}

	rule := &Rule{
		Rule: &eval.Rule{
			ID:         ruleDef.ID,
			Expression: ruleDef.Expression,
			Tags:       tags,
		},
		Definition: ruleDef,
	}

	if err := rule.Parse(); err != nil {
		return nil, &ErrRuleLoad{Definition: ruleDef, Err: errors.Wrap(err, "syntax error")}
	}

	if err := rule.GenEvaluator(rs.model, &rs.opts.Opts); err != nil {
		return nil, &ErrRuleLoad{Definition: ruleDef, Err: err}
	}

	eventType, err := GetRuleEventType(rule.Rule)
	if err != nil {
		return nil, &ErrRuleLoad{Definition: ruleDef, Err: err}
	}

	// ignore event types not supported
	if _, exists := rs.opts.EventTypeEnabled["*"]; !exists {
		if _, exists := rs.opts.EventTypeEnabled[eventType]; !exists {
			return nil, &ErrRuleLoad{Definition: ruleDef, Err: ErrEventTypeNotEnabled}
		}
	}

	for _, event := range rule.GetEvaluator().EventTypes {
		bucket, exists := rs.eventRuleBuckets[event]
		if !exists {
			bucket = &RuleBucket{}
			rs.eventRuleBuckets[event] = bucket
		}

		if err := bucket.AddRule(rule); err != nil {
			return nil, err
		}
	}

	// Merge the fields of the new rule with the existing list of fields of the ruleset
	rs.AddFields(rule.GetEvaluator().GetFields())

	rs.rules[ruleDef.ID] = rule

	return rule.Rule, nil
}

// NotifyRuleMatch notifies all the ruleset listeners that an event matched a rule
func (rs *RuleSet) NotifyRuleMatch(rule *Rule, event eval.Event) {
	for _, listener := range rs.listeners {
		listener.RuleMatch(rule, event)
	}
}

// NotifyDiscarderFound notifies all the ruleset listeners that a discarder was found for an event
func (rs *RuleSet) NotifyDiscarderFound(event eval.Event, field eval.Field, eventType eval.EventType) {
	for _, listener := range rs.listeners {
		listener.EventDiscarderFound(rs, event, field, eventType)
	}
}

// AddListener adds a listener on the ruleset
func (rs *RuleSet) AddListener(listener RuleSetListener) {
	rs.listeners = append(rs.listeners, listener)
}

// HasRulesForEventType returns if there is at least one rule for the given event type
func (rs *RuleSet) HasRulesForEventType(eventType eval.EventType) bool {
	bucket, found := rs.eventRuleBuckets[eventType]
	if !found {
		return false
	}
	return len(bucket.rules) > 0
}

// GetBucket returns rule bucket for the given event type
func (rs *RuleSet) GetBucket(eventType eval.EventType) *RuleBucket {
	if bucket, exists := rs.eventRuleBuckets[eventType]; exists {
		return bucket
	}
	return nil
}

// GetApprovers returns all approvers
func (rs *RuleSet) GetApprovers(fieldCaps map[eval.EventType]FieldCapabilities) (map[eval.EventType]Approvers, error) {
	approvers := make(map[eval.EventType]Approvers)
	for _, eventType := range rs.GetEventTypes() {
		caps, exists := fieldCaps[eventType]
		if !exists {
			continue
		}

		eventApprovers, err := rs.GetEventApprovers(eventType, caps)
		if err != nil {
			continue
		}
		approvers[eventType] = eventApprovers
	}

	return approvers, nil
}

// GetEventApprovers returns approvers for the given event type and the fields
func (rs *RuleSet) GetEventApprovers(eventType eval.EventType, fieldCaps FieldCapabilities) (Approvers, error) {
	bucket, exists := rs.eventRuleBuckets[eventType]
	if !exists {
		return nil, ErrNoEventTypeBucket{EventType: eventType}
	}

	return bucket.GetApprovers(rs.eventCtor(), fieldCaps)
}

// GetFieldValues returns all the values of the given field
func (rs *RuleSet) GetFieldValues(field eval.Field) []eval.FieldValue {
	var values []eval.FieldValue

	for _, rule := range rs.rules {
		rv := rule.GetFieldValues(field)
		if len(rv) > 0 {
			values = append(values, rv...)
		}
	}

	return values
}

// IsDiscarder partially evaluates an Event against a field
func (rs *RuleSet) IsDiscarder(event eval.Event, field eval.Field) (bool, error) {
	eventType, err := event.GetFieldEventType(field)
	if err != nil {
		return false, err
	}

	bucket, exists := rs.eventRuleBuckets[eventType]
	if !exists {
		return false, &ErrNoEventTypeBucket{EventType: eventType}
	}

	ctx := rs.pool.Get(event.GetPointer())
	defer rs.pool.Put(ctx)

	for _, rule := range bucket.rules {
		isTrue, err := rule.PartialEval(ctx, field)
		if err != nil || isTrue {
			return false, err
		}
	}
	return true, nil
}

// Evaluate the specified event against the set of rules
func (rs *RuleSet) Evaluate(event eval.Event) bool {
	ctx := rs.pool.Get(event.GetPointer())
	defer rs.pool.Put(ctx)

	eventType := event.GetType()

	result := false
	bucket, exists := rs.eventRuleBuckets[eventType]
	if !exists {
		return result
	}
	rs.logger.Tracef("Evaluating event of type `%s` against set of %d rules", eventType, len(bucket.rules))

	for _, rule := range bucket.rules {
		if rule.GetEvaluator().Eval(ctx) {
			rs.logger.Tracef("Rule `%s` matches with event `%s`\n", rule.ID, event)

			rs.NotifyRuleMatch(rule, event)
			result = true
		}
	}

	if !result {
		rs.logger.Tracef("Looking for discarders for event of type `%s`", eventType)

		for _, field := range bucket.fields {
			if rs.opts.SupportedDiscarders != nil {
				if _, exists := rs.opts.SupportedDiscarders[field]; !exists {
					continue
				}
			}

			isDiscarder := true
			for _, rule := range bucket.rules {
				isTrue, err := rule.PartialEval(ctx, field)
				if err != nil || isTrue {
					isDiscarder = false
					break
				}
			}
			if isDiscarder {
				rs.NotifyDiscarderFound(event, field, eventType)
			}
		}
	}

	return result
}

// GetEventTypes returns all the event types handled by the ruleset
func (rs *RuleSet) GetEventTypes() []eval.EventType {
	eventTypes := make([]string, 0, len(rs.eventRuleBuckets))
	for eventType := range rs.eventRuleBuckets {
		eventTypes = append(eventTypes, eventType)
	}
	return eventTypes
}

// AddFields merges the provided set of fields with the existing set of fields of the ruleset
func (rs *RuleSet) AddFields(fields []eval.EventType) {
NewFields:
	for _, newField := range fields {
		for _, oldField := range rs.fields {
			if oldField == newField {
				continue NewFields
			}
		}
		rs.fields = append(rs.fields, newField)
	}
}

// generatePartials generates the partials of the ruleset. A partial is a boolean evalution function that only depends
// on one field. The goal of partial is to determine if a rule depends on a specific field, so that we can decide if
// we should create an in-kernel filter for that field.
func (rs *RuleSet) generatePartials() error {
	// Compute the partials of each rule
	for _, bucket := range rs.eventRuleBuckets {
		for _, rule := range bucket.GetRules() {
			if err := rule.GenPartials(); err != nil {
				return err
			}
		}
	}
	return nil
}

// AddPolicyVersion adds the provided policy filename and version to the map of loaded policies
func (rs *RuleSet) AddPolicyVersion(filename string, version string) {
	rs.loadedPolicies[strings.ReplaceAll(filename, ".", "_")] = version
}

// NewRuleSet returns a new ruleset for the specified data model
func NewRuleSet(model eval.Model, eventCtor func() eval.Event, opts *Opts) *RuleSet {
	return &RuleSet{
		model:            model,
		eventCtor:        eventCtor,
		opts:             opts,
		eventRuleBuckets: make(map[eval.EventType]*RuleBucket),
		rules:            make(map[eval.RuleID]*Rule),
		macros:           make(map[eval.RuleID]*Macro),
		loadedPolicies:   make(map[string]string),
		logger:           opts.Logger,
		pool:             eval.NewContextPool(),
	}
}
