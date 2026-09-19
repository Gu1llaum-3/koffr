# Plan lot 1 — Diagnostic d'un parc réel

> Statut : **terminé le 2026-09-19**. Six vagues mergées, `verify` et CI verts sur `main`.
> Reste la recette (`docs/recette/lot-1-scenario.md`), puis `/cloturer-lot`. Exécuté par `/executer-plan`. Les règles communes à tous les plans sont
> dans `METHODE.md` § « Exécution d'un plan » et ne sont pas répétées ici.

C'est le `L0` du cahier des charges : aucune sauvegarde, et c'est voulu. Le lot valide la thèse
technique du produit — savoir, sur un parc réel, quel outil sauvegardera quelle base, et le prouver
en l'exécutant.

## Périmètre

**Exigences couvertes — 20** :

| `E-nnn` | Ce qu'elle exige | Source |
| --- | --- | --- |
| `E-007` | Version prouvée en exécutant ; à défaut, échec actionnable, jamais d'archive douteuse | § 2 `P3` |
| `E-011` | PostgreSQL 12 à 18, MySQL 8.x, MariaDB 10.6+ | § 3, ADR-0004 |
| `E-013` | Trois sources d'outils : hôte, installation gérée, `docker exec` | § 3 |
| `E-034` | `config validate` vérifie **aussi** la joignabilité des bases et l'existence des outils | § 5.1 `F1.3` |
| `E-038` | Énumérer **toutes** les sources sans préférence initiale | § 5.2 `F2.1` |
| `E-039` | Version obtenue **en exécutant** le candidat, mise en cache, invalidée au `mtime` | § 5.2 `F2.2` |
| `E-040` | Filtrer par compatibilité, retenir **la plus proche par le haut** ; la provenance ne départage que les ex æquo | § 5.2 `F2.3` |
| `E-041` | Ne jamais croiser MySQL et MariaDB ; famille détectée **à la connexion** | § 5.2 `F2.4` |
| `E-042` | Sans candidat : message nommant la version attendue, les versions trouvées, **la commande exacte** | § 5.2 `F2.5` |
| `E-043` | `koffr tools install` installe dans `/var/lib/koffr/tools/` | § 5.2 `F2.6` |
| `E-044` | Empreinte SHA-256 épinglée **et signature** vérifiées avant première exécution | § 5.2 `F2.7` |
| `E-045` | Installation automatique **désactivée par défaut**, bornée par liste blanche, tracée | § 5.2 `F2.8` |
| `E-046` | La stratégie `exec` exige le socket Docker et se déclare **par base** | § 5.2 `F2.9` |
| `E-047` | PostgreSQL dump : `major(pg_dump) ≥ major(serveur)` | § 5.2 matrice |
| `E-048` | PostgreSQL restauration : `major(pg_restore) ≥ major(archive)` | § 5.2 matrice |
| `E-049` | PostgreSQL cible : `major(cible) ≥ major(origine)` **conseillé**, signalé, jamais bloquant | § 5.2 matrice |
| `E-050` | MySQL/MariaDB : famille identique et `version(client) ≥ version(serveur)` | § 5.2 matrice |
| `E-103a` | Surface CLI : `doctor [--database]`, `tools list` / `install` / `remove` | § 5.12 |
| `E-104a` | `doctor` : joignabilité, version du serveur, outil retenu avec version et provenance | § 5.12 |
| `E-131` | Chaîne d'intégration dédiée qui construit et publie les binaires d'outils | § 11 |

**Écarté de ce lot, et pourquoi** :

- **La détection MyISAM** que le glossaire rattache à la sonde. Aucune exigence du lot 1 ne la
  demande ; elle sert à la politique de tampon (`E-053`…`E-056`), au **lot 2**. La sonde est
  écrite pour qu'on l'y ajoute sans la rouvrir.
- **Les quatre champs restants de `doctor`** (mode de tampon, destinations accessibles, prochaine
  exécution, état de la dernière sauvegarde) : `E-104b`, au **lot 6**. Ils supposent des lots qu'on
  n'a pas encore.
- **`E-049` ne s'applique qu'à la restauration** : la règle est écrite et testée ici, son usage est
  au lot 4.

**Critère de sortie**, repris de `ROADMAP.md` et rendu vérifiable :

1. Sur un parc réel d'au moins trois bases hétérogènes, `koffr doctor` nomme pour **chacune** sa
   joignabilité, la version de son serveur et l'outil retenu avec sa version et sa provenance,
   sans se tromper ;
2. le **piège du § 5.2** est évité, et c'est un test qui le dit : hôte en 14, base en 16, outil
   géré en 16 → **c'est le 16 qui est choisi** ;
3. ~~**scénario 2 du § 8**~~ — **amendé le 2026-09-19 (ADR-0014)** : l'échec nomme la version
   manquante et les versions trouvées ; il ne donne **pas** de commande de correction, koffr
   n'installant aucun outil ;
