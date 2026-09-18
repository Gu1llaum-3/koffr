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
- **N-12 (tranchée par le propriétaire le 2026-09-18, option (a)) — que fait-on du dépôt distant
  existant ?** *Contexte* : `github.com/Gu1llaum-3/koffr` contient déjà une implémentation
  antérieure et indépendante du produit (voir le bloc sous la tâche `1.4`). *Ce qui est bloqué* :
  la tâche `1.4` — « CI verte au premier push » n'a pas de push possible — et, au-delà, le
  critère de sortie 1 du lot. *Options* : (a) le distant est un prototype abandonné, on le
  remplace par le travail issu de ce plan ; (b) le distant est la vraie base de code, et ce lot 0
  devient une reprise d'existant, pas un squelette — le plan et la roadmap sont réécrits ;
  (c) on pousse ce travail dans un autre dépôt, et `N-2` change de chemin de module.
  **Réponse du propriétaire** : option (a). Le distant était un prototype abandonné ; il a été
  renommé `koffr-old` et un dépôt vide a été recréé sous le même nom. Rien n'est écrasé, le
  prototype reste consultable, et `N-2` tient : le chemin de module ne change pas.
- **N-14 (2026-09-18) — la règle réseau d'ADR-0010 vise les clients, pas les serveurs.**
  *Contexte* : ADR-0010 écrit « tout sauf `store`, `engine`, `egress` : l'ouverture d'une connexion
  réseau ». Pris à la lettre, cela interdit à `internal/httpd` d'importer `net/http`, alors que
  l'ADR le désigne comme l'interface web locale — la règle se contredirait. `ARCHITECTURE.md` lève
  l'ambiguïté en parlant de « tout **client** réseau ». *Décision* : `net` et `net/smtp` restent
  réservés à `store`, `engine` et `egress` ; `net/http` leur est ouvert **plus `httpd`**, qui
  écoute et n'appelle pas. *Exclut* : un `httpd` qui ferait un appel sortant — il passera par
  `egress` comme les autres.
- **N-15 (2026-09-18) — `config show` n'a aucun chemin de code qui imprime un secret.**
  *Contexte* : le plan écrit `config show --redact` ; le CDC ne décrit pas la commande. Un drapeau
  qui se désactive crée la fuite que `E-115` interdit. *Décision* : le masquage est le seul
  comportement ; `--redact` existe, vaut `true`, et `--redact=false` est **refusé** avec un message
  qui l'explique. *Exclut* : une option de débogage qui imprimerait les secrets en clair.
- **N-16 (2026-09-18) — le drapeau de chemin de configuration s'appelle `--config`.**
  *Contexte* : `N-11` dit `--config`, la vérification de bout en bout n° 3 du plan écrit `--file`.
  *Décision* : `--config`, drapeau persistant de la racine, avec `--state-dir` ; la ligne du § 
  « Vérification de bout en bout » se lit avec `--config`. *Exclut* : deux noms pour une chose.
- **N-13 (2026-09-18) — l'identité git de ce dépôt est locale et sans adresse personnelle.**
  *Constat* : `~/.gitconfig` porte l'identité **professionnelle** du propriétaire, et les deux
  premiers commits en avaient hérité ; le prototype, lui, était signé d'une adresse Gmail
  personnelle. *Décision* : `.git/config` fixe `Gu1llaum-3
  <67098259+Gu1llaum-3@users.noreply.github.com>` pour ce dépôt seul, et les deux commits ont été
  réécrits **avant tout push**. *Raison* : le dépôt est public (ADR-0001) ; une adresse poussée
  dans un commit public ne se retire plus. *Exclut* : hériter du `~/.gitconfig` global, qui reste
  professionnel pour les autres dépôts de la machine.

Aucune de ces décisions ne survit au lot au sens d'ADR : celle qui le devait — la portée de Windows —
a été écrite en **ADR-0011** avant ce plan.

## Vagues

### Vague 0 — Avant la première branche

