package crypto_test

import (
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/Gu1llaum-3/koffr/internal/domain/crypto"
)

// CRY-05 — the recipients of a database are **its own when it declares any**,
// and the fleet's otherwise. Never both: `Q-04`, tranchée par ADR-0016, so that
// one can say for whom an archive is encrypted by looking at **one** place.
func TestCRY05ADatabasesOwnRecipientsReplaceTheFleets(t *testing.T) {
	fleet := recipientsOf(t, "fleet-a", "fleet-b")
	own := recipientsOf(t, "own-a", "own-b")

	applied := crypto.Effective(fleet, own)

	if len(applied.Keys) != len(own.Keys) {
		t.Fatalf("%d recipients apply, want the %d the database declares", len(applied.Keys), len(own.Keys))
	}
	for index, key := range own.Keys {
		if applied.Keys[index].String() != key.String() {
			t.Errorf("recipient %d is not the one the database declares", index)
		}
	}

	// Not a merge: no fleet key survives.
	for _, applied := range applied.Keys {
		for _, fleetKey := range fleet.Keys {
			if applied.String() == fleetKey.String() {
				t.Error("a fleet key survived a database that declares its own: the lists were merged")
			}
		}
	}
}

// CRY-05 — a database that declares none inherits the fleet's, which is the
// normal case: a single list for the whole machine.
func TestCRY05ADatabaseWithoutRecipientsInheritsTheFleets(t *testing.T) {
	fleet := recipientsOf(t, "fleet-a", "fleet-b")

	applied := crypto.Effective(fleet, crypto.Recipients{})

	if len(applied.Keys) != len(fleet.Keys) {
		t.Fatalf("%d recipients apply, want the %d of the fleet", len(applied.Keys), len(fleet.Keys))
	}
	if applied.From != fleet.From {
		t.Errorf("the applied list does not say where it came from: %q", applied.From)
	}
}

// CRY-05 — and the warning of E-132 follows the list that **applies**: a
// database that declares a single key of its own is warned about, even when the
// fleet declares two.
func TestCRY05TheEscrowWarningFollowsTheListThatApplies(t *testing.T) {
	fleet := recipientsOf(t, "fleet-a", "fleet-b")
	alone := recipientsOf(t, "own-a")

	applied := crypto.Effective(fleet, alone)

	warning := applied.Warning()
	if warning == "" {
		t.Fatal("a database with one recipient of its own was not warned about")
	}
	if !strings.Contains(warning, "escrow") {
		t.Errorf("the warning does not name the escrow key: %s", warning)
	}
}

func recipientsOf(t *testing.T, names ...string) crypto.Recipients {
	t.Helper()

	made := crypto.Recipients{From: strings.Join(names, "+")}

	for range names {
		key, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}

		made.Keys = append(made.Keys, key.Recipient())
	}

	return made
}