4. ~~**scénario 3 du § 8**~~ — **sans objet (ADR-0014)** : `koffr tools install` n'existe pas ;
5. `koffr config validate` signale une base injoignable et un outil manquant, **sans rien exécuter
   d'autre** ;
6. jamais `mysqldump` d'Oracle pour une MariaDB, ni l'inverse — y compris quand c'est le seul outil
   présent.

**Les critères 3 et 4 ont été amendés par ADR-0014** le 2026-09-19 : `D-06` tranchée, installation gérée hors MVP.

## État de départ (vérifié le 2026-09-18)

Constaté dans le code, pas supposé.

- **Lot 0 clos** : `main` vert en local et en CI, 20 paquets, 36 commits, 8 règles `CFG-nn`.
- **`internal/domain/resolve` et `internal/engine` ne contiennent qu'un `doc.go`.** Aucune ligne de
  code de résolution ni de sonde n'existe.
- **Aucun pilote de base n'est dans `go.mod`** : ni `jackc/pgx/v5`, ni `go-sql-driver/mysql`, ni le
  client Docker. `testcontainers-go` non plus.
- **La CLI expose `version` et `config` seulement.** `doctor` et `tools` n'existent pas.
- **`config validate` ne valide que la forme** et résout les secrets (`CFG-09`) ; il n'ouvre aucune
  connexion.
- **Le schéma de configuration porte déjà ce dont le lot a besoin** : `tools: auto` ou
  `{strategy, container}` par base (`CFG-05`), et la famille n'y est **pas** déclarée — conforme à
  `E-041`, qui veut qu'elle soit détectée à la connexion.
- **Les sept tables de `E-028` existent** ; aucune ne stocke de cache d'outils.
- **Le spike `E-130` a conclu positivement** : un outil peut être livré avec ses bibliothèques et
  exécuté sur Debian, Rocky **et Alpine**. Deux contraintes en découlent : `DT_RUNPATH` ne s'hérite
  pas, et `PT_INTERP` est **absolu** — un bundle ne se déplace pas, il se re-patche.
- **L'instance de recette n'a pas Docker** et dispose de 2 Go de mémoire. Insuffisant pour la
  matrice d'ADR-0004.
- **Aucune donnée** : rien à mesurer, aucun comptage à reporter.

## Ce que le cahier des charges dit, et ce qu'il ne dit pas

- **Dit** : § 5.2 en entier — les neuf `F2.n`, la matrice de compatibilité et le « piège à
  éviter » ; § 5.12 la surface CLI et le rôle central de `doctor` ; § 3 le périmètre des moteurs.
- **Ne dit pas** : où vit le cache de versions → `N-4` ; le format de sortie de `doctor` → `N-5` ;
  ce que `koffr tools list` montre quand rien n'est installé → `N-6`. Aucune de ces trois n'appelle
  une `Q-nn` : ce sont des choix d'implémentation, annoncés à la recette.
- **Contredit** : rien dans ce lot.
- **Bloque** : `D-06` (qui construit, signe et héberge les binaires d'outils) et `Q-15` (quelle
  signature, quelle clé). La **vague 6** ne démarre pas sans elles. `Q-20` (les outils gérés
  hors Linux) est réduite à macOS par ADR-0011 et se traite par un message d'erreur, pas par du
  code : c'est `N-7`.

## Décisions d'implémentation

- **N-1 — `internal/engine` peut importer `database/sql` pour sonder.** Figée par **ADR-0013**,
  accepté le 2026-09-18. Rappel :
  *Constat* : `AR-08` d'ADR-0010 réserve `database/sql` à `internal/state`. Or ADR-0002 prévoit
  explicitement `jackc/pgx/v5` **et** `go-sql-driver/mysql` « pour les sondes et la détection de
  famille, jamais pour le dump » — et `go-sql-driver/mysql` n'a pas d'API utilisable hors
  `database/sql`. *Ce que l'interdit visait* : empêcher qu'on lise un jour les tables d'une base
  **sauvegardée** par un pilote au lieu de la dumper en sous-process, et qu'on touche l'état de
  koffr ailleurs que dans `state`. *Proposition* : un ADR qui amende `AR-08` en
  « `database/sql` est réservé à `internal/state`, **sauf `internal/engine` pour les sondes** ;
  `modernc.org/sqlite` reste réservé à `state` sans exception ». *Exclut* : lire une base
  sauvegardée par un pilote, ce qui reste interdit et le restera par revue, faute de règle de lint
  capable d'exprimer « pour les sondes seulement ».
- **N-9 (2026-09-18) — les tests contre conteneurs sont sautés bruyamment, et exigés en CI.**
  *Raison* : une machine sans Docker ne peut pas les jouer, et les taire rendrait `verify` vert
  sans rien prouver. `KOFFR_REQUIRE_DOCKER=1` en CI fait échouer leur absence. *Exclut* : un
  simulacre de serveur, et un `verify` qui mentirait par omission.
