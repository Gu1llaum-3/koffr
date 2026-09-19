# Plan lot 2 — Sauvegarder une base vers un fichier chiffré

> Statut : **validé par le propriétaire le 2026-09-19**. Exécuté par `/executer-plan`. Les règles communes à tous les plans sont
> dans `METHODE.md` § « Exécution d'un plan » et ne sont pas répétées ici.

À la fin de ce lot, koffr sauvegarde. C'est la chaîne 01→05 du § 4.1 : résolution, dump,
compression, chiffrement, écriture. Le manifeste et la vérification sont au lot 3.

## Périmètre

**Exigences couvertes — 20** :

| `E-nnn` | Ce qu'elle exige | Source |
| --- | --- | --- |
| `E-012a` | La destination « système de fichiers local » | § 3 |
| `E-024` | Sept étapes dans cet ordre ; ce lot en livre cinq | § 4.1 |
| `E-025` | **Le dump brut n'est jamais matérialisé** : dump, zstd, `age` et empreinte en une passe | § 4.5 `F3.3` |
| `E-029` | Trois modes de tampon, sélectionnables **par base** | § 4.5 |
| `E-030` | `stage` **imposé** dès `-Fd`, dès plus d'une destination, ou si la vérification l'exige | § 4.5 |
| `E-051` | Un job actif par base, verrou local ; une demande concurrente est **refusée**, pas mise en file | § 5.3 `F3.1` |
| `E-053` | Le point de coupure suit la politique de tampon ; le mode appliqué est **enregistré** | § 5.3 `F3.4` |
| `E-054` | PostgreSQL : `-Fc` par défaut, `-Fd` au-delà d'un seuil, indisponible en `exec` — **signalé** | § 5.3 `F3.5` |
| `E-055` | En `stage`, la connexion se ferme **dès la fin du dump** | § 5.3 `F3.6` |
| `E-056` | MySQL/MariaDB : `--single-transaction`, routines, déclencheurs, événements ; MyISAM **détecté et signalé** | § 5.3 `F3.7` |
| `E-061` | Espace disque estimé et vérifié **avant** de commencer, avec marge | § 5.3 `F3.10` |
| `E-066` | Une **interface unique** de destination : écrire, lire, lister, supprimer, tester | § 5.5 `F5.1` |
| `E-070` | Chemin distant déterministe et lisible : `<base>/<AAAA>/<MM>/<base>_<horodatage>_<id>.<ext>` | § 5.5 `F5.5` |
| `E-072` | Chiffrement **en flux** avec `filippo.io/age`, destinataires X25519 | § 5.6 `F6.1` |
| `E-073` | Plusieurs destinataires **par base** | § 5.6 `F6.2` |
| `E-074` | La clé privée **n'est jamais** requise ni présente | § 5.6 `F6.3`, ADR-0007 |
| `E-075` | L'archive reste déchiffrable par l'outil `age` standard, **sans koffr** | § 5.6 `F6.4` |
| `E-076` | `koffr keygen` affiche la clé publique et **n'écrit jamais** la privée | § 5.6 `F6.5` |
| `E-103b` | La CLI expose `keygen` et `backup <db> [--dry-run]` | § 5.12 |
| `E-132` | Destinataires multiples **obligatoires**, séquestre documenté, avertissement si une seule clé | § 11 |

**Écarté de ce lot, et pourquoi** :

- **Le manifeste et la vérification** (`E-057` à `E-059`, `E-062` à `E-064`) → lot 3. `E-024` liste
  sept étapes ; ce lot en livre **cinq**, et le dit.
- **S3 et SFTP** (`E-067` à `E-069`) → lot 4. `E-066` demande **l'interface** ; seul `filesystem`
  l'implémente ici, et c'est ce qui prouve que l'interface tient.
- **Les objets globaux d'un cluster** (`pg_dumpall --globals-only`) : hors MVP, `B-01`, hypothèse
  de `Q-07`.
- **`E-052` (jobs parallèles plafonnés)** et **`E-060` (délai de grâce `SIGTERM`)** → lot 5, avec le
  planificateur qui les rend observables.

**Critère de sortie**, repris de `ROADMAP.md` et rendu vérifiable :

1. `koffr backup <db>` produit sur `/srv/backups` une archive chiffrée d'une base **PostgreSQL** et
   d'une base **MariaDB** ;
2. **scénario 6 du § 8** : l'archive est **illisible sans la clé privée**, et se déchiffre avec
   l'outil `age` standard, **sans koffr** ;
