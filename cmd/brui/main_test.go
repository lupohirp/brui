package main

import (
	"errors"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args     []string
		path     string
		insecure bool
		err      bool
	}{
		{nil, ".", false, false},
		{[]string{"coll"}, "coll", false, false},
		{[]string{"-k", "coll"}, "coll", true, false},
		{[]string{"coll", "--insecure"}, "coll", true, false},
		{[]string{"--env", "SIT"}, "", false, true},
		{[]string{"a", "b"}, "", false, true},
	}
	for _, c := range cases {
		o, err := parseArgs(c.args)
		if (err != nil) != c.err {
			t.Errorf("%v: err=%v", c.args, err)
			continue
		}
		if !c.err && (o.path != c.path || o.insecure != c.insecure) {
			t.Errorf("%v: got %+v", c.args, o)
		}
	}
	if _, err := parseArgs([]string{"-h"}); !errors.Is(err, errHelp) {
		t.Errorf("-h should request help, got %v", err)
	}
}

func TestVersionFlag(t *testing.T) {
	for _, a := range []string{"-v", "--version"} {
		if _, err := parseArgs([]string{a}); !errors.Is(err, errVersion) {
			t.Fatalf("%s: want errVersion, got %v", a, err)
		}
	}
}
