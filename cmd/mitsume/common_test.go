package main

import (
	"flag"
	"io"
	"reflect"
	"testing"
)

func newSplitTestFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("heartbeat-file", "", "")
	fs.Bool("dry-run", false, "")

	return fs
}

func TestSplitFlags_TrailingBoolFlagAfterPositional(t *testing.T) {
	t.Parallel()
	fs := newSplitTestFlagSet()
	flags, positionals := splitFlags(fs, []string{"job1", "--dry-run"})
	if !reflect.DeepEqual(flags, []string{"--dry-run"}) {
		t.Errorf("flags = %v", flags)
	}
	if !reflect.DeepEqual(positionals, []string{"job1"}) {
		t.Errorf("positionals = %v", positionals)
	}
}

func TestSplitFlags_TrailingValueFlagConsumesNextArg(t *testing.T) {
	t.Parallel()
	fs := newSplitTestFlagSet()
	flags, positionals := splitFlags(fs, []string{"job1", "--heartbeat-file", "/tmp/hb.json"})
	if !reflect.DeepEqual(flags, []string{"--heartbeat-file", "/tmp/hb.json"}) {
		t.Errorf("flags = %v", flags)
	}
	if !reflect.DeepEqual(positionals, []string{"job1"}) {
		t.Errorf("positionals = %v", positionals)
	}
}

func TestSplitFlags_EqualsFormDoesNotConsumeNextArg(t *testing.T) {
	t.Parallel()
	fs := newSplitTestFlagSet()
	flags, positionals := splitFlags(fs, []string{"--heartbeat-file=/tmp/hb.json", "job1"})
	if !reflect.DeepEqual(flags, []string{"--heartbeat-file=/tmp/hb.json"}) {
		t.Errorf("flags = %v", flags)
	}
	if !reflect.DeepEqual(positionals, []string{"job1"}) {
		t.Errorf("positionals = %v", positionals)
	}
}

func TestSplitFlags_DoubleDashTreatsRestAsPositional(t *testing.T) {
	t.Parallel()
	fs := newSplitTestFlagSet()
	flags, positionals := splitFlags(fs, []string{"--dry-run", "--", "--not-a-flag"})
	if !reflect.DeepEqual(flags, []string{"--dry-run"}) {
		t.Errorf("flags = %v", flags)
	}
	if !reflect.DeepEqual(positionals, []string{"--not-a-flag"}) {
		t.Errorf("positionals = %v", positionals)
	}
}

func TestSplitFlags_SingleDashIsPositional(t *testing.T) {
	t.Parallel()
	fs := newSplitTestFlagSet()
	flags, positionals := splitFlags(fs, []string{"-"})
	if len(flags) != 0 {
		t.Errorf("flags = %v", flags)
	}
	if !reflect.DeepEqual(positionals, []string{"-"}) {
		t.Errorf("positionals = %v", positionals)
	}
}

func TestSplitFlags_UnknownFlagIsLeftForParseError(t *testing.T) {
	t.Parallel()
	fs := newSplitTestFlagSet()
	flags, _ := splitFlags(fs, []string{"--no-such-flag"})
	if !reflect.DeepEqual(flags, []string{"--no-such-flag"}) {
		t.Errorf("flags = %v", flags)
	}
	if err := fs.Parse(flags); err == nil {
		t.Errorf("fs.Parse should reject unknown flag")
	}
}
