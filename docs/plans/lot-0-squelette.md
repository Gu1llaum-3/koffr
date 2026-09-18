# Plan lot 0 — Squelette, outillage et spike des outils

> Statut : **validé par le propriétaire le 2026-09-18**. Exécuté par `/executer-plan`. Les règles communes à tous les plans sont
> dans `METHODE.md` § « Exécution d'un plan » et ne sont pas répétées ici.

## Périmètre

**Exigences couvertes** — 13 :

| `E-nnn` | Ce qu'elle exige | Source |
| --- | --- | --- |
| `E-009` | Aucune dépendance de service ; état SQLite ; binaire statique sans CGO | § 2 `P5`, `N3` |
| `E-026` | Arborescence `/etc/koffr`, `/var/lib/koffr` (base, `tools/`, `tmp/` purgé), `/var/log/koffr` | § 4.3 |
| `E-027` | SQLite par `modernc.org/sqlite` ; `mattn/go-sqlite3` interdit | § 4.4, § 12 |
| `E-028` | Sept tables : `databases`, `jobs`, `job_logs`, `backups`, `backup_locations`, `schedules`, `alerts` | § 4.4 |
| `E-032` | YAML unique, analyse stricte, clé inconnue = erreur | § 5.1 `F1.1` |
| `E-033` | Trois formes par champ sensible : littéral, `*_env`, `*_file` | § 5.1 `F1.2` |
| `E-036` | Fuseau déclaré explicitement, jamais hérité du système | § 5.1 `F1.5` |
| `E-037` | La configuration accepte la forme cible du § 5.1 | § 5.1 |
| `E-115` | Lire la configuration n'expose aucun mot de passe avec `*_file` / `*_env` | § 6 |
| `E-117` | Binaire statique `CGO_ENABLED=0`, trois cibles, < 30 Mo | § 7 `N1`, ADR-0011 |
| `E-119` | Aucun service tiers requis | § 7 `N3` |
| `E-121` | Journaux JSON sur la sortie standard + fichier avec rotation interne | § 7 `N5` |
| `E-130` | Spike d'édition de liens validé sur Debian, Rocky et Alpine | § 11 risque 1 |

**Écarté de ce lot, et pourquoi** :

