# ADR-0012 — Le fichier reçoit tout, la console reçoit ce que son lecteur attend

- **Date** : 2026-09-18
- **Statut** : **accepté** — confirmé par le propriétaire le 2026-09-18, après lecture. Le choix
  lui avait d'abord été délégué ; la confirmation lève cette réserve.
- **Exigences** : `E-121`, `E-115`, `E-103`
- **Références** : anomalie `A-08` du rejeu de recette ; ADR-0003 (langues) ; lié à ADR-0009

## Contexte

`E-121` demande des « journaux structurés en JSON vers **la sortie standard**, doublés d'un fichier
avec rotation interne, et compatibles `journald` sans configuration ». Elle est au § 7 `N5`, parmi
les exigences **non fonctionnelles**, et son objectif est nommé : `journald`.

La vague 3 du plan de corrections a câblé les journaux en prenant cette phrase pour une règle
unique, valable partout. Le rejeu de recette a montré ce que ça donne :

```
$ koffr version
{"time":"2026-09-18T18:25:28Z","level":"INFO","msg":"command started","command":"version", …}
koffr dev (commit 94dc78f, built 2026-09-18T16:25:20Z) go1.27.1 linux/arm64
```

Un exploitant qui tape `koffr version` n'a rien demandé de tel. C'est `A-08`.

Le problème n'est pas le flux choisi : c'est qu'il y a **trois lecteurs**, et qu'ils n'attendent pas
la même chose.

| Lecteur | Ce qu'il attend |
| --- | --- |
| L'humain qui tape une commande | La réponse, et une erreur bien visible. Rien d'autre |
| `serve` sous `systemd` (lot 5) | Le flux JSON complet, ramassé par `journald` |
| Celui qui enquête après coup | **Tout**, y compris qui a lancé quoi et quand |

Par ailleurs `systemd` capture **la sortie standard comme la sortie d'erreur** dans le journal
(`StandardOutput=journal` et `StandardError=journal` sont les défauts d'un service). La lettre
« sortie standard » de `E-121` décrit donc la convention d'un **démon**, et elle n'engage rien sur
une commande tapée à la main — que le § 7 ne mentionne pas.

## Décision

**Le fichier reçoit tout ; la console reçoit ce que son lecteur attend.** Deux destinataires, deux
seuils, un seul flux d'événements.

- **Le fichier de `E-026`** (`/var/log/koffr/koffr.log`) reçoit **tous** les événements au niveau
  demandé, `info` par défaut. Une commande lancée à la main y laisse sa trace : dans un agent de
  sauvegarde, savoir qui a lancé quoi et quand a de la valeur.
- **La console d'une commande CLI** reçoit les **avertissements et les erreurs**, sur la **sortie
  d'erreur**. Le résultat de la commande, lui, va sur la sortie standard : `koffr config show`
  redirigé dans un fichier ne doit pas y ramasser de lignes de journal.
- **La console de `serve`** (lot 5) reçoit **tout**, sur la **sortie standard**, comme `E-121`
  l'écrit. C'est là que `journald` le lit.
- **`--log-level` fixe le niveau du fichier.** Quand l'utilisateur le pose **explicitement**, la
  console suit : demander `--log-level debug` et ne rien voir à l'écran serait absurde.
- Le masquage des attributs sensibles (`E-115`) s'applique aux **deux** destinataires : il vit dans
  le gestionnaire, pas dans l'un des deux chemins.

## Conséquences

- `internal/obs` distribue un même événement à plusieurs gestionnaires ayant chacun son seuil. La
  bibliothèque standard n'offre pas cela : c'est une trentaine de lignes à écrire et à tester.
- **L'écart à la lettre de `E-121` est ici, écrit.** Sur la CLI, les journaux ne vont pas sur la
  sortie standard. L'objectif de l'exigence — `journald` sans configuration — est tenu par `serve`,
  qui est le composant que `systemd` lance.
- Une commande silencieuse n'est plus muette pour autant : ses erreurs remontent par le code de
  retour et le message de `cmd/koffr`, pas par le journal.
- Le lot 5 devra **poser explicitement** le niveau console de `serve` et son flux. S'il l'oublie,
  un démon se tait : à vérifier à sa recette.
- **Ce qui rouvrirait la décision** : un exploitant qui veut voir le flux complet d'une commande
  sans passer par le fichier (déjà couvert par `--log-level`), ou un environnement où `systemd` ne
  ramasse pas la sortie d'erreur.

## Alternatives écartées

- **Supprimer la ligne `command started`.** Règle le bruit et perd la trace. Dans un outil de
  sauvegarde, la trace vaut plus que l'économie d'une ligne dans un fichier.
- **Appliquer `E-121` à la lettre partout** (tout sur la sortie standard). Rend `koffr config show
  > koffr.yaml` inutilisable, et fait de chaque commande un émetteur de bruit.
- **Un seul seuil, relevé à `warn`.** Le fichier perdrait la trace des commandes, qui est
  précisément ce qu'on veut y garder.
- **Deux drapeaux, `--log-level` et `--console-level`.** Une option de plus à comprendre pour un
  besoin que « le drapeau explicite fait suivre la console » couvre déjà.
