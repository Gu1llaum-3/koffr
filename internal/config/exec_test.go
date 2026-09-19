package config

import (
	"strings"
	"testing"
)

// RSV-10 — a database that asks for the exec strategy without saying which
// container is refused when the configuration is checked, not at two in the
// morning when the job runs (E-046).
func TestRSV10ExecWithoutAContainerIsRefusedAtValidation(t *testing.T) {
	_, err := Load(write(t, header+`
databases:
  - id: erp
    engine: mariadb
    tools:
      strategy: exec
`))
	if err == nil {
		t.Fatal("a database asking for exec without a container was accepted")
	}
	for _, want := range []string{"erp", "container", "exec"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%v", want, err)
		}
	}
}

// And a strategy koffr does not know is refused too, rather than silently
// meaning "auto".
func TestAnUnknownToolStrategyIsRefused(t *testing.T) {
	_, err := Load(write(t, header+`
databases:
  - id: erp
    engine: mariadb
    tools:
      strategy: kubectl
      container: erp
`))
	if err == nil {
		t.Fatal("an unknown tool strategy was accepted")
	}
	if !strings.Contains(err.Error(), "kubectl") {
		t.Errorf("the error does not name the strategy:\n%v", err)
	}
}

// The declared form of the specification § 5.1 still passes: exec with its
// container.
func TestExecWithItsContainerIsAccepted(t *testing.T) {
	config := load(t, header+`
databases:
  - id: erp
    engine: mariadb
    tools:
      strategy: exec
      container: erp-mariadb
`)

	if got := config.Databases[0].Tools.Container; got != "erp-mariadb" {
		t.Errorf("container = %q, want %q", got, "erp-mariadb")
	}
}
