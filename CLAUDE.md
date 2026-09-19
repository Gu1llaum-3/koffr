# koffr — CLAUDE.md

koffr est un agent de sauvegarde de bases de données autonome — un binaire Go statique, sans Docker
et sans serveur central obligatoire — pour l'exploitant d'un parc PostgreSQL, MySQL et MariaDB
auto-hébergé. Ce fichier est volontairement
court : il dit où lire, quoi lancer, et les conventions qu'on ne peut pas déduire du code. Le
reste vit dans les documents pointés ci-dessous.

## À lire avant d'agir

- **`ROADMAP.md` en début de chaque tâche** : quel lot est en cours, son critère de sortie, son plan.
- `METHODE.md` : comment on travaille (cycle, registres, exécution des plans, Definition of done).
- @ARCHITECTURE.md : structure, règles de dépendance, flux d'une requête.
- `docs/adr/` : décisions figées. On en change par un nouvel ADR, jamais en contournant dans le code.
- `docs/cdc/` : le cahier des charges et le registre d'exigences `E-nn`. C'est la spécification
  fonctionnelle ; ce qu'il ne dit pas se demande (`docs/questions.md`), ne s'invente pas.
- `docs/README.md` : index de la documentation et table des registres.
- `docs/cdc/KEEPER-CDC.md` : le cahier des charges reçu, **jamais modifié**. Le produit s'y appelle
  encore « Keeper » et ses commandes `keeper …` : lire `koffr` partout (ADR-0001).

## TDD : le test avant le code, toujours

Ce projet est développé en **TDD**. Ce n'est pas une préférence de style, c'est la façon dont on
prouve qu'une règle est appliquée.

- **Rouge, vert, refactor.** Pour tout code de domaine, de service, de job ou d'intégration : on
  écrit le test, on le lance, on **constate le rouge**, puis on écrit le code minimal qui le fait
  passer, puis on refactore, le test restant vert. Un test écrit après le code n'est pas du TDD.
- **Une règle métier = une ligne dans `rules.md` = un test nommé.** La ligne cite sa source
  (`E-nn`, `Q-nn`, ADR) ; le test porte le numéro de la règle dans son nom.
- **Le test dit ce que le code doit faire, pas ce qu'il fait.** Il se lit comme la règle. Cas
  nominal, cas limite, cas d'erreur typée : les trois, ou on dit lequel manque et pourquoi.
- **Là où un test est impossible ou disproportionné** (interface pure, configuration, script
  unique), on le **dit** dans la tâche du plan au lieu de forcer un test sans valeur ; un écran a
  au minimum son test de composant ou son parcours.
- **On ne coche pas une tâche dont le test n'a pas tourné**, et on ne merge pas une vague sans
  `verify` vert.

Le mode opératoire détaillé (fixtures, base réelle, nommage) est dans le skill `implementer`.

## Commandes

Toutes les tâches passent par `mise` (`N-3` du plan du lot 0) ; il n'y a pas de `Makefile`.

```sh
mise install                 # Go et golangci-lint aux versions de mise.toml
mise run check               # go vet + go build (bloquant)
mise run lint                # golangci-lint : format, lint et interdits globaux (bloquant)
mise run fmt                 # applique les formateurs (gofumpt, goimports)
mise run test                # tests ; `-race` si la machine a un compilateur C, sinon sans, en le disant
mise run build               # binaire statique CGO_ENABLED=0 dans dist/koffr
mise run verify              # les quatre ci-dessus, dans l'ordre ; vert avant tout commit sur main
```

Versions des outils : `mise.toml` fait autorité (Go 1.27, `golangci-lint` 2.13.2), y compris en CI.
Dépendances pinnées dans `go.mod` ; mise à jour hebdomadaire planifiée dans `docs/maintenance.md`.

**Les journaux vont au fichier, pas à l'écran.** Une commande répond, elle ne raconte pas : la
console ne reçoit que les avertissements et les erreurs, sur la **sortie d'erreur**, tandis que le
fichier de `E-026` garde tout, y compris la trace de la commande. `--log-level` fixe le niveau du
fichier, et **fait suivre la console quand on le pose explicitement**. `serve` (lot 5) écrira tout
sur la **sortie standard**, comme `E-121` le demande. Voir ADR-0012.