- **N-2 Les sondes vivent dans `internal/engine`, le domaine ne les connaît que par un port.**
  `internal/domain/resolve` déclare `ServerProbe` et `ToolFinder` ; `engine` les implémente ;
  `cmd/koffr` câble. *Raison* : `AR-01` et `AR-02` l'imposent, et c'est ce qui rend la matrice de
  compatibilité testable **sans base**. *Exclut* : un domaine qui ouvre une connexion.
- **N-3 Les tests de sonde tournent contre des conteneurs éphémères, jamais contre un simulacre.**
  `testcontainers-go` en dépendance **de test** (ADR-0002, `E-123`). Par vague, la version la plus
  récente de chaque famille ; la matrice complète d'ADR-0004 sur `main`. *Raison* : une sonde qui
  ment sur une version est exactement le défaut que `P3` combat. *Exclut* : un simulacre de pilote.
- **N-4 Le cache de versions d'outils vit en mémoire, pour la durée du processus.** Clé : chemin du
  binaire ; invalidé si le `mtime` change. *Raison* : `E-039` demande un cache et une invalidation,
  pas une persistance ; `E-028` fige sept tables et `N-9` du lot 0 interdit d'en inventer une
  huitième sans exigence. *Exclut* : une table de plus, et un cache qui survivrait à un `koffr
  tools install`.
- **N-5 `doctor` sort un tableau texte, une ligne par base, et rien d'autre.** Pas de `--json` :
  `E-103a` n'en demande pas, et l'inventer maintenant fige un format avant qu'on sache ce que le
  lot 6 y ajoutera. *Exclut* : un format machine non demandé.
- **N-6 `koffr tools list` montre **tous les candidats énumérés**, pas seulement les outils gérés.**
  Chaque ligne : moteur, version, chemin, provenance, et si le candidat est exécutable. *Raison* :
  c'est la commande qui rend `E-038` observable, et elle a de la valeur **avant** que l'installation
  gérée existe. *Exclut* : une commande qui ne listerait que `/var/lib/koffr/tools/`.
- **N-7 Hors Linux, `koffr tools install` échoue avec un message qui nomme les deux issues** — les
  outils de l'hôte, ou la stratégie `exec`. *Raison* : ADR-0011 et `Q-20` ; le spike ne vaut que
  pour ELF. *Exclut* : une installation gérée sur macOS.
- **N-8 La famille est détectée par la bannière de version du serveur, à la connexion.** Jamais par
  la configuration, jamais par le port, jamais par le nom du binaire trouvé. *Raison* : `E-041`
  textuellement. *Exclut* : une clé `family` dans `koffr.yaml`.

## Vagues

### Vague 1 — Sondes des moteurs (`lot1/wave-1-engine-probes`)

Exigences : `E-011`, `E-041`, et la moitié de `E-104a`. L'inconnue d'abord : si les conteneurs de
test ne tiennent pas, tout le lot change de forme.

- [x] **1.1** `internal/arch` : `AR-08` amendée d'après **ADR-0013** — `database/sql` ouvert à
      `internal/engine`, `AR-08b` gardant `modernc.org/sqlite` à `state` seul. **Une fixture
      violante pour chacune** : une règle assouplie sans fixture est une règle qu'on ne vérifie
      plus. `depguard` suit.

      > Fait le 2026-09-18. **Pas de rouge par antériorité** : la fixture existante déclenchait
      > déjà les deux règles. Prouvé autrement, comme la rétro du lot 0 le demande — en retirant
      > l'exception de `engine`, `TestTheCheckAcceptsWhatTheRulesAllow` échoue ; en retirant la
      > fixture d'`AR-08b`, `TestTheCheckCatchesAViolationOfEveryRule` échoue. `depguard` refuse
      > `database/sql` dans `pipeline` et l'accepte dans `engine`.
- [x] **1.2** Test d'abord `internal/engine/probe_test.go` — contre un conteneur **réel**
      PostgreSQL 18 : joignabilité, version majeure et mineure lues du serveur. Cas d'erreur :
      hôte injoignable, mauvais identifiants, base absente — trois erreurs **typées et
      distinctes**. Puis le code (`jackc/pgx/v5`).

      > Fait le 2026-09-18, contre un **vrai** PostgreSQL 18. La version est lue des paramètres
      > que le serveur annonce à la connexion : **aucune requête** n'est émise, ce qui va au-delà
      > de ce que `1.4` demande. Les trois erreurs sont distinguées par les codes `28P01`, `28000`
      > et `3D000`.
      > **`N-9` ajoutée** : sans Docker, les tests de sonde sont **sautés bruyamment** et la CI
      > pose `KOFFR_REQUIRE_DOCKER=1`, qui transforme l'absence en échec — même marché que `A-01`
      > pour `-race`. Sans cela, `verify` serait vert en ne prouvant rien.
      > **Détour** : la première version lisait `KOFFR_REQUIRE_DOCKER` dans le test Go, et
      > `AR-05` l'a refusé — seul `internal/config` lit l'environnement. La règle avait raison :
      > la décision est passée dans `scripts/run-tests.sh`, là où celle de `-race` vit déjà.
