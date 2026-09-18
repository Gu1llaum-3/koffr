# Rétrospective lot N — {{Nom}}

- **Période** : du AAAA-MM-JJ au AAAA-MM-JJ, {{n}} vagues, {{n}} commits.
- **Plan** : `docs/plans/lot-N-<nom>.md`. **Critère de sortie** : atteint | atteint sauf … | non atteint (D-nn).

## Ce qui s'est passé

Cinq à dix lignes de faits : ce qui a été livré, ce qui a été écarté, ce qui a pris plus longtemps
que prévu, ce qui a été refait.

## Ce qui a marché

À garder tel quel. Une puce par pratique, avec l'exemple concret.

## Ce qui a coûté

Une puce par cause, avec le coût observé (une vague refaite, une migration jetée, une recette
reportée). Sans blâme : la cause est dans la méthode, l'outil ou l'information manquante.

## Ce qu'on change

| Quoi                             | Où                                                  | Fait |
| -------------------------------- | --------------------------------------------------- | ---- |
| {{Une règle de méthode}}         | `METHODE.md` § …                                    | [ ]  |
| {{Un piège de la stack}}         | `CLAUDE.md` § Conventions, ou skill `implementer`   | [ ]  |
| {{Un gabarit à amender}}         | `docs/plans/0000-template.md`, …                    | [ ]  |
| {{Une décision à figer}}         | ADR-NNNN                                            | [ ]  |

Chaque ligne est appliquée par `/cloturer-lot` avant de fermer le lot ; ce qui ne l'est pas est
dit. Une leçon se **fond** dans la section concernée du document cible, au présent ; on n'ajoute
pas de section « leçons du lot N ».

## Pour le kit

Ce qui vaudrait pour tout projet, pas seulement celui-ci : à reporter dans le kit de pilotage.
