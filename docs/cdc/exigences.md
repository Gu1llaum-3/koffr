# Registre des exigences — `E-nnn`

Table de traçabilité du projet. Une ligne = une exigence vérifiable, reformulée au plus près du
texte du CDC, avec sa source. Un lot, un plan, une règle `<MOD>-nn` et un test citent leur `E-nnn`.

Format et vocabulaire : `docs/cdc/README.md`. Analyse du document : `analyse.md`.

**Conventions propres à ce registre.**

- La numérotation est à **trois chiffres** (`E-001`) : le document en produit 132.
- Le CDC porte ses **propres identifiants stables** (`F2.3`, `N7`, `P1`), qu'il demande de citer
  dans les tickets, les commits et les tests (§ Instructions, point 4). La colonne « Source » les
  porte ; ils ne remplacent pas le `E-nnn`, ils le doublent.
- Le CDC est en Markdown, **sans pagination** : la source est un § et un identifiant, pas une page.
- **Priorité** : `doit` = `Obligatoire` du CDC, `devrait` = `Souhaitable`. Les exigences tirées de la
  prose (non numérotées par le CDC) portent la priorité que leur contexte impose, ou `à qualifier`.
- Les onze scénarios d'acceptation du § 8 ne sont **pas** des `E-nnn` : ce sont les recettes des
  exigences ci-dessous. Correspondance dans `analyse.md` § 10.
- Ordre : celui du CDC, pour qu'on puisse suivre le document ouvert à côté.

---

## § 1.3 — Non-objectifs

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-001 | Keeper ne réimplémente ni `pg_dump` ni `mysqldump` : il les appelle. | § 1.3, § 10, instruction 6 | hors périmètre | doit | — | écartée (CDC § 1.3) |
| E-002 | Keeper ne sauvegarde ni volumes, ni machines, ni fichiers : son périmètre est la base de données. | § 1.3 | hors périmètre | doit | — | écartée (CDC § 1.3) |
| E-003 | Keeper ne fait ni réplication, ni PITR, ni streaming WAL, ni sauvegarde physique. | § 1.3, § 10 | hors périmètre | doit | — | écartée (CDC § 1.3) |
| E-004 | Keeper n'est pas multi-tenant : le serveur central vise le parc d'une seule organisation. | § 1.3 | hors périmètre | doit | — | écartée (CDC § 1.3) |

## § 2 — Principes directeurs (contrat non négociable)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-005 | Toute fonction du produit — planification, chiffrement, vérification, rétention, restauration, alerte, consultation — est utilisable sans serveur central. | § 2 `P1` | contrainte | doit | 8 | à faire |
| E-006 | Aucun identifiant de base, aucune clé de stockage, aucune clé de chiffrement ne transite vers le serveur central ni n'y est stocké ; son seul pouvoir d'écriture est la modification d'un planning, validée par l'agent contre ses propres garde-fous. | § 2 `P2`, § 6.1 | sécurité | doit | 8 | à faire |
| E-007 | La version de l'outil de dump est vérifiée en l'exécutant, jamais déduite d'un chemin ; à défaut d'outil compatible, le job échoue avec un message actionnable plutôt que de produire une archive douteuse. | § 2 `P3` | technique | doit | 1 | couverte (`RSV-02`, `RSV-09`) — version prouvée en exécutant, échec plutôt qu'archive douteuse |
| E-008 | Une archive n'est marquée valide qu'après contrôle d'intégrité **et** vérification qu'elle est structurellement relisible ; une sauvegarde non vérifiée est signalée comme telle dans l'interface. | § 2 `P4` | fonctionnel | doit | 3 | à faire |
| E-009 | Le produit ne dépend d'aucun service tiers (ni Redis, ni base externe, ni runtime) : l'état tient dans un fichier SQLite et le binaire est statique, compilé sans CGO. | § 2 `P5`, `N3` | technique | doit | 0 | couverte (`mise run release` : aucun paquet cgo, ELF sans interpréteur ; état SQLite `internal/state`) |
| E-010 | L'absence de sauvegarde réussie est un événement de premier ordre, au même titre qu'un échec. | § 2 `P6`, § 5.10 | fonctionnel | doit | 6 | à faire |