- [x] **1.3** Test — contre MariaDB 11.4 **et** MySQL 8.4 : la **famille** est lue de la bannière
      du serveur (`N-8`), jamais déduite. Un MariaDB et un MySQL sur le même port se distinguent.
      Puis le code.
- [x] **1.4** Test — la sonde n'exécute **rien d'autre** que sa requête de version : pas de
      `SHOW TABLES`, pas de dump. Vérifié en lisant les requêtes émises.
- [x] **1.5** `internal/domain/resolve/ports.go` : le port `ServerProbe` déclaré **par le domaine**,
      implémenté par `engine` (`N-2`). Un faux de test l'implémente, pour que la suite se teste sans
      base.
- [x] **1.6** Vague verte : `verify`, commit `feat(engine): probe a server for its version and family`.

### Vague 2 — Énumération des candidats (`lot1/wave-2-tool-discovery`)

Exigences : `E-013`, `E-038`, `E-039`, et `tools list` de `E-103a`.

- [x] **2.1** Test d'abord `internal/engine/discover_test.go` — l'énumération visite **toutes** les
      sources : chemins système connus par distribution, `PATH`, `/var/lib/koffr/tools/`, sortie de
      `pg_lsclusters` si présente. **Aucune préférence** à ce stade : l'ordre de sortie ne porte pas
      de sens (`E-038`).
- [x] **2.2** Test — la version d'un candidat est obtenue **en l'exécutant** (`--version`), jamais
      par son chemin : un binaire nommé `pg_dump-16` qui répond `15.4` est en **15.4** (`E-039`).
      Cas d'erreur : binaire non exécutable, sortie illisible, délai dépassé.
- [x] **2.3** Test — le cache rend la seconde interrogation **sans exécution**, et un `mtime` qui
      change la **réinvalide** (`E-039`, `N-4`).
- [x] **2.4** Test `internal/cli/tools_test.go` — `koffr tools list` montre tous les candidats avec
      leur provenance (`N-6`) ; sortie stable et triée.
- [x] **2.5** Règles `RSV-01` (énumération exhaustive), `RSV-02` (version prouvée par exécution),
      `RSV-03` (cache et invalidation) dans `internal/domain/resolve/rules.md`.
- [x] **2.6** Vague verte : `verify`, commit `feat(resolve): enumerate every tool source and prove each version`.

### Vague 3 — Matrice de compatibilité et choix (`lot1/wave-3-compatibility-matrix`)

Exigences : `E-007`, `E-040`, `E-042`, `E-047`, `E-048`, `E-049`, `E-050`. Le cœur du lot, et il se
teste **entièrement sans base** grâce au port de la vague 1.

- [x] **3.1** Test d'abord `internal/domain/resolve/matrix_test.go` — les quatre règles de la
      matrice, chacune avec son cas nominal, sa limite et son cas d'erreur : `E-047`, `E-048`,
      `E-050`, et `E-049` qui **signale sans bloquer**.
- [x] **3.2** Test — **le piège du § 5.2** : hôte en 14, base en 16, outil géré en 16 → **le 16 est
      choisi**. La provenance ne départage que des candidats **également compatibles**, dans l'ordre
      hôte, géré, conteneur (`E-040`).
- [x] **3.3** Test — « la version la plus proche par le haut » : avec 16, 17 et 18 disponibles pour
      un serveur en 16, c'est **16** qui est retenu, pas 18.
- [x] **3.4** Test — **jamais de famille croisée** (`E-041`) : un `mysqldump` d'Oracle face à une
      MariaDB est écarté **même s'il est le seul candidat**, et l'échec le dit.
- [x] **3.5** Test — sans candidat compatible, l'erreur nomme **la version attendue, les versions
      trouvées et la commande exacte** (`E-042`, `E-007`). Le texte fait partie de la règle : le test
      l'épelle.
- [x] **3.6** Règles `RSV-04` à `RSV-09` dans `rules.md`, avec leur source.
- [x] **3.7** Vague verte : `verify`, commit `feat(resolve): pick the closest compatible tool, never a crossed family`.

### Vague 4 — `doctor` et `config validate` (`lot1/wave-4-doctor`)

Exigences : `E-034`, `E-104a`, et `doctor` de `E-103a`. La commande la plus importante du § 5.12.

- [x] **4.1** Test d'abord `internal/cli/doctor_test.go` — pour chaque base : joignabilité, version
      du serveur, outil retenu avec **version et provenance** (`E-104a`). Une base injoignable est
      une **ligne de plus**, pas un arrêt : `doctor` diagnostique un parc, il ne s'arrête pas au
      premier problème.
