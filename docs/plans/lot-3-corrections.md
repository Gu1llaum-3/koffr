# Plan lot 3 — Corrections de recette

> Statut : **validé par le propriétaire le 2026-10-08**. Exécuté par `/executer-plan`. Les règles communes à tous les plans sont
> dans `METHODE.md` § « Exécution d'un plan » et ne sont pas répétées ici.

Travail issu de la session de recette du lot 3 (2026-10-08). Cinq des six critères de sortie du lot
sont **tenus**, et le plus important l'est sur du réel : un dépôt d'archives s'inventorie avec `jq`
seul, et se restaure depuis une machine sans koffr avec la seule clé de séquestre — 400 000
commandes et 300 000 lignes rendues, sommes identiques à l'originale ; 250 000 factures de même.
Ce plan corrige les onze anomalies que la session a trouvées autour, et livre les deux décisions
que le propriétaire a prises en séance.

## Périmètre

**Les onze anomalies `A-19` à `A-29`**, dont deux bloquantes, et les deux décisions de la session.
Le registre fait foi : `docs/recette/anomalies.md`.

| `A-nn` | Constat | Ce qu'il faut |
| --- | --- | --- |
| `A-19` | **Les archives d'avant le lot 3 sont invisibles de `koffr list`** : rien ne les inscrit au catalogue, rien ne balaie le dépôt. Un exploitant qui met à jour perd de vue tout son historique | `koffr list` **lit aussi la destination** : une archive qu'elle porte et que le catalogue ignore est montrée, marquée « not in the catalogue » |
| `A-20` | Sur un job qui **échoue avant la vérification**, `koffr backup` affiche encore `not in this release: verification arrives at the lot 3`, en exécutant le lot 3. C'est le **critère de sortie n° 6** | Le champ `Deferred` **disparaît** : au lot 3 aucune des sept étapes n'est absente, sur aucun chemin |
| `A-21` | `pg_dump` meurt en cours de flux → l'étape rapportée est **`compression`**, alors que le journal vient d'écrire `step dump: done` | L'échec d'un sous-process est rapporté sur l'étape **`dump`** |
| `A-22` | Le manifeste écrit `"format": "pgc"` ; le § 5.3 écrit `"pg_custom"`. koffr réutilise l'extension de fichier comme valeur du champ | Le manifeste écrit `pg_custom` ; l'extension du fichier reste `pgc` |
| `A-23` | Tout est rendu **en UTC** alors que `agent.timezone: Europe/Paris` est déclaré, et `koffr list` et `koffr verify` emploient deux formats différents | ADR-0006 appliqué : base en UTC, **affichage et manifeste en `agent.timezone`**, un seul format |
| `A-24` | `koffr verify` passe le catalogue à `failed` mais **ne touche pas le manifeste**, qui continue d'annoncer « vérifiée » | **Le manifeste reste figé** — c'est l'instantané de la sauvegarde. Le `README` documente le contrôle `jq` + `sha256sum` |
| `A-25` | Sur une archive dont la structure n'a **jamais** été contrôlée, `koffr verify` affirme qu'elle l'a été « while the dump streamed past » | La phrase ne se dit **que si le contrôle a eu lieu** |
| `A-26` | Sans `pg_restore`, **toute** sauvegarde PostgreSQL échoue — et `doctor` affiche une ligne entièrement verte | `doctor` le signale et nomme la majeure à installer |
| `A-27` | `list --destination nexistepas` répond `no backup in the catalogue yet`, **code 0** : une faute de frappe produit une liste vide rassurante | Refus nommant les destinations connues, **code 1** |
| `A-28` | `backups.manifest` et `backups.job_id` restent vides, et la table **`jobs` n'a aucune ligne** | `jobs` reçoit sa ligne, `job_id` la référence, `manifest` garde le manifeste déposé |
| `A-29` | La ligne de journal de la vérification recopie **toute** la sortie de `pg_restore --list` — 30 lignes dans un champ JSON — et cette table des matières **nomme l'utilisateur de connexion** | Une **synthèse** : nombre d'entrées, et ce qui cloche s'il y a lieu. Jamais le texte |

