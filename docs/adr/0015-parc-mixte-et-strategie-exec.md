# ADR-0015 — Un parc mixte MySQL et MariaDB exige la stratégie `exec`

- **Date** : 2026-09-19
- **Statut** : **accepté** — 2026-09-19
- **Exigences** : `E-013`, `E-041`, `E-046`, `E-124`
- **Références** : **borne ADR-0014** sans le rouvrir ; anomalie `A-10` de la recette du lot 1 ;
  CDC § 1.1 (la cible est un **parc**), § 5.2 `F2.4` et `F2.9`

## Contexte

ADR-0014 a sorti l'installation gérée du MVP, en s'appuyant sur un fait vrai : **une machine qui
héberge un serveur de base héberge presque toujours son client**. `postgresql-16` dépend de
`postgresql-client-16` ; l'image MariaDB livre `mariadb-dump`.

La recette du lot 1, sur une Ubuntu 26.04 réelle, a montré la limite de ce raisonnement — mesurée,
pas supposée :

```
mariadb-client-core : Conflicts: virtual-mysql-client-core
mysql-client-core   : Conflicts: mariadb-client-core
E: Unable to satisfy dependencies. Reached two conflicting assignments
```

**Sur Debian, Ubuntu et leurs dérivés, les clients MySQL et MariaDB ne peuvent pas coexister.**
Installer l'un désinstalle l'autre, et `/usr/bin/mysqldump` appartient alors à celui qui est
resté — au point que, pendant la recette, `mysqldump` était en réalité celui de **MariaDB**.

La conséquence est directe : une machine koffr ne peut **pas** sauvegarder à la fois une base MySQL
et une base MariaDB avec les seuls outils de l'hôte. Le raisonnement d'ADR-0014 est vrai **par
base** et faux **pour un parc mixte sur une seule machine** — or le § 1.1 vise précisément un parc.

Deux faits complètent le tableau. `E-041` **interdit** de se rabattre sur l'outil de l'autre
famille, et koffr l'applique : pendant la recette, il a refusé le `mysqldump` de MariaDB pour la
base MySQL et a répondu « found none ». Et la stratégie `exec` — l'outil pris **dans le conteneur
de la base** — est livrée, testée, et donne exactement le client qui correspond au serveur.

## Décision

**Pour un parc mixte MySQL et MariaDB, la stratégie `exec` n'est pas un confort : c'est la
réponse.** Elle est documentée comme telle.

- Le `README` dit le conflit de paquets, **cite son message**, et désigne `exec` comme la sortie.
  Un exploitant le lit avant de se heurter à `apt`, pas après.
- Trois configurations sont possibles, et koffr les sert toutes les trois : une machine **par
  famille** ; une machine qui ne porte **qu'une** famille en clients d'hôte et met l'autre en
  `exec` ; ou **tout** en `exec` quand les bases sont en conteneurs.
- **ADR-0014 n'est pas rouvert.** `A-10` ne montre pas que l'installation gérée manque : elle
  montre que `exec` en est le complément nécessaire. Les deux sources restantes de `E-013`
  couvrent le besoin ensemble, pas séparément.
- `E-041` reste **absolue** : jamais d'outil de l'autre famille, même seul disponible, même quand
  la distribution ne laisse pas d'autre choix. Le refus est le comportement correct.

## Conséquences

- **`exec` passe de fonctionnalité optionnelle à pièce maîtresse** pour une partie du parc visé.
  Ce qui en dépend prend du poids : le socket Docker devient un prérequis pour ces bases, et le
  lot 2 devra faire passer le **dump lui-même** par cette voie, pas seulement la résolution.
- **`E-124`, l'image conteneur du lot 7, se resserre.** Elle ne peut pas embarquer les deux
  familles de clients — le conflit s'y applique aussi. Elle embarquera au plus une famille et
  s'appuiera sur `exec` pour l'autre. À trancher au lot 7, mais la contrainte est connue dès
  maintenant.
- Une machine sans Docker **et** avec un parc mixte ne peut pas être servie. C'est une limite
  réelle du MVP, écrite ici plutôt que découverte en production.
- **Ce qui rouvrirait la décision** : une distribution qui permettrait les deux clients côte à côte
  — les paquets amont d'Oracle et de MariaDB, installés hors du gestionnaire de paquets, le
  permettent peut-être, ce qui n'a pas été mesuré ; ou le retour de l'installation gérée, qui
  poserait les deux familles dans `/var/lib/koffr/tools/` sans conflit.

## Alternatives écartées

- **Rouvrir ADR-0014 et remettre l'installation gérée** : elle résoudrait `A-10` proprement — deux
  familles dans un répertoire que la distribution ne contrôle pas — mais au prix exact que cette
  décision-là a retiré. À rouvrir si un exploitant se retrouve réellement coincé.
- **Assouplir `E-041` quand un seul outil est présent** : c'est-à-dire dumper une MariaDB avec
  l'outil d'Oracle parce qu'il ne reste que lui. Produit une archive que personne ne pourra
  restaurer — le défaut exact que le § 5.2 existe pour empêcher.
- **Documenter des paquets amont hors gestionnaire** (dépôts d'Oracle et de MariaDB) : peut-être
  praticable, mais non mesuré pendant la recette, et cela reviendrait à écrire une procédure
  d'installation manuelle par distribution — la charge qu'ADR-0014 refuse.
- **Ne rien dire et laisser l'exploitant découvrir le conflit** : c'est ce que fait le produit
  aujourd'hui, et la recette a montré ce que ça donne — un message « found none » parfaitement
  correct et parfaitement incompréhensible.