3. **scénario 11, première moitié** : sur une base volumineuse en mode `stage`, **aucun fichier de
   plus que la taille compressée** n'apparaît sur disque, et la connexion à la base **se ferme avant
   le début de l'envoi** ;
4. une seconde demande sur une base déjà en cours est **refusée**, pas mise en file ;
5. `koffr keygen` affiche une clé publique et **rien n'est écrit** sur la machine ;
6. une configuration à **un seul destinataire** produit un avertissement au démarrage.

## État de départ (vérifié le 2026-09-19)

Constaté dans le code, pas supposé.

- **Lot 1 clos** : `main` vert en local et en CI, 20 exigences traitées, 10 règles `RSV-nn`.
- **`internal/domain/backup`, `internal/domain/crypto`, `internal/pipeline` et `internal/store` ne
  contiennent qu'un `doc.go`.** Aucune ligne de sauvegarde n'existe.
- **`internal/engine` est fourni** : sondes des trois familles, énumération et exécution des
  candidats, stratégie `exec` dans le conteneur. Il sait **trouver** un outil ; il ne sait pas
  encore **le lancer pour dumper**.
- **Ni `filippo.io/age` ni `klauspost/compress/zstd` ne sont dans `go.mod`** en dépendance directe.
- **Les sept tables existent** : `backups` porte `sha256_raw`, `sha256_stored`, `size_bytes`,
  `stored_bytes` ; `backup_locations` porte `remote_path`, `status`, `size_bytes`. **Rien à migrer
  pour ce lot.**
- **La CLI expose `version`, `config`, `doctor`, `tools`.** Ni `backup` ni `keygen`.
- **`internal/state` n'est appelé par aucune commande** : ce lot sera le premier à écrire dans la
  base — c'est lui qui liera le pilote SQLite au binaire (**+3,5 Mio** mesurés au lot 0).
- **Binaire actuel** : 10,0 / 9,3 / 9,5 Mio. Marge avant le seuil de 30 Mo : **20 Mio**.

## Ce que le cahier des charges dit, et ce qu'il ne dit pas

- **Dit** : § 4.1 les sept étapes ; § 4.5 la politique de tampon **et son raisonnement** — quatre
  coûts nommés du mode `stream` ; § 5.3 les dix `F3.n` ; § 5.5 `F5.1` et `F5.5` ; § 5.6 le
  chiffrement asymétrique **et pourquoi**.
- **Ne dit pas**, et quatre questions ouvertes le couvrent, **avec leur hypothèse déjà écrite dans
  `docs/questions.md`** : `Q-01` (mode par défaut) → `N-1` ; `Q-04` (destinataires par base) →
  `N-2` ; `Q-07` (portée du dump) → `N-3` ; `Q-08` (seuil `-Fd`, marge disque, niveau zstd) →
  `N-4`. **Aucune ne bloque le lot** : les quatre hypothèses sont celles du registre, annoncées à
  la recette.
- **Contredit** : le § 4.5 donne `stage` comme « **Défaut** » et `F3.4` écrit « mode `auto` par
  défaut ». C'est `Q-01`, et `N-1` tranche provisoirement.

## Décisions d'implémentation

- **N-1 Le mode de tampon par défaut est `auto`** (hypothèse de `Q-01`). *Raison* : la configuration
  cible du § 5.1 écrit `staging: auto`, et c'est ce que `doctor` est censé expliquer. *Exclut* : un
  `stage` inconditionnel, qui échouerait sur une machine au disque juste. **Le mode effectivement
  appliqué est journalisé et enregistré**, pour que deux exécutions restent comparables.
- **N-2 Les destinataires sont globaux, et une liste par base les remplace** (hypothèse de `Q-04`).
  Le champ par base est **ajouté au schéma dès ce lot**, même vide, pour ne pas casser la
  configuration plus tard. *Exclut* : une fusion des deux listes, dont personne ne saurait dire ce
  qu'elle contient.
- **N-3 Une entrée de configuration = une base, `--no-owner --no-privileges` conservés**
  (hypothèse de `Q-07`). *Raison* : c'est ce que le manifeste du CDC montre. *Exclut* : les objets
  globaux du cluster, qui restent au backlog (`B-01`), et une entrée qui désignerait « toutes les
  bases ».
