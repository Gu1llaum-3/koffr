# Méthode de pilotage

Comment on travaille sur ce projet, avec Claude Code comme exécutant principal. Ce fichier est la
référence de la méthode ; `CLAUDE.md` renvoie ici plutôt que de la répéter. On le modifie par une
rétrospective (`docs/retro/`), pas au fil de l'eau.

## Trois principes

1. **Une décision qui n'est pas écrite ne se code pas.** Un choix structurant est un ADR accepté ;
   un arbitrage en suspens est une ligne de `docs/decisions.md` ; une inconnue métier est une ligne
   de `docs/questions.md`. Claude propose, le propriétaire tranche, et la trace précède le code.
2. **Un document porte un état, jamais un journal.** La roadmap dit où on en est, pas comment on y
   est arrivé. Le récit vit dans les plans (pendant) et les rétros (après). Un document qui
   grossit sans qu'on le relise est un signal : on le scinde ou on le purge.
3. **Une règle vérifiable vit dans l'outillage.** Lint, typage, tests, CI. `CLAUDE.md` ne garde que
   ce que l'outil ne peut pas vérifier et que le code ne dit pas.

## Le cycle

```
cahier des charges ──/demarrer-projet──► exigences E-nn, questions Q-nn, décisions D-nn,
                                          ADR proposés, ROADMAP par lots
                                                   │
                  ┌────────────────────────────────┘
                  ▼
      lot suivant de ROADMAP.md ──/ecrire-plan──► docs/plans/lot-N-<nom>.md (validé par le propriétaire)
                                                   │
                                           /executer-plan : une vague = une branche, TDD,
                                           vérification verte, merge ; pause entre les vagues
                                                   │
                                           recette avec un utilisateur (docs/recette/)
                                                   │
                                  /cloturer-lot ──► docs/retro/lot-N.md, roadmap cochée,
                                                    leçons fondues dans le skill de stack,
                                                    METHODE.md amendée si besoin
```

Un seul plan en exécution à la fois. Un travail transverse (sécurité, passe d'interface,
corrections de recette) a lui aussi un plan et une ligne dans la roadmap ; il ne se glisse pas
« entre deux » sans trace.

## Les documents et leur rôle

| Document                        | Il porte                                                                                  | Il ne porte pas                                           |
| ------------------------------- | ----------------------------------------------------------------------------------------- | --------------------------------------------------------- |
| `CLAUDE.md`                     | Où lire, quoi lancer, conventions non déductibles, Definition of done                    | La méthode (ici), l'architecture, l'état du projet        |
| `ARCHITECTURE.md`               | Structure, règles de dépendance, flux d'une requête, modules                             | Des décisions (ADR), des règles métier (`rules.md`)       |
| `ROADMAP.md`                    | Un lot par ligne : périmètre en `E-nn`, critère de sortie, plan, statut en un mot         | Sous-tâches, récit, registres, méthode                    |
| `docs/plans/lot-N-<nom>.md`     | Le contrat d'exécution d'un lot : état de départ vérifié, décisions `N-n`, vagues, cases  | Ce qui vaut pour tous les lots (ici § Exécution)          |
| `docs/adr/NNNN-*.md`            | Une décision figée, son contexte, ses conséquences, les alternatives écartées             | Une décision non prise (→ `decisions.md`)                 |
| `docs/decisions.md`             | Les arbitrages en attente, qui tranche, ce qui est bloqué                                 | Des décisions prises (→ ADR), des questions métier        |
| `docs/questions.md`             | Ce qu'on demande aux utilisateurs et au métier, la réponse datée, ce qu'on en a fait      | Des arbitrages de projet                                  |
| `docs/backlog.md`               | Ce qu'on a vu et refusé de faire maintenant, d'où ça vient, ce que ça coûterait           | Des bugs (→ plan de corrections), des exigences du CDC    |
| `docs/cdc/exigences.md`         | Le registre `E-nn` : chaque exigence du CDC, sa source, son lot, son état                 | Des interprétations non validées                          |
| `<module>/rules.md`             | Les règles métier du module, une par test, avec leur source                              | De la doc technique, du récit                             |
| `docs/recette/`                 | Scénarios de recette, anomalies `A-nn`, décisions attendues de la session                 |                                                           |
| `docs/retro/lot-N.md`           | Ce qui a marché, ce qui a coûté, ce qu'on change dans la méthode et dans les skills       | L'état du lot (roadmap)                                   |
| `.claude/skills/*`              | Le savoir-faire répétable, thématique, au présent                                         | L'histoire du projet (« leçons du lot N »)                |
| Mémoire persistante de Claude   | Préférences et retours du propriétaire                                                    | L'état du projet, qui vit dans le dépôt                   |

## Les registres

Un préfixe par registre, jamais deux registres avec la même lettre. La table de référence est
dans `docs/README.md` ; tout nouveau registre s'y déclare avant d'exister.

| Préfixe    | Registre                              | Fichier                        | Numérotation                        |
| ---------- | ------------------------------------- | ------------------------------ | ----------------------------------- |
| `E-nn`     | Exigences du cahier des charges       | `docs/cdc/exigences.md`        | Continue, jamais réutilisée         |
| `ADR-NNNN` | Décisions figées                      | `docs/adr/`                    | Continue, jamais réutilisée         |
| `D-nn`     | Décisions en attente                  | `docs/decisions.md`            | Continue ; une D tranchée est barrée, pas supprimée |
| `Q-nn`     | Questions au métier                   | `docs/questions.md`            | Continue ; une Q répondue est barrée |
| `B-nn`     | Backlog                               | `docs/backlog.md`              | Continue                            |
| `A-nn`     | Anomalies de recette                  | `docs/recette/anomalies.md`    | Continue                            |
| `N-n`      | Décisions d'implémentation d'un plan  | Le plan lui-même               | Par plan ; citée hors plan `lot-4/N-3` |
| `<MOD>-nn` | Règles métier d'un module             | `<module>/rules.md`            | Par module ; le code `MOD` (2-4 lettres) est déclaré dans `ARCHITECTURE.md` |

