# ROADMAP — koffr

> À lire en début de chaque tâche. **Un lot par ligne, son critère de sortie, son plan.** Les
> sous-tâches vivent dans `docs/plans/lot-N-<nom>.md`, le récit dans `docs/retro/`, les registres
> dans `docs/`. Un seul plan en exécution à la fois (`METHODE.md`).

Découpage établi le 2026-09-18 depuis `docs/cdc/exigences.md`, sur la base des jalons `L0`–`L5` du
CDC (§ 9) et des ADR 0001 à 0010, tous acceptés. **139 exigences : 124 affectées à un lot, 15 hors
périmètre** (§ 3 et § 10 du CDC, reportées dans `docs/backlog.md`). Aucune exigence n'est orpheline.

## État

| Lot | Nom                                        | Exigences | Statut  | Plan | Fin |
| --- | ------------------------------------------ | --------- | ------- | ---- | --- |
| 0   | Squelette, outillage et spike des outils   | 13        | en recette | `docs/plans/lot-0-squelette.md` | |
| 0c  | Corrections de recette du lot 0            | 7 `A-nn` | plan en cours | `docs/plans/lot-0-corrections.md` | |
| 1   | Diagnostic d'un parc réel                  | 20        | à faire | | |
| 2   | Sauvegarder une base vers un fichier chiffré | 20      | à faire | | |
| 3   | Manifeste, catalogue et vérification       | 11        | à faire | | |
| 4   | Restauration et destinations distantes     | 13        | à faire | | |
| 5   | Planification et rétention                 | 14        | à faire | | |
| 6   | Alertes et diagnostic complet              | 9         | à faire | | |
| 7   | Interface locale et mise en service        | 10        | à faire | | |
| 8   | Liaison au serveur central                 | 13        | à faire | | |
| F   | Recette générale, répétitions, retour arrière | 1      | à faire | | |

Statuts admis : `à faire`, `plan en cours`, `en cours`, `en recette`, `terminé`, `arrêté (D-nn)`.
Un mot, une date. Le pourquoi est dans le plan ou la rétro.

## Règles de pilotage

Celles de `METHODE.md` § « Règles de pilotage », plus celles propres à ce projet :

- **Recette sur un parc réel**, jamais sur un jeu de données inventé : une machine de test avec de
  vraies bases PostgreSQL, MySQL et MariaDB. L'utilisateur référent reste à nommer (`D-01`).
- **Aucun lot ne se clôt sans son scénario du § 8.** Les onze scénarios d'acceptation du CDC sont
  répartis ci-dessous ; ils sont le critère de sortie, pas une recette facultative.
