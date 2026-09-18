# Recette

Une session de recette met un utilisateur réel devant l'application, avec un scénario écrit, et
en sort des anomalies `A-nn` et des décisions. Elle a lieu à la fin de chaque lot qui produit un
écran, et régulièrement pendant (`METHODE.md` § Règles de pilotage).

| Fichier                    | Contenu                                                                   |
| -------------------------- | ------------------------------------------------------------------------- |
| `lot-N-scenario.md`        | Le scénario joué : pas à pas, ce qu'on doit voir, ce qui est voulu        |
| `programme-<date>.md`      | Le déroulé d'une session : préparation la veille, horaire, décisions attendues |
| `anomalies.md`             | Registre `A-nn` : constat, écran, gravité, décision, correction (plan)   |

## Gabarit d'un scénario

```markdown
# Recette lot N — {{Nom}}

## Avant de commencer
Données, comptes, état de la base, ce qui doit tourner. Qui prépare.

## Ce qui est voulu et pourrait passer pour un bug
Une ligne par écart assumé, avec son `N-n`, son ADR ou sa `Q-nn`.

## Parcours
### 1. {{Titre}}
1. Faire … → on doit voir …
2. …
**Décision attendue** : …

## Comment rapporter
Une anomalie = écran, geste, attendu, constaté, gravité (bloquant / gênant / cosmétique). Dans
`anomalies.md`, numérotée `A-nn`.
```