- [x] **0.1** Renommer le dossier de travail `keeper` → `koffr`. **À faire par le propriétaire entre
      deux sessions** : cela change le répertoire courant de la session en cours.
- [x] **0.2** Fait le 2026-09-18 : ADR-0011 écrit et accepté, `D-04` barrée, `E-117` amendée à trois
      cibles, `Q-20` réduite à macOS, index et roadmap à jour.

### Vague 1 — Dépôt, outillage, CI (`lot0/wave-1-repository-and-ci`)

- [x] **1.1** `git init` sur `main`, `.gitignore` Go, `LICENSE` Apache-2.0, `NOTICE`, `README.md` en
      anglais (ADR-0003) renvoyant vers la documentation française. *Pas de test : configuration,
      justifié.*
- [x] **1.2** Test d'abord `internal/build/build_test.go` — `Info()` porte nom, version, commit,
      date, version de Go et plateforme ; la sortie `--json` a ces clés et aucune vide en build de
      release. Puis `go.mod` (`github.com/Gu1llaum-3/koffr`, `go 1.27`), `cmd/koffr/main.go`, racine
      cobra, commande `version [--json]` alimentée par `-ldflags` (`N-1`, `N-2`).
- [x] **1.3** Tâches `mise` : `check` (`go vet` + `go build`), `lint` (`golangci-lint run`), `test`
      (`go test ./... -race`), `build`, `verify` (les quatre). `.golangci.yml` au **schéma v2**.
      `golangci-lint` épinglé dans `mise.toml` (`N-3`, `N-4`). `CLAUDE.md` § Commandes rempli.
- [x] **1.4** `.github/workflows/verify.yml` : `mise` puis `verify`, sur push et PR, **verte au
      premier push**.

      > Arrêt (2026-09-18) : le fichier est écrit, mais **il ne peut pas être poussé**.
      > `github.com/Gu1llaum-3/koffr` **existe déjà sur GitHub**, public, `main` par défaut,
      > **59 commits** du 2026-09-05 au 2026-09-09, et contient **une autre implémentation
      > complète de koffr** : même chemin de module, `go 1.26.0`, `Makefile` et `lefthook`,
      > `AGENTS.md`, `adr/` à la racine avec **7 ADR qui ne sont pas les nôtres**, et
      > `internal/{backup,binlog,catalog,cli,config,crypto,executor,httpapi,logging,manifest,
      > notify,pipeline,restore,retention,scheduler,source,storage,verify,watch}` — soit une
      > bonne partie du périmètre des lots 2 à 5 de notre roadmap.
      > L'état de départ du plan (« le dépôt n'est pas sous git », « aucun code ») est vrai
      > **en local** et faux **sur le dépôt distant nommé par `N-2`**. Rien n'a été poussé :
      > un push de notre `main` serait rejeté, et un push forcé détruirait ces 59 commits.
      > Résolu le 2026-09-18 par **`N-12`** (option (a)) : le distant a été renommé `koffr-old`
      > et un dépôt vide recréé sous le même nom. La tâche reprend.
- [x] **1.5** Vague verte : `verify`, commit `chore: bootstrap go module, tooling and ci`.

### Vague 2 — Spike d'édition de liens (`lot0/wave-2-tool-linking-spike`) — `E-130`

L'inconnue en premier : si elle tombe mal, le lot 1 change avant d'être écrit.

- [x] **2.1** `scripts/spike-rpath.sh` : extraire `pg_dump` 16 et `mariadb-dump` 11.4 avec leurs
      bibliothèques partagées, ajuster le `RPATH` (`patchelf`), puis **exécuter `--version`** dans
      Debian 12, Rocky 9 et Alpine 3.20, en `linux/arm64` (natif) et `linux/amd64` (émulé).
- [x] **2.2** Rapport `docs/inputs/spike-2026-09-rpath.md` : ce qui marche, ce qui casse, sur quelle
      distribution et pourquoi. **Conclusion explicite** : `E-043` est tenable, ou ne l'est pas.
