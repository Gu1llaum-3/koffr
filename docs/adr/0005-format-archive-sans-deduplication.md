# ADR-0005 — Une sauvegarde produit une archive autonome, sans déduplication

- **Date** : 2026-09-18
- **Statut** : accepté
- **Exigences** : `E-025`, `E-057`, `E-058`, `E-059`, `E-070`, `E-072`, `E-075`, `E-129`
- **Références** : tranche `D-03` ; CDC § 10 et § 11.1 ; lié à ADR-0007 (chiffrement)

## Contexte

Le CDC exige de trancher la déduplication **maintenant** : « change entièrement le format de
stockage et ne se rattrape pas après coup » (§ 11.1), et « à décider avant la v1 si l'objectif est de
concurrencer les outils à dépôt, après si l'objectif est la fiabilité du dump logique » (§ 10).

Deux exigences Obligatoire cadrent la réponse. `E-075` (`F6.4`) : une archive doit rester
déchiffrable avec l'outil `age` standard, sans koffr, « aucun format maison ». `E-070` (`F5.5`) : le
chemin distant est déterministe et lisible, pour qu'« un dépôt reste exploitable si l'agent
disparaît ». Un dépôt dédupliqué est, par construction, un format propriétaire avec un index : il
contredit les deux.

## Décision

Une exécution de sauvegarde produit **une archive autonome**, restaurable seule. Pas de
déduplication, pas de sauvegarde incrémentale, pas de dépôt à blocs.

- Chaîne de production figée : dump → zstd → `age` → écriture, en flux, empreinte calculée au vol
  (`E-025`).
- Nommage : `<base>/<AAAA>/<MM>/<base>_<horodatage>_<id>.<ext>` (`E-070`), avec une extension qui dit
  la chaîne appliquée (`.pgdump.zst.age`, `.sql.zst.age`).
- Identifiant d'archive : **ULID** — triable par le temps, sans coordination, lisible. Le CDC montre
  `01JQ8F3K2M7X9P4W` (16 caractères) alors qu'un ULID en compte 26 ; l'exemple est traité comme
  illustratif et c'est l'ULID complet qui est retenu.
- À côté de chaque archive, sur **chaque** destination, un manifeste JSON non chiffré portant les
  champs de `E-058`, sans aucun identifiant de connexion (`E-059`).
- Restaurer ne demande que : l'archive, le manifeste, la clé privée, et un outil compatible. Jamais
  le catalogue, jamais koffr.

## Conséquences

- Le coût de stockage est celui du dump logique complet, chaque nuit, compressé. C'est le prix
  assumé de la portabilité et de la restaurabilité à froid.
- koffr ne concurrence pas les outils à dépôt (restic, borg, kopia) sur le volume. Il se positionne
  sur la justesse du dump logique et la démonstrabilité — ce que le § 1.2 appelle « justesse
  démontrable ».
- Le catalogue local (`E-028`) est un **index**, jamais une source de vérité : sa perte ne perd
  aucune archive. Cela doit être vrai par construction et testé.
- `E-129` passe de « écartée du MVP » à « écartée du produit ». Elle reste inscrite au registre,
  barrée, avec renvoi vers cet ADR.
- **Ce qui rouvrirait la décision** : renoncer à `E-075` et `E-070`, c'est-à-dire accepter un format
  maison. Cela ne se fait pas par une option de configuration mais par un nouvel ADR qui remplace
  celui-ci, et par un nouveau CDC — c'est un autre produit.

## Alternatives écartées

- **Déduplication dès le MVP** : contredit `E-075` et `E-070`, allonge le MVP d'un lot entier, et
  déplace le risque vers un format maison sur un outil de sauvegarde — ce que le § 1.3 refuse
  explicitement pour les dumpers.
- **Écartée du MVP mais rouverte en v2** : laisse la question peser sur chaque décision de format et
  obligerait à rouvrir `E-075`, `E-070`, donc une partie du § 6.
- **Incrémental par `--incremental` de PostgreSQL 17+** : ne s'applique qu'à la sauvegarde physique,
  hors périmètre (`E-003`).
