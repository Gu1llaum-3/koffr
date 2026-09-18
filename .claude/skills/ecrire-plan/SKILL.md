---
name: ecrire-plan
description: Écrit le plan d'exécution d'un lot de la ROADMAP dans docs/plans/lot-N-<nom>.md, conforme au gabarit — périmètre en exigences E-nn, état de départ vérifié dans le code, décisions d'implémentation N-n, vagues avec tâches TDD vérifiables, recette. Ne code rien ; le plan est validé par le propriétaire avant /executer-plan. À utiliser avec le numéro du lot en argument.
---

# Écrire le plan d'un lot

Un plan est le **contrat d'exécution** d'un lot : ce qu'on livre, dans quel ordre, comment on
saura que c'est fait. Il est écrit avant la première ligne de code, validé par le propriétaire,
puis exécuté mécaniquement par `/executer-plan`. Un bon plan rend l'exécution ennuyeuse.

## Avant d'écrire

1. Lire `ROADMAP.md` : la ligne du lot, son périmètre `E-nn`, son critère de sortie, ses
   dépendances. Si une `D-nn` ou une `Q-nn` bloquante est ouverte, le dire en tête du plan et
   proposer soit d'attendre, soit une hypothèse `N-n` annoncée à la recette.
2. Lire chaque `E-nn` du lot dans `docs/cdc/exigences.md` **et le § du CDC** qu'elle cite. Pas de
   plan depuis le résumé.
3. Lire les ADR acceptés qui touchent le lot, `ARCHITECTURE.md`, les `rules.md` des modules
   concernés, la rétro du lot précédent (`docs/retro/`).
4. **Vérifier l'état de départ dans le code et la base** : les fichiers existent-ils, les tables
   ont-elles des lignes, les tests passent-ils. Écrire ce qu'on a constaté, daté. Un plan bâti sur
   une supposition fausse coûte une vague.
5. **Mesurer** si des données existent : comptages, valeurs distinctes, cas limites présents.
   Coller les chiffres dans le plan.

## Écrire

Depuis `docs/plans/0000-template.md`, dans `docs/plans/lot-N-<nom-kebab>.md`. Les règles communes
sont dans `METHODE.md` § « Exécution d'un plan » : **on ne les répète pas et on n'hérite pas d'un
autre plan** (« celles du lot précédent, plus… » est interdit). Le plan n'écrit que ce qui lui est
propre.

### Décisions d'implémentation `N-n`

Tout ce qu'un exécutant devrait sinon décider seul : forme d'une table, nom d'un cas d'usage,
ordre de tri par défaut, comportement d'un cas limite, hypothèse en attente d'une `Q-nn`. Chacune
avec sa raison et ce qu'elle exclut. Si une `N-n` survivra au lot (elle contraint les suivants),
c'est un ADR : l'écrire avec `/ecrire-adr` **avant** la validation du plan.

### Vagues

- Une vague = une branche, deux à six tâches, mergeable seule, `verify` vert à la fin. Une vague
  qui ne peut pas être mergée seule est mal coupée.
- Ordre : ce qui débloque d'abord (schéma et règles avant écrans), les inconnues tôt.
- Chaque tâche est **vérifiable** : elle nomme le fichier de test et les cas, puis le code. Une
  tâche « implémenter X » sans test est réécrite ou justifiée (interface, configuration).
- Chaque règle métier du lot a sa ligne `MOD-nn` dans le `rules.md` du module, citée par la tâche
  qui la code, avec sa source `E-nn`.
- La dernière tâche de chaque vague : « Vague verte : `verify`, commit `<type>(<scope>): <what>` »
  en anglais.
- Noms de branches en anglais : `lotN/wave-n-<slug-en>`.

### Recette

Le scénario de recette s'écrit **avec le plan**, pas après : `docs/recette/lot-N-scenario.md`
depuis le gabarit de `docs/recette/README.md`. Ce qui est voulu et pourrait passer pour un bug y
est listé dès maintenant, avec son `N-n`.

## Présenter et s'arrêter

Résumer au propriétaire : périmètre, ce qui est écarté et pourquoi, les `N-n` qui méritent son
avis, les vagues en une ligne chacune, les risques. Marquer le plan `brouillon`. **Ne pas
exécuter.** Quand le propriétaire valide, écrire « validé par le propriétaire le AAAA-MM-JJ » en
tête, passer la roadmap à `plan en cours` → `en cours`, et proposer `/executer-plan lot-N`.

## Ce que le skill ne fait jamais

- Écrire du code ou un schéma « pour voir ».
- Élargir le périmètre au-delà des `E-nn` du lot : une idée va dans `backlog.md`.
- Décider une question ouverte : elle devient une `N-n` hypothèse, annoncée, ou bloque le plan.
- Chiffrer en semaines.