- **`D-06` (qui construit, signe et héberge les binaires d'outils) bloque une vague du lot 1**, pas
  le lot entier : la résolution et le diagnostic avancent sans elle.
- Le CDC est une source, pas une vérité : une exigence contredite par une mesure devient une `Q-nn`.

---

## Lot 0 — Squelette, outillage et spike des outils

**Périmètre** : `E-009`, `E-026`, `E-027`, `E-028`, `E-032`, `E-033`, `E-036`, `E-037`, `E-115`,
`E-117`, `E-119`, `E-121`, `E-130`.

Ce que tout projet livre avant sa première ligne métier, plus ce que la pile acceptée impose. Le
`L0` du CDC n'est **pas** ce lot : il est le lot 1. Ce lot-ci le précède.

- Dépôt `koffr` créé (il n'est pas encore sous git), `main` protégé, CI déclarée et **verte au
  premier push**.
- Module Go `github.com/Gu1llaum-3/koffr`, Go 1.27, `CGO_ENABLED=0`, binaire produit pour les trois
  cibles de `E-117` amendée (ADR-0011),
  **taille mesurée par la CI et bloquante à 30 Mo**.
- Configuration : analyse stricte du YAML (`E-032`), formes `*_env` / `*_file` (`E-033`), fuseau
  explicite (`E-036`), forme cible (`E-037`), `config show --redact` qui ne fuit aucun secret
  (`E-115`). Seul le paquet `config` lit l'environnement.
- État SQLite `modernc.org/sqlite`, sept tables, migrations SQL numérotées et embarquées (`E-027`,
  `E-028`), arborescence disque de `E-026` avec `tmp/` purgé au démarrage.
- Journaux `slog` JSON sur la sortie standard et fichier avec rotation (`E-121`).
- **Frontières d'architecture d'ADR-0010 en règles de lint bloquantes** (`depguard`, `forbidigo`) ;
  `verify` regroupe `check`, `lint`, tests, build.
- Porte de sortie `internal/egress` en mode « puits », avec le test « aucune connexion sortante avec
  la configuration de développement » (ADR-0008).
- **Spike `E-130` — le risque n° 1 du CDC** : extraire `pg_dump` avec ses bibliothèques partagées,
  ajuster le `RPATH`, et l'exécuter réellement sur **Debian, Rocky et Alpine**. Le CDC le place
  « avant `L0` » ; il conditionne `E-043` et les scénarios 2 et 3.
- `ARCHITECTURE.md` complété, `CLAUDE.md` § Commandes et § Conventions, `.claude/skills/implementer/`
  remplis pour Go.

**Sortie** : `verify` vert en local **et** en CI ; un binaire statique de moins de 30 Mo pour les
**trois** cibles ; `koffr config validate` rejette une clé inconnue et accepte la configuration cible
du § 5.1 ; une règle de dépendance violée fait échouer le lint ; **le spike a tourné sur les trois
distributions et son résultat est écrit** — s'il échoue, `E-043` est rouverte par un ADR avant le
lot 1.
**Dépend de** : rien. `D-04` a été tranchée le 2026-09-18 par ADR-0011 (Windows hors périmètre,
`E-117` ramenée à trois cibles).

## Lot 1 — Diagnostic d'un parc réel

C'est le `L0` du CDC : aucune sauvegarde, et c'est voulu. Le lot valide la thèse technique du
produit.

**Périmètre** : `E-007`, `E-011`, `E-013`, `E-034`, `E-038` à `E-050`, `E-103a`, `E-104a`, `E-131`.

Sondes des trois moteurs (version, famille, MyISAM), énumération de toutes les sources d'outils sans
préférence, version obtenue **en exécutant** le candidat, matrice de compatibilité complète,
installation gérée, empreintes épinglées et signature, `config validate`, `doctor`.

**Sortie** : sur un parc réel d'au moins trois bases hétérogènes, `koffr doctor` nomme pour chacune
sa joignabilité, la version de son serveur et l'outil retenu avec sa version et sa provenance, sans
se tromper — y compris le cas du « piège à éviter » du § 5.2 (hôte en 14, base en 16, outil géré en
16 : c'est le 16 qui est choisi). **Scénarios 2 et 3 du § 8** passent : une base PostgreSQL 17 avec
seulement le client 15 échoue en nommant la version manquante et la commande de correction, puis
`koffr tools install postgresql 17` la fait réussir sans toucher la configuration.
**Dépend de** : `D-06` et `Q-15` avant la vague « installation gérée » (`E-043`, `E-044`, `E-131`) ;
`Q-20` pour les outils gérés hors Linux.

## Lot 2 — Sauvegarder une base vers un fichier chiffré

**Périmètre** : `E-012a`, `E-024`, `E-025`, `E-029`, `E-030`, `E-051`, `E-053` à `E-056`, `E-061`,
`E-066`, `E-070`, `E-072` à `E-076`, `E-103b`, `E-132`.

La chaîne 01→05 : dump en sous-process, zstd, `age`, empreinte au vol, écriture sur disque, avec la
politique de tampon `stage` / `stream` / `auto` et le verrou par base.

**Sortie** : `koffr backup <db>` produit sur `/srv/backups` une archive chiffrée d'une base
PostgreSQL et d'une base MariaDB. **Scénario 6 du § 8** : l'archive est illisible sans la clé privée
et se déchiffre avec l'outil `age` standard, sans koffr. **Scénario 11, première moitié** : sur une
base de 40 Go en mode `stage`, aucun fichier de plus que la taille compressée n'apparaît sur disque
et la connexion à la base se ferme avant le début de l'envoi.
**Dépend de** : `Q-01` (mode par défaut), `Q-04` (destinataires par base), `Q-07` (portée du dump et
`--no-owner --no-privileges`), `Q-08` (seuil `-Fd`, marge d'espace disque).

## Lot 3 — Manifeste, catalogue et vérification

**Périmètre** : `E-008`, `E-014`, `E-057`, `E-058`, `E-059`, `E-062`, `E-063`, `E-064`, `E-103c`,
`E-113`, `E-114`.

Étapes 06→07 : manifeste déposé à côté de l'archive, catalogue local, empreinte recalculée,
relecture structurelle (`pg_restore --list`, marqueur de fin MySQL), distinction visible entre
vérifié et non vérifié.

**Sortie** : `koffr list` distingue à l'œil une archive vérifiée d'une archive non vérifiée ;
`koffr verify <backup-id>` recalcule l'empreinte et relit la structure ; un dépôt d'archives est
inventoriable **sans la clé privée et sans koffr**, à partir des seuls manifestes, et aucun manifeste
ne contient d'identifiant de connexion.
**Dépend de** : `Q-02` (la vérification relit la destination ou le fichier tampon).

## Lot 4 — Restauration et destinations distantes

À la fin de ce lot, l'outil est utilisable en production.

**Périmètre** : `E-012b`, `E-067`, `E-068`, `E-069`, `E-071`, `E-082` à `E-087`, `E-103d`, `E-123`.

S3 avec téléversement en parties, reprise et taille adaptative ; SFTP ; écriture parallèle depuis une
seule lecture ; restauration avec contrôles préalables, `--identity` (ADR-0007), modes de nettoyage
et cible alternative ; matrice d'intégration élargie d'ADR-0004.

**Sortie** : **scénario 5 du § 8** — restauration complète d'une base de 5 Go depuis S3 vers une
instance vierge, avec égalité vérifiée du nombre de lignes de chaque table. **Scénario 11, seconde
moitié** : une coupure réseau pendant l'envoi est reprise sans relancer le dump. La matrice `N7`
élargie (PostgreSQL 12 à 18, MySQL 8.0 et 8.4, MariaDB 10.6/10.11/11.4) est verte, aller-retour
compris.
**Dépend de** : `Q-06` (identifiants de restauration distincts), `Q-02`, `Q-13` (`allow_restore` par
défaut).

## Lot 5 — Planification et rétention

**Périmètre** : `E-035`, `E-052`, `E-060`, `E-077` à `E-081`, `E-088` à `E-091`, `E-103e`, `E-122`.

Planificateur interne dans le fuseau déclaré, échéances persistées, plancher opposable, politique
grand-père/père/fils avec ses règles de sûreté, `serve`, arrêt propre.

**Sortie** : **scénario 7 du § 8** — l'agent arrêté vingt-quatre heures émet au redémarrage un
**unique** `backup_missed`, sans rafale de rattrapage. **Scénario 9** — la rétention appliquée sur
trente archives simulées est conforme à la politique, et se suspend dès que la dernière sauvegarde
est en échec. `--dry-run` s'affiche avant toute première application réelle.
**Dépend de** : `Q-05` (sémantique de `min_interval`), `Q-08` (plancher de rétention, délai de
grâce), `Q-09` (seaux de rétention, moment, destinations, suppression impossible), `Q-12` (relecture
à chaud), `Q-18` (changement d'heure), `Q-21` (purge des journaux).

## Lot 6 — Alertes et diagnostic complet

**Périmètre** : `E-010`, `E-015`, `E-031`, `E-065`, `E-092` à `E-095`, `E-104b`.

Les neuf événements, e-mail SMTP et webhook JSON, anti-répétition, rétablissement, revérification
périodique d'archives anciennes, et les champs manquants de `doctor`.

**Sortie** : **scénario 8 du § 8** — une base rendue injoignable produit **une seule** alerte, un
rappel après le délai configuré, puis un événement de rétablissement au retour. `koffr doctor`
affiche désormais les sept champs de `E-104a` + `E-104b` pour chaque base.
**Dépend de** : `Q-08` (tolérance de `backup_missed`, `N` sondes, délai de rappel).

## Lot 7 — Interface locale et mise en service

**Périmètre** : `E-016`, `E-098` à `E-102`, `E-103f`, `E-116`, `E-120`, `E-124`.

Interface web embarquée (`embed.FS`, HTMX servi localement), six vues, actions — **sans la
restauration, réduite par ADR-0007 à l'affichage de la commande à lancer** —, unité `systemd`
durcie, image conteneur, documentation d'installation et les quatre recommandations du § 6.2.

**Sortie** : **scénario 1 du § 8** — sur une machine vierge **sans Docker installé**, l'installation
du binaire, la rédaction de la configuration et la première sauvegarde réussie d'un PostgreSQL 16
tiennent en moins de dix minutes, chronomètre en main, en suivant la seule documentation.
**Dépend de** : `Q-10` (authentification hors `127.0.0.1` — ADR-0009 pose l'hypothèse du refus),
`Q-11` (HTMX souhaitable ou obligatoire).

## Lot 8 — Liaison au serveur central

**Périmètre** : `E-005`, `E-006`, `E-017`, `E-096`, `E-097`, `E-105` à `E-112`.

Poussée HTTPS, jeton fichier, contenu strictement limité, liste blanche d'un seul champ modifiable,
rejet et notification des instructions interdites, délégation des alertes avec repli, file locale.

**Sortie** : **scénario 10 du § 8, le critère transversal** — un serveur central simulé qui envoie un
ordre de restauration se voit rejeté, l'incident est journalisé et **notifié localement**, et aucun
chemin de code ne relie une réponse du serveur à une restauration. Serveur coupé : les sauvegardes,
les vérifications et les alertes continuent à l'identique, et les événements de la coupure sont
réémis au retour.
**Dépend de** : `Q-16` (forme du protocole — **aucune conception ne démarre sans réponse**), `Q-23`
(calendrier de la liaison), `D-05` (ouverture du CDC de `koffr-server`).

## Lot final — Recette générale, répétitions, mise en service

**Périmètre** : `E-118`.

**Sortie** : les **onze scénarios** du § 8 rejoués de bout en bout sur une machine vierge sans
anomalie bloquante ; l'empreinte mémoire au repos mesurée sous 50 Mo et **indépendante de la taille
des bases** (`E-118`) ; la procédure de mise en service répétée à blanc au moins deux fois ; la
procédure de retour arrière écrite et testée ; toutes les `E-nnn` couvertes ou renvoyées par une
décision écrite.