## § 3 — Périmètre du MVP

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-011 | Les moteurs supportés sont PostgreSQL 12 à 18, MySQL 8.x et MariaDB 10.6+. | § 3 | contrainte | doit | 1 | couverte (ADR-0004) — les trois familles sondées contre de vrais serveurs 16.15, 11.4.13 et 8.4.11 |
| E-012a | La destination « système de fichiers local » est supportée. | § 3 | contrainte | doit | 2 | couverte (`BKP-11`, `BKP-12`, `store/storetest`) |
| E-012b | Les destinations S3-compatible et SFTP sont supportées. | § 3 | contrainte | doit | 4 | à faire |
| E-013 | Les sources d'outils de dump supportées sont la détection sur l'hôte, l'installation gérée par l'agent et `docker exec`. | § 3 | contrainte | doit | 1 | couverte pour **deux** des trois sources — hôte (`RSV-01`) et conteneur (`RSV-10`) ; l'installation gérée est reportée (ADR-0014) |
| E-014 | La vérification du MVP couvre l'empreinte et la relecture de structure d'archive. | § 3 | contrainte | doit | 3 | à faire |
| E-015 | Les canaux d'alerte du MVP sont l'e-mail SMTP et le webhook JSON. | § 3, `F10.1` | contrainte | doit | 6 | à faire |
| E-016 | Le MVP livre une interface web locale en lecture avec actions, et une CLI complète. | § 3 | contrainte | doit | 7 | à faire |
| E-017 | Le protocole du serveur central est défini et implémenté **côté agent** dans le MVP, avant que le serveur existe, pour que la frontière de confiance soit posée avant de pouvoir être contournée. | § 3 (décision de cadrage) | contrainte | doit | 8 | en question (Q-23) |
| E-018 | MongoDB, SQLite, Redis, Valkey, SQL Server et les volumes sont hors du MVP. | § 3 | hors périmètre | doit | — | écartée (CDC § 3) |
| E-019 | Azure Blob, GCS, Google Drive et rclone sont hors du MVP. | § 3 | hors périmètre | doit | — | écartée (CDC § 3) |
| E-020 | `kubectl exec` et les dumpers natifs sont hors du MVP. | § 3, § 10 | hors périmètre | doit | — | écartée (CDC § 3) |
| E-021 | La restauration de test dans une base jetable est hors du MVP. | § 3, § 10 | hors périmètre | doit | — | écartée (CDC § 3) |
| E-022 | Les intégrations natives Slack, Discord, PagerDuty et SMS sont hors du MVP : le webhook générique les couvre. | § 3, `F10.1` | hors périmètre | doit | — | écartée (CDC § 3) |
| E-023 | L'authentification multi-utilisateur et le RBAC sont hors du MVP. | § 3, § 10 | hors périmètre | doit | — | écartée (CDC § 3) |

