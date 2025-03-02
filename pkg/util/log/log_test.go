// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Go vet raise an error when test the "Warn" method: call has possible formatting directive %s
// +build !dovet

package log

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/DataDog/datadog-agent/pkg/util/scrubber"
	"github.com/cihub/seelog"
	"github.com/stretchr/testify/assert"
)

func changeLogLevel(level string) error {
	if logger == nil {
		return errors.New("cannot set log-level: logger not initialized")
	}

	return logger.changeLogLevel(level)
}

// createExtraTextContext defines custom formatter for context logging on tests.
func createExtraTextContext(string) seelog.FormatterFunc {
	return func(_ string, _ seelog.LogLevel, context seelog.LogContextInterface) interface{} {
		contextList, _ := context.CustomContext().([]interface{})
		builder := strings.Builder{}
		for i := 0; i < len(contextList); i += 2 {
			builder.WriteString(fmt.Sprintf("%s:%v", contextList[i], contextList[i+1]))
			if i != len(contextList)-2 {
				builder.WriteString(", ")
			} else {
				builder.WriteString(" | ")
			}
		}
		return builder.String()
	}
}

func TestBasicLogging(t *testing.T) {
	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	seelog.RegisterCustomFormatter("ExtraTextContext", createExtraTextContext)
	l, err := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.DebugLvl, "[%LEVEL] %FuncShort: %ExtraTextContext%Msg\n")
	assert.Nil(t, err)

	SetupLogger(l, "debug")
	assert.NotNil(t, logger)

	Tracef("%s", "foo")
	Debugf("%s", "foo")
	Infof("%s", "foo")
	Warnf("%s", "foo")
	Errorf("%s", "foo")
	Criticalf("%s", "foo")
	w.Flush()

	// Trace will not be logged
	assert.Equal(t, strings.Count(b.String(), "foo"), 5)

	// Alias to avoid go-vet false positives
	Wn := Warn
	Err := Error
	Crt := Critical

	Trace("bar")
	Debug("bar")
	Info("bar")
	Wn("bar")
	Err("bar")
	Crt("bar")
	w.Flush()

	// Trace will not be logged
	assert.Equal(t, strings.Count(b.String(), "bar"), 5)

	Tracec("baz", "number", 1, "str", "hello")
	Debugc("baz", "number", 1, "str", "hello")
	Infoc("baz", "number", 1, "str", "hello")
	Warnc("baz", "number", 1, "str", "hello")
	Errorc("baz", "number", 1, "str", "hello")
	Criticalc("baz", "number", 1, "str", "hello")
	w.Flush()

	// Trace will not be logged
	assert.Subset(t, strings.Split(b.String(), "\n"), []string{
		"[DEBUG] TestBasicLogging: number:1, str:hello | baz",
		"[INFO] TestBasicLogging: number:1, str:hello | baz",
		"[WARN] TestBasicLogging: number:1, str:hello | baz",
		"[ERROR] TestBasicLogging: number:1, str:hello | baz",
		"[CRITICAL] TestBasicLogging: number:1, str:hello | baz",
	})
}

func TestLogBuffer(t *testing.T) {
	// reset buffer state
	logsBuffer = []func(){}
	bufferLogsBeforeInit = true
	logger = nil

	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	l, err := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.DebugLvl, "[%LEVEL] %FuncShort: %Msg")
	assert.Nil(t, err)

	Tracef("%s", "foo")
	Debugf("%s", "foo")
	Infof("%s", "foo")
	Warnf("%s", "foo")
	Errorf("%s", "foo")
	Criticalf("%s", "foo")

	SetupLogger(l, "debug")
	assert.NotNil(t, logger)

	w.Flush()

	// Trace will not be logged, Error and Critical will directly be logged to Stderr
	assert.Equal(t, strings.Count(b.String(), "foo"), 5)
}
func TestLogBufferWithContext(t *testing.T) {
	// reset buffer state
	logsBuffer = []func(){}
	bufferLogsBeforeInit = true
	logger = nil

	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	l, err := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.DebugLvl, "[%LEVEL] %FuncShort: %Msg")
	assert.Nil(t, err)

	Tracec("baz", "number", 1, "str", "hello")
	Debugc("baz", "number", 1, "str", "hello")
	Infoc("baz", "number", 1, "str", "hello")
	Warnc("baz", "number", 1, "str", "hello")
	Errorc("baz", "number", 1, "str", "hello")
	Criticalc("baz", "number", 1, "str", "hello")

	SetupLogger(l, "debug")
	assert.NotNil(t, logger)
	w.Flush()

	// Trace will not be logged, Error and Critical will directly be logged to Stderr
	assert.Equal(t, strings.Count(b.String(), "baz"), 5)
}