- `E-034` (`config validate` vérifie la **joignabilité des bases** et l'**existence des outils**) →
  lot 1 : il suppose les sondes et le résolveur. Le lot 0 ne valide que la **forme**.
- `E-103a` (surface CLI de diagnostic) → lot 1. Le lot 0 livre `koffr version` seul, comme traversée
  minimale (`N-1`), et **ne coche pas** `E-103a`.
- `E-035` (relecture à chaud) → lot 5. `E-118` (empreinte mémoire) → lot final, faute d'un flux à
  mesurer.

**Critère de sortie**, repris de `ROADMAP.md` et rendu vérifiable :

1. `mise run verify` vert en local **et** CI verte au premier push ;
2. un binaire statique `CGO_ENABLED=0` pour `linux/amd64`, `linux/arm64` et `darwin/arm64`, taille
   mesurée par la CI et **bloquante à 30 Mo** ;
3. `koffr config validate` accepte la forme cible du § 5.1 copiée telle quelle, et rejette une clé
   inconnue **en la nommant, avec sa ligne** ;
4. une violation des frontières d'ADR-0010 fait échouer `verify` ;
5. le spike a tourné sur les trois distributions et **son résultat est écrit**.

## État de départ (vérifié le 2026-09-18)

Constaté dans le dépôt et sur le poste, pas supposé.

- **Le dépôt n'est pas sous git** : `git rev-parse --is-inside-work-tree` échoue.
- **Aucun code** : ni `.go`, ni `go.mod`, ni `go.sum`. Le dépôt ne contient que la documentation de
  pilotage — 21 fichiers Markdown, 11 ADR (10 acceptés + le gabarit), un registre de 139 `E-nnn`.
- **Go 1.27.1** installé par `mise` (`~/.local/share/mise/installs/go/1.27.1`), conforme à ADR-0002.
  `mise` 2026.3.9.
- **`golangci-lint` 2.8.0**, installé par **Homebrew** et non par `mise` — donc non épinglé, donc
  susceptible de diverger de la CI. Construit avec go1.25.5.
- **Docker 29.2.0**, architecture **aarch64**.
- `.gitignore` est celui d'un projet Node (`node_modules/`, `dist/`, `.svelte-kit/`) : à remplacer.
- `mise.toml` ne déclare que `go = "1.27"`.
- **Le poste est `darwin/arm64`** : le spike `E-130` (ELF, `RPATH`) ne peut pas tourner nativement.
  Il passera par des conteneurs Linux — `arm64` nativement, `amd64` en émulation.
- **Aucune donnée** : rien à mesurer, aucun comptage à reporter.

## Ce que le cahier des charges dit, et ce qu'il ne dit pas

- **Dit** : § 4.3 l'arborescence disque ; § 4.4 les sept tables et le pilote pur Go ; § 5.1 les cinq
  exigences de configuration et la forme cible complète ; § 7 `N1`, `N3`, `N5` ; § 11 le spike et sa
  place « avant `L0` ».
- **Ne dit pas** : quelle purge pour `job_logs` → `Q-21`, hypothèse au lot 5, sans effet ici ; si la
  configuration porte des destinataires par base → `Q-04` ; ni des identifiants de restauration →
  `Q-06`. **Les deux ajouteront des clés au schéma écrit ici**, en ajout, sans rupture (`N-7`).
- **Contredit** : le § 4.3 présente `koffr.yaml` comme « lue à chaud » quand `F1.4` la classe en
  souhaitable → `Q-12`. Sans effet au lot 0 : la relecture à chaud n'est pas livrée.

## Décisions d'implémentation

- **N-1 `koffr version` est livré au lot 0** comme traversée minimale exigée par le kit (« un module
  de bout en bout qui traverse toutes les couches »). *Raison* : sans binaire, `E-117` n'est pas
  mesurable. *Exclut* : cocher `E-103a`, qui reste au lot 1 avec le reste de la surface CLI.
- **N-2 Module `github.com/Gu1llaum-3/koffr`**, CI GitHub Actions. *Raison* : réponse du
  propriétaire du 2026-09-18. *Exclut* : toute migration ultérieure sans réécriture des imports.
- **N-3 Les tâches sont des tâches `mise`**, pas un `Makefile`. *Raison* : `mise` est déjà le
  gestionnaire de versions du projet ; une seule porte d'entrée. *Exclut* : `make`, `task`, `just`.
- **N-4 `golangci-lint` est épinglé dans `mise.toml`**. *Raison* : la version installée vient de
  Homebrew et dérivera de la CI. *Exclut* : dépendre d'un outil installé hors du projet.
- **N-5 Rotation des journaux par `gopkg.in/natefinch/lumberjack`**. *Raison* : pur Go, hors liste du
  § 12 mais admis par le critère d'ADR-0002 (compile sans CGO, poids négligeable). *Exclut* : écrire
  notre propre rotation, et dépendre de `logrotate`, qui serait un service tiers contraire à `E-119`.
- **N-6 `import _ "time/tzdata"`**. *Raison* : sans la base de fuseaux embarquée, un binaire statique
  ne résout aucun fuseau sur une machine qui n'a pas `zoneinfo`, et `E-036` ne tient pas. *Exclut* :
  dépendre de la base de fuseaux du système.
- **N-7 Le schéma de configuration est exactement celui du § 5.1**, ni plus ni moins. *Raison* :
  l'analyse stricte de `E-032` fait de toute clé non déclarée une erreur ; on ne devine pas les clés
  des lots suivants. *Exclut* : anticiper `Q-04` et `Q-06`, qui seront des ajouts.
- **N-8 Les frontières d'ADR-0010 sont vérifiées par un test Go** (`internal/arch`) lisant le graphe
  réel des imports. *Raison* : `depguard` ne sait pas exprimer « un module du domaine n'importe pas
  son voisin ». *Exclut* : faire de `depguard` l'autorité ; il garde les interdits globaux
  (`mattn/go-sqlite3`) et `forbidigo` garde `os.Getenv` et `os/exec`.
- **N-9 Les sept tables sont créées par une migration unique au lot 0**, même si cinq ne servent
  qu'aux lots suivants. *Raison* : `E-028` est une exigence de ce lot. *Exclut* : créer les tables au
  fil des lots, ce qui multiplierait les migrations sur un schéma déjà décrit par le CDC.
- **N-10 `tmp/` est purgé à l'ouverture de l'état** — donc par `serve` —, pas à chaque commande CLI.
  *Raison* : sinon une commande lancée à la main détruirait l'espace de travail d'un job en cours.
  *Exclut* : une purge dans le `PersistentPreRun` de cobra.
- **N-11 Les chemins de production de `E-026` sont les valeurs par défaut**, surchargés par
  `--config` et `--state-dir`. *Raison* : le développement se fait sur macOS, où `/etc/koffr` n'est
  pas accessible. *Exclut* : des variables d'environnement dédiées — seul `internal/config` lit
  l'environnement, et seulement celles que la configuration référence via `*_env` (ADR-0010).

Aucune de ces décisions ne survit au lot au sens d'ADR : celle qui le devait — la portée de Windows —
a été écrite en **ADR-0011** avant ce plan.

## Vagues

### Vague 0 — Avant la première branche

- [x] **0.1** Renommer le dossier de travail `keeper` → `koffr`. **À faire par le propriétaire entre
      deux sessions** : cela change le répertoire courant de la session en cours.
- [x] **0.2** Fait le 2026-09-18 : ADR-0011 écrit et accepté, `D-04` barrée, `E-117` amendée à trois
      cibles, `Q-20` réduite à macOS, index et roadmap à jour.

### Vague 1 — Dépôt, outillage, CI (`lot0/wave-1-repository-and-ci`)

- [ ] **1.1** `git init` sur `main`, `.gitignore` Go, `LICENSE` Apache-2.0, `NOTICE`, `README.md` en
      anglais (ADR-0003) renvoyant vers la documentation française. *Pas de test : configuration,
      justifié.*
- [ ] **1.2** Test d'abord `internal/build/build_test.go` — `Info()` porte nom, version, commit,
      date, version de Go et plateforme ; la sortie `--json` a ces clés et aucune vide en build de
      release. Puis `go.mod` (`github.com/Gu1llaum-3/koffr`, `go 1.27`), `cmd/koffr/main.go`, racine
      cobra, commande `version [--json]` alimentée par `-ldflags` (`N-1`, `N-2`).
- [ ] **1.3** Tâches `mise` : `check` (`go vet` + `go build`), `lint` (`golangci-lint run`), `test`
      (`go test ./... -race`), `build`, `verify` (les quatre). `.golangci.yml` au **schéma v2**.
      `golangci-lint` épinglé dans `mise.toml` (`N-3`, `N-4`). `CLAUDE.md` § Commandes rempli.
- [ ] **1.4** `.github/workflows/verify.yml` : `mise` puis `verify`, sur push et PR, **verte au
      premier push**.
- [ ] **1.5** Vague verte : `verify`, commit `chore: bootstrap go module, tooling and ci`.

### Vague 2 — Spike d'édition de liens (`lot0/wave-2-tool-linking-spike`) — `E-130`

L'inconnue en premier : si elle tombe mal, le lot 1 change avant d'être écrit.

- [ ] **2.1** `scripts/spike-rpath.sh` : extraire `pg_dump` 16 et `mariadb-dump` 11.4 avec leurs
      bibliothèques partagées, ajuster le `RPATH` (`patchelf`), puis **exécuter `--version`** dans
      Debian 12, Rocky 9 et Alpine 3.20, en `linux/arm64` (natif) et `linux/amd64` (émulé).
- [ ] **2.2** Rapport `docs/inputs/spike-2026-09-rpath.md` : ce qui marche, ce qui casse, sur quelle
      distribution et pourquoi. **Conclusion explicite** : `E-043` est tenable, ou ne l'est pas.
- [ ] **2.3** Si Alpine (musl) échoue — le résultat attendu — écrire la conclusion et **ouvrir un
      ADR** amendant `E-043` (outils gérés sur glibc seulement ; Alpine renvoyé à la stratégie
      `exec` ou aux outils de l'hôte), **avant** le lot 1.
- [ ] **2.4** Vague verte : commit `chore(tools): add linking spike and its report`.

### Vague 3 — Frontières d'architecture (`lot0/wave-3-architecture-boundaries`)

- [ ] **3.1** Squelette des paquets d'ADR-0010, un `doc.go` d'une phrase par paquet.
- [ ] **3.2** Test d'abord `internal/arch/boundaries_test.go` — les neuf règles de dépendance
      d'ADR-0010 vérifiées sur le graphe réel des imports, avec des fixtures violantes dans
      `testdata/` qui **doivent** faire échouer le test (`N-8`).
- [ ] **3.3** `.golangci.yml` : `depguard` interdit `mattn/go-sqlite3` (`E-027`) ; `forbidigo`
      interdit `os.Getenv` hors `internal/config` et `os/exec` hors `internal/engine`.
- [ ] **3.4** Vague verte : commit `chore(arch): enforce dependency boundaries in verify`.

### Vague 4 — Configuration (`lot0/wave-4-configuration`)

Exigences : `E-032`, `E-033`, `E-036`, `E-037`, `E-115`.

- [ ] **4.1** Test `internal/config/parse_test.go` — **`CFG-01`** : une clé inconnue est une erreur
      qui nomme la clé **et sa ligne**. Cas : à la racine, dans `databases[0]`, dans
      `destinations[0]`. Puis le code (`yaml.v3` + `KnownFields(true)`).
- [ ] **4.2** Test — **`CFG-02`** : chaque champ sensible accepte `x`, `x_env` et `x_file` ;
      **`CFG-03`** : deux formes déclarées à la fois est une erreur, et un `*_file` absent échoue
      **au démarrage**, pas au premier usage.
- [ ] **4.3** Test — **`CFG-04`** : `agent.timezone` est obligatoire et validé par
      `time.LoadLocation` ; la variable `TZ` du système n'a aucun effet. `import _ "time/tzdata"`
      (`N-6`).
- [ ] **4.4** Test — **`CFG-05`** : la forme cible du § 5.1, **copiée telle quelle** du CDC dans
      `internal/config/testdata/reference.yaml`, est acceptée, et chaque champ atterrit où attendu.
- [ ] **4.5** Test — **`CFG-06`** : `config show --redact` n'affiche aucune valeur sensible, et le
      type de configuration ne fuit rien via `%v`, `%+v` ni `slog` (`E-115`).
- [ ] **4.6** `internal/config/rules.md` créé depuis `docs/modeles/rules.md` : `CFG-01` à `CFG-06`,
      chacune avec sa source `E-nnn` et le nom de son test.
- [ ] **4.7** Vague verte : `verify`, commit `feat(config): strict yaml parsing with env and file secrets`.

### Vague 5 — État local (`lot0/wave-5-local-state`)

Exigences : `E-027`, `E-028`, `E-026`.

- [ ] **5.1** Test `internal/state/open_test.go` — l'ouverture pose `journal_mode=WAL`,
      `foreign_keys=ON`, `busy_timeout=5000`, `synchronous=NORMAL` (ADR-0006), avec le pilote
      `modernc.org/sqlite` (nom de pilote **`sqlite`**, pas `sqlite3`).
- [ ] **5.2** Test `internal/state/migrate_test.go` — la migration `0001` crée les **sept** tables de
      `E-028` (`N-9`), est idempotente, et enregistre sa version ; une base d'une version inconnue
      refuse de démarrer.
- [ ] **5.3** Test — les statuts sont contraints par `CHECK` (un statut inconnu échoue à
      l'insertion) ; les horodatages sont en RFC 3339 **UTC** ; tailles et durées sont des entiers
      (ADR-0006).
- [ ] **5.4** Test `internal/state/paths_test.go` — les chemins de `E-026` sont les valeurs par
      défaut, surchargeables par `--config` et `--state-dir` (`N-11`), et `tmp/` est purgé à
      l'ouverture de l'état, pas à chaque commande (`N-10`).
- [ ] **5.5** `internal/config/rules.md` complété : **`CFG-07`** (chemins par défaut et surcharges),
      **`CFG-08`** (purge de `tmp/`).
- [ ] **5.6** Vague verte : `verify`, commit `feat(state): sqlite schema, migrations and disk layout`.

### Vague 6 — Journaux et porte de sortie (`lot0/wave-6-logging-and-egress`)

Exigences : `E-121`, `E-119`, et la porte de sortie d'ADR-0008.

- [ ] **6.1** Test `internal/obs/log_test.go` — `slog` écrit du JSON sur la sortie standard, un objet
      par ligne, avec `time`, `level`, `msg` et les attributs attendus, sans préfixe qui gênerait
      `journald`.
- [ ] **6.2** Test — le fichier de journal tourne à la taille configurée et conserve N archives, sans
      aucun service tiers (`E-119`, `N-5`).
- [ ] **6.3** Test — un champ sensible passé à `slog` sort **masqué** ; complète `E-115`.
- [ ] **6.4** Test `internal/egress/sink_test.go` — la porte de sortie en mode « puits » journalise
      et **n'ouvre aucune connexion** ; test de garde « aucune connexion sortante avec la
      configuration de développement » (ADR-0008).
- [ ] **6.5** Vague verte : `verify`, commit `feat(obs): structured logs, rotation and egress sink`.

### Vague 7 — Publication et documentation (`lot0/wave-7-release-and-documentation`)

Exigences : `E-117` (amendée), `E-009`, `E-119`.

- [ ] **7.1** Test — le binaire construit avec `CGO_ENABLED=0` ne dépend d'aucune bibliothèque
      dynamique, et `go list -deps` ne contient ni `mattn/go-sqlite3` ni aucun paquet exigeant CGO.
- [ ] **7.2** Construction des **trois** cibles d'ADR-0011, taille mesurée et **seuil bloquant à
      30 Mo** dans la CI.
- [ ] **7.3** `ARCHITECTURE.md` complété : vue d'ensemble, arborescence réelle, flux d'une
      sauvegarde, auth et droits (renvoi ADR-0009), tests, déploiement.
- [ ] **7.4** `CLAUDE.md` § Conventions : **les pièges réellement rencontrés** — schéma
      `.golangci.yml` v2, pilote `sqlite` et non `sqlite3`, `time/tzdata` obligatoire pour un binaire
      statique, `KnownFields(true)` pour l'analyse stricte, et ce que le spike a appris.
- [ ] **7.5** `.claude/skills/implementer/` rempli pour Go : structure d'un module, exemples
      canoniques, nommage des tests, fixtures.
- [ ] **7.6** Vague verte : `verify`, commit `chore: release matrix and stack documentation`.

## Vérification de bout en bout

1. `mise run verify` vert en local ; CI verte sur le premier push.
2. `go build` pour les trois cibles avec `CGO_ENABLED=0` ; la taille de chacune est notée dans le
   journal d'exécution. Le seuil de 30 Mo est franchi **volontairement une fois** pour vérifier
   qu'il bloque réellement.
3. `koffr config validate --file internal/config/testdata/reference.yaml` → accepté.
4. Le même fichier avec `timezon:` au lieu de `timezone:` → refusé, avec le nom de la clé et sa ligne.
5. `koffr config show --redact` → aucune valeur sensible à l'écran.
6. Un test d'architecture violant délibérément une règle d'ADR-0010 → `verify` échoue.
7. Les sept tables existent dans `koffr.db` ; relancer la migration ne change rien.
8. `docs/inputs/spike-2026-09-rpath.md` existe, nomme les trois distributions et **conclut**.

## Recette

- Scénario : `docs/recette/lot-0-scenario.md`.
- **Décision attendue de la session** : `D-01` — qui joue la recette des lots suivants, et sur quel
  parc réel (une machine de test avec de vraies bases, pas un jeu de données inventé).
- **Ce qui est voulu et pourrait passer pour un bug** :
  - `koffr version` existe alors que la surface CLI appartient au lot 1 (`N-1`) ;
  - les sept tables existent alors que cinq resteront vides jusqu'au lot 2 (`N-9`) ;
  - `config validate` ne teste **ni** la joignabilité des bases **ni** l'existence des outils :
    c'est `E-034`, au lot 1 ;
  - aucune commande ne sauvegarde quoi que ce soit — c'est le lot.

## Risques et points à vérifier en route

| Risque | Ce qu'on fait s'il se réalise |
| --- | --- |
| Le spike échoue sur Alpine (musl) — **attendu** | On l'écrit, on ouvre l'ADR qui amende `E-043`, et le lot 1 perd sa vague « installation gérée » ; `D-06` perd une partie de son objet |
| Le poste est `darwin/arm64` : `linux/amd64` passe par l'émulation, qui peut mentir sur l'édition de liens | Si le résultat est douteux, refaire le spike sur une vraie machine Linux `amd64` **avant** le lot 1 |
| `golangci-lint` 2.8.0 est construit avec go1.25.5 et doit analyser du code `go 1.27` | Monter `golangci-lint` ; s'il n'existe pas de version compatible, `go vet` et les tests restent bloquants, le lint passe en avertissement, avec une `N-n` datée dans le journal |
| Le seuil de 30 Mo ne mord pas au lot 0 (binaire minuscule) | Normal : on mesure dès maintenant pour voir la pente. Il mordra au lot 4 avec le SDK S3 et le client Docker |
| `depguard` ne sait pas exprimer toutes les règles d'ADR-0010 | Déjà traité par `N-8` : le test Go fait autorité |
| Le renommage du dossier casse la session en cours | Le propriétaire le fait entre deux sessions (tâche `0.1`) |

## Ce qui reste ouvert

`D-01` (référent de recette), `D-05` (calendrier du CDC de `koffr-server`), `D-06` (qui construit,
signe et héberge les binaires d'outils — **bloque une vague du lot 1**), `Q-15` (signature des
archives d'outils), `Q-20` (réduite à macOS par ADR-0011), et les 19 autres `Q-nn`. Aucune ne bloque
le lot 0.

## Journal d'exécution

Rempli par `/executer-plan` : échecs, décisions `N-n` ajoutées en route, écarts au plan, datés.