**Le détecteur de course est facultatif en local, obligatoire en CI.** `go test -race` exige cgo,
donc un compilateur C, qu'une machine neuve conforme aux prérequis n'a pas (`A-01`). La tâche `test`
le constate et choisit, **en écrivant lequel des deux cas s'applique**. La CI pose
`KOFFR_REQUIRE_RACE=1`, qui transforme son absence en échec : la garantie est mesurée là, pas sur le
poste de chacun. Pour l'avoir en local : `build-essential` sur Debian et Ubuntu.

## Conventions de la stack (ce que le modèle ne sait pas ou sait faux)

Une puce par piège **réellement rencontré** au lot 0. Pas de rappel de ce que la documentation dit
déjà.

- **`yaml.v3` ignore les champs privés quand il évalue `omitempty`.** Un type dont la valeur est
  privée (`config.Secret`) est donc toujours vu comme vide et **disparaît de la sortie** au lieu
  d'être sérialisé. Il lui faut un `IsZero() bool`. Symptôme : un secret renseigné s'efface au lieu
  d'être masqué.
- **`node.Decode` dans un `UnmarshalYAML` perd la strictité du décodeur parent.** `KnownFields(true)`
  ne s'y propage pas : une clé inconnue y passe en silence. Décoder les clés à la main dans ce cas
  (voir `internal/config/tools.go`), ce qui préserve aussi les numéros de ligne.
- **`KnownFields(true)` est la seule façon d'obtenir `E-032`.** Sans lui, `yaml.v3` ignore les clés
  inconnues. Le message d'erreur donne la clé **et** sa ligne : ne pas le reformuler, il est ce que
  l'exigence demande.
- **Le pilote SQLite s'appelle `sqlite`, pas `sqlite3`.** `sqlite3` est celui de `mattn`, qui exige
  CGO et casserait `E-117`. `depguard` l'interdit.
- **`import _ "time/tzdata"` est obligatoire.** Sans la base de fuseaux embarquée, un binaire
  statique ne résout aucun fuseau sur une machine sans `zoneinfo`, et `E-036` ne tient plus.
- **Les `PRAGMA` SQLite s'appliquent par connexion.** Les poser par `Exec` après l'ouverture ne
  couvre que la première connexion du pool. Ils vont dans la chaîne de connexion
  (`?_pragma=journal_mode(WAL)&…`).
- **Les tables sont `STRICT`.** Sans cela, SQLite accepte `"12 MB"` dans une colonne `INTEGER`. Les
  horodatages sont contraints par un `GLOB`, pas par convention.
- **`lumberjack` supprime et compresse ses archives dans une goroutine, après `Close`.** Le plafond
  `MaxBackups` est une promesse tenue peu après, pas à l'instant de l'écriture : un test qui le
  constate au lieu de l'attendre est instable.
- **Sur macOS, un binaire Go lie toujours `libSystem`, même avec `CGO_ENABLED=0`.** macOS n'a pas
  d'ABI d'appel système stable. « Statique » y veut dire « sans cgo » ; la vraie staticité ne se
  vérifie que sur les cibles Linux (absence de `PT_INTERP`), ce que fait `mise run release`.
- **`go test -race` exige CGO**, alors que `mise run build` impose `CGO_ENABLED=0`. Les deux tâches
  sont distinctes exprès ; ne pas « harmoniser » en désactivant le détecteur de course. Et sans
  compilateur C, Go force `CGO_ENABLED=0` **tout seul** : `-race` devient impossible sur une machine
  qui n'a pourtant rien d'anormal. D'où `scripts/run-tests.sh` et `KOFFR_REQUIRE_RACE` (`A-01`).
  Tester `go env CGO_ENABLED` **ne suffit pas** : la variable peut être exportée à la main sur une
  machine sans compilateur. Le script vérifie en plus que `go env CC` est trouvable.
- **Un interdit `depguard` sur un module absent de `go.mod` ne peut pas être démontré** :
  `typecheck` échoue d'abord et court-circuite les autres linters. La garde existe, sa preuve
  attend qu'on ait une raison d'ajouter le module.
- **`testcontainers-go` ne lit pas le contexte Docker.** Sur une machine dont le contexte n'est pas
  le socket par défaut — Colima, Podman —, il annonce `rootless Docker not found`. Il faut
  `DOCKER_HOST`, **et** `TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock` : le réapeur
  monte le socket **dans** un conteneur, et c'est le chemin vu de l'intérieur de la machine
  virtuelle qui compte. Sans la seconde, l'échec parle d'un `mkdir` sur le socket.