- **N-4 Valeurs par défaut** (hypothèse de `Q-08`) : seuil `-Fd` = **20 Go**, marge disque =
  **×1,5**, compression = **`zstd:3`**, réglable par base. *Exclut* : des valeurs inventées ailleurs
  que dans le registre des questions.
- **N-5 Le verrou de `E-051` est un fichier posé dans `/var/lib/koffr/`, pas une ligne en base.**
  *Raison* : il doit survivre à une base corrompue et être visible à l'œil ; un verrou qu'on ne peut
  pas constater sans SQL est un verrou qu'on ne débloque pas à 3 h du matin. *Exclut* : une colonne
  `locked_at`, et une file d'attente — `F3.1` dit **refusé**.
- **N-6 `pipeline` assemble, `store` écrit, `engine` dumpe, `crypto` connaît les destinataires.**
  Le domaine `backup` orchestre par des ports et ne voit ni `io.Writer` de fichier, ni conteneur.
  *Raison* : ADR-0010. *Exclut* : un paquet `pipeline` qui connaîtrait les destinations.
- **N-7 L'empreinte `sha256_raw` est celle du flux *avant* compression, `sha256_stored` celle de ce
  qui est écrit.** *Raison* : le manifeste du § 5.3 porte les deux, et seule la seconde se vérifie
  sans déchiffrer. *Exclut* : une seule empreinte, qui rendrait `E-062` impossible au lot 3.

## Vagues

### Vague 1 — Chiffrement et clés (`lot2/wave-1-encryption`)

Exigences : `E-072` à `E-076`, `E-132`, et `keygen` de `E-103b`. L'inconnue d'abord : si une archive
`age` produite par koffr ne se déchiffre pas avec l'outil standard, tout le lot change de forme.

- [ ] **1.1** Test d'abord `internal/domain/crypto/recipients_test.go` — **`CRY-01`** : une clé
      publique `age` valide est acceptée, une clé mal formée est **refusée en nommant la ligne** du
      fichier de destinataires ; un fichier vide est une erreur.
- [ ] **1.2** Test — **`CRY-02`** : au moins **deux** destinataires sont exigés ; une seule clé
      produit un **avertissement au démarrage** qui nomme le séquestre (`E-132`).
- [ ] **1.3** Test `internal/pipeline/encrypt_test.go` — **`CRY-03`** : le chiffrement est **en
      flux**, sans matérialiser l'entrée, et l'archive produite est **déchiffrable par la commande
      `age` réelle**, pas par notre propre code (`E-075`). Le test invoque le binaire `age` s'il est
      présent et **se saute bruyamment** sinon, comme les conteneurs.
- [ ] **1.4** Test — **`CRY-04`** : l'archive est **illisible** avec une autre clé privée, et
      déchiffrable par **chacun** des destinataires déclarés (`E-073`).
- [ ] **1.5** Test `internal/cli/keygen_test.go` — `koffr keygen` affiche la paire, **n'écrit
      aucun fichier**, et le dit (`E-076`, ADR-0007).
- [ ] **1.6** `internal/domain/crypto/rules.md` : `CRY-01` à `CRY-04`.
- [ ] **1.7** Vague verte : `verify`, commit `feat(crypto): age recipients, streaming encryption and keygen`.

### Vague 2 — La chaîne en flux (`lot2/wave-2-streaming-pipeline`)

Exigences : `E-025`, et la moitié de `E-024`.

- [ ] **2.1** Test d'abord `internal/pipeline/pipeline_test.go` — **le dump brut n'est jamais
      matérialisé** : un flux d'entrée de taille connue traverse zstd, `age` et l'empreinte **en une
      passe**, et la mémoire retenue reste bornée quelle que soit l'entrée.
- [ ] **2.2** Test — les **deux** empreintes sont calculées au vol : `sha256_raw` avant
      compression, `sha256_stored` sur ce qui sort (`N-7`).
- [ ] **2.3** Test — une erreur **au milieu** du flux — dump interrompu, disque plein — remonte
      **typée**, et rien d'incomplet n'est laissé derrière.
- [ ] **2.4** Test — la compression est `zstd:3` par défaut et réglable (`N-4`).
- [ ] **2.5** `rules.md` du pipeline : **`PIP-01`** (une seule passe), **`PIP-02`** (deux
      empreintes), **`PIP-03`** (erreur en cours de flux).
- [ ] **2.6** Vague verte : `verify`, commit `feat(pipeline): stream a dump through zstd, age and two checksums`.

### Vague 3 — Écrire quelque part (`lot2/wave-3-filesystem-store`)

