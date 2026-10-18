package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHelpNeverStartsEnvironment(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"generate", "--help"}, {"use", "--help"}, {"dev", "--help"}, {"pull", "--help"}} {
		var output bytes.Buffer
		if err := run(context.Background(), args, &output); err != nil {
			t.Fatal(args, err)
		}
		if output.Len() == 0 {
			t.Fatal("missing help", args)
		}
	}
	if err := run(context.Background(), []string{"unknown"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown command accepted")
	}
}

func TestVersion(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &output); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) != "keel "+version {
		t.Fatal(output.String())
	}
}
