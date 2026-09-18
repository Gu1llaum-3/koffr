# ADR-0006 — L'état local est un SQLite unique, aux types explicites

- **Date** : 2026-09-18
- **Statut** : accepté
- **Exigences** : `E-027`, `E-028`, `E-036`, `E-051`, `E-058`, `E-081`, `E-089`
- **Références** : lié à ADR-0002 (stack), ADR-0005 (format) ; `Q-19` et `Q-21` restent ouvertes

## Contexte

`E-027` impose SQLite via `modernc.org/sqlite`, `E-028` nomme sept tables. SQLite n'a ni type date,
ni type décimal, ni énumération : tout ce qui n'est pas décidé ici sera décidé par accident dans la
première requête écrite. `E-036` impose par ailleurs un fuseau applicatif déclaré, jamais hérité du
système, et le manifeste du § 5.3 montre des horodatages **avec décalage local**
(`2026-09-18T02:00:03+02:00`).

koffr n'a pas d'utilisateur concurrent : un seul processus écrit, avec un verrou par base (`E-051`).
Le verrou optimiste du gabarit du kit n'a donc pas d'objet ici.

## Décision

Un fichier `/var/lib/koffr/koffr.db`, en mode WAL, avec des types explicites et des migrations
versionnées.

- **Ouverture** : `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`, `synchronous=NORMAL`.
- **Horodatages** : `TEXT` au format RFC 3339 **en UTC** (`…Z`), une seule représentation en base.
  La conversion vers `agent.timezone` se fait à l'affichage et à l'écriture du manifeste, qui
  conserve le décalage local comme le montre le CDC.
- **Statuts** : `TEXT` contraint par `CHECK`, jamais d'entier, jamais d'énumération applicative
  silencieuse. Valeurs écrites en toutes lettres (`running`, `succeeded`, `failed`).
- **Tailles et durées** : `INTEGER`, en octets et en millisecondes. Aucun flottant nulle part.
- **Identifiants** : ULID (`TEXT`) pour les archives et les jobs (ADR-0005) ; les identifiants de
  base, de destination et de canal sont ceux de la configuration, tels quels.
- **Migrations** : fichiers SQL numérotés, embarqués par `embed`, appliquées au démarrage dans une
  transaction, jamais de retour arrière automatique. Le SQL généré se relit avant commit.
- **Audit** : `created_at` et `updated_at` sur chaque table ; l'historique des suppressions de
  rétention (`E-081`) est conservé dans sa propre table, indépendante de la disparition de l'archive.
- **Pas de suppression logique** sauf là où `E-081` l'impose ; pas de verrou optimiste (un seul
  processus écrivain, verrou par base).
- **Le catalogue est un index, jamais une source de vérité** : toute information nécessaire à une
  restauration vit aussi dans le manifeste déposé à côté de l'archive.

## Conséquences

- Stocker en UTC et afficher en heure locale impose une conversion à chaque frontière. C'est le seul
  moyen de rendre comparables deux exécutions qui encadrent un changement d'heure (`Q-18`).
- Les `CHECK` sur les statuts font échouer une migration qui oublierait une valeur : c'est voulu.
- La croissance de `job_logs` n'est pas traitée ici : `Q-21` reste ouverte, et le lot qui livre les
  journaux devra soit trancher, soit inscrire une `N-n` datée.
- Le cycle de vie du catalogue en cas de renommage d'une base (`Q-19`) n'est pas traité ici non plus.
- **Ce qui rouvrirait la décision** : une contention d'écriture (plusieurs processus koffr sur une
  même machine), qui n'est pas prévue.

## Alternatives écartées

- **Horodatages en heure locale en base** : rend incomparables les enregistrements de part et
  d'autre d'un changement d'heure, et dépend d'une configuration mutable.
- **Horodatages en entier Unix** : illisibles à l'inspection manuelle de la base, ce qui est
  précisément l'usage de secours d'un outil de sauvegarde.
- **Migrations écrites en Go** : moins relisibles qu'un fichier SQL, et `METHODE.md` demande que le
  SQL généré se relise avant commit.
