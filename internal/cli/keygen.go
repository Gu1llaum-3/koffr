package cli

import (
	"fmt"

	"filippo.io/age"
	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/config"
)

func newKeygenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "keygen",
		Short: "Generate an age key pair; the private key is shown once and never stored",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pair, err := age.GenerateX25519Identity()
			if err != nil {
				return fmt.Errorf("generate a key pair: %w", err)
			}

			// Printed, never written. koffr holds the public key and nothing
			// else: that is what makes a stolen agent give away the databases
			// as they are now rather than the history of what they were
			// (ADR-0007, E-076).
			cmd.Printf("public key   %s\n", pair.Recipient())
			cmd.Printf("private key  %s\n", pair)
			cmd.Println()
			cmd.Printf("koffr does not store the private key, and never will. Put it somewhere safe\n" +
				"now — a password manager, a sealed envelope, anywhere but this machine. If you\n" +
				"lose it, every archive encrypted for it is unreadable forever.\n\n")
			cmd.Printf("Add the public key to %s. Generate a second pair and add it too: that\n"+
				"is the escrow key, and it is what makes losing the first one survivable.\n",
				config.DefaultPaths().RecipientsFile())

			return nil
		},
	}
}
