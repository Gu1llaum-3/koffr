---
name: ecrire-adr
description: Rédige un ADR dans docs/adr/ depuis une décision prise ou à proposer — numéro suivant, gabarit respecté, exigences E-nn et décision D-nn citées, alternatives écartées, index mis à jour, statut proposé tant que le propriétaire ne l'a pas accepté. À utiliser dès qu'un choix contraindra les lots suivants ou qu'une D-nn est tranchée.
---

# Écrire un ADR

Un ADR fige une décision structurante : ce qui contraindra les lots suivants, ce qu'on ne veut pas
rediscuter à chaque tâche. On ne modifie jamais un ADR accepté : on en écrit un nouveau qui
l'amende ou le remplace, et on met à jour le statut de l'ancien dans l'index.

## Quand

- Une `D-nn` est tranchée par le propriétaire.
- Une décision d'implémentation `N-n` d'un plan survivra au lot (elle contraint les suivants).
- Un choix technique est fait qui coûterait cher à défaire (stack, modèle de données, auth,
  découpage en modules, format d'échange avec un tiers).
- Une règle de `METHODE.md` ou de `CLAUDE.md` change de fond.

Pas d'ADR pour : une règle métier (→ `rules.md`), un détail d'implémentation local (→ `N-n`), une
idée (→ `backlog.md`).

## Écrire

1. Numéro : le suivant dans `docs/adr/README.md`, jamais réutilisé.
2. Fichier `docs/adr/NNNN-<titre-kebab>.md` depuis `0000-template.md`. Titre **à l'indicatif**,
   court : ce que la décision fait (« Les montants sont des décimaux à quatre chiffres »), pas un
   sujet (« Montants »).
3. **Contexte** : faits, mesures, § du CDC, `E-nn`, la `D-nn` ou la `Q-nn` d'origine. Pas
   d'opinion. Si on a mesuré, les chiffres sont là.
4. **Décision** : une phrase, puis trois à six modalités.
5. **Conséquences** : ce que ça impose, interdit, coûte ; **ce qui rouvrirait la décision**.
6. **Alternatives écartées** : une ligne chacune, avec la raison. Une alternative qu'on n'a pas
   envisagée n'est pas « écartée ».
7. Statut **`proposé`**, sauf si le propriétaire a pris la décision explicitement dans la
   conversation : alors `accepté`, avec la date.
8. Index `docs/adr/README.md` mis à jour ; statut de l'ADR amendé ou remplacé mis à jour ; la
   `D-nn` d'origine barrée avec le renvoi ; le plan ou `CLAUDE.md` mis à jour si l'ADR change une
   règle qu'ils énoncent.

## Présenter

Trois lignes au propriétaire : la décision, la raison principale, ce qu'elle exclut. Si le statut
est `proposé`, le dire et attendre.

## Ce que le skill ne fait jamais

- Passer un ADR en `accepté` de sa propre initiative.
- Modifier le corps d'un ADR accepté.
- Écrire un ADR sans alternative écartée ni condition de réouverture.