- [x] **2.3** Si Alpine (musl) échoue — le résultat attendu — écrire la conclusion et **ouvrir un
      ADR** amendant `E-043` (outils gérés sur glibc seulement ; Alpine renvoyé à la stratégie
      `exec` ou aux outils de l'hôte), **avant** le lot 1.

      > Sans objet (2026-09-18) : **la condition ne s'est pas réalisée**. Alpine passe, `--version`
      > comme dump réel par TCP, en `arm64` et en `amd64`. Aucun ADR n'est écrit ; la raison
      > technique est dans le rapport. `E-043` reste en l'état.
- [x] **2.4** Vague verte : commit `chore(tools): add linking spike and its report`.

### Vague 3 — Frontières d'architecture (`lot0/wave-3-architecture-boundaries`)

- [x] **3.1** Squelette des paquets d'ADR-0010, un `doc.go` d'une phrase par paquet.
- [x] **3.2** Test d'abord `internal/arch/boundaries_test.go` — les neuf règles de dépendance
      d'ADR-0010 vérifiées sur le graphe réel des imports, avec des fixtures violantes dans
      `testdata/` qui **doivent** faire échouer le test (`N-8`).
- [x] **3.3** `.golangci.yml` : `depguard` interdit `mattn/go-sqlite3` (`E-027`) ; `forbidigo`
      interdit `os.Getenv` hors `internal/config` et `os/exec` hors `internal/engine`.
- [x] **3.4** Vague verte : commit `chore(arch): enforce dependency boundaries in verify`.

### Vague 4 — Configuration (`lot0/wave-4-configuration`)

Exigences : `E-032`, `E-033`, `E-036`, `E-037`, `E-115`.

- [x] **4.1** Test `internal/config/parse_test.go` — **`CFG-01`** : une clé inconnue est une erreur
      qui nomme la clé **et sa ligne**. Cas : à la racine, dans `databases[0]`, dans
      `destinations[0]`. Puis le code (`yaml.v3` + `KnownFields(true)`).
- [x] **4.2** Test — **`CFG-02`** : chaque champ sensible accepte `x`, `x_env` et `x_file` ;
      **`CFG-03`** : deux formes déclarées à la fois est une erreur, et un `*_file` absent échoue
      **au démarrage**, pas au premier usage.
- [x] **4.3** Test — **`CFG-04`** : `agent.timezone` est obligatoire et validé par
      `time.LoadLocation` ; la variable `TZ` du système n'a aucun effet. `import _ "time/tzdata"`
      (`N-6`).
- [x] **4.4** Test — **`CFG-05`** : la forme cible du § 5.1, **copiée telle quelle** du CDC dans
      `internal/config/testdata/reference.yaml`, est acceptée, et chaque champ atterrit où attendu.
- [x] **4.5** Test — **`CFG-06`** : `config show --redact` n'affiche aucune valeur sensible, et le
      type de configuration ne fuit rien via `%v`, `%+v` ni `slog` (`E-115`).
- [x] **4.6** `internal/config/rules.md` créé depuis `docs/modeles/rules.md` : `CFG-01` à `CFG-06`,
      chacune avec sa source `E-nnn` et le nom de son test.
- [x] **4.7** Vague verte : `verify`, commit `feat(config): strict yaml parsing with env and file secrets`.

### Vague 5 — État local (`lot0/wave-5-local-state`)

Exigences : `E-027`, `E-028`, `E-026`.

- [x] **5.1** Test `internal/state/open_test.go` — l'ouverture pose `journal_mode=WAL`,
      `foreign_keys=ON`, `busy_timeout=5000`, `synchronous=NORMAL` (ADR-0006), avec le pilote
      `modernc.org/sqlite` (nom de pilote **`sqlite`**, pas `sqlite3`).
- [x] **5.2** Test `internal/state/migrate_test.go` — la migration `0001` crée les **sept** tables de
      `E-028` (`N-9`), est idempotente, et enregistre sa version ; une base d'une version inconnue
      refuse de démarrer.
- [x] **5.3** Test — les statuts sont contraints par `CHECK` (un statut inconnu échoue à
      l'insertion) ; les horodatages sont en RFC 3339 **UTC** ; tailles et durées sont des entiers
      (ADR-0006).
- [x] **5.4** Test `internal/state/paths_test.go` — les chemins de `E-026` sont les valeurs par
      défaut, surchargeables par `--config` et `--state-dir` (`N-11`), et `tmp/` est purgé à
      l'ouverture de l'état, pas à chaque commande (`N-10`).
- [x] **5.5** `internal/config/rules.md` complété : **`CFG-07`** (chemins par défaut et surcharges),
      **`CFG-08`** (purge de `tmp/`).
- [x] **5.6** Vague verte : `verify`, commit `feat(state): sqlite schema, migrations and disk layout`.

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

### 2026-09-18 — vague 1, tâches 1.1 à 1.3

- Plan validé, vague 0 cochée : le dossier de travail s'appelle déjà `koffr` (`0.1`), ADR-0011 est
  écrit (`0.2`).
- `git init -b main`, commit initial de la documentation de pilotage, puis branche
  `lot0/wave-1-repository-and-ci`.
- `1.1` fait : `.gitignore` Go, `LICENSE` Apache-2.0 (texte canonique), `NOTICE`, `README.md`
  anglais renvoyant vers les documents français.
- `1.2` fait en TDD : `internal/build/build_test.go` écrit d'abord, **rouge constaté**
  (`undefined: Info`), puis `internal/build`, `internal/cli` (cobra, `version [--json]`) et
  `cmd/koffr/main.go`. Le marquage `-ldflags` est vérifié sur un binaire réel.
- `1.3` fait : cinq tâches `mise` (`check`, `lint`, `fmt`, `test`, `build`, `verify`),
  `.golangci.yml` au schéma v2, `CLAUDE.md` § Commandes rempli.
- **Écart favorable au risque « `golangci-lint` 2.8.0 construit avec go1.25.5 »** : `mise` épingle
  désormais `golangci-lint` **2.13.2, construit avec go1.27.0**. Le repli prévu (lint en
  avertissement) est sans objet.
- `mise run verify` : **code de retour 0**.

### 2026-09-18 — vague 5, état local

- `internal/state` : ouverture, migrations embarquées, les **sept tables de `E-028`** en une seule
  migration relue à la main (`N-9`), et la purge de `tmp/`. `internal/config` porte désormais
  l'arborescence de `E-026` (`Paths`), ce que `internal/cli` consomme — `AR-04` lui interdit
  d'importer `internal/state`.
- Les `PRAGMA` d'ADR-0006 sont posés **dans la chaîne de connexion**, pas par des `Exec` après
  ouverture : SQLite les applique par connexion, et `database/sql` en ouvre autant qu'il veut. Un
  test tient deux connexions à la fois pour le prouver.
- Le schéma applique ADR-0006 de façon **opposable**, pas documentaire : tables `STRICT` (une
  taille écrite « 12 MB » est refusée), statuts contraints par `CHECK`, horodatages imposés par un
  `GLOB` qui **refuse** `+02:00`, un `datetime()` SQLite et un entier Unix.
- **Écart de méthode, assumé et signalé** : les tâches `5.3` et une partie de `5.4` n'ont **pas**
  eu leur rouge. Le schéma écrit en `5.2` et la purge écrite en `5.1` les satisfaisaient déjà. À
  défaut, les contraintes ont été **retirées temporairement** pour vérifier que les tests
  échouent sans elles — ils échouent —, puis remises. Un rouge par suppression vaut moins qu'un
  rouge par antériorité ; c'est le découpage du plan qui l'a produit, pas un raccourci.
- `CFG-07` (chemins) et `CFG-08` (purge) écrites dans `internal/config/rules.md`, avec la raison
  pour laquelle elles y vivent alors qu'un de leurs tests tourne dans `internal/state`.
- **Mesure de `E-117`** : le binaire fait **3,9 Mio**, mais `internal/state` **n'est encore lié
  par aucune commande** — `serve` est au lot 5. Une sonde qui le lie donne **7,4 Mio** :
  `modernc.org/sqlite` coûte **+3,5 Mio**, et il reste **22,6 Mio** avant le seuil de 30 Mo. La
  mesure de la tâche `7.2` devra le dire, sans quoi elle annoncera une marge qui n'existe pas.
- **Non créée** : la table d'historique des suppressions de rétention qu'ADR-0006 mentionne pour
  `E-081`. `E-028` en nomme sept, `N-9` dit sept. Elle viendra avec le lot 5, par migration.
- `mise run verify` : **code de retour 0**.

### 2026-09-18 — vague 4, configuration

- Six règles écrites, `CFG-01` à `CFG-06`, chacune avec sa ligne dans `internal/config/rules.md`,
  sa source et le nom de son test. Chaque test a été écrit avant son code et son **rouge
  constaté** : `undefined: Load`, `_env` et `_file` non résolus, `Location` absente,
  `undefined: Parse`, `Redacted` absente, `unknown command "config"`.
- `internal/config/testdata/reference.yaml` est **extrait par script du § 5.1 du CDC**, pas
  retapé : seul `keeper` devient `koffr` (ADR-0001). Aucune clé ne diffère.
- **Séparation `Parse` / `Load`** : `Parse` vérifie la forme sans toucher à l'environnement ni au
  disque ; `Load` résout ensuite les secrets. C'est ce qui permet à `config validate` de tourner
  depuis un poste sans les fichiers de production, et à `config show` de ne jamais lire un secret.
- `N-15` et `N-16` ajoutées.
- **Deux pièges de la pile rencontrés**, à inscrire en § Conventions au `7.4` :
  1. `yaml.v3` ignore les **champs privés** quand il évalue `omitempty`. Un type dont la valeur
     est privée est donc toujours vu comme vide et **disparaît de la sortie** : il faut lui donner
     un `IsZero()`. Sans cela un secret renseigné s'effaçait au lieu d'être masqué.
  2. `node.Decode` dans un `UnmarshalYAML` **perd la strictité** du décodeur parent. Les clés de
     `tools` sont donc lues à la main, ce qui préserve `CFG-01` et les numéros de ligne.
- **Faux positif attrapé** : la première version du test `CFG-06` sur `access_key_id` passait à
  vide, faute de destination dans le document d'essai. Deux `omitempty` manquants s'étaient
  glissés avec. Test renforcé, tags corrigés.
- `B-06` ouverte : `config show` imprime les champs non renseignés.
- Vérifications de bout en bout **3, 4 et 5 du plan atteintes** : la forme cible est acceptée ;
  `timezon:` est refusé en nommant la clé et la ligne 8 ; `config show` n'imprime aucune valeur
  sensible.
- `mise run verify` : **code de retour 0**.

### 2026-09-18 — vague 3, frontières d'architecture

- 19 paquets d'ADR-0010 créés, un `doc.go` d'une phrase chacun.
- `internal/arch/boundaries_test.go` écrit d'abord, **rouge constaté** (fixtures absentes, linter
  muet), puis les fixtures et la configuration du linter.
- **Huit des neuf règles** sont vérifiées sur le graphe réel des imports (`AR-01` à `AR-04`,
  `AR-06` à `AR-09`), chacune avec sa fixture violante sous `testdata/violations/` : un test qui
  cesserait de détecter une règle échoue. Une fixture **autorisée** (un module du domaine qui
  n'importe que `shared`) garde le contrôle de ses faux positifs.
- **`AR-05` (seul `config` lit l'environnement) n'est pas exprimable sur un graphe d'imports** :
  c'est un appel, pas un import. `forbidigo` la porte (`os.Getenv`, `os.LookupEnv`, `os.Environ`),
  et un troisième test vérifie que la configuration du linter la porte toujours.
- `depguard` : `mattn/go-sqlite3` interdit partout (`E-027`), `os/exec` interdit hors
  `internal/engine`.
- **Les règles mordent, vérifié** : un fichier fautif dans `internal/pipeline` produit
  `depguard: 1` et `forbidigo: 1` ; le même import dans `internal/engine`, et `os.Getenv` dans
  `internal/config`, passent. Le test d'architecture, lui, a bien signalé `AR-07`.
- **Limite constatée** : l'interdit `mattn/go-sqlite3` ne peut pas être prouvé tant que le module
  n'est pas dans `go.mod` — `typecheck` échoue d'abord et court-circuite les autres linters. La
  garde existe, sa démonstration attend qu'on ait une raison d'ajouter le module. À noter en
  § Conventions au `7.4`.
- `N-14` ajoutée : la règle réseau vise les clients, pas `httpd` qui écoute.
- Les codes `AR-nn` doivent être repris dans `ARCHITECTURE.md` à la tâche `7.3`.
- `mise run verify` : **code de retour 0**. Binaire `dist/koffr` : **3 262 514 octets** (3,1 Mio),
  très en deçà des 30 Mo de `E-117` — normal à ce stade, la pente se mesurera au lot 4.
- `1.4` : arrêt sur un état de départ faux (dépôt distant déjà peuplé), tranché par `N-12`, puis
  repris. Actions épinglées après vérification de leurs versions réelles : `actions/checkout@v7`,
  `jdx/mise-action@v4` — les versions que le modèle « connaissait » (v5, v3) étaient périmées.
- **`N-13`** appliquée avant tout push : identité locale `Gu1llaum-3
  <67098259+Gu1llaum-3@users.noreply.github.com>`, les deux premiers commits réécrits. Audit :
  plus aucune adresse professionnelle ni personnelle, ni dans les commits, ni dans les fichiers
  suivis.
- `1.5` : `mise run verify` **code de retour 0**, merge `--no-ff` dans `main`, premier push.
  **CI verte au premier push** — exécution `35350393081`, `verify` en 44 s.
- Ordre imposé par le démarrage : la CI ne pouvait pas tourner **avant** le merge, faute de dépôt
  distant. La vague a donc été vérifiée en local, mergée, puis poussée en une fois. Les vagues
  suivantes passeront par une branche poussée et sa CI avant merge.

### 2026-09-18 — vague 2, spike `E-130`

- `scripts/spike-rpath.sh` écrit et rejoué : 2 outils × 3 distributions × 2 architectures,
  **18 exécutions, 18 succès**. Rapport : `docs/inputs/spike-2026-09-rpath.md`.
- **Première version rouge partout**, Debian comprise : `patchelf --set-rpath` écrit `DT_RUNPATH`,
  qui **n'est pas hérité** par les dépendances. Corrigé en posant un `runpath` sur **chaque
  bibliothèque** du bundle. C'est l'enseignement principal du spike.
- **Écart au plan, favorable** : le plan tenait l'échec sur Alpine pour acquis. Alpine **passe** —
  le bundle embarque son propre chargeur glibc, et depuis glibc 2.34 `nss_files` et `nss_dns` sont
  intégrés à `libc.so.6` (vérifié sur la libc 2.41 embarquée). La tâche `2.3` est donc **sans
  objet** : aucun ADR n'amende `E-043`, et le lot 1 garde sa vague « installation gérée ».
- **Extension assumée de la tâche `2.1`** : au-delà du `--version` demandé, `pg_dump` sauvegarde
  **réellement** une base par TCP en désignant le serveur **par son nom**, depuis les trois cibles.
  Sans cela, la conclusion demandée par `2.2` n'aurait porté que sur l'édition de liens, pas sur
  la résolution de noms — le vrai risque sur musl.
- **Contrainte nouvelle pour `E-026`** : `PT_INTERP` est absolu et n'interprète pas `$ORIGIN`. Un
  outil géré ne se **déplace** pas, il se **re-patche**. À prendre en compte au lot 1.
- `B-05` ouverte : aucun lint des scripts shell (`shellcheck`) dans `verify`.
- Poste `darwin/arm64` : `amd64` est émulé. Résultat identique à `arm64`, à reconfirmer sur une
  machine `amd64` réelle.
- `mise run verify` : **code de retour 0**.
