package docsgen

import (
	"bytes"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/clientux"
)

func TestCLIReferenceCoversCommandDocs(t *testing.T) {
	output, err := GenerateCLI()
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range clientux.CLICommandDocs() {
		if !bytes.Contains(output, []byte("| "+command.Name+" ")) {
			t.Fatalf("CLI reference missing command %s", command.Name)
		}
		for _, alias := range command.Aliases {
			if !bytes.Contains(output, []byte(alias)) {
				t.Fatalf("CLI reference missing alias %s", alias)
			}
		}
	}
}

func TestCLIReferenceCoversDoctorCheckDocs(t *testing.T) {
	output, err := GenerateCLI()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output, []byte("## What Doctor Checks")) {
		t.Fatal("CLI reference missing doctor checks section")
	}
	for _, check := range clientux.DoctorCheckDocs(cliDoctorCheckDocInput()) {
		if !bytes.Contains(output, []byte("| "+check.Name+" ")) {
			t.Fatalf("CLI reference missing doctor check %s", check.Name)
		}
	}
}
