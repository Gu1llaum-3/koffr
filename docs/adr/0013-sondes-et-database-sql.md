# ADR-0013 — `internal/engine` peut ouvrir une connexion de base pour sonder, jamais pour lire

- **Date** : 2026-09-18
- **Statut** : **accepté** — par le propriétaire, le 2026-09-18
- **Exigences** : `E-041`, `E-011`, `E-104a`, `E-034`
- **Références** : **amende `AR-08` d'ADR-0010** ; lié à ADR-0002 (pile) et ADR-0004 (moteurs) ;
  `N-1` du plan du lot 1

## Contexte

Deux ADR acceptés se contredisent, et le lot 1 bute dessus.

**ADR-0010, règle `AR-08`** : « tout sauf `internal/state` — interdit d'importer `database/sql`,
`modernc.org/sqlite` ». Elle est vérifiée par `internal/arch` sur le graphe réel des imports, et
par `depguard`.

**ADR-0002** : « `jackc/pgx/v5` et `go-sql-driver/mysql` **pour les sondes et la détection de
famille, jamais pour le dump** ». Ces sondes vivent dans `internal/engine` (ADR-0010 : « adaptateurs
postgresql, mysql, mariadb — sondes, sous-process »).

Or `E-041` exige que la famille MySQL ou MariaDB soit détectée **à la connexion**, jamais déduite
de la configuration, et `E-104a` demande la version du serveur. Il faut donc se connecter.

Fait technique : **`go-sql-driver/mysql` n'expose pas d'API utilisable hors `database/sql`**. Son
`mysql.NewConnector` rend un `driver.Connector` de `database/sql/driver`, chemin de bas niveau qui
n'est ni documenté pour un usage direct ni couvert par sa compatibilité. `pgx/v5`, lui, a une API
native qui évite `database/sql` — mais la moitié MySQL du périmètre d'ADR-0004 n'a pas cette issue.

État constaté le 2026-09-18 : `internal/engine` ne contient qu'un `doc.go`, et aucun pilote de base
n'est dans `go.mod`. Rien n'est encore écrit contre cette contrainte.

**Ce que l'interdit visait.** ADR-0002 le dit en toutes lettres : « une règle de lint doit empêcher
qu'un jour un développeur *optimise* en lisant les tables par le pilote ». La cible est **la lecture
des données à sauvegarder** — le dump doit rester un sous-process —, et accessoirement le
cloisonnement de l'état local de koffr. Sonder un serveur pour sa version n'est ni l'un ni l'autre.

## Décision

**`database/sql` est réservé à `internal/state`, sauf `internal/engine`, qui peut l'utiliser pour
sonder un serveur — jamais pour lire les données d'une base à sauvegarder.**

- `AR-08` devient : `database/sql` est interdit partout **sauf** dans `internal/state` et
  `internal/engine`. `modernc.org/sqlite` reste réservé à `internal/state`, **sans exception** :
  `engine` n'a aucune raison de toucher l'état local de koffr.
- `AR-02` est **inchangée** : `internal/domain/**` garde l'interdit complet, `database/sql` compris.
  Le domaine ne se connecte à rien ; il reçoit ce qu'il lui faut par le port `ServerProbe`.
- Dans `engine`, une connexion de pilote ne sert qu'à **sonder** : joignabilité, version du serveur,
  famille, et les caractéristiques de schéma que les lots suivants exigeront (détection MyISAM pour
  la politique de tampon, `E-053`…`E-056`). **Jamais** à lire les données à sauvegarder : le dump
  reste un sous-process, c'est `E-001` et `P3`.
- `internal/arch` encode l'exception, **avec sa fixture violante** : un paquet qui n'est ni `state`
  ni `engine` et qui importe `database/sql` doit faire échouer le contrôle. `depguard` suit.
- **Ce que le lint ne peut pas exprimer** — « pour les sondes seulement » — est porté par une règle
  écrite de `internal/domain/resolve/rules.md` et par un test qui vérifie que la sonde n'émet
  **que** sa requête de version.

## Conséquences

- **La garantie « le dump est un sous-process » cesse d'être purement mécanique.** Jusqu'ici aucun
  paquet hors `state` ne pouvait ouvrir une connexion SQL ; désormais `engine` le peut. Ce qui la
  tient est un test qui épelle les requêtes émises, et la petite taille d'`engine`.
- Le compromis est assumé : sans lui, la moitié MySQL du périmètre d'ADR-0004 n'est pas
  implémentable, ou seulement par un chemin de bas niveau non supporté.
- `internal/arch` gagne une exception, donc une fixture de plus. Une règle assouplie sans fixture
  est une règle qu'on ne vérifie plus.
- La revue de code d'`engine` porte désormais une question fixe : *cette requête sert-elle à
  sonder ?* À poser à chaque ajout.
- **Ce qui rouvrirait la décision** : un pilote MySQL offrant une API de bas niveau supportée, qui
  rendrait l'exception inutile ; ou une sonde qui se mettrait à lire des données, auquel cas c'est
  la sonde qu'on corrige, pas la règle qu'on élargit.

## Alternatives écartées

- **Passer par `database/sql/driver` seul**, que la règle `AR-08` ne nomme pas. Techniquement
  possible, et c'est précisément le problème : on contournerait la règle **par sa formulation** au
  lieu de la modifier par une décision. Le chemin est de surcroît non supporté par le pilote.
- **Mettre les sondes dans `internal/state`.** `state` deviendrait le paquet qui se connecte aux
  bases **des autres**, à l'opposé de sa raison d'être — l'état local de koffr.
- **Un paquet `internal/probe` dédié, avec l'exception.** Ajoute un paquet pour une seule règle
  d'import, et sépare les sondes des adaptateurs de moteur avec lesquels elles partagent tout :
  chaînes de connexion, identifiants, erreurs typées.
- **Retirer MySQL du périmètre.** Contredit ADR-0004 et le § 3, et retire la moitié de la cible de
  `E-041`, qui est précisément de ne pas confondre les deux familles.
- **Garder `AR-08` et sonder en lançant `psql` et `mysql` en sous-process.** Déplace le problème :
  il faudrait alors que ces clients soient présents pour **diagnostiquer** l'absence de clients, ce
  que `doctor` doit justement savoir faire sans eux.
