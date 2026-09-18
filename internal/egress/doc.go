// Package egress is the single way out of the process for operational traffic — SMTP, webhooks, the uplink, tool downloads — and it logs instead of sending outside production (ADR-0008).
package egress