Exigences : `E-012a`, `E-066`, `E-070`.

- [ ] **3.1** Test d'abord `internal/store/store_test.go` — **l'interface unique** de `E-066` :
      écrire en flux, lire en flux, lister, supprimer, tester l'accès. Le test est écrit **contre
      l'interface**, pas contre `filesystem`, pour que S3 et SFTP le rejouent au lot 4.
- [ ] **3.2** Test — **`STO-01`** : le chemin est déterministe et lisible —
      `<base>/<AAAA>/<MM>/<base>_<horodatage>_<id>.<ext>` — et se reconstruit sans la base locale
      (`E-070`).
- [ ] **3.3** Test — une écriture interrompue **ne laisse pas d'archive partielle** visible :
      fichier temporaire puis renommage atomique.
- [ ] **3.4** Test — tester l'accès dit **pourquoi** il échoue : répertoire absent, droits, disque
      plein.
- [ ] **3.5** `internal/store/rules.md` n'existe pas — `store` est un adaptateur (ADR-0010) :
      `STO-01` va dans `internal/domain/backup/rules.md`, avec sa raison.
- [ ] **3.6** Vague verte : `verify`, commit `feat(store): the single destination interface, and the filesystem one`.

### Vague 4 — Dumper pour de vrai (`lot2/wave-4-dump`)

Exigences : `E-054`, `E-055`, `E-056`, et la stratégie `exec` **appliquée au dump**.

- [ ] **4.1** Test d'abord `internal/engine/dump_test.go` — contre un **vrai** PostgreSQL : le dump
      sort sur `stdout`, en `-Fc`, avec `--no-owner --no-privileges` (`N-3`), et l'archive obtenue
      est relisible par `pg_restore --list`.
- [ ] **4.2** Test — contre une **vraie** MariaDB : `--single-transaction`, routines, déclencheurs
      et événements (`E-056`).
- [ ] **4.3** Test — **MyISAM détecté** et signalé : une table MyISAM plantée dans la base rend un
      avertissement que le job porte (`E-056`).
- [ ] **4.4** Test — le dump passe par la stratégie `exec` quand la base la déclare, **et `-Fd` y
      est refusé avec un message qui le dit** (`E-054`, ADR-0015).
- [ ] **4.5** Test — `E-055` : en `stage`, le sous-process est **terminé et la connexion fermée**
      avant que l'envoi ne commence. Vérifié en observant l'ordre, pas en le supposant.
- [ ] **4.6** Vague verte : `verify`, commit `feat(engine): dump a database to a stream, per family`.

### Vague 5 — Le cas d'usage de sauvegarde (`lot2/wave-5-backup-use-case`)

Exigences : `E-024`, `E-029`, `E-030`, `E-051`, `E-053`, `E-061`, et `backup` de `E-103b`.

- [ ] **5.1** Test d'abord `internal/domain/backup/lock_test.go` — **`BKP-01`** : un seul job par
      base ; une seconde demande est **refusée** en nommant le job en cours, jamais mise en file
      (`E-051`, `N-5`). Un verrou dont le processus est mort **ne bloque pas** éternellement.
- [ ] **5.2** Test `internal/domain/backup/staging_test.go` — **`BKP-02`** : les trois modes ;
      **`BKP-03`** : `stage` est **imposé** par `-Fd`, par plus d'une destination, ou par la
      vérification (`E-030`) ; **`BKP-04`** : `auto` choisit selon l'espace libre et **enregistre le
      mode appliqué** (`E-053`, `N-1`).
- [ ] **5.3** Test — **`BKP-05`** : l'espace disque est estimé **avant** de commencer, extrapolé de
      la dernière sauvegarde réussie ou à défaut de la taille de la base, avec la marge de `N-4` ;
      un job qui remplirait le disque **bascule en `stream` ou est refusé** (`E-061`).
- [ ] **5.4** Test — **`BKP-06`** : les sept étapes de `E-024` s'enchaînent **dans l'ordre**, et le
      job échoue si l'une manque. Les deux dernières — vérification et manifeste — sont **absentes
      et déclarées telles** jusqu'au lot 3.
- [ ] **5.5** Test `internal/cli/backup_test.go` — `koffr backup <db>` et `--dry-run`, qui **dit ce
      qu'il ferait** sans rien écrire.
