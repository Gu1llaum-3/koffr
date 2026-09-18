# Plan lot N — {{Nom}}

> Statut : brouillon | **validé par le propriétaire le AAAA-MM-JJ** | en exécution | terminé.
> Exécuté par `/executer-plan`. Les règles communes à tous les plans sont dans `METHODE.md`
> § « Exécution d'un plan » et ne sont pas répétées ici.

## Périmètre

- Exigences couvertes : `E-nn`, `E-nn`… (une ligne chacune, avec le § du cahier des charges).
- Exigences **écartées** de ce lot et pourquoi (renvoi `D-nn`, ADR ou lot ultérieur).
- Critère de sortie, repris de `ROADMAP.md`, reformulé en points vérifiables.

## État de départ (vérifié le AAAA-MM-JJ)

Ce qui existe déjà et sur quoi le lot s'appuie, **vérifié** dans le code et la base au moment
d'écrire le plan, pas supposé. Comptages si des données existent.

## Ce que le cahier des charges dit, et ce qu'il ne dit pas

- Dit : …
- Ne dit pas : … → `Q-nn` posée le …, ou hypothèse `N-n` ci-dessous, annoncée à la recette.
- Contredit : … → `D-nn`.

## Décisions d'implémentation

Numérotées `N-1`, `N-2`… propres à ce plan. Chacune : la décision, sa raison, ce qu'elle exclut.
Une décision structurante (qui survivra au lot) devient un ADR avant l'exécution.

- **N-1 {{Titre}}.** …

## Vagues

Une vague = une branche `lotN/wave-n-<slug-en>`, mergée quand la vérification est verte. Chaque
tâche est vérifiable ; une tâche qui produit du code commence par son test.

### Vague 1 — {{Nom}} (`lotN/wave-1-<slug>`)

- [ ] **1.1** {{Test d'abord : `chemin/fichier.spec.ts` — cas à couvrir.}} Puis le code.
- [ ] **1.2** {{Règle `MOD-nn` écrite dans `rules.md` avec sa source `E-nn`.}}
- [ ] **1.3** Vague verte : `verify`, commit `feat(<module>): <what changed>`.

### Vague 2 — {{Nom}} (`lotN/wave-2-<slug>`)

- [ ] **2.1** …

## Vérification de bout en bout

Ce qu'on fait, à la main ou par script, pour constater que le critère de sortie est atteint : les
parcours joués, les comptages attendus, les écrans ouverts.

## Recette

- Scénario : `docs/recette/lot-N-scenario.md`.
- Décisions attendues de la session : …
- Ce qui est **voulu** et pourrait passer pour un bug : …

## Risques et points à vérifier en route

Une ligne chacun, avec ce qu'on fera si le risque se réalise.

## Ce qui reste ouvert

`D-nn` et `Q-nn` qui ne bloquent pas ce lot mais le suivant.

## Journal d'exécution

Rempli par `/executer-plan` : échecs, décisions `N-n` ajoutées en route, écarts au plan, datés.