- [x] **4.2** Test — `--database ID` restreint à une base ; un identifiant inconnu est une erreur qui
      **nomme les identifiants connus**.
- [x] **4.3** Test — `koffr config validate` vérifie **en plus** la joignabilité et l'existence des
      outils (`E-034`), et **n'exécute rien d'autre**. `--offline` conserve la vérification de forme
      seule (`CFG-09`).
- [x] **4.4** Test — le code de retour distingue « tout va bien » de « au moins une base en
      défaut ».
- [x] **4.5** `internal/config/rules.md` : `CFG-09` **amendée** — `validate` atteint désormais les
      bases et les outils.
- [x] **4.6** Vague verte : `verify`, commit `feat(cli): doctor reports each database and the tool that will dump it`.

### Vague 5 — Stratégie `exec` (`lot1/wave-5-exec-strategy`)

Exigences : `E-046`, et le troisième tiers de `E-013`.

- [x] **5.1** Test d'abord `internal/engine/exec_test.go` — la stratégie `exec` énumère l'outil
      **dans le conteneur de la base**, et sa version est obtenue en l'exécutant **là**.
- [x] **5.2** Test — sans socket Docket accessible, l'échec **nomme** le socket attendu ; la
      stratégie n'est jamais activée globalement, seulement par base (`E-046`).
- [x] **5.3** Test — une base déclarant `strategy: exec` sans `container` est refusée **à la
      validation de configuration**, pas au premier job.
- [x] **5.4** Règle `RSV-10` dans `rules.md`.
- [x] **5.5** Vague verte : `verify`, commit `feat(engine): resolve a tool inside the database container`.

### Vague 6 — Les outils sont un prérequis (`lot1/wave-6-host-tools-only`) — ADR-0014

> **Amendée le 2026-09-19.** `D-06` a été tranchée : l'installation gérée **sort du MVP**
> (ADR-0014), `Q-15` devient sans objet, et `E-043`, `E-044`, `E-045` et `E-131` sont reportées.
> La vague qui devait les livrer devient celle qui **assume leur absence** proprement.

- [x] **6.1** **ADR-0014** écrit et accepté : l'installation gérée sort du MVP ; les sources sont
      l'hôte et le conteneur. `D-06` barrée, `Q-15` sans objet, `E-043`, `E-044`, `E-045` et
      `E-131` reportées au registre et au backlog (`B-08`).
- [x] **6.2** Test d'abord — le message d'échec de `RSV-09` **ne promet plus** `koffr tools install`
      et nomme la famille, la version attendue, les versions trouvées et leur provenance.
- [x] **6.3** Test d'abord — `koffr tools install` et `koffr tools remove` **n'existent pas**, et
      l'échec explique pourquoi plutôt que d'afficher l'aide en réussissant.
- [x] **6.4** `README.md` : les clients de dump et de restauration deviennent un **prérequis**
      documenté, avec les paquets des trois familles de distributions et la sortie `exec` pour une
      base en conteneur.
- [x] **6.5** `E-042` passée à **partiellement couverte** au registre, avec la raison ; `E-103a`
      amendée ; `E-013` couverte pour deux sources sur trois.
- [x] **6.6** Vague verte : `verify`, commit `feat(resolve): tools are a prerequisite, not something koffr installs`.

## Vérification de bout en bout

Sur l'instance de recette, augmentée pour l'occasion :

1. Trois bases hétérogènes réelles — PostgreSQL 16, MariaDB 11.4, MySQL 8.4 — et `koffr doctor` les
   décrit toutes les trois sans se tromper ;
2. le piège : hôte en 14, base en 16, outil géré en 16 → `doctor` annonce le **16**, provenance
   `managed` ;
3. `koffr tools list` montre les candidats de l'hôte avec leurs versions **réelles** ;
4. une MariaDB avec seulement `mysqldump` d'Oracle présent → échec nommant la famille attendue ;
5. `koffr config validate` sur une base éteinte → signale l'injoignabilité, code de retour non nul ;
6. scénarios **2 et 3** du § 8, si la vague 6 a pu être exécutée.

## Recette

- Scénario : `docs/recette/lot-1-scenario.md`, écrit avec ce plan.
- **Décisions attendues** : `D-06` et `Q-15` si elles ne sont pas tranchées avant ; la
  réouverture de `D-01` (l'instance de recette doit porter Docker et neuf bases — 2 Go n'y
  suffiront pas).
- **Ce qui est voulu et pourrait passer pour un bug** :
  - `doctor` n'affiche que **trois** des sept champs du § 5.12 : les quatre autres sont `E-104b`,
    au lot 6 ;
  - aucune sauvegarde n'est possible — c'est le lot 2 ;
  - `koffr tools install` échoue sur macOS, et c'est écrit (`N-7`) ;
  - `doctor` n'a pas de `--json` (`N-5`) ;
  - le cache de versions ne survit pas au processus (`N-4`).