## Langues et nommage

- **Code** : identifiants, commentaires, noms de fichiers de code, noms de branches, messages de
  commit, noms de tables et de colonnes : **anglais**.
- **Pilotage** : tout fichier que Claude lit ou écrit pour piloter le projet (`CLAUDE.md`,
  `ARCHITECTURE.md`, `ROADMAP.md`, `METHODE.md`, ADR, plans, registres, rétros, `rules.md`,
  skills) : **français**.
- **Interface et messages aux utilisateurs** : la langue des utilisateurs, fixée par un ADR.
- Un glossaire métier FR → EN vit dans `CLAUDE.md` ; un identifiant, une fois choisi, ne change
  plus.

## Exécution d'un plan

Ces règles valent pour tous les plans ; un plan ne les répète pas et n'en hérite pas d'un autre,
il n'écrit que ce qui lui est propre.

1. Le plan est **validé par le propriétaire** avant la première ligne de code.
2. **Une vague = une branche** `lot<N>/wave-<n>-<slug-en>` depuis `main`, merge `--no-ff` quand la
   vérification est verte. `main` est toujours vert.
3. **TDD** pour tout code de domaine et de serveur : le test d'abord, rouge, puis le code minimal,
   puis refactor. Pour l'interface et la configuration, un test n'est pas toujours possible : on le
   dit plutôt que de forcer. **Quand le découpage du plan rend le rouge impossible** — une tâche
   qui teste ce que la précédente a écrit —, on le **dit dans le journal**, et on prouve au moins
   que le test mord : retirer la contrainte, constater l'échec, la remettre. Un rouge par
   suppression vaut moins qu'un rouge par antériorité, et ne se présente jamais comme tel.
4. **Vérification complète à la fin de chaque vague** (la commande `verify` de `CLAUDE.md`), codes
   de retour lus, pas seulement la dernière ligne. Quand une CI existe, on attend sa **fin** avant
   de merger, en s'assurant d'abord que l'exécution qu'on surveille **existe** : une commande de
   surveillance lancée trop tôt échoue sans rien avoir observé, et son échec ressemble à celui
   d'une CI rouge.
5. **Mesurer avant de modéliser** : quand une donnée existe (base, export, fichier), une requête de
   dix secondes précède la décision de schéma.
5. bis **L'état de départ se vérifie aussi hors du dépôt local.** Tout ce que le plan **nomme** —
   dépôt distant, registre de paquets, nom de domaine, machine cible — se regarde avant d'écrire
   « rien n'existe ». Un `git rev-parse` qui échoue dit que le dossier n'est pas sous git, pas que
   le dépôt distant est vide.
6. **Un échec arrête la vague.** On rapporte, on note l'échec sous la tâche dans le plan, on
   attend. Une tâche ambiguë ou destructrice attend une confirmation.
7. **Une case se coche au moment où la tâche passe**, pas en bloc à la fin.
8. **Ce qui sort du plan ne se code pas** : une idée va dans `docs/backlog.md`, une inconnue dans
   `docs/questions.md`, un arbitrage dans `docs/decisions.md`. Le plan est amendé par une décision
   `N-n` datée si le périmètre bouge.
9. **Aucune écriture externe depuis un poste de développement** : appels tiers, mails, fichiers
   déposés chez un partenaire passent par une porte de sortie qui journalise et n'envoie pas hors
   production. Aucun hôte, jeton ou destinataire dans le code.
10. **Pause entre deux vagues**, sauf demande explicite d'enchaîner.

## Definition of done d'un lot

1. Toutes les vagues du plan sont mergées, `verify` vert sur `main`, CI verte.
2. Chaque exigence `E-nn` du lot est soit couverte (test + ligne de `rules.md` avec sa source),
   soit renvoyée par une décision écrite (`D-nn` ou ADR).
3. Aucune règle métier sans test, aucun test sans règle nommée.
4. Toute mutation vérifie les droits dans la couche métier, jamais dans la couche transport.
5. Toute évolution de schéma a sa migration relue.
6. Recette faite avec un utilisateur, **sur une machine distincte du poste de développement**,
   anomalies `A-nn` consignées, décisions attendues prises ou inscrites en `D-nn`. Quand la recette
   a donné lieu à des corrections, le scénario est **rejoué en entier** après elles : une recette
   corrigée mais non rejouée ne voit pas ce que ses propres corrections ont cassé.
7. `docs/retro/lot-N.md` écrite, roadmap cochée, ADR écrits pour toute décision structurante prise
   en route, skill de stack amendé, `METHODE.md` amendée si la rétro le demande.

## Règles de pilotage

- **Règle d'arrêt** : un lot qui dépasse de moitié son périmètre ou son budget s'arrête ; on
  re-découpe et on l'écrit dans un ADR ou une `D-nn`.
- **Recette régulière** avec un utilisateur réel à partir du premier lot qui produit un écran. Sans
  créneau tenu deux fois de suite, le lot est en risque et la roadmap le dit.
- **Pas de chiffrage en semaines dans la roadmap** : on pilote par lots livrés et critères de sortie.
  Un chiffrage, s'il est demandé, vit dans le plan et se relit à la rétro.
- **Le cahier des charges est une source, pas une vérité** : une exigence qui contredit une mesure
  ou un usage observé devient une `Q-nn` ou une `D-nn`, pas une ligne de code.