- [ ] **5.6** `internal/domain/backup/rules.md` : `BKP-01` à `BKP-06`, plus `STO-01`.
- [ ] **5.7** Vague verte : `verify`, commit `feat(backup): back up a database to an encrypted archive`.

### Vague 6 — De bout en bout, sur un vrai parc (`lot2/wave-6-end-to-end`)

- [ ] **6.1** Test d'intégration : `koffr backup` sur un **vrai** PostgreSQL et une **vraie**
      MariaDB, archive écrite sur disque, **déchiffrée par l'outil `age` standard**, et le contenu
      relu par `pg_restore --list` (scénario 6 du § 8).
- [ ] **6.2** Test — **scénario 11, première moitié** : en `stage`, aucun fichier de plus que la
      taille compressée n'apparaît, et la connexion se ferme avant l'envoi.
- [ ] **6.3** Mesurer le binaire : `internal/state`, `age` et `zstd` y entrent. Noter l'écart.
- [ ] **6.4** `README` : la procédure de séquestre (`E-132`), et comment déchiffrer une archive
      **sans koffr**.
- [ ] **6.5** Vague verte : `verify`, commit `chore: back up a real fleet end to end`.

## Vérification de bout en bout

Sur l'instance de recette, avec son parc réel :

1. `koffr keygen` → une clé publique à l'écran, **aucun fichier** créé ;
2. `koffr backup boutique` → une archive sous `/srv/backups/boutique/2026/09/` ;
3. `age --decrypt -i cle.txt archive` → le dump, **sans koffr** ;
4. `pg_restore --list` sur le résultat → la liste des objets ;
5. deux `koffr backup boutique` simultanés → le second **refusé** ;
6. une configuration à une seule clé → **avertissement** au démarrage ;
7. `koffr backup erp --dry-run` → ce qui serait fait, et **rien** sur le disque.

## Recette

- Scénario : `docs/recette/lot-2-scenario.md`, écrit avec ce plan.
- **Décisions attendues** : les quatre hypothèses `N-1` à `N-4` sont **à confirmer ou à corriger**
  par le propriétaire — ce sont `Q-01`, `Q-04`, `Q-07` et `Q-08`, portées depuis le démarrage du
  projet. La recette est le moment de les trancher, sur du concret.
- **Ce qui est voulu et pourrait passer pour un bug** :
  - aucun **manifeste** n'est déposé à côté de l'archive — c'est le lot 3 ;
  - `koffr verify` n'existe pas — lot 3 ; `koffr list` non plus ;
  - seule la destination **`filesystem`** fonctionne — S3 et SFTP au lot 4 ;
  - les droits et les propriétaires **ne sont pas** sauvegardés (`N-3`, `Q-07`) ;
  - la clé privée n'est **nulle part** sur la machine : c'est le cœur d'ADR-0007.

## Risques et points à vérifier en route

| Risque | Ce qu'on fait s'il se réalise |
| --- | --- |
| L'archive `age` produite en flux ne se déchiffre pas avec l'outil standard | C'est l'inconnue de la **vague 1**, placée en premier exprès. Si elle se réalise, `E-075` est rouverte par un ADR avant d'écrire la suite |
| `E-025` — « jamais matérialisé » est facile à croire et difficile à prouver | La tâche `2.1` mesure la mémoire retenue, et la `6.2` regarde le disque pendant un vrai dump. Une promesse non mesurée ne compte pas |
| Le binaire dépasse le seuil : `state` (+3,5 Mio mesurés), `age`, `zstd` s'ajoutent aux 10 Mio actuels | Mesuré à chaque vague et en `6.3`. `mise run release` bloque à 30 Mo. Si la marge tombe sous 5 Mio, on le dit **avant** le lot 4 et son SDK S3 |
| Le verrou par fichier survit à un processus tué et bloque la base | Traité en `5.1` : le verrou porte le PID, et un verrou orphelin est repris. À tester explicitement |
| `-Fd` écrit une arborescence, pas un flux : la chaîne de la vague 2 ne s'y applique pas | `E-030` impose `stage` dans ce cas. La vague 5 le vérifie ; si le couplage est plus profond que prévu, une `N-n` datée le dit |
| Les quatre hypothèses `N-1` à `N-4` sont infirmées à la recette | Elles sont **isolées** : le mode par défaut, la liste de destinataires, les options de dump et quatre constantes. Aucune ne traverse l'architecture |

## Journal d'exécution

Rempli par `/executer-plan` : échecs, décisions `N-n` ajoutées en route, écarts au plan, datés.