## Risques et points à vérifier en route

| Risque | Ce qu'on fait s'il se réalise |
| --- | --- |
| `engine` peut désormais ouvrir une connexion SQL : la garantie « le dump est un sous-process » n'est plus purement mécanique (ADR-0013) | Ce qui la tient est la tâche `1.4` — un test qui épelle les requêtes émises — et la petite taille du paquet. Toute requête ajoutée à `engine` se justifie en revue |
| `testcontainers-go` ne démarre pas sur l'instance de recette (2 Go, pas de Docker) | L'instance est augmentée **avant** la vague 1, ou les tests d'intégration ne tournent qu'en CI et on le dit |
| La matrice d'ADR-0004 (onze instances) rend la CI trop lente | Prévu par l'ADR : sous-ensemble par vague, matrice complète sur `main`. À mesurer dès la vague 1 |
| `pg_lsclusters` n'existe que sur Debian et dérivés | Son absence n'est pas une erreur : c'est une source qui ne rend rien. À tester explicitement |
| La détection de famille se révèle ambiguë sur une MariaDB récente qui se présente comme MySQL | C'est le cœur d'`E-041` : si la bannière ne suffit pas, on mesure sur les conteneurs réels et on écrit une `N-n` avec la règle retenue |
| `D-06` reste ouverte jusqu'à la fin du lot | Le lot se clôt **sans** la vague 6, avec `E-043`, `E-044`, `E-045` et `E-131` reportées par une décision écrite, et les scénarios 2 et 3 non joués |
| Le binaire grossit avec `pgx`, le pilote MySQL et le client Docker | Marge actuelle : 25,7 Mio. Mesurer à chaque vague ; `mise run release` bloque à 30 Mo |

## Journal d'exécution

Rempli par `/executer-plan` : échecs, décisions `N-n` ajoutées en route, écarts au plan, datés.

### 2026-09-19 — vague 6, les outils deviennent un prérequis

- **`D-06` tranchée par le propriétaire** : l'installation gérée sort du MVP. Raisonnement retenu —
  une machine qui héberge un serveur de base héberge presque toujours son client, et les deux
  autres sources de `E-013` sont déjà livrées. **ADR-0014** l'écrit ; `Q-15` devient sans objet.
- **Ce que j'ai proposé et qui a été écarté** : nommer les paquets par distribution dans le message
  d'échec, pour tenir `E-042` à la lettre. Le propriétaire l'a refusé — c'est une correspondance de
  plus à maintenir, exactement la charge que la décision retire. Le prérequis part dans le
  `README`, et `E-042` est inscrite **partiellement couverte**, avec sa raison.
- Message final : `no compatible tool to dump postgresql 17.2: expected a postgresql client of`
  `version 17 or later, found postgresql 15.4 (host)`. Il ne promet rien qu'il ne tienne.
- **`koffr tools install` n'existe pas**, et l'échec le dit — une commande cobra sans `RunE` aurait
  affiché son aide **en réussissant**, ce qui ressemble à un succès.
- `README.md` porte désormais les paquets clients des trois familles de distributions, la
  coexistence de plusieurs versions PostgreSQL, et la sortie `exec` pour une base en conteneur.
- `mise run verify` : **0**.

### 2026-09-19 — vague 5, stratégie `exec`

- `RSV-10` écrite avant le code. `internal/config` refuse désormais `strategy: exec` **sans
  `container`**, et une stratégie inconnue — `kubectl` par exemple — plutôt que de la traiter en
  silence comme `auto`. L'échec arrive **à la validation**, pas au premier job.
- `internal/engine/container.go` : l'outil est cherché **dans** le conteneur et sa version lue en
  l'exécutant **là**. Vérifié contre un vrai conteneur MariaDB 11.4, provenance `container`.
- **Sans socket Docker, l'échec nomme le socket** et le conteneur. Aucun repli sur un outil de
  l'hôte : l'exploitant a demandé celui du conteneur pour une raison (`E-046`).
- **Lecture d'ADR-0002 à noter** : l'ADR nomme `docker/docker/client`, qui est le chemin de module
  historique. C'est bien celui-là qui est utilisé (`v28.5.2+incompatible`) ; il tire
  `github.com/pkg/errors`, archivé mais exigé par le SDK.
- **Mesure de `E-117`, et elle compte** : le client Docker coûte **+5,7 Mio**. Les trois cibles
  passent de 4,3 à **10,0 / 9,3 / 9,5 Mio**. Marge restante : **20 Mio**, avant que `internal/state`
  soit lié (+3,5 Mio mesurés au lot 0) et avant le SDK S3 du lot 4. Le seuil de 30 Mo cesse d'être
  théorique.
