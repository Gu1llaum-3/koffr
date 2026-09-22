package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/config"
)

// A-17, Q-04 — a database may declare its own recipients file, and the strict
// parser accepts it. `N-2` of the lot 2 plan promised the field "dès ce lot";
// it was never added, and nothing tested it, so nothing said so.
func TestADatabaseMayDeclareItsOwnRecipientsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "koffr.yaml")
	body := `agent:
  id: prod-fr-01
  timezone: Europe/Paris

encryption:
  recipients_file: /etc/koffr/recipients.txt

databases:
  - id: shop
    engine: postgresql
    host: 127.0.0.1
    port: 5432
    database: shop
    user: koffr_backup
    recipients_file: /etc/koffr/shop-recipients.txt
  - id: erp
    engine: mariadb
    host: 127.0.0.1
    port: 3306
    database: erp
    user: koffr_backup
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := loaded.Databases[0].RecipientsFile; got != "/etc/koffr/shop-recipients.txt" {
		t.Errorf("the database's own recipients file was lost: %q", got)
	}
	if got := loaded.Databases[1].RecipientsFile; got != "" {
		t.Errorf("a database that declares none got %q", got)
	}
	if loaded.Encryption.RecipientsFile != "/etc/koffr/recipients.txt" {
		t.Error("the fleet list was lost")
	}
}

// And a misspelling of it is still an error, like every other key (E-032).
func TestAMisspeltRecipientsFileIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "koffr.yaml")
	body := `agent:
  id: prod-fr-01
  timezone: Europe/Paris
databases:
  - id: shop
    engine: postgresql
    host: 127.0.0.1
    port: 5432
    database: shop
    user: koffr_backup
    recipient_file: /etc/koffr/shop.txt
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("a misspelt key was accepted")
	}
	if !strings.Contains(err.Error(), "recipient_file") {
		t.Errorf("the error does not name the key:\n%v", err)
	}
}