// Set up for scrubbing tests, by temporarily setting Scrubber; this avoids testing
// the default scrubber's functionality in this module
func setupScrubbing(t *testing.T) {
	oldScrubber := scrubber.DefaultScrubber
	scrubber.DefaultScrubber = scrubber.New()
	scrubber.DefaultScrubber.AddReplacer(scrubber.SingleLine, scrubber.Replacer{
		Regex: regexp.MustCompile("SECRET"),
		Repl:  []byte("******"),
	})
	t.Cleanup(func() { scrubber.DefaultScrubber = oldScrubber })
}

func TestCredentialScrubbingLogging(t *testing.T) {
	setupScrubbing(t)

	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	l, err := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.DebugLvl, "[%LEVEL] %FuncShort: %Msg")
	assert.Nil(t, err)

	SetupLogger(l, "info")
	assert.NotNil(t, logger)

	Info("don't tell anyone: ", "SECRET")
	Infof("this is a SECRET password: %s", "hunter2")
	w.Flush()

	assert.Equal(t, strings.Count(b.String(), "SECRET"), 0)
	assert.Equal(t, strings.Count(b.String(), "don't tell anyone:  ******"), 1)
	assert.Equal(t, strings.Count(b.String(), "this is a ****** password: hunter2"), 1)
}

func TestExtraLogging(t *testing.T) {
	setupScrubbing(t)

	var a, b bytes.Buffer
	w := bufio.NewWriter(&a)
	wA := bufio.NewWriter(&b)

	l, err := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.DebugLvl, "[%LEVEL] %Msg")
	assert.Nil(t, err)
	lA, err := seelog.LoggerFromWriterWithMinLevelAndFormat(wA, seelog.DebugLvl, "[%LEVEL] %Msg")
	assert.Nil(t, err)

	SetupLogger(l, "info")
	assert.NotNil(t, logger)

	err = RegisterAdditionalLogger("extra", lA)
	assert.Nil(t, err)

	Info("don't tell anyone: ", "SECRET")
	Infof("this is a SECRET password: %s", "hunter2")
	w.Flush()
	wA.Flush()

	assert.Equal(t, strings.Count(a.String(), "SECRET"), 0)
	assert.Equal(t, strings.Count(a.String(), "don't tell anyone:  ******"), 1)
	assert.Equal(t, strings.Count(a.String(), "this is a ****** password: hunter2"), 1)
	assert.Equal(t, b.String(), a.String())
}

func TestFormatErrorfScrubbing(t *testing.T) {
	setupScrubbing(t)

	err := formatErrorf("%s", "a SECRET message")
	assert.Equal(t, "a ****** message", err.Error())
}

func TestFormatErrorScrubbing(t *testing.T) {
	setupScrubbing(t)

	err := formatError("a big SECRET")
	assert.Equal(t, "a big ******", err.Error())
}

func TestFormatErrorcScrubbing(t *testing.T) {
	setupScrubbing(t)

	err := formatErrorc("super-SECRET")
	assert.Equal(t, "super-******", err.Error())

	err = formatErrorc("secrets", "key", "a SECRET", "SECRET-key2", "SECRET2")
	assert.Equal(t, "secrets (key:a ******, ******-key2:******2)", err.Error())
}