## § 4 — Architecture

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-024 | Une sauvegarde suit sept étapes dans cet ordre : résolution, dump, compression, chiffrement, écriture, vérification, manifeste. | § 4.1 | technique | doit | 2 | **partiellement couverte** (`BKP-06`, `BKP-20`) : étapes 01→05 livrées, vérification et manifeste **déclarées absentes**, lot 3 |
| E-025 | Le dump brut n'est jamais matérialisé, ni en mémoire ni sur disque : dump, compression (zstd), chiffrement et calcul d'empreinte sont chaînés en flux, en une seule passe sur la sortie du sous-process. | § 4.1, § 4.5, `F3.3` | technique | doit | 2 | couverte (`BKP-07`, mesurée en recette : tampon 23,1 Mo pour 295 Mo de dump) |
| E-026 | L'agent respecte l'arborescence disque prescrite : `/etc/keeper/` (`keeper.yaml`, `recipients.txt`), `/var/lib/keeper/` (`keeper.db`, `tools/<moteur>/<version>/bin/`, `tmp/` purgé au démarrage), `/var/log/keeper/keeper.log` avec rotation interne. | § 4.3 | exploitation | doit | 0 | couverte (`CFG-07`, `CFG-08`) — chemins d'ADR-0001 |
| E-027 | L'état local est tenu par SQLite via `modernc.org/sqlite`, implémentation pure Go, pour conserver `CGO_ENABLED=0` ; `mattn/go-sqlite3` est interdit. | § 4.4, § 12, `N1` | technique | doit | 0 | couverte (`depguard`, `state/open_test.go › TestTheDriverIsThePureGoOne`) |
| E-028 | L'état local comprend les tables `databases`, `jobs`, `job_logs`, `backups`, `backup_locations`, `schedules` et `alerts`, avec le contenu décrit au § 4.4. | § 4.4 | donnée | doit | 0 | couverte (`state/migrate_test.go › TestTheSevenTablesOfE028AreCreated`) |
| E-029 | Trois modes de tampon existent et sont sélectionnables par base : `stage` (fichier tampon compressé et chiffré, puis envoi depuis ce fichier), `stream` (envoi direct, sans reprise possible) et `auto` (arbitrage par exécution selon l'espace, le nombre de destinations, le format de dump et la politique de vérification). | § 4.5 | fonctionnel | doit | 2 | couverte (`BKP-02`, ADR-0016) |
| E-030 | Le mode `stage` est imposé dès que le format `-Fd` est retenu, dès qu'il y a plus d'une destination, ou dès que la vérification structurelle est exigée sans egress. | § 4.5 | fonctionnel | doit | 2 | couverte (`BKP-03`) |
| E-031 | `keeper doctor` indique, pour chaque base, le mode de tampon qui sera effectivement appliqué. | § 4.5, `F3.4`, § 5.12 | fonctionnel | doit | 6 | à faire |

## § 5.1 — Configuration (`F1`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-032 | La configuration tient dans un fichier YAML unique analysé strictement : toute clé inconnue est une erreur, jamais un avertissement. | § 5.1 `F1.1` | technique | doit | 0 | couverte (`CFG-01`) |
| E-033 | Chaque champ sensible accepte trois formes : valeur littérale, `*_env` (variable d'environnement) et `*_file` (fichier, compatible `systemd` credentials et Vault Agent). | § 5.1 `F1.2` | sécurité | doit | 0 | couverte (`CFG-02`, `CFG-03`, `CFG-09`) |
| E-034 | `keeper config validate` vérifie la syntaxe, la cohérence, la joignabilité des bases et l'existence des outils, sans rien exécuter d'autre. | § 5.1 `F1.3` | fonctionnel | doit | 1 | couverte (`CFG-09`) — `config validate` atteint les bases et les outils |
| E-035 | La configuration est relue à chaud sur `SIGHUP` et sur changement de mtime ; une configuration invalide est rejetée, l'ancienne conservée, et une alerte émise. | § 5.1 `F1.4`, § 4.3 | fonctionnel | devrait | 5 | en question (Q-12) |
| E-036 | Le fuseau horaire des plannings est déclaré explicitement dans la configuration, jamais hérité de l'environnement système. | § 5.1 `F1.5` | donnée | doit | 0 | couverte (`CFG-04`) |
| E-037 | La configuration accepte la forme cible du § 5.1 : sections `agent`, `encryption`, `databases`, `destinations`, `alerts` (`channels`, `rules`) et `server`, avec les clés qui y figurent. | § 5.1 (forme cible) | donnée | doit | 0 | couverte (`CFG-05`, `CFG-10`) ; `Q-04` et `Q-06` **ajouteront** des clés, sans rupture (`N-7`) |

## § 5.2 — Résolution des outils (`F2`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-038 | La résolution énumère toutes les sources d'outils sans préférence initiale : chemins système connus par distribution, `PATH`, répertoire géré par l'agent, sortie de `pg_lsclusters` si présente, et conteneur de la base si la stratégie `exec` est autorisée. | § 5.2 `F2.1` | fonctionnel | doit | 1 | couverte (`RSV-01`) — quatre sources, sans préférence |
| E-039 | La version de chaque candidat est obtenue en l'exécutant (`pg_dump --version`), jamais en interprétant son chemin ; le résultat est mis en cache et invalidé au changement de mtime du binaire. | § 5.2 `F2.2`, `P3` | fonctionnel | doit | 1 | couverte (`RSV-02`, `RSV-03`) — version par exécution, cache invalidé au `mtime` |
| E-040 | Les candidats sont filtrés par la règle de compatibilité de l'opération demandée, puis c'est la version la plus proche par le haut qui est retenue ; la provenance (hôte, puis outil géré, puis conteneur) ne sert qu'à départager à version égale. | § 5.2 `F2.3` (+ « piège à éviter ») | fonctionnel | doit | 1 | couverte (`RSV-08`) — la plus proche par le haut ; la provenance ne départage que les ex æquo |
| E-041 | Les familles MySQL et MariaDB ne sont jamais croisées — `mysqldump` pour MySQL, `mariadb-dump` pour MariaDB — et la famille est détectée à la connexion, jamais déduite de la configuration. | § 5.2 `F2.4` | fonctionnel | doit | 1 | couverte (`RSV-02`, `RSV-07`) — famille lue du serveur **et** de l'outil ; vérifiée sur un cas réel produit par un conflit de paquets |
| E-042 | En l'absence de candidat compatible, le job échoue avec un message nommant la version attendue, les versions trouvées et la commande exacte pour corriger ; aucun repli sur un outil incompatible n'est possible. | § 5.2 `F2.5`, `P3` | fonctionnel | doit | 1 | **partiellement couverte** (`RSV-09`) : la version attendue et les versions trouvées y sont, **pas la commande de correction** — koffr n'installe aucun outil (ADR-0014). Les clients sont un prérequis du `README` |
| E-043 | `keeper tools install` installe un outil à la demande dans `/var/lib/keeper/tools/`, bibliothèques partagées embarquées et `RPATH` ajusté. | § 5.2 `F2.6`, § 11 | fonctionnel | doit | 1 | **reportée** après la mise en service (ADR-0014) — le spike `E-130` reste valable |
| E-044 | Toute archive d'outil téléchargée est vérifiée contre une empreinte SHA-256 épinglée dans la version de l'agent, et sa signature contrôlée avant première exécution. | § 5.2 `F2.7` | sécurité | doit | 1 | **reportée** avec `E-043` (ADR-0014) ; `Q-15` sans objet |
| E-045 | L'installation automatique à la découverte d'une version inconnue est désactivée par défaut ; si elle est activée, elle est bornée par une liste blanche de versions et tracée comme événement. | § 5.2 `F2.8` | sécurité | doit | 1 | **reportée** avec `E-043` (ADR-0014) |
| E-046 | La stratégie `exec` exige le socket Docker et se déclare par base, jamais globalement. | § 5.2 `F2.9` | sécurité | doit | 1 | couverte (`RSV-10`) — `exec` par base, socket exigé, refus à la validation sans `container` |
| E-047 | PostgreSQL, dump : le candidat est compatible si `major(pg_dump) ≥ major(serveur)`. | § 5.2 (matrice) | fonctionnel | doit | 1 | couverte (`RSV-04`) |
| E-048 | PostgreSQL, restauration : le candidat est compatible si `major(pg_restore) ≥ major(archive)`. | § 5.2 (matrice) | fonctionnel | doit | 1 | couverte (`RSV-05`) |
| E-049 | PostgreSQL, cible de restauration : `major(cible) ≥ major(origine)` est conseillé ; une restauration descendante est **signalée, jamais bloquée**. | § 5.2 (matrice) | fonctionnel | doit | 1 | couverte (`RSV-06`) — signalée, jamais bloquante ; utilisée au lot 4 |
| E-050 | MySQL / MariaDB : le candidat est compatible si la famille est identique et si `version(client) ≥ version(serveur)`. | § 5.2 (matrice) | fonctionnel | doit | 1 | couverte (`RSV-07`) |

## § 5.3 — Sauvegarde (`F3`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-051 | Un seul job est actif par base à tout instant, garanti par un verrou local ; une demande concurrente est refusée, pas mise en file. | § 5.3 `F3.1` | fonctionnel | doit | 2 | couverte (`BKP-01`, vérifiée en recette, verrou orphelin compris) |
| E-052 | Le nombre de jobs parallèles est plafonné globalement par configuration (`max_parallel_jobs`). | § 5.3 `F3.2`, § 5.1 | fonctionnel | doit | 5 | à faire |
| E-053 | Le point de coupure vers les destinations suit la politique de tampon du § 4.5 ; le mode effectivement appliqué est enregistré dans le manifeste. | § 5.3 `F3.4`, § 4.5 | fonctionnel | doit | 2 | couverte (`BKP-04`, `Q-01` tranchée par ADR-0016 : `auto`) |
| E-054 | PostgreSQL : le format `-Fc` est le défaut ; le format répertoire `-Fd` avec parallélisme est utilisé au-delà d'un seuil configurable, impose alors le mode `stage`, reste indisponible en stratégie `exec`, et l'agent le signale explicitement. | § 5.3 `F3.5`, § 11 | fonctionnel | doit | 2 | **partiellement couverte** (`BKP-15`) : `-Fc` et le refus de `-Fd` livrés ; le dump répertoire lui-même attend le tampon, `N-11` |
| E-055 | En mode `stage`, la connexion à la base est fermée dès la fin du dump, sans attendre la fin des envois. | § 5.3 `F3.6`, § 4.5 | fonctionnel | doit | 2 | couverte (`BKP-14`, observée en recette : zéro chevauchement dump/envoi) |
| E-056 | MySQL et MariaDB : `--single-transaction` par défaut sur InnoDB, avec routines, déclencheurs et événements ; la présence de tables MyISAM est détectée à la sonde et signalée dans le manifeste et dans l'interface, car elle invalide la cohérence transactionnelle. | § 5.3 `F3.7`, § 11 | fonctionnel | doit | 2 | couverte (`BKP-13`, `RSV-11`) |
| E-057 | Chaque job produit un manifeste JSON non chiffré et sans secret, stocké à côté de l'archive sur **chaque** destination. | § 5.3 `F3.8` | donnée | doit | 3 | à faire |
| E-058 | Le manifeste porte au moins : identifiant d'archive, base, moteur, début, durée, version du serveur, outil (nom, version, provenance, chemin, `argv`), format, chaîne de traitement, mode de tampon, tailles brute et stockée, empreintes brute et stockée, destinataires, état de vérification (empreinte, structure, horodatage). | § 5.3 (exemple de manifeste) | donnée | doit | 3 | à faire |
| E-059 | Le manifeste ne contient jamais d'identifiant de connexion et reste lisible sans la clé privée, pour permettre l'inventaire d'un dépôt d'archives. | § 5.3, § 6 | sécurité | doit | 3 | à faire |
| E-060 | Sur `SIGTERM`, un dump en cours dispose d'un délai de grâce configurable ; passé ce délai il est interrompu et le job marqué en échec, jamais en succès. | § 5.3 `F3.9`, `N6` | fonctionnel | doit | 5 | en question (Q-08) |
| E-061 | L'espace disque est estimé et vérifié avant de commencer, avec marge : taille compressée attendue extrapolée de la dernière sauvegarde réussie, à défaut de la taille de la base ; un job qui remplirait le disque bascule en `stream` ou est refusé en amont. | § 5.3 `F3.10` | fonctionnel | doit | 2 | couverte (`BKP-05`, `RSV-12`, `Q-08` tranchée par ADR-0016 : diviseur 8) |

## § 5.4 — Vérification (`F4`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-062 | L'empreinte SHA-256 est calculée au vol pendant l'écriture, puis recalculée à la relecture de la destination pour au moins une destination par sauvegarde. | § 5.4 `F4.1` | fonctionnel | doit | 3 | en question (Q-02) |
| E-063 | La vérification structurelle est effective : pour PostgreSQL, `pg_restore --list` produit une table des matières cohérente ; pour MySQL et MariaDB, le marqueur de fin de dump est contrôlé et la décompression complète se fait sans erreur. | § 5.4 `F4.2` | fonctionnel | doit | 3 | à faire |
| E-064 | Une archive non vérifiée est visuellement distincte d'une archive vérifiée dans l'interface et dans la CLI ; l'absence de vérification n'est jamais assimilée à un succès. | § 5.4 `F4.3`, `P4` | fonctionnel | doit | 3 | à faire |
| E-065 | Des archives anciennes choisies au hasard sont revérifiées périodiquement, pour détecter la corruption silencieuse du stockage. | § 5.4 `F4.4` | fonctionnel | devrait | 6 | à faire |

## § 5.5 — Destinations (`F5`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-066 | Une interface unique de destination — écrire en flux, lire en flux, lister, supprimer, tester l'accès — est implémentée par les trois destinations du MVP. | § 5.5 `F5.1` | technique | doit | 2 | couverte (`BKP-11`, `BKP-12`) : l'interface et `filesystem` ; S3 et SFTP rejoueront la suite de conformité au lot 4 |
| E-067 | L'écriture sur plusieurs destinations se fait en parallèle à partir d'une seule lecture du flux ou du fichier tampon, sans jamais repasser par le dump. | § 5.5 `F5.2` | fonctionnel | doit | 4 | à faire |
| E-068 | L'échec d'une destination n'annule pas les autres : le job est réussi si au moins une destination a reçu et vérifié l'archive, l'état par destination est conservé, et l'échec est alerté. | § 5.5 `F5.3`, § 5.10 | fonctionnel | doit | 4 | en question (Q-02) |
| E-069 | S3 : téléversement en parties avec reprise et taille de partie adaptative — calculée depuis la taille attendue en mode `stage`, révisée en cours de route en mode `stream` — avec compatibilité explicitement testée contre MinIO, Scaleway, OVH et Backblaze, pas seulement AWS. | § 5.5 `F5.4`, § 11 | intégration | doit | 4 | à faire |
| E-070 | Le chemin distant est déterministe et lisible par un humain : `<base>/<AAAA>/<MM>/<base>_<horodatage>_<id>.<ext>`, pour qu'un dépôt reste exploitable si l'agent disparaît. | § 5.5 `F5.5` | donnée | doit | 2 | couverte (`BKP-10`, extension `.pgc.zst.age` depuis `A-13`) |
| E-071 | La documentation décrit une politique S3 en écriture seule et l'activation d'Object Lock, avec leurs conséquences sur la rétention. | § 5.5 `F5.6` | exploitation | devrait | 4 | en question (Q-09) |

## § 5.6 — Chiffrement (`F6`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-072 | Le chiffrement se fait en flux avec `filippo.io/age`, destinataires déclarés par clé publique X25519. | § 5.6 `F6.1` | sécurité | doit | 2 | couverte (`CRY-03`) |
| E-073 | Plusieurs destinataires sont possibles par base — clé opérationnelle et clé de séquestre — pour qu'une clé perdue ne condamne pas les archives. | § 5.6 `F6.2` | sécurité | doit | 2 | couverte (`CRY-04`, `CRY-05`, `Q-04` tranchée par ADR-0016) |
| E-074 | La clé privée n'est jamais requise par l'agent ni présente dans sa configuration ; le déchiffrement est une opération manuelle et distincte. | § 5.6 `F6.3`, § 6 | sécurité | doit | 2 | couverte (`CRY-04`, ADR-0007) |
| E-075 | Une archive chiffrée reste déchiffrable avec l'outil `age` standard, sans Keeper : aucun format maison. | § 5.6 `F6.4` | sécurité | doit | 2 | couverte (`CRY-03`, prouvée par le binaire `age` réel — `mise run interop` et `mise run e2e`) |
| E-076 | `keeper keygen` génère une paire, affiche la clé publique et n'écrit jamais la clé privée sur la machine de l'agent : elle est affichée une fois, à charge de l'opérateur de la mettre à l'abri. | § 5.6 `F6.5` | sécurité | doit | 2 | couverte (`CRY-02`, clé publique sur la sortie standard depuis `A-12`) |

## § 5.7 — Rétention (`F7`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-077 | La rétention suit une politique grand-père / père / fils : `last`, `daily`, `weekly`, `monthly` ; une archive retenue par une seule règle est conservée. | § 5.7 `F7.1` | fonctionnel | doit | 5 | en question (Q-09) |
| E-078 | Aucune suppression n'a lieu si la dernière sauvegarde a échoué, ni s'il resterait moins d'archives valides que le plancher configuré : la rétention ne doit jamais aggraver une panne. | § 5.7 `F7.2` | fonctionnel | doit | 5 | en question (Q-08) |
| E-079 | Seules les archives vérifiées comptent dans le décompte de rétention. | § 5.7 `F7.3`, `P4` | fonctionnel | doit | 5 | en question (Q-02) |
| E-080 | `--dry-run` est disponible et affiché avant toute première application réelle de la rétention sur une base donnée. | § 5.7 `F7.4` | fonctionnel | doit | 5 | à faire |
| E-081 | Toute suppression est journalisée avec la règle qui l'a motivée, et conservée dans l'historique même après disparition de l'archive. | § 5.7 `F7.5` | donnée | doit | 5 | à faire |

## § 5.8 — Restauration (`F8`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-082 | Une restauration est toujours initiée localement, par la CLI ou l'interface web locale ; aucune commande distante ne peut la déclencher, quelle qu'en soit la source. | § 5.8 `F8.1`, § 6.1 | sécurité | doit | 4 | à faire |
| E-083 | Une confirmation explicite est obligatoire, avec rappel de la base cible et du volume concerné ; elle est contournable en mode non interactif par un drapeau dédié, jamais par `--yes` seul. | § 5.8 `F8.2` | fonctionnel | doit | 4 | à faire |
| E-084 | Avant toute restauration, l'agent contrôle l'outil compatible, la connectivité, les droits suffisants, l'espace disponible et le fait que l'archive est déchiffrable : l'échec se produit en amont, jamais à mi-parcours. | § 5.8 `F8.3` | fonctionnel | doit | 4 | en question (Q-06) ; clé privée tranchée par ADR-0007 |
| E-085 | Les modes de nettoyage sont explicites et ordonnés du moins au plus destructif — `none`, `clean`, `drop-schemas`, `drop-database` — avec `clean` par défaut. | § 5.8 `F8.4` | fonctionnel | doit | 4 | à faire |
| E-086 | Une restauration peut viser une cible différente de l'origine (`--into`), pour la recette et la migration, sans toucher la base d'origine. | § 5.8 `F8.5` | fonctionnel | doit | 4 | en question (Q-06) |
| E-087 | `allow_restore` vaut `false` par défaut ; l'armement se fait par base et expire après un délai configurable. | § 5.8 `F8.6` | sécurité | devrait | 4 | en question (Q-13) |

## § 5.9 — Planification (`F9`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-088 | Le planificateur est interne au processus, sans dépendance externe, et interprète des expressions cron à cinq champs dans le fuseau déclaré. | § 5.9 `F9.1`, `N3` | fonctionnel | doit | 5 | en question (Q-18) |
| E-089 | Le dernier et le prochain déclenchement sont persistés ; au démarrage, une échéance manquée pendant l'arrêt déclenche l'événement `backup_missed` et **non** une rafale de rattrapage. | § 5.9 `F9.2` | fonctionnel | doit | 5 | à faire |
| E-090 | Un décalage aléatoire est configurable par base, pour éviter que quarante agents ne frappent leur stockage à 2 h 00 pile. | § 5.9 `F9.3` | fonctionnel | devrait | 5 | à faire |
| E-091 | Le plancher `min_interval` est opposable à toute modification distante : une fréquence proposée plus lâche que le plancher est rejetée et alertée. | § 5.9 `F9.4`, § 5.1 | sécurité | doit | 5 | en question (Q-05) |

## § 5.10 — Alertes (`F10`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-092 | L'agent émet les neuf événements du § 5.10 avec leur déclencheur et leur gravité : `backup_missed`, `backup_failed`, `verify_failed` (critiques), `database_unreachable`, `destination_failed`, `tool_missing`, `retention_blocked` (avertissements), `uplink_lost` (information), `remote_command_rejected` (critique). | § 5.10 (table des événements) | fonctionnel | doit | 6 | en question (Q-08) |
| E-093 | Les canaux du MVP sont l'e-mail SMTP et le webhook JSON générique, ce dernier couvrant Slack, Discord, Teams et Mattermost sans code dédié. | § 5.10 `F10.1` | intégration | doit | 6 | à faire |
| E-094 | Une alerte n'est réémise que si l'état change, ou après un délai de rappel configurable : une base en panne trois jours ne produit pas 4 320 e-mails. | § 5.10 `F10.2` | fonctionnel | doit | 6 | en question (Q-08) |
| E-095 | Un événement de rétablissement est émis au retour à la normale. | § 5.10 `F10.3` | fonctionnel | doit | 6 | à faire |
| E-096 | Avec `delegate_alerts: true`, le serveur central prend en charge la notification ; si le serveur est injoignable depuis `fallback_after`, l'agent réémet localement, y compris les événements survenus pendant la coupure. | § 5.10 `F10.4`, `P1` | fonctionnel | doit | 8 | à faire |
| E-097 | `remote_command_rejected` est toujours notifié localement et jamais délégué : c'est un signal de compromission potentielle du serveur. | § 5.10 `F10.5`, § 6 | sécurité | doit | 8 | à faire |

## § 5.11 — Interface web locale (`F11`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-098 | L'interface est servie par le binaire lui-même, ressources embarquées via `embed.FS` : aucune construction d'actifs au démarrage, aucun accès réseau sortant pour l'afficher. | § 5.11 `F11.1`, § 12 | technique | doit | 7 | à faire |
| E-099 | `keeper ui` écoute sur `127.0.0.1` par défaut et ouvre le navigateur ; toute écoute sur une autre interface exige une authentification configurée, sinon l'agent refuse de démarrer. | § 5.11 `F11.2` | sécurité | doit | 7 | en question (Q-10) ; hypothèse dans ADR-0009 |
| E-100 | L'interface offre les vues : tableau de bord du parc local, fiche par base, historique des jobs, journal détaillé d'un job, catalogue des archives, prochaines exécutions. | § 5.11 `F11.3` | fonctionnel | doit | 7 | à faire |
| E-101 | L'interface offre les actions : déclencher une sauvegarde, vérifier une archive, appliquer la rétention en simulation, lancer une restauration avec confirmation. | § 5.11 `F11.4` | fonctionnel | doit | 7 | à faire (ADR-0007 : portée réduite, l'interface n'exécute pas la restauration) |
| E-102 | L'interface est rendue côté serveur, ses interactions passent par HTMX : pas de SPA, pas de chaîne de compilation JavaScript. | § 5.11 `F11.5`, § 12 | technique | devrait | 7 | en question (Q-11) |

## § 5.12 — Surface CLI

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-103a | La CLI expose `version [--json]`, `config validate [--file]`, `config show [--redact]`, `doctor [--database]`, `tools list` / `install` / `remove`. | § 5.12 | fonctionnel | doit | 1 | couverte pour `version`, `config validate`, `config show`, `doctor` et `tools list` ; `tools install` et `tools remove` **n'existent pas** (ADR-0014) |
| E-103b | La CLI expose `keygen` et `backup <db> [--dry-run]`. | § 5.12 | fonctionnel | doit | 2 | couverte (`keygen`, `backup <db> [--dry-run]`) |
| E-103c | La CLI expose `list [<db>] [--destination]` et `verify <backup-id>`. | § 5.12 | fonctionnel | doit | 3 | à faire (ADR-0001) |
| E-103d | La CLI expose `restore <db> --from <backup-id> [--into DSN] [--clean-mode MODE]`, avec `--identity` ajouté par ADR-0007. | § 5.12, ADR-0007 | fonctionnel | doit | 4 | à faire (ADR-0001, ADR-0007) |
| E-103e | La CLI expose `retention apply [<db>] [--dry-run]` et `serve`. | § 5.12 | fonctionnel | doit | 5 | à faire (ADR-0001) |
| E-103f | La CLI expose `ui [--listen]`. | § 5.12 | fonctionnel | doit | 7 | à faire (ADR-0001) |
| E-104a | `doctor` affiche pour chaque base sa joignabilité, la version de son serveur, et l'outil retenu avec sa version et sa provenance. | § 5.12 | fonctionnel | doit | 1 | couverte (`resolve.Diagnose`, `cli/doctor_test.go`) — joignabilité, version du serveur, outil retenu avec version et provenance |
| E-104b | `doctor` affiche en outre, pour chaque base, le mode de tampon qui sera appliqué, les destinations accessibles, la prochaine exécution et l'état de la dernière sauvegarde. | § 5.12, § 4.5 | fonctionnel | doit | 6 | à faire |

## § 5.13 — Liaison au serveur central (`F13`)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-105 | L'agent pousse vers le serveur en HTTPS ; le serveur n'ouvre jamais de connexion vers l'agent, qui n'expose donc aucun port au-delà de son interface locale. | § 5.13 `F13.1`, § 6 | sécurité | doit | 8 | à faire |
| E-106 | L'authentification se fait par un jeton propre à l'agent, lu depuis un fichier ; mTLS est accepté en option. | § 5.13 `F13.2` | sécurité | doit | 8 | en question (Q-16) |
| E-107 | Le contenu poussé se limite à l'identité et la version de l'agent, la liste des bases avec leur état, les résultats de jobs, les événements et les prochaines échéances : jamais d'identifiant, de clé ou de contenu de configuration sensible. | § 5.13 `F13.3`, `P2` | sécurité | doit | 8 | en question (Q-16) |
| E-108 | Le seul champ que le serveur peut modifier est l'expression cron d'une base dont `allow_remote_schedule` vaut `true` ; tout autre champ reçu est ignoré. | § 5.13 `F13.4`, § 6.1 | sécurité | doit | 8 | à faire |
| E-109 | Une réponse du serveur contenant une instruction hors du champ autorisé — restauration, configuration, identifiant, installation d'outil, assouplissement d'un plancher de fréquence — est rejetée, journalisée et notifiée localement comme incident de sécurité. | § 5.13 `F13.5`, § 6.1 | sécurité | doit | 8 | à faire |
| E-110 | Le niveau de détail des journaux transmis est configurable, avec masquage systématique des chaînes de connexion et des valeurs sensibles. | § 5.13 `F13.6` | sécurité | devrait | 8 | à faire |
| E-111 | L'indisponibilité du serveur n'a aucun effet sur les sauvegardes : les événements sont mis en file localement et retransmis au retour. | § 5.13 `F13.7`, `P1` | fonctionnel | doit | 8 | en question (Q-16) |

## § 6 — Modèle de menace (contrat de sécurité)

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-112 | Un serveur central compromis n'obtient aucun identifiant de base ou de stockage, aucune clé, aucune capacité de restauration et aucun accès réseau aux bases ; il n'obtient que la vue du parc et la modification des plannings dans la limite des planchers locaux. | § 6, § 6.1 | sécurité | doit | 8 | à faire |
| E-113 | Un agent compromis ne permet pas de déchiffrer les archives passées : il ne détient qu'une clé publique. | § 6, `F6.3` | sécurité | doit | 3 | à faire |
| E-114 | Un stockage compromis n'expose que des archives chiffrées et leurs manifestes — donc des métadonnées : noms de bases, tailles, horaires — et jamais le contenu des archives ni un identifiant. | § 6, `F3.8` | sécurité | doit | 3 | à faire |
| E-115 | La lecture du fichier de configuration n'expose aucun mot de passe dès lors que les formes `*_file` ou `*_env` sont utilisées : elle n'expose que la topologie du parc. | § 6, `F1.2` | sécurité | doit | 0 | couverte (`CFG-06`, `CFG-09`, `obs/redact_test.go`) |
| E-116 | La documentation porte les quatre recommandations de déploiement du § 6.2 : utilisateur de base dédié en lecture seule distinct de l'utilisateur de restauration, Object Lock ou versionnement avec des clés en écriture seule, clé privée conservée hors production avec séquestre et procédure testée, absence de route réseau du serveur central vers les bases. | § 6.2 | exploitation | à qualifier | 7 | en question (Q-06) |

## § 7 — Exigences non fonctionnelles

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-117 | Le binaire est statique, compilé avec `CGO_ENABLED=0`, publié pour `linux/amd64`, `linux/arm64` et `darwin/arm64`, et pèse moins de 30 Mo. **Amendée par ADR-0011** : la cible `windows/amd64` du CDC est retirée. | § 7 `N1`, ADR-0011 | technique | doit | 0 | couverte (`mise run release`, seuil bloquant en CI) — amendée par ADR-0011, nuancée par `N-17` (macOS lie `libSystem`) |
| E-118 | L'empreinte mémoire au repos est inférieure à 50 Mo et indépendante de la taille des bases sauvegardées. | § 7 `N2` | technique | doit | final | à faire |
| E-119 | Aucun service tiers n'est requis : ni base de données externe, ni file de messages, ni runtime. | § 7 `N3`, `P5` | technique | doit | 0 | couverte (rotation interne `internal/obs`, SQLite embarqué, aucun service requis) |
| E-120 | Une unité `systemd` durcie est fournie : utilisateur dédié, `ProtectSystem`, `NoNewPrivileges`, `PrivateTmp`. | § 7 `N4` | exploitation | doit | 7 | à faire |
| E-121 | Les journaux sont structurés en JSON vers la sortie standard, doublés d'un fichier avec rotation interne, et compatibles `journald` sans configuration. | § 7 `N5`, § 12 | exploitation | doit | 0 | couverte (`internal/obs`, ADR-0012) ; `Q-21` (purge de `job_logs`) reste au lot 5 |
| E-122 | L'arrêt est propre : plus aucun nouveau job après `SIGTERM`, délai de grâce pour les jobs en cours, état cohérent en base quoi qu'il arrive. | § 7 `N6`, `F3.9` | technique | doit | 5 | à faire |
| E-123 | Des tests d'intégration réels tournent contre PostgreSQL 13 à 18 et MariaDB 10.6, 10.11 et 11.4 via conteneurs éphémères, en incluant systématiquement l'aller-retour sauvegarde puis restauration. | § 7 `N7`, § 12 | technique | doit | 4 | à faire (ADR-0004 : matrice élargie à MySQL 8.x et PostgreSQL 12) |
| E-124 | Une image conteneur est publiée en complément du binaire, pour ceux qui la préfèrent, jamais comme prérequis. | § 7 `N8` | exploitation | doit | 7 | à faire |

## § 10 — Hors périmètre, avec leur raison

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-125 | Les dumpers natifs en Go sont écartés du MVP ; la question se rouvre pour SQLite, Redis et Valkey, où le natif est trivial et sans risque. | § 10 | hors périmètre | doit | — | écartée (CDC § 10) |
| E-126 | La restauration de test automatique est écartée du MVP et conçue en v2 comme option, car elle suppose de provisionner une instance jetable. | § 10 | hors périmètre | doit | — | écartée (CDC § 10) |
| E-127 | PITR et sauvegarde physique relèvent d'un produit distinct, pas d'une option. | § 10 | hors périmètre | doit | — | écartée (CDC § 10) |
| E-128 | Le multi-utilisateur et le RBAC sont écartés : l'interface locale est protégée par l'accès à la machine, la gestion des droits appartient au serveur central. | § 10, § 3 | hors périmètre | doit | — | écartée (CDC § 10) |
| E-129 | La déduplication et la sauvegarde incrémentale sont écartées du MVP, mais le CDC demande de trancher **avant la v1** car elles changent entièrement le format de stockage. | § 10, § 11.1 | hors périmètre | doit | — | écartée (ADR-0005) |

## § 11 — Traitements de risque qui deviennent des exigences

| # | Exigence | Source | Type | Priorité | Lot | État |
| --- | --- | --- | --- | --- | --- | --- |
| E-130 | L'embarquement des bibliothèques partagées et l'ajustement du `RPATH` des outils installés sont validés par un essai réel sur Debian, Rocky et Alpine **avant `L0`** : c'est le risque technique numéro un du document. | § 11 (risque 1) | technique | doit | 0 | couverte (`docs/inputs/spike-2026-09-rpath.md`, 18 exécutions) — conclusion **positive**, `E-043` non amendée |
| E-131 | Une chaîne d'intégration dédiée construit et publie les binaires d'outils par versions figées, avec empreintes épinglées dans la version de l'agent, pour sept versions de PostgreSQL et trois de MariaDB sur deux architectures. | § 11 (risque 2), `F2.6`, `F2.7` | exploitation | doit | 1 | **reportée** avec `E-043` (ADR-0014) — plus d'archive à construire ni à héberger |
| E-132 | Des destinataires multiples sont obligatoires dès la configuration initiale, la procédure de séquestre est documentée, et un avertissement est émis au premier démarrage tant qu'une seule clé est déclarée. | § 11 (risque 3), `F6.2` | sécurité | doit | 2 | couverte (`CRY-02`, `CRY-05`) : **avertissement**, pas refus — divergence assumée et confirmée en recette |

---

## Ce que le registre ne couvre pas

- Les onze scénarios du § 8 : recette, pas exigences (`analyse.md` § 10).
- Les jalons `L0`–`L5` du § 9 : proposition de découpage, traitée à l'étape 3 dans `ROADMAP.md`.
- La pile technique du § 12 : « indicative, à confirmer au moment d'écrire le `go.mod` » ; elle
  fonde l'ADR de stack (étape 2), sauf `E-027` et `E-117` qui sont des contraintes fermes.