- **Écart signalé, non codé** : `ContainerFinder` n'est **câblé dans aucune commande**. Les tâches
  de cette vague ne le demandent pas, et le faire changerait le comportement de `doctor`, qui
  appartient à la vague 4. Conséquence à annoncer à la recette : la provenance `container`
  n'apparaît pas encore dans `doctor`. À câbler au lot 2, quand la sauvegarde l'utilisera.
- `mise run verify` : **0**.

### 2026-09-19 — vague 4, `doctor` et `config validate`

- `resolve.Diagnose` écrit dans le **domaine** : il ne connaît ni pilote ni sous-process, il reçoit
  la sonde et l'énumérateur par leurs ports. `internal/cli/doctor.go` ne fait que rendre.
- **Une base qui ne répond pas est une ligne, pas un arrêt.** Sans version de serveur, koffr ne
  pose pas la question de compatibilité : il dit ce qu'il n'a pas pu faire et ne devine pas le
  reste.
- **`E-034` est levée** : `config validate` atteint le parc et les outils. `CFG-09` amendée, et la
  ligne « non porté » du lot 0 **barrée** avec sa date.
- `validate` rend **le premier** problème — il répond « cette configuration est-elle utilisable ? » ;
  `doctor` marche tout le parc. Deux commandes, deux questions.
- **Deux tests plus anciens sont tombés, et ils avaient raison** : leurs fixtures déclaraient des
  bases que rien n'écoute. Celui sur le journal passe `--offline` ; celui sur les secrets affirme
  désormais que l'échec parle de **la base** et jamais du fichier de secret — ce qui teste mieux la
  règle qu'avant.
- **Vérifié contre un vrai PostgreSQL 16** :

  ```
  DATABASE     REACHABLE    SERVER            TOOL                  VERSION  SOURCE
  boutique     yes          postgresql 16.15  …/pg_dump             18.4     host
  erp-eteinte  unreachable  -                 -                     -        -

  erp-eteinte: the server did not answer: dial tcp 127.0.0.1:53306: connect: connection refused
  ```
  Code de retour **1**, comme `4.4` le demande.
- **La CI a échoué après le merge, et j'avais mergé quand même.** Deux fautes distinctes :
  1. **Le code.** `Diagnose` itérait une **map**, donc l'ordre des diagnostics était aléatoire :
     `config validate` rendait un premier échec différent à chaque appel. Corrigé — `Diagnose`
     prend une **liste** `[]Subject` et répond **dans l'ordre de la configuration**, celui que
     l'exploitant a écrit. Vérifié 20 fois de suite.
  2. **Mon geste.** Mon enchaînement de commandes plaçait `echo "CI = $?"` **avant** le `&&` du
     merge : `echo` réussit toujours, donc le merge partait quel que soit le résultat de la CI.
     C'est la deuxième fois dans ce projet ; le garde-fou est d'enchaîner le merge **directement**
     sur `gh run watch --exit-status`, sans rien entre les deux.
- `mise run verify` : **0**.

### 2026-09-19 — vague 3, matrice de compatibilité et choix

- `RSV-04` à `RSV-09` écrites **avant** le code. Le cœur du lot tient en 150 lignes de domaine et
  se teste **sans une seule base** — c'est ce que le port de la vague 1 achetait.
- **Le piège du § 5.2 est un test** : hôte en 14, base en 16, géré en 16 → le **16** est choisi.
  À versions égales, et seulement là, la provenance départage : hôte, géré, conteneur.
- **« La plus proche par le haut »** vérifié : avec 16, 17 et 18 pour un serveur en 16, c'est le
  **16** qui sort, pas le 18.
- **Les familles ne se croisent dans aucun sens** : `mysqldump` d'Oracle refusé pour MariaDB,
  `mariadb-dump` refusé pour MySQL, **même seul candidat**. Côté MySQL la comparaison porte sur la
  version complète, pas la majeure : un client 10.6 ne parle pas pour un serveur 10.11.
- **`E-049` signale sans bloquer** : restaurer une archive 17 vers une cible 16 produit un
  avertissement qui nomme les deux versions, et rien de plus.
- Message d'`E-042` obtenu, tel qu'un exploitant le lira :
  `no compatible tool to dump postgresql 17.2: expected a postgresql tool of version 17 or later,`
  `found postgresql 15.4 (host), postgresql 14.11 (host). Install one with: koffr tools install postgresql 17`
- **Une écriture du journal a échoué en silence** au premier essai (ancre introuvable) : le plan
  n'avait **ni cases cochées ni journal**, alors que le commit était fait. Vérifier le fichier
  après l'avoir modifié, pas seulement le code de retour du commit.
- `mise run verify` : **0**.

### 2026-09-18 — vague 1, sondes des moteurs

- **`AR-08` amendée** d'après ADR-0013, **une fixture par règle** plus une fixture `allowed.go` qui
  prouve l'exception. `depguard` suit. Les deux tests ont été prouvés par suppression, faute de
  rouge par antériorité.