**Ce que la session a décidé, au-delà des anomalies** :

- **Métadonnées en clair confirmées** : un dépôt volé livre noms de bases, moteur, version exacte
  du serveur, horodatages, tailles, `tool.path` et clés publiques — et c'est voulu, parce qu'un
  manifeste lisible est ce qui rend le dépôt exploitable quand koffr a disparu. Le § 6 est confirmé
  tel quel, **aucun changement de code**. Reste à l'écrire noir sur blanc dans le `README`.
- **`koffr list` gagne deux colonnes** : la **durée du job** et la **taille brute du dump**, à côté
  de la taille stockée. Le manifeste porte déjà `duration_ms` et `size_raw` ; le catalogue porte
  `size_bytes` ; la durée manque en base.

**Écarté de ce plan** :

- `Q-02` — quelle destination est relue quand il y en a plusieurs. Elle demande S3 : recette du
  lot 4, comme le plan du lot 3 l'avait déjà posé (`N-5`).
- `E-065` (revérification périodique, lot 6), `E-068` (échec d'une destination, lot 4),
  `B-01`, `B-09`, `B-10`.
- Une commande `koffr import` : `A-19` est tranchée par la lecture de la destination, pas par un
  geste de l'exploitant.

**Critère de sortie de ce plan** :

1. le **critère de sortie n° 6 du lot 3** est tenu : `koffr backup` ne déclare aucune étape absente,
   **y compris quand il échoue** ;
2. une archive écrite par le binaire du lot 2, présente sur la destination et absente du catalogue,
   **apparaît** dans `koffr list` ;
3. `koffr list` et `koffr verify` affichent l'heure de `agent.timezone`, et le manifeste porte le
   décalage local comme le § 5.3 le montre ;
4. le manifeste écrit `pg_custom` ;
5. `doctor` refuse de dire qu'une base PostgreSQL va bien quand `pg_restore` manque ;
6. `list --destination <inconnue>` et `jobs` vide ne se produisent plus ;
7. aucune ligne de journal ne porte la table des matières d'un dump.

## État de départ (vérifié le 2026-10-08)

Constaté dans le code, dans le catalogue d'une vraie session de recette et sur un dépôt réel.

- **`main` est vert**, local et CI. Lot 3 mergé en six vagues, binaire 17 Mio en `linux/arm64`
  (13 Mio pour celui du lot 2 : l'écart de 4 Mio est SQLite, entré par `cmd/koffr`).
- **`deferredToLot3` existe encore** : `internal/domain/backup/service.go:40`, posé par
  `freshSteps()` ligne 742 et effacé seulement par le chemin nominal. `journalDeferred` (ligne 258)
  le journalise quand aucun `StructureWatcher` n'est câblé.
- **`extensionFor`** (`internal/cli/backup.go:257`) rend `pgc`, et cette valeur sert **à la fois**
  d'extension de fichier et de champ `format` du manifeste. Le test unitaire du manifeste
  (`manifest_test.go:118`) emploie bien `pg_custom` : c'est le câblage qui diverge, pas la règle.
- **`renderArchives`** (`internal/cli/list.go`) formate avec `.UTC().Format("2006-01-02 15:04Z")` ;
  `koffr verify` rend `2026-10-08T12:00:13Z`. Deux formats, aucun fuseau.
- **`catalog.Catalog`** a quatre méthodes et **aucune ne lit une destination**. Mais
  `store.Filesystem.List(ctx, prefix) ([]backup.Entry, error)` **existe déjà** et est testé : la
  capacité est là, le port manque. `AR-03` interdit à `catalog` d'employer `backup.Entry` ; il lui
  faut le sien.
- **`catalog.Backup` porte déjà `Job` et `Manifest`**, documentés « empty until the scheduler writes
  job rows » — et les deux colonnes sont vides en base. La table `jobs` est vide aussi : **0 ligne**
  après onze sauvegardes.
- **`renderArchives` répond `no backup in the catalogue yet` sur zéro ligne**, sans distinguer le
  dépôt vide de la destination inconnue.
- `internal/cli/doctor.go` (219 lignes) énumère les outils de **dump** ; rien n'y cherche
  `pg_restore`, dont `engine.tableOfContents` a pourtant besoin.

### Mesuré en séance, pas supposé (2026-10-08)

| Constat | Chiffre |
| --- | --- |
| Archives produites, dont une par le binaire du lot 2 | 7 fichiers, 6 manifestes |
| Archives vues par `koffr list` avant correction | **5 sur 6** — celle du lot 2 manque |
| Lignes de la table `jobs` après onze sauvegardes | **0** |
| Taille du champ `detail` d'une ligne de journal de vérification | **30 lignes** de table des matières |
| Corruption d'un octet détectée sans koffr, par `jq` + `sha256sum` | **oui**, `EMPREINTE DIFFERENTE` |
| Occurrences d'un identifiant de connexion dans tout le dépôt | **0** sur huit motifs cherchés |

## Ce que le cahier des charges dit, et ce qu'il ne dit pas

- **Dit** : le § 5.3 montre `"started_at": "2026-09-18T02:00:03+02:00"` et `"format": "pg_custom"`.
  ADR-0006 dit « la conversion vers `agent.timezone` se fait à l'affichage et à l'écriture du
  manifeste ». `A-22` et `A-23` ne sont donc pas des arbitrages : ce sont des écarts à une règle
  déjà écrite.
- **Ne dit pas** : ce que `koffr list` doit montrer d'une archive que le catalogue ignore. Tranché
  en séance → `N-1`.
- **Ne dit pas** : ce qu'une ligne de journal doit garder d'un contrôle de structure. Le § 5.10 fixe
  les noms d'événements, pas le contenu des champs → `N-5`.

## Décisions d'implémentation

- **N-1 Une archive que le catalogue ignore est montrée, jamais inventée.** `koffr list` liste la
  destination et marque « not in the catalogue » toute archive qu'elle y trouve sans ligne en base.
  Son manifeste la renseigne quand il est là ; sinon seul le nom du fichier parle, et les colonnes
  qu'on ne peut pas remplir restent vides. *Raison* : `E-064` veut que l'absence ne passe jamais
  pour un succès — une archive invisible est pire qu'une archive non vérifiée. *Exclut* : écrire en
  base ce qu'on a trouvé sur le disque. Le catalogue reste la trace de ce que **ce** koffr a fait ;
  la destination reste la vérité du dépôt (ADR-0006).
- **N-2 `catalog` déclare son propre port de lecture de dépôt.** Une interface `Repository` chez
  `catalog`, avec son propre type d'entrée — jamais `backup.Entry`. *Raison* : `AR-03`, un module du
  domaine ne connaît pas son voisin. *Exclut* : partager un type entre `backup` et `catalog`, et
  faire remonter `internal/store` jusqu'à `internal/cli`.
- **N-3 Le fuseau voyage avec la commande, pas avec le domaine.** `agent.timezone` est résolu une
  fois dans `cmd/koffr`, passé à `internal/cli` comme un `*time.Location`, et le manifeste le reçoit
  par son constructeur. *Raison* : `AR-05`, seul `internal/config` lit l'environnement, et un module
  du domaine n'a pas à connaître la configuration de l'agent. *Exclut* : un `time.Local` implicite,
  qui dépend de `/etc/localtime` et ferait mentir `E-036`.
- **N-4 Le format du manifeste et l'extension du fichier sont deux fonctions distinctes.**
  `manifestFormat(family)` rend `pg_custom` ou `sql` ; `extensionFor(family)` rend `pgc` ou `sql`.
  *Raison* : `A-22` est née de leur confusion. *Exclut* : dériver l'une de l'autre.
- **N-5 Le journal garde ce qu'il a conclu, pas ce qu'il a lu.** La ligne de vérification porte le
  nombre d'entrées de la table des matières et, en cas de refus, ce qui cloche — jamais le texte.
  *Raison* : `A-29`, deux fois — la lisibilité du journal, et l'utilisateur de connexion que la
  table des matières nomme alors que `E-059` l'exclut du manifeste. *Exclut* : tronquer le texte,
  qui garderait les premières lignes, donc les noms de propriétaires.
- **N-6 `doctor` gagne une colonne `VERIFIABLE`.** Elle dit `yes`, ou ce qui manque pour que la
  structure puisse être contrôlée. *Raison* : `doctor` est la commande qui existe pour dire « ça
  marchera » ; une panne garantie doit s'y voir. *Exclut* : faire échouer `doctor` — un outil de
  dump présent et un `pg_restore` absent, c'est un avertissement, pas une erreur de configuration.
- **N-7 La durée vit en base.** `backups.duration_ms`, `INTEGER`, par migration. *Raison* : la
  colonne de `koffr list` demandée en séance, et le manifeste la porte déjà (`duration_ms`) — la
  recalculer depuis `started_at` et `finished_at` perdrait la précision que le manifeste garde.
  *Exclut* : lire le manifeste pour afficher une liste.

## Vagues

### Vague 1 — Plus aucune étape absente (`lot3c/wave-1-no-deferred-step`)

Les deux bloquantes d'abord, et la plus simple des deux en premier : elle est le critère de sortie.

- [x] **1.1** Test d'abord `internal/domain/backup/service_test.go` — **`BKP-24`** : sur un job qui
      **échoue à l'étape du dump**, aucune étape rendue ne porte de mention d'absence. Le test
      parcourt les sept et vérifie que le champ est vide partout, chemin d'échec compris.
- [x] **1.2** Retirer `deferredToLot3`, le champ `Deferred` et `journalDeferred`. Le test de la
      vague 4 du lot 3 qui les retournait est **retourné à nouveau**, pas supprimé.
- [x] **1.3** Test — **`BKP-25`**, `A-21` : quand un sous-process meurt en cours de flux, l'étape
      rapportée est **`dump`**, pas celle qui lisait le flux. Le test ferme le flux en erreur
      pendant la compression et lit l'étape rendue **et** la ligne de journal.
- [x] **1.4** `internal/domain/backup/rules.md` : `BKP-24`, `BKP-25`, avec leur source (`E-024`,
      `A-20`, `A-21`).
- [x] **1.5** Vague verte : `verify`, commit `fix(backup): no step is declared absent, not even when a job fails`.

### Vague 2 — Le dépôt se voit, même sans catalogue (`lot3c/wave-2-list-the-repository`)

- [x] **2.1** Test d'abord `internal/domain/catalog/repository_test.go` — **`CAT-05`** : le port
      `Repository` de `N-2` rend les archives d'une destination, et les entrées sont triées, les
      plus récentes d'abord (`CLAUDE.md` : une sortie ordonnée).
- [x] **2.2** Test `internal/cli/list_test.go` — **`CAT-06`**, `A-19` : une archive présente sur la
      destination et **absente du catalogue** apparaît dans `koffr list`, marquée
      `not in the catalogue`, et les colonnes qu'on ne peut pas remplir restent vides. Le cas
      inverse — une ligne de catalogue dont l'archive a disparu du dépôt — est **aussi** couvert.
- [x] **2.3** Test — **`CAT-07`**, `A-27` : `list --destination <inconnue>` refuse en nommant les
      destinations connues, **code 1** ; `list` sur un dépôt réellement vide garde son message et le
      **code 0**. Les deux cas ne se confondent plus.
- [x] **2.4** Câbler le port dans `cmd/koffr` : `internal/store` y est déjà connu, `internal/cli`
      reçoit le port construit (`AR-04`, `N-3` du plan du lot 3). `internal/arch` reste vert **sans
      modification** — c'est lui qui le prouve.
- [x] **2.5** `internal/domain/catalog/rules.md` : `CAT-05`, `CAT-06`, `CAT-07`.
- [x] **2.6** Vague verte : `verify`, commit `feat(catalog): show the archives a destination holds, catalogued or not`.

### Vague 3 — L'heure est celle qu'on a déclarée (`lot3c/wave-3-declared-timezone`)

- [x] **3.1** Test d'abord `internal/domain/catalog/manifest_test.go` — **`CAT-08`**, `A-23` : avec
      `Europe/Paris`, le manifeste écrit `started_at` et `verified.at` **avec le décalage local**,
      comme le § 5.3 le montre ; avec `UTC`, il écrit `Z`. Un fuseau à décalage non entier
      (`Asia/Kathmandu`, `+05:45`) est couvert : c'est là que les formats naïfs cassent.
- [x] **3.2** Test — **`CAT-09`**, `A-22` : le manifeste écrit `"format": "pg_custom"` pour
      PostgreSQL et `"sql"` pour MySQL et MariaDB, **quelle que soit** l'extension du fichier
      (`N-4`). Le test énumère les deux familles et vérifie que le nom de fichier, lui, ne change
      pas.
- [x] **3.3** Test `internal/cli/list_test.go` et `verify_test.go` — `koffr list` et `koffr verify`
      rendent l'heure de `agent.timezone`, dans **un seul** format. Le test pose un fuseau et lit la
      sortie que l'exploitant lit.
- [x] **3.4** `N-3` : le fuseau est résolu dans `cmd/koffr` et passé à `internal/cli` ; aucun
      `time.Local` nulle part. Une garde de source, sur le modèle des quatre déjà en place, refuse
      `time.Local` hors `internal/config`.
- [x] **3.5** `internal/domain/catalog/rules.md` : `CAT-08`, `CAT-09`.
- [x] **3.6** Vague verte : `verify`, commit `fix(catalog): render time in the declared timezone, and name the format as the spec does`.

### Vague 4 — Ce que le journal et le catalogue gardent (`lot3c/wave-4-journal-and-jobs`)

- [x] **4.1** Test d'abord `internal/domain/backup/journal_test.go` — **`BKP-26`**, `A-29` : la
      ligne de vérification porte le **nombre d'entrées** de la table des matières et, en cas de
      refus, ce qui cloche ; elle ne porte **jamais** le texte. Le test injecte une table des
      matières nommant un propriétaire et vérifie que ce nom n'apparaît dans aucun champ — la
      leçon de `BKP-21`, appliquée au journal.
- [x] **4.2** Migration : `backups.duration_ms` (`INTEGER`, `N-7`). **SQL relu avant commit**, et
      le test de `internal/state` passe sur une vraie base.
- [x] **4.3** Test `internal/state/catalog_test.go` — **`CAT-10`**, `A-28` : une sauvegarde écrit sa
      ligne dans **`jobs`**, `backups.job_id` la référence, et `backups.manifest` garde le manifeste
      déposé. Le test relit les trois.
- [x] **4.4** Test `internal/cli/list_test.go` : `koffr list` montre la **durée** et la **taille
      brute** à côté de la taille stockée, décision de la session du 2026-10-08.
- [x] **4.5** `rules.md` : `BKP-26` dans `backup`, `CAT-10` dans `catalog`.
- [x] **4.6** Vague verte : `verify`, commit `feat(catalog): record the job, the manifest and the duration`.

### Vague 5 — Ce que koffr dit quand il ne peut pas (`lot3c/wave-5-say-what-is-missing`)

- [ ] **5.1** Test d'abord `internal/cli/doctor_test.go` — **`RSV-14`**, `A-26` : sans `pg_restore`,
      `doctor` dit que cette base ne pourra **pas** être sauvegardée et nomme la majeure à
      installer ; avec, il dit `yes`. MySQL et MariaDB disent `yes` sans `pg_restore` : elles n'en
      ont pas besoin (`N-6`).
- [ ] **5.2** Test `internal/cli/verify_test.go` — **`VRF-06`**, `A-25` : sur une archive dont le
      catalogue dit `none`, `koffr verify` dit que la structure n'a **jamais** été vérifiée ; sur
      une archive à `structure`, il garde la phrase d'ADR-0017. La phrase ne se dit que si le
      contrôle a eu lieu.
- [ ] **5.3** `internal/domain/resolve/rules.md` : `RSV-14`. `internal/domain/verify/rules.md` :
      `VRF-06`.
- [ ] **5.4** Vague verte : `verify`, commit `fix(cli): say what is missing instead of looking fine`.

### Vague 6 — Le dépôt se lit sans nous (`lot3c/wave-6-document-the-repository`)

- [ ] **6.1** `README` : comment vérifier une archive **sans koffr** — `jq` + `sha256sum` contre
      `sha256_stored` —, et pourquoi le manifeste ne suit pas un `verify` en échec (`A-24`, décision
      du 2026-10-08). Le geste est **mesuré** : il a démasqué l'archive corrompue en séance.
- [ ] **6.2** `README` : ce qu'un dépôt volé livre, nommément — bases, moteur, version exacte du
      serveur, horodatages, tailles, chemin de l'outil, clés publiques — et pourquoi c'est voulu
      (§ 6 confirmé le 2026-10-08).
- [ ] **6.3** `scripts/check-inventory.sh` : ajouter le contrôle d'empreinte au contrôle de taille.
      Le script compare aujourd'hui `size_stored` au fichier ; il comparera aussi `sha256_stored`.
      C'est ce qui a trouvé la corruption en séance, et c'est à une ligne de `sha256sum`.
- [ ] **6.4** Rejouer les six parcours de `docs/recette/lot-3-scenario.md` sur le parc de recette,
      et mesurer le binaire. Écart attendu : faible. Marge actuelle : 13 Mio sous `E-117`.
- [ ] **6.5** Vague verte : `verify`, commit `docs: read an archive repository without koffr, and what it reveals`.

## Vérification de bout en bout

Sur le parc de recette, après la vague 6 :

1. une archive écrite par le binaire du **lot 2**, posée sur la destination sans ligne de catalogue,
   apparaît dans `koffr list` marquée `not in the catalogue` ;
2. `koffr backup` d'une base dont le serveur tombe en cours de dump → l'étape **`dump`** est nommée,
   et **aucune** étape ne se dit absente ;
3. `koffr list` et `koffr verify` affichent `13:58+02:00` là où ils affichaient `11:58Z`, et le
   manifeste porte le même décalage ;
4. `jq -r .format` sur un manifeste PostgreSQL → `pg_custom` ;
5. `pg_restore` retiré de la machine → `doctor` le dit **avant** que la sauvegarde n'échoue ;
6. `koffr list --destination nexistepas` → refus nommant les destinations connues, code 1 ;
7. `sqlite3 … "select count(*) from jobs"` → autant de lignes que de sauvegardes ;
8. `grep koffr_backup /var/log/koffr/koffr.log` → **rien** ;
9. `mise run verify` vert, CI verte.

## Recette

Ce plan **rejoue** `docs/recette/lot-3-scenario.md` (tâche `6.4`) ; il n'ouvre pas de nouveau
scénario — c'est le schéma des corrections des lots 0, 1 et 2.

- **Ce qui est voulu et pourrait passer pour un bug** : le manifeste **ne suit pas** un `koffr
  verify` en échec (`A-24`, décision du 2026-10-08) ; le catalogue n'inscrit **pas** ce que la
  lecture du dépôt a trouvé (`N-1`) ; `doctor` **avertit** sans échouer quand `pg_restore` manque
  (`N-6`) ; le journal ne garde **pas** la table des matières (`N-5`).
- **Décisions attendues de la session** : les deux colonnes ajoutées à `koffr list` tiennent-elles
  dans un terminal étroit ? Et la marque `not in the catalogue` se lit-elle sans explication ?

## Risques et points à vérifier en route

| Risque | Ce qui le borne |
| --- | --- |
| Lire la destination à chaque `koffr list` coûte cher sur un gros dépôt | `Filesystem.List` prend un préfixe ; la lecture est bornée à la base et à la destination demandées. Mesuré en `6.4` |
| Le port `Repository` fait remonter `internal/store` jusqu'à `internal/cli` | `N-2` et `AR-03` ; `internal/arch` doit rester vert **sans modification**, sinon la règle est contournée |
| Le fuseau casse les tests qui comparaient des chaînes UTC | Attendu : les tests des vagues 3 à 6 du lot 3 seront **retournés**, pas supprimés — la leçon du lot 3 |
| La migration `duration_ms` sur une base existante | `INTEGER` nullable, aucune donnée réécrite. SQL relu avant commit (`4.2`) |
| `time.Local` rentre par une dépendance | Garde de source en `3.4`, sur le modèle des quatre déjà en place |

## Ce qui reste ouvert

- **`Q-02`** — quelle destination est relue quand il y en a plusieurs. Se tranche à la recette du
  **lot 4**, qui apporte S3 et SFTP. `N-5` du plan du lot 3 tient en attendant.
- **`D-01`** — le dispositif de recette. L'instance Multipass est restée injoignable le 2026-10-08
  et la session a été jouée sur un parc Docker équivalent. À trancher : Multipass réparé, ou Docker
  adopté.
- **`D-08`**, **`B-01`**, **`B-07`**, **`B-09`**, **`B-10`**.

## Journal d'exécution

### Vague 1 — 2026-10-08

**`N-8` ajoutée en route — une archive que rien n'a contrôlée n'est pas une sauvegarde.** En
retirant la mention d'absence, le trou qu'elle masquait est apparu : un service construit **sans
vérificateur** produisait un job qui **réussissait**, étape 06 muette. `P4` dit le contraire. Le
job échoue désormais (`BKP-28`), et le monde de test câble un vérificateur par défaut, comme la
production le fait depuis le lot 3. *Exclut* : laisser le domaine réussir sur un câblage incomplet.

**`A-30` trouvée en corrigeant `A-21`** — et c'est la plus grave des deux. En mode **flux**, la
fermeture du dump était avalée par un `defer func() { _ = dump.Close() }()`. Or `Close` est ce qui
attend le sous-process et transforme un code de sortie non nul en erreur. Un `pg_dump` mort après
que son tube a atteint la fin de fichier laissait donc une archive **tronquée** que koffr écrivait,
vérifiait et appelait sauvegarde — `pg_restore --list` rend 0 sur un dump tronqué, mesuré au lot 3.
Le test l'a constaté noir sur blanc : sept étapes faites, `Verification:{Checked:true
ChecksumOK:true StructureOK:true}`, sur un dump mort. Corrigée dans la même vague (`BKP-27`), et
inscrite au registre.

**Écart au plan, assumé** : la vague livre quatre règles au lieu des deux prévues — `BKP-24` et
`BKP-25` du plan, plus `BKP-27` (`A-30`) et `BKP-28` (`N-8`). Les deux ajoutées sont dans le même
code et la même séance de test ; les séparer aurait laissé `main` avec une brèche `P4` connue.

**Le dump garde sa première ligne de journal.** Un job qui meurt en cours de dump écrit
`step done dump` avec sa décision de tampon, puis `step failed dump`. La première est ce qu'un job
tué laisse derrière lui — la raison d'être de `BKP-20` —, la seconde est ce qu'`A-21` demande.
L'étape est **reprise** dans le résultat (`Done: false`), de sorte que l'écran ne la compte pas.

### Vague 2 — 2026-10-08

**`N-9` ajoutée en route — `koffr list` ne résout aucun secret.** Lire une destination demande la
configuration, et `loadResolved` ouvre les `password_file` au passage : une machine dont un fichier
de mot de passe a disparu n'aurait plus pu **lister ses archives**. `CFG-09` sépare justement les
deux étapes ; `list` emploie désormais `loadShape`, qui ne lit que la forme. Un test le constate en
cassant un secret. *Exclut* : faire dépendre une commande de lecture de ce qu'il faut pour écrire.

**Constaté sur la machine de recette**, avec le vrai binaire du lot 2 : une archive écrite par lui
apparaît maintenant dans `koffr list`, marquée `not in the catalogue` ; et
`koffr list --destination nexistepas` répond `no destination has this identifier: "nexistepas";
the configuration declares disque-local`, code 1.

**Écart au plan, mineur** : le plan prévoyait que les tests de la vague montrent une archive
« dont les colonnes qu'on ne peut pas remplir restent vides ». Les fixtures des tests de `list`
pointaient sur des chemins (`boutique/2026/09/a.pgc.zst.age`) que `F5.5` ne décrit pas, et que la
lecture du dépôt ne sait donc pas identifier. Elles ont été réécrites au chemin déterministe, et
**les fichiers sont réellement déposés** : une fixture qui ne remplissait que le catalogue
décrivait un dépôt ayant perdu ses fichiers.

### Vague 3 — 2026-10-08

**`N-3` amendée.** Le plan la faisait résoudre dans `cmd/koffr` et voyager jusqu'à `internal/cli`.
Inutile : `config.Config.Location()` existe depuis le lot 0, et `internal/cli` tient déjà la
configuration chargée. Le fuseau vient donc de là. `AR-05` est intacte — `internal/config` reste le
seul à lire l'environnement — et il y a un câblage de moins. *Exclut* toujours : un `time.Local`
implicite, et c'est ce que la garde tient.

**La garde de `3.4` lit l'arbre syntaxique, pas le texte.** Première version écrite en `grep` : elle
s'est signalée **elle-même**, non pas sur son code mais sur la **phrase de commentaire** qui explique
ce qu'elle interdit. Deuxième essai, en écrivant les motifs par concaténation : même résultat, pour
la même raison. Elle inspecte désormais les `SelectorExpr` nommés `Local`, et sa fixture violante
est un fichier planté dans un répertoire temporaire — un contrôle qu'on n'a pas vu échouer ne prouve
rien (`CLAUDE.md`).

**Un seul format d'horodatage** : `2006-01-02 15:04 -07:00`, rendu par `moment()` dans
`internal/cli/streams.go`, employé par `koffr list` et par `koffr verify`.

**`storesFor` ne résout plus les secrets non plus** : relire une archive demande un chemin, et
l'archive est chiffrée pour des clés que koffr ne détient pas. Même raison que `N-9`.

### Vague 4 — 2026-10-08

**`N-7` tombe, et la tâche `4.2` avec elle : aucune migration.** Le plan voulait ajouter
`backups.duration_ms`. Vérification faite dans le schéma du lot 0 : `jobs.duration_ms` existe déjà,
`backups.size_bytes` porte la taille brute et `backups.finished_at` l'heure de fin. Les deux
colonnes demandées en séance se lisent donc de ce qui est là. **Une migration qu'on n'écrit pas est
une migration qu'on n'a pas à relire.** La tâche `4.2` est cochée sur ce constat, pas sur du SQL.

**`A-29` se corrige à la source, et se garde à l'arrivée.** `tableOfContents.Conclude` rendait tout
le texte de `pg_restore --list` ; il rend maintenant `« 16 table-of-contents entries »`. Et le
domaine borne ce qu'il journalise par `Conclusion()` — une ligne, 200 caractères — pour qu'un futur
observateur ne puisse pas y déverser un roman. **Tronquer aurait été pire que résumer** : on aurait
gardé les premières lignes, qui sont justement celles qui nomment les propriétaires.

**Écart au plan** : le test `VRF-01` du lot 3 vérifiait que le verdict **nommait la table semée**.
Il ne le peut plus, par construction. Il est **retourné** : il vérifie que le verdict compte les
entrées, et qu'il ne porte **ni** le nom de la table **ni** l'en-tête du listing.

**Constaté sur la machine de recette** : `jobs` reçoit ses lignes avec leur durée (903 ms, 386 ms),
`backups.job_id` les désigne, `backups.manifest` garde 1 031 octets, la ligne de journal de la
vérification dit `"detail":"16 table-of-contents entries"`, et `koffr list` montre `TOOK` et
`DUMPED`.

Rempli par `/executer-plan` : échecs, décisions `N-n` ajoutées en route, écarts au plan, datés.
