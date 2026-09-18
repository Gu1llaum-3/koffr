---
name: executer-plan
description: Exécute un plan de lot validé (docs/plans/lot-N-*.md) vague par vague — une branche par vague, TDD, cases cochées au fil de l'eau, vérification complète avant merge, pause entre les vagues, arrêt sur échec ou ambiguïté. Reprend à la première tâche non cochée. À utiliser avec le nom du plan en argument.
---

# Exécuter un plan

Ce skill est destiné à **l'exécution uniquement**. On ne redessine pas, on n'audite pas, on ne
réécrit pas le plan : on l'exécute, on coche, on s'arrête quand quelque chose ne va pas. Les règles
communes sont dans `METHODE.md` § « Exécution d'un plan » ; ce qui suit en est le mode opératoire.

## Préalables

- Le plan porte « validé par le propriétaire le … » en tête. Sinon, s'arrêter : un plan brouillon
  ne s'exécute pas.
- `main` est propre et `verify` y est vert. Sinon, le dire avant tout.
- Le plan est celui en cours dans `ROADMAP.md`. Un autre plan en cours → s'arrêter et demander.

## Lecture et reprise

1. Lire le plan, détecter les vagues (sections `### Vague n`) et les tâches (`- [ ]`, `- [x]`).
2. Résumer : vagues, tâches par vague, **point de reprise** = première tâche non cochée de la
   première vague incomplète. Les tâches cochées sont considérées faites, sans les refaire.
3. Vérifier que la branche de la vague en cours existe si la vague est entamée ; sinon la créer
   depuis `main` : `lotN/wave-n-<slug-en>`.

## Une vague

1. Annoncer la vague et lister ses tâches non cochées.
2. Pour chaque tâche, dans l'ordre :
   - Si elle produit du code de domaine ou de serveur : **le test d'abord**, le lancer, constater
     le rouge, puis le code minimal, puis le vert, puis refactor.
   - Si elle écrit une règle : la ligne `MOD-nn` dans `rules.md` avec sa source, **avant** le code
     qui l'applique.
   - Si elle touche le schéma : générer la migration, **relire le SQL**, le dire.
   - Cocher la case **immédiatement** après le succès, en préservant le texte et le formatage du
     plan. Jamais en bloc à la fin.
3. Dernière tâche de la vague : `verify` complet, **codes de retour lus** ; parcours de bout en
   bout si un parcours critique est touché. Commit en anglais, conventionnel.
4. Merge `--no-ff` dans `main` quand tout est vert. Supprimer la branche.
5. **Micro-bilan** : ce qui a été livré (fichiers, règles, tests), les écarts au plan, ce qui reste.
   Pas « fait ».
6. **Pause** : `Vague n terminée. Passer à la vague n+1 « <titre> » ?` — sauf si le propriétaire a
   explicitement demandé d'enchaîner plusieurs vagues nommées.

## Quand ça ne va pas

- **Échec d'une tâche** (test qui ne passe pas après un effort raisonnable, commande qui échoue,
  contradiction avec le code existant) : arrêter la vague, écrire sous la tâche un bloc
  `> Échec (AAAA-MM-JJ) : …` dans le plan, rapporter, attendre.
- **Ambiguïté** : le plan ne dit pas ce qu'il faut faire dans un cas rencontré → s'arrêter, poser
  une question courte, et consigner la réponse en `N-n` dans le plan avant de continuer.
- **Le plan a tort** (l'état de départ était faux, une dépendance manque) : s'arrêter, dire ce qui
  est faux, proposer l'amendement en `N-n`. Ne pas contourner en silence.
- **Une idée en route** : `docs/backlog.md`, une ligne, et on continue. **Une inconnue métier** :
  `docs/questions.md`. **Un arbitrage** : `docs/decisions.md`. Rien de tout ça ne se code.
- **Un changement destructeur** (suppression de données, migration irréversible, écriture vers
  un tiers) : confirmation explicite, toujours, même si le plan le prévoit.

## Fin du plan

Quand la dernière vague est mergée : mettre le plan en `terminé`, rapporter le critère de sortie
point par point (atteint / pas atteint / pourquoi), passer la roadmap à `en recette`, et proposer
la session de recette depuis `docs/recette/lot-N-scenario.md`. La clôture (rétro, roadmap
`terminé`) est faite par `/cloturer-lot` après la recette.

## Ce que le skill ne fait jamais

- Exécuter un plan non validé, ou une tâche qui n'y est pas.
- Cocher une tâche dont la vérification n'a pas tourné.
- Merger une vague dont `verify` n'est pas vert, ou en sautant la relecture du SQL généré.
- Réécrire le plan au-delà des `N-n`, des blocs d'échec et des cases.
- Enchaîner les vagues sans pause quand ce n'est pas demandé.
