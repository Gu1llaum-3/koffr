# Documentation — index

Ce que chaque dossier contient et, surtout, la **table des registres** : un préfixe par registre,
jamais deux registres avec la même lettre. Un nouveau registre se déclare ici avant d'exister.

## Registres

| Préfixe    | Registre                             | Fichier                     | Qui l'alimente                       |
| ---------- | ------------------------------------ | --------------------------- | ------------------------------------ |
| `E-nn`     | Exigences du cahier des charges      | `cdc/exigences.md`          | `/demarrer-projet`, puis à la main   |
| `ADR-NNNN` | Décisions figées                     | `adr/`                      | `/ecrire-adr`                        |
| `D-nn`     | Décisions en attente                 | `decisions.md`              | Quiconque bute sur un arbitrage      |
| `Q-nn`     | Questions au métier                  | `questions.md`              | Quiconque bute sur une inconnue      |
| `B-nn`     | Backlog                              | `backlog.md`                | Quiconque a une idée hors périmètre  |
| `A-nn`     | Anomalies de recette                 | `recette/anomalies.md`      | Les sessions de recette              |
| `N-n`      | Décisions d'implémentation d'un plan | `plans/lot-N-*.md`          | `/ecrire-plan`, `/executer-plan`     |
| `<MOD>-nn` | Règles métier d'un module            | `src/**/<module>/rules.md`  | `/implementer`                       |

Une entrée ne se supprime jamais : elle se barre, avec la date et le renvoi vers ce qui l'a
tranchée.

## Dossiers

| Dossier / fichier   | Contenu                                                                    |
| ------------------- | -------------------------------------------------------------------------- |
| `cdc/`              | Le cahier des charges reçu (tel quel), son analyse, le registre d'exigences |
| `adr/`              | Décisions figées, `README.md` en index, `0000-template.md`                 |
| `plans/`            | Un plan par lot, `0000-template.md`                                        |
| `retro/`            | Une rétrospective par lot, `0000-template.md`                              |
| `recette/`          | Scénarios de recette, programme des sessions, anomalies                    |
| `modeles/`          | Gabarits qui n'ont pas de dossier propre (`rules.md` d'un module)          |
| `decisions.md`      | Registre `D-nn`                                                            |
| `questions.md`      | Registre `Q-nn`                                                            |
| `backlog.md`        | Registre `B-nn`                                                            |
| `maintenance.md`    | Mises à jour de dépendances, rotation des secrets, sauvegardes             |
| `inputs/`           | Documents reçus autres que le CDC (maquettes, exports, comptes rendus)     |