- **Sonde PostgreSQL** : la version vient des **paramètres annoncés à la connexion**, donc
  **aucune requête** n'est émise — au-delà de ce que `1.4` demandait. Trois erreurs distinguées par
  les codes `28P01`, `28000`, `3D000`.
- **Sonde MySQL/MariaDB** : une requête, `SELECT VERSION()`, et la famille lue de la bannière. Un
  MariaDB **déclaré `mysql` dans la configuration** est rapporté MariaDB : `E-041` tient dans les
  deux sens, vérifié contre de vrais serveurs 11.4 et 8.4.
- **La garde d'ADR-0013 est écrite et mord** : `queries_test.go` lit les littéraux SQL du paquet et
  refuse tout ce qui n'est pas `SELECT VERSION()`. Remplacer la requête par `SHOW TABLES` fait
  échouer le test. C'est ce qui remplace le lint là où il ne sait pas exprimer « pour les sondes ».
- **`N-9` ajoutée**, et son détour : lire `KOFFR_REQUIRE_DOCKER` dans le test Go violait `AR-05`.
  La décision est passée dans `scripts/run-tests.sh`, là où celle de `-race` vit déjà.
- **Deux erreurs de ma part, corrigées** : `Version.IsZero` incluait `Raw`, ce qui rendait
  inutilisable une version illisible mais citable ; et l'analyse prenait **tous** les nombres de la
  chaîne, transformant `16.10 (Debian 16.10-1.pgdg13+1)` en 16.10.**16**. Les deux ont été trouvées
  par des tests écrits avant le code.
- **Piège de la pile** inscrit en § Conventions : `testcontainers-go` ne lit pas le contexte Docker
  et exige `DOCKER_HOST` **et** `TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE` sur Colima ou Podman.
- **Un commit a été fait avec `verify` rouge** (violation de `AR-05` non vue), puis amendé avant
  toute poussée. La règle reste : lire le code de retour **avant** de commiter.
- **La CI a rattrapé une régression que j'avais introduite et que je ne pouvais pas voir.** Après
  avoir prouvé que la garde de requêtes mordait, j'ai restauré `mysql.go` par `git checkout` — or
  le fichier n'avait **jamais été indexé** : la commande a remis le stub de 12 lignes à la place de
  l'implémentation. Deux choses ont masqué la casse en local : le test concerné était **sauté**
  dans le shell sans `DOCKER_HOST` (c'est le coût assumé de `N-9`), et **le cache de `go test`**
  répondait `(cached)` dans l'autre. `KOFFR_REQUIRE_DOCKER=1` en CI a fait exactement ce pour quoi
  il existe. Geste inscrit dans le skill `implementer` : pour retirer puis remettre, **copier le
  fichier**, jamais `git checkout`, et relancer avec `-count=1`.
- `mise run verify` : **0**. Suite complète avec conteneurs et `-race`, **sans cache** : **0**.

### 2026-09-19 — vague 2, énumération des candidats

- `RSV-01` à `RSV-03` écrites dans `internal/domain/resolve/rules.md` **avant** le code.
- Quatre sources visitées : chemins système avec globs par distribution, `PATH`, répertoire géré,
  et **`pg_lsclusters`** quand la machine l'a. Une source vide n'est pas une erreur.
- **La famille d'un *outil* est lue de son exécution**, comme celle d'un serveur : un binaire nommé
  `mysqldump` qui répond `10.6.21-MariaDB` appartient à MariaDB. Sans cela, `E-041` serait tenue
  côté serveur et trahie côté outil.
- **`N-10` implicite devenue explicite** : un candidat qu'on ne peut pas exécuter, qui répond
  n'importe quoi ou qui dépasse 5 s est **écarté**, jamais deviné.
- **Trois défauts de mon fait, trouvés par les tests** :
  1. mes tests trouvaient les vrais outils du poste via `PATH` — le code avait raison, le test
     était sous-spécifié. `LookPath` est désormais **injecté**, et `engine.NoPath` décrit une
     machine sans outil ;
  2. `exec.CommandContext` tue le shell mais pas le `sleep` qu'il a lancé : `Run` attendait 30 s
     au lieu de 500 ms. Corrigé par `WaitDelay` ;
  3. `Version.String()` rendait l'annonce entière, ce qui donnait une colonne de tableau illisible.
     Elle rend les nombres ; `Raw` garde l'annonce pour les messages.
- **`--search-path` veut dire « à la place de »** : quand l'exploitant nomme les répertoires, `PATH`
  n'est plus consulté. L'aide le disait déjà, le code ne le faisait pas.
- **Vérifié sur une machine réelle** : `koffr tools list` distingue MariaDB 12.0.2 de MySQL 9.0.1,
  tous deux installés côte à côte, et donne pour chacun sa version et son chemin.
- `mise run verify` : **0**.