- **Ce qu'une commande affiche est ordonné.** Deux exécutions identiques donnent la même sortie :
  un exploitant compare deux relevés, et une différence qui bouge toute seule ne se lit pas. Un
  parcours de `map` n'est **jamais** la source d'une sortie — Go randomise l'ordre, et le défaut ne
  se voit qu'en CI.
- **Sur Debian et Ubuntu, `/usr/bin/pg_dump` est un lien vers `pg_wrapper`**, qui choisit la version
  à lancer d'après son `argv[0]`. Résoudre le lien avant d'exécuter lui retire cette information :
  il répond `Can't exec "--version" … line 153`. Les liens se résolvent pour **dédupliquer**, jamais
  pour exécuter.
- **Les clients MySQL et MariaDB ne peuvent pas coexister** sur Debian et Ubuntu
  (`Conflicts: virtual-mysql-client-core`) : installer l'un supprime l'autre, et `mysqldump`
  appartient alors à celui qui reste. Un parc mixte passe par la stratégie `exec` (ADR-0015).
- **Vérifier la version courante d'une action ou d'un outil avant de l'épingler.** Ce que le modèle
  « connaît » date de son entraînement : `actions/checkout@v5` et `jdx/mise-action@v3` étaient
  périmées (v7 et v4). Une requête à l'API du dépôt coûte deux secondes.
- **`golangci-lint` est au schéma v2** (`version: "2"`, sections `linters.settings`, `formatters`).
  Les formateurs sont rapportés par `golangci-lint run`, pas seulement par `fmt` : le format est
  donc bloquant.
- **Outils gérés (spike `E-130`)** : `DT_RUNPATH` **ne s'hérite pas**. Poser un `runpath` sur le
  seul binaire laisse ses bibliothèques chercher les leurs dans le système ; il en faut un sur
  **chaque bibliothèque** du bundle. Et `PT_INTERP` est **absolu** — un bundle ne se déplace pas,
  il se re-patche. Détail dans `docs/inputs/spike-2026-09-rpath.md`.

## Règles d'architecture (vérifiées par le lint et par `internal/arch`)

Les neuf règles d'ADR-0010, `AR-01` à `AR-09`, sont décrites avec ce qui les tient dans
`ARCHITECTURE.md`. En résumé :

- `internal/domain/**` n'importe **aucun** adaptateur, ni `net`, `net/http`, `net/smtp`, `os/exec`,
  `database/sql`.
- **Un module du domaine ne connaît pas son voisin** : il reçoit ce dont il a besoin par un port
  déclaré chez lui, câblé dans `cmd/koffr`. Seul `domain/shared` est commun.
- `internal/cli` et `internal/httpd` **n'importent jamais `internal/state`** : ils passent par un
  cas d'usage.
- **Seul `internal/config` lit l'environnement** ; seul `internal/engine` lance un sous-process ;
  seuls `store`, `engine` et `egress` ouvrent une connexion sortante (`net/http` est en plus ouvert
  à `httpd`, qui **écoute**).
- **`database/sql` est réservé à `internal/state` et `internal/engine`** (ADR-0013). Dans `engine`,
  une connexion de pilote sert à **sonder** — joignabilité, version, famille — et **jamais** à lire
  les données d'une base à sauvegarder : le dump reste un sous-process. Ce que le lint ne sait pas
  exprimer, un test le dit : la sonde n'émet **que** sa requête de version.
  `modernc.org/sqlite` reste réservé à `state`, sans exception.
- `protocol/` n'importe **rien** du dépôt : c'est ce que `koffr-server` importera.
- Chaque module du domaine a un `rules.md` : règle, source (`E-nnn`, `Q-nn`, ADR ou `N-n`), test.
  Les adaptateurs n'en ont pas.
- **Une violation fait échouer `verify`.** Toute règle ajoutée ici s'accompagne de sa fixture
  violante dans `internal/arch/testdata/`, sinon le contrôle ne prouve rien.

## Données

Fixé par ADR-0006, et **opposable dans le schéma**, pas seulement écrit ici :

- **Horodatages** : `TEXT` en RFC 3339 **UTC** (`…Z`), contraints par un `GLOB` qui refuse un
  décalage local, un `datetime()` SQLite et un entier Unix. La conversion vers `agent.timezone` se
  fait à l'affichage et dans le manifeste, jamais en base.
- **Statuts** : `TEXT` en toutes lettres, contraints par `CHECK`. Jamais d'entier. Les neuf noms
  d'événements sont dans le schéma : un dixième est une migration, pas une chaîne inventée.
