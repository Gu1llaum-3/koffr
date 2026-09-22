# ADR-0016 — La politique de sauvegarde est fixée : tampon, destinataires, portée, estimation

- **Date** : 2026-09-22
- **Statut** : **accepté** — 2026-09-22
- **Exigences** : `E-029`, `E-030`, `E-053`, `E-058`, `E-061`, `E-073`, `E-132`
- **Références** : recette du lot 2 (`docs/recette/lot-2-scenario.md`, session du 2026-09-22) ;
  questions `Q-01`, `Q-04`, `Q-07`, `Q-08` ; décisions `N-1`, `N-2`, `N-3`, `N-4` et `N-12` du plan
  du lot 2 ; CDC § 4.5, § 5.3

## Contexte

Quatre questions ouvertes depuis le démarrage du projet ont été implémentées **sous hypothèse** au
lot 2, et annoncées comme telles à la recette. La session du 2026-09-22 les a tranchées sur un parc
réel — PostgreSQL 16.15 de 372 Mo, MariaDB 11.4.13 — et non sur un raisonnement.

Les mesures qui ont pesé :

| Base | Dump brut | Archive | Taux réel | Ce que koffr réservait |
| --- | --- | --- | --- | --- |
| `boutique` (PostgreSQL, 372 Mo) | 295 Mo | 23,1 Mo | **7,8 %** | 139 Mo |
| `erp` (MariaDB, 12 Mo) | 9,4 Mo | 0,37 Mo | **3,9 %** | 4,7 Mo |

Et le contraste entre les deux modes, échantillonné pendant les jobs :

| Mode | Pic du tampon | Échantillons où le dump et l'envoi coexistent |
| --- | --- | --- |
| `stage` | 23,1 Mo (= l'archive) | **0** |
| `stream` | **0** | **46** |

En `stream`, la transaction reste donc ouverte sur la production pendant **tout** l'envoi. Ce n'est
pas un détail de performance : c'est la durée pendant laquelle le MVCC de la base gonfle, et elle
dépend de la bande passante d'une destination distante.

## Décision

**La politique de sauvegarde est celle-ci, et elle ne se rediscute plus par tâche.**

- **`Q-01` — le mode de tampon par défaut est `auto`.** Il préfère `stage` et ne bascule en
  `stream` que si le disque ne suit pas. Le mode **effectivement appliqué** et sa raison sont
  enregistrés, parce que deux exécutions de la même base peuvent légitimement différer.
- **`Q-04` — les destinataires sont globaux, et une liste déclarée par base les *remplace*.** Le
  champ existe dans le schéma **dès maintenant**, même vide. Jamais de fusion des deux listes :
  on doit pouvoir dire pour qui une archive est chiffrée en regardant **un seul** endroit.
- **`Q-07` — une entrée de configuration sauvegarde *une base*, sans droits ni propriétaires**
  (`--no-owner --no-privileges`). L'archive se restaure dans n'importe quel cluster, y compris un
  cluster reconstruit après sinistre où les rôles d'origine n'existent plus. **Conserver les
  propriétaires sans sauvegarder les rôles serait le pire des deux mondes** : un dump qui ne se
  restaure que sur la machine qu'on vient de perdre.
- **`Q-08` — sans historique, la taille attendue d'une archive est la taille de la base *divisée
  par 8*** (12,5 %), et non par 4. La marge reste **× 1,5**, le seuil `-Fd` reste **20 Go**, la
  compression reste **`zstd:3`** : rien ne les a remis en cause.
- **Les objets globaux d'un cluster remontent du backlog.** `B-01` n'est plus une idée : c'est ce
  qui rend la reprise après sinistre réelle, et `D-08` en fixera le lot.

## Conséquences

- **`N-12` est amendée** : le diviseur passe de 4 à 8. Sur-réserver n'est pas gratuit — cela
  bascule en `stream`, dont la recette vient de montrer le coût. Le risque symétrique est borné :
  12,5 % reste au-dessus des 7,8 % du pire cas mesuré, et le contrôle d'espace n'est de toute façon
  qu'une estimation, pas une garantie.
- **Le diviseur cesse de compter au lot 3.** Dès que le catalogue enregistre la taille réelle des
  archives, l'estimation vient de l'historique et non plus d'une hypothèse (`N-13`).
- **Le champ de destinataires par base est une évolution de schéma**, à livrer au plan de
  corrections : l'ajouter plus tard casserait des configurations écrites entre-temps.
- **Le README doit dire ce que koffr *ne* sauvegarde pas** — rôles, tablespaces, droits — plutôt
  que de laisser un exploitant le découvrir le jour de l'incident.
- **Ce qui rouvrirait la décision** : une base dont les données sont déjà compressées (images,
  documents) ferait tomber le taux réel au-dessus de 12,5 % et remettrait `Q-08` sur la table ;
  la livraison de `B-01` rendrait possible le couple standard de l'entreprise — dump avec
  propriétaires **plus** objets globaux — et rouvrirait `Q-07`.

## Alternatives écartées

- **`stage` par défaut, partout** : comportement identique sur toutes les machines, plus facile à
  raisonner. Écarté parce qu'un disque juste ferait alors **échouer** le job au lieu de le
  dégrader, et qu'une sauvegarde dégradée vaut mieux qu'une sauvegarde absente.
- **`stream` par défaut** : aucun disque requis. Écarté sur la mesure — 46 échantillons de
  transaction ouverte pendant l'envoi, sans reprise possible après une coupure.
- **Fusionner les deux listes de destinataires** : pratique pour ajouter une clé à une base
  sensible sans répéter le séquestre. Écarté parce que personne ne saurait plus, en lisant une
  base, pour qui son archive est chiffrée.
- **Conserver droits et propriétaires** : restauration fidèle. Écarté tant que `B-01` n'existe
  pas ; à rouvrir quand il existera.
- **Rendre la portée réglable par base** : sert les deux cas tout de suite. Écarté parce que
  l'option « fidèle » produirait aujourd'hui des archives qui ne se restaurent nulle part ailleurs
  — une option qui piège.
- **Garder le diviseur à 4** : prudent. Écarté parce que la prudence a un coût mesuré, et qu'on
  vient de le mesurer.
