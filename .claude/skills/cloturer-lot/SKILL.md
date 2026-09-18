---
name: cloturer-lot
description: Clôt un lot après sa recette — écrit la rétrospective docs/retro/lot-N.md, applique chaque changement qu'elle décide (METHODE.md, CLAUDE.md, gabarits, skill de stack), met à jour la roadmap et le registre d'exigences, écrit les ADR manquants, purge la mémoire de Claude de l'état du projet. À utiliser quand le plan est terminé et la session de recette faite.
---

# Clôturer un lot

La clôture est le moment où le projet apprend. Elle produit une rétrospective **et applique** ce
qu'elle décide ; une leçon qui n'est pas appliquée dans un document cible est perdue à la session
suivante.

## Préalables

- Le plan est `terminé`, `verify` vert sur `main`, CI verte.
- La session de recette a eu lieu : `docs/recette/anomalies.md` est à jour, les décisions
  attendues sont prises ou inscrites en `D-nn`. Sinon, s'arrêter : on ne clôt pas sans recette.
- Les anomalies `bloquant` sont corrigées ou ont un plan de corrections dans la roadmap.

## Étape 1 — Rétrospective

Écrire `docs/retro/lot-N.md` depuis le gabarit. Sources : le plan et son journal d'exécution, les
commits (`git log main --since=<début du lot>`), les anomalies, les `N-n` ajoutées en route, ce que
le propriétaire a dit pendant le lot.

- **Faits d'abord** : vagues prévues / faites, tâches refaites, échecs notés dans le plan, temps
  passé si connu, exigences couvertes / écartées.
- **Ce qui a coûté** : chercher la cause dans la méthode ou l'information, pas dans les personnes.
  « L'état de départ n'avait pas été vérifié » est une cause ; « on s'est trompé » n'en est pas une.
- **Ce qu'on change** : chaque ligne nomme un document cible et une section. Une leçon de stack →
  `CLAUDE.md` § Conventions ou skill `implementer` ; une leçon de méthode → `METHODE.md` ; un
  gabarit à amender → le gabarit ; une décision → ADR.
- **Pour le kit** : ce qui vaut pour tout projet.

Présenter la rétro au propriétaire, intégrer ses ajouts.

## Étape 2 — Appliquer

Pour chaque ligne de « Ce qu'on change » :

- Modifier le document cible **dans la section concernée, au présent**. On ne crée pas de section
  « Leçons du lot N » ; on fond la leçon là où le lecteur la cherchera. Si un document dépasse ce
  que quelqu'un relit (roadmap > 150 lignes, skill > 250 lignes, `CLAUDE.md` > 150 lignes), le
  dire et proposer une scission.
- Cocher la ligne dans la rétro. Ce qui n'est pas appliqué reste décoché **et est dit**.

Puis :

- `ROADMAP.md` : le lot passe à `terminé` avec sa date ; la ligne reste courte. Les lots suivants
  sont réordonnés si la rétro le demande (avec une `D-nn` si c'est un arbitrage).
- `docs/cdc/exigences.md` : chaque `E-nn` du lot passe à `couverte (MOD-nn, test)`, `reportée`
  ou `écartée`, avec son renvoi. Aucune `E-nn` du lot ne reste `à faire` sans explication.
- `docs/adr/` : un ADR pour toute décision structurante prise en route et pas encore écrite
  (`/ecrire-adr`).
- `docs/decisions.md`, `docs/questions.md` : lignes tranchées ou répondues barrées avec leur renvoi.
- `docs/backlog.md` : les idées notées en route ont leur origine et leur coût.
- **Mémoire persistante de Claude** : supprimer toute note d'état du projet (« lot N terminé »,
  « reste à faire… ») ; l'état vit dans le dépôt. Garder uniquement les préférences et retours du
  propriétaire (`feedback-*`, `user-*`), et y ajouter ceux exprimés pendant le lot.

## Étape 3 — Rapporter

Un bilan court : critère de sortie atteint ou pas, exigences couvertes / total, ce qui a été changé
dans la méthode et les skills, ce qui reste ouvert, et le lot suivant avec ses dépendances. Puis :

```
Lot N clos. Prochaine étape : `/ecrire-plan N+1`.
```

## Ce que le skill ne fait jamais

- Clôturer sans recette, ou avec une anomalie bloquante sans plan.
- Écrire une leçon sans l'appliquer, ou l'appliquer en ajoutant une section historique.
- Modifier un ADR accepté (→ nouvel ADR).
- Laisser une `E-nn` du lot sans état.