- **Tailles et durées** : `INTEGER`, en octets et en millisecondes. **Aucun flottant nulle part.**
- **Identifiants** : ULID (`TEXT`) pour les archives et les jobs ; les identifiants de base, de
  destination et de canal sont ceux de la configuration, tels quels.
- **Audit** : `created_at` et `updated_at` sur chaque table.
- **Pas de suppression logique** sauf là où `E-081` l'impose ; **pas de verrou optimiste** — un seul
  processus écrit, avec un verrou par base.
- **Le catalogue est un index, jamais la source de vérité.** Tout ce qu'une restauration exige vit
  aussi dans le manifeste déposé à côté de l'archive.
- **Migrations** : fichiers SQL numérotés dans `internal/state/migrations/`, embarqués par `embed`,
  appliquées au démarrage dans une transaction. **Aucun retour arrière automatique** : un état écrit
  par un koffr plus récent refuse de s'ouvrir. Le SQL se relit avant commit.

## Écritures externes

- **Aucun hôte, jeton, clé ou destinataire dans le code ni en base.** Tout vient de la
  configuration d'environnement validée au démarrage. Une variable manquante met l'intégration en
  mode « puits » (journalise, n'envoie pas), jamais en erreur, jamais vers une valeur réelle.
- **Tout appel sortant d'exploitation** (SMTP, webhook, liaison, téléchargement d'outils) passe par
  `internal/egress`. Les **destinations** (`store/`) et les **moteurs** (`engine/`) écrivent
  réellement : c'est la fonction du produit, voir ADR-0008. Le domaine ne connaît
  aucun client réseau.
- Hors production, la porte journalise et **n'envoie pas**. Les mails vont dans un collecteur local.
  On ne teste pas contre un tiers réel depuis un poste de développement.

## Interface

- Langue des utilisateurs : **anglais** (ADR-0003). Pas de phrases d'explication à l'écran : des champs,
  des valeurs, des libellés courts, et des messages au moment de l'action. Le pourquoi vit dans
  `rules.md`.
- Une idée de modernisation ou une fonctionnalité hors cahier des charges va dans
  `docs/backlog.md`, jamais dans le code avant décision.

## Langues et nommage

- Documentation, ADR, roadmap, plans, registres, skills, `rules.md`, ce fichier : **français**.
- Code, identifiants, commentaires, noms de fichiers de code, **noms de branches**, **messages de
  commit**, tables et colonnes : **anglais**.
- Textes d'interface, messages d'erreur affichés, journaux, e-mails : **anglais** (ADR-0003).

### Glossaire métier FR → EN

Un terme métier, un identifiant, pour toujours. Alimenté par `/demarrer-projet` depuis le cahier
des charges, complété au fil des lots.

Le CDC **fixe déjà** la plupart des identifiants : clés de configuration (§ 5.1), noms de composants
(§ 4.2), tables (§ 4.4), champs du manifeste (§ 5.3), noms d'événements (§ 5.10), commandes (§ 5.12).
Ceux-là ne se renégocient pas. La colonne « Notes » signale ce qui est **proposé** par nous, donc
encore modifiable, et ce qui est **en question**.

Les commandes sont écrites ici telles que le CDC les nomme (`keeper …`). ADR-0001 **propose** de
renommer le produit en `koffr` (agent) et `koffr-server` (serveur central) : dès qu'il est accepté,
lire `koffr …` partout, ainsi que `/etc/koffr/`, `/var/lib/koffr/` et `koffr.service`.

