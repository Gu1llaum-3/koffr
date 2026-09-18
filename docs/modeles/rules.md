# Règles — module `{{module}}` (code `{{MOD}}`)

Une règle par ligne, une ligne par test. La source est une exigence `E-nn`, une réponse `Q-nn`,
un ADR ou une décision de plan `N-n` ; jamais « de mémoire ». Une règle qu'on retire est barrée,
pas supprimée.

## {{Cas d'usage ou écran}}

| #        | Règle (une phrase, vérifiable)                     | Source            | Test                          |
| -------- | -------------------------------------------------- | ----------------- | ----------------------------- |
| {{MOD}}-01 | {{…}}                                            | `E-nn` § …        | `service.spec.ts › "…"`       |
| {{MOD}}-02 | {{…}}                                            | `Q-nn` (réponse du …) | |

## Divergences avec le cahier des charges

Ce que le module fait **autrement** que le CDC ne le dit, et pourquoi (mesure, réponse `Q-nn`,
ADR). Annoncé à la recette.

## Non porté

Ce que le CDC demande et que le module ne fait pas, avec la décision (`D-nn`, ADR, `B-nn`).

## Constantes et seuils

| Nom | Valeur | Source | Confirmé par |
| --- | ------ | ------ | ------------ |
