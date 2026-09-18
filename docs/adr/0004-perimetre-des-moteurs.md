# ADR-0004 — Le périmètre des moteurs est celui du § 3, et la matrice de tests le couvre entièrement

- **Date** : 2026-09-18
- **Statut** : accepté
- **Exigences** : `E-011`, `E-041`, `E-050`, `E-123`, `E-056`
- **Références** : répond à `Q-14` ; CDC § 3 et `N7`

## Contexte

Le § 3 annonce « PostgreSQL 12–18, MySQL 8.x, MariaDB 10.6+ ». `N7` (`E-123`) n'exige des tests
d'intégration réels que contre « PostgreSQL 13 à 18 et MariaDB 10.6, 10.11 et 11.4 ». MySQL d'Oracle
et PostgreSQL 12 sont donc annoncés et jamais testés — alors que `E-041` (`F2.4`) fait de la
non-confusion des familles MySQL et MariaDB un point dur du produit, et que la moitié de cette règle
est `mysqldump` d'Oracle.

Un moteur annoncé et non testé est exactement ce que `P3` et `P4` refusent : une garantie qu'on ne
peut pas démontrer.

## Décision

Le périmètre du § 3 fait foi. La matrice de tests est **complétée** pour le couvrir.

- Moteurs supportés : PostgreSQL 12 à 18, MySQL 8.x (Oracle), MariaDB 10.6+.
- Matrice d'intégration : PostgreSQL 12, 13, 15, 16, 17, 18 ; MySQL 8.0 et 8.4 ; MariaDB 10.6, 10.11
  et 11.4. Chaque entrée joue l'aller-retour sauvegarde puis restauration (`E-123`).
- La matrice complète tourne en intégration continue sur `main` et avant une publication ; un
  sous-ensemble (la version la plus récente de chaque famille) tourne à chaque vague, pour que le
  temps de retour reste utilisable.
- `E-123` est reformulée pour refléter cette matrice ; le CDC reçu n'est pas modifié.

## Conséquences

- La famille MySQL d'Oracle impose un second binaire de dump (`mysqldump`) et son propre jeu de
  pièges (plugins d'authentification, options divergentes) : c'est un coût de développement réel,
  pas seulement un coût de CI.
- `E-041` garde tout son sens dans les deux sens : il faut refuser d'utiliser `mysqldump` d'Oracle
  trouvé sur l'hôte pour dumper une MariaDB, même quand c'est le seul outil disponible.
- PostgreSQL 12 est en fin de support amont depuis novembre 2024. Le coût de son maintien est faible
  (la règle `major(pg_dump) ≥ major(serveur)` fait qu'un client récent le dumpe), mais il occupe une
  entrée de matrice. À réexaminer à la rétrospective du lot qui livre la sauvegarde.
- Le temps de CI est le prix de cette décision : onze instances de bases à démarrer.
- **Ce qui rouvrirait la décision** : un temps de CI devenu insupportable, ou l'absence d'utilisateur
  MySQL réel constatée à la recette.

## Alternatives écartées

- **MySQL dans le périmètre sans tests** : promettre ce qu'on ne démontre pas, contraire à `P3`/`P4`.
- **Retirer PostgreSQL 12** : aligne le § 3 sur `N7` et économise une entrée de matrice, mais retire
  du périmètre un serveur encore largement déployé, que le client récent sait dumper.
- **MariaDB seule pour le MVP** : MVP plus court, mais renonce à une moitié de la cible du § 1.1, et
  l'ajout ultérieur demanderait de revalider toute la résolution d'outils.