| Français | Identifiant | Notes |
| -------- | ----------- | ----- |
| agent | `agent` | l'installation sur une machine ; `agent.id` |
| base (de données à sauvegarder) | `database` | jamais `db` ; table `databases`, clé `database_id` |
| moteur | `engine` | valeurs `postgresql`, `mysql`, `mariadb` |
| famille (MySQL / MariaDB) | `family` | proposé ; `F2.4` détecte la famille à la connexion |
| outil (de dump ou de restauration) | `tool` | binaire externe ; commandes `keeper tools …` |
| résolution, résolveur | `resolver` | composant § 4.2 |
| provenance d'un outil | `source` | valeurs `host`, `managed`, `container` (manifeste § 5.3) |
| stratégie de dump | `strategy` | valeur `exec` pour le dump via conteneur |
| version majeure | `major` | la matrice de compatibilité raisonne en majeures |
| sonde | `probe` | proposé ; joignabilité, version, famille, détection MyISAM |
| job, exécution | `job` | table `jobs` |
| ligne de journal de job | `job_log` | table `job_logs` |
| sauvegarde, archive | `backup` | table `backups`, identifiant `backup_id` |
| emplacement (archive × destination) | `backup_location` | table `backup_locations` |
| manifeste | `manifest` | JSON non chiffré déposé à côté de l'archive |
| empreinte | `checksum` | champs `sha256_raw`, `sha256_stored` |
| chaîne de traitement | `pipeline` | composant § 4.2 ; champ `pipeline` du manifeste |
| mise en tampon | `staging` | clé `staging`, modes `stage`, `stream`, `auto` |
| compression | `compression` | zstd ; niveau noté `zstd:3` dans le manifeste |
| chiffrement | `encryption` | section `encryption` |
| destinataire (clé publique `age`) | `recipient` | `recipients_file`, `recipients.txt` |
| clé de séquestre | `escrow key` | proposé ; second destinataire obligatoire (`E-132`) |
| destination | `destination` | types `filesystem`, `s3`, `sftp` |
| abstraction de destination | `store` | composant § 4.2 |
| catalogue | `catalog` | composant § 4.2 |
| vérification | `verify` | champ `verified` : `checksum`, `structure`, `at` |
| rétention | `retention` | règles `last`, `daily`, `weekly`, `monthly` |
| restauration | `restore` | commande `keeper restore` |
| mode de nettoyage | `clean-mode` | `none`, `clean`, `drop-schemas`, `drop-database` |
| armement de la restauration | `allow_restore` | défaut `false` (en question, `Q-13`) |
| planning | `schedule` | table `schedules`, clé `schedule` (cron 5 champs) |
| planificateur | `scheduler` | composant § 4.2 |
| plancher de fréquence | `min_interval` | **nom en question** (`Q-05`) : sémantique inverse de son nom |
| décalage aléatoire | `jitter` | proposé ; `F9.3` |
| alerte | `alert` | table `alerts` |
| événement | `event` | noms fixés : `backup_missed`, `backup_failed`, `verify_failed`, `database_unreachable`, `destination_failed`, `tool_missing`, `retention_blocked`, `uplink_lost`, `remote_command_rejected` |
| canal d'alerte | `channel` | section `alerts.channels` |
| règle d'alerte | `rule` | section `alerts.rules` |
| anti-répétition | `suppression` | proposé ; délai de rappel `reminder_after` (proposé) |
| rétablissement | `recovered` | proposé ; ne jamais écrire `recovery`, qui dit « restauration » |
| liaison au serveur central | `uplink` | composant § 4.2 ; section `server` |
| serveur central | `server` | jamais `master`, jamais `hub` |
| parc | `fleet` | proposé ; le mot « parc » est du CDC, l'identifiant est à nous |
| jeton de liaison | `token` | `token_file` |
| interface web locale | `httpd` (composant), `ui` (commande) | deux mots pour deux choses |
| diagnostic | `doctor` | commande `keeper doctor` |
| configuration | `config` | `keeper.yaml` |
| fuseau horaire | `timezone` | clé `agent.timezone` |
| délai de grâce | `grace_period` | proposé ; `F3.9` |
| verrou de base | `lock` | `F3.1`, un job actif par base |

## Definition of done

Celle de `METHODE.md` § « Definition of done d'un lot ». En résumé, une tâche est finie quand :
`verify` passe ; chaque règle métier a un test **écrit avant le code** et une ligne dans `rules.md` ; les frontières
d'architecture tiennent ; toute mutation vérifie les droits ; toute évolution de schéma a sa
migration relue ; code en anglais, pilotage en français ; la roadmap et le plan sont à jour ; un
ADR existe pour toute décision structurante.

## Git

- Commits conventionnels **en anglais** : `feat(<module>): …`, `fix(<module>): …`, `chore: …`,
  `docs: …`. Le sujet dit ce que le commit change, pas ce qu'on a fait pour y arriver.
- Branches **en anglais** : `lot<N>/wave-<n>-<slug>` pour une vague de plan, `fix/<slug>`,
  `chore/<slug>`. `main` toujours vert ; merge `--no-ff` d'une vague vérifiée.
- Ne jamais commiter `.env`, `CLAUDE.local.md`, ni un document reçu marqué confidentiel.