func TestWarnNotNil(t *testing.T) {
	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	assert.NotNil(t, Warn("test"))

	l, _ := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.CriticalLvl, "[%LEVEL] %FuncShort: %Msg")
	SetupLogger(l, "critical")

	assert.NotNil(t, Warn("test"))

	changeLogLevel("info")

	assert.NotNil(t, Warn("test"))
}

func TestWarnfNotNil(t *testing.T) {
	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	assert.NotNil(t, Warn("test"))

	l, _ := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.CriticalLvl, "[%LEVEL] %FuncShort: %Msg")
	SetupLogger(l, "critical")

	assert.NotNil(t, Warn("test"))

	changeLogLevel("info")

	assert.NotNil(t, Warn("test"))
}

func TestWarncNotNil(t *testing.T) {
	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	assert.NotNil(t, Warnc("test", "key", "val"))

	seelog.RegisterCustomFormatter("ExtraTextContext", createExtraTextContext)
	l, _ := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.CriticalLvl, "[%LEVEL] %FuncShort: %ExtraTextContext%Msg")
	SetupLogger(l, "critical")

	assert.NotNil(t, Warnc("test", "key", "val"))

	changeLogLevel("info")

	assert.NotNil(t, Warnc("test", "key", "val"))
}

func TestErrorNotNil(t *testing.T) {
	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	assert.NotNil(t, Error("test"))

	l, _ := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.CriticalLvl, "[%LEVEL] %FuncShort: %Msg")
	SetupLogger(l, "critical")

	assert.NotNil(t, Error("test"))

	changeLogLevel("info")

	assert.NotNil(t, Error("test"))
}

func TestErrorfNotNil(t *testing.T) {
	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	assert.NotNil(t, Errorf("test"))

	l, _ := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.CriticalLvl, "[%LEVEL] %FuncShort: %Msg")
	SetupLogger(l, "critical")

	assert.NotNil(t, Errorf("test"))

	changeLogLevel("info")

	assert.NotNil(t, Errorf("test"))
}

func TestErrorcNotNil(t *testing.T) {
	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	assert.NotNil(t, Errorc("test", "key", "val"))

	seelog.RegisterCustomFormatter("ExtraTextContext", createExtraTextContext)
	l, _ := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.CriticalLvl, "[%LEVEL] %FuncShort: %ExtraTextContext%Msg")
	SetupLogger(l, "critical")

	assert.NotNil(t, Errorc("test", "key", "val"))

	changeLogLevel("info")

	assert.NotNil(t, Errorc("test", "key", "val"))
}

func TestCriticalNotNil(t *testing.T) {
	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	assert.NotNil(t, Critical("test"))

	l, _ := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.InfoLvl, "[%LEVEL] %FuncShort: %Msg")
	SetupLogger(l, "info")

	assert.NotNil(t, Critical("test"))
}

func TestCriticalfNotNil(t *testing.T) {
	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	assert.NotNil(t, Criticalf("test"))

	l, _ := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.InfoLvl, "[%LEVEL] %FuncShort: %Msg")
	SetupLogger(l, "info")

	assert.NotNil(t, Criticalf("test"))
}

func TestCriticalcNotNil(t *testing.T) {
	var b bytes.Buffer
	w := bufio.NewWriter(&b)

	assert.NotNil(t, Criticalc("test", "key", "val"))

	seelog.RegisterCustomFormatter("ExtraTextContext", createExtraTextContext)
	l, _ := seelog.LoggerFromWriterWithMinLevelAndFormat(w, seelog.InfoLvl, "[%LEVEL] %FuncShort: %ExtraTextContext%Msg")
	SetupLogger(l, "info")

	assert.NotNil(t, Criticalc("test", "key", "val"))
}
