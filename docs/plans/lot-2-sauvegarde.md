# Plan lot 2 — Sauvegarder une base vers un fichier chiffré

> Statut : **terminé le 2026-09-22** — six vagues mergées, `verify` vert sur `main`, CI verte.
> Validé par le propriétaire le 2026-09-19. Exécuté par `/executer-plan`. Reste la **recette**
> (`docs/recette/lot-2-scenario.md`), puis `/cloturer-lot 2`. Les règles communes à tous les plans
> sont dans `METHODE.md` § « Exécution d'un plan » et ne sont pas répétées ici.

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
- **N-8 (2026-09-19) — la preuve d'interopérabilité `age` est un script, pas un test Go.**
  *Raison* : `E-075` ne se démontre qu'en lançant le **vrai** binaire `age`, et `AR-07` réserve
  `os/exec` à `internal/engine`. Le test Go écrit l'archive, la clé et le texte attendu dans un
  répertoire donné par un **drapeau de test** — pas une variable d'environnement, qu'`AR-05`
  réserve à `internal/config` — et `scripts/check-age-interop.sh` exécute `age` par-dessus.
  Sauté bruyamment sans `age`, **exigé en CI** par `KOFFR_REQUIRE_AGE=1`. *Exclut* : prouver
  `E-075` avec notre propre bibliothèque, ce qui ne prouverait qu'un aller-retour.
- **N-9 (2026-09-19) — les garanties du pipeline sont des règles de `backup`, pas d'un code `PIP`.**
  *Constat* : la tâche `2.5` écrit « `rules.md` du pipeline » et invente un code `PIP`. Or ADR-0010
  fait de `internal/pipeline` un **adaptateur**, et `ARCHITECTURE.md` ne déclare pas de code `PIP` :
  les codes existants sont `CFG`, `RSV`, `BKP`, `VRF`, `CAT`, `RET`, `RST`, `SCH`, `ALR`, `CRY`,
  `UPL`. *Correction* : les trois règles deviennent **`BKP-07`, `BKP-08` et `BKP-09`** dans
  `internal/domain/backup/rules.md`, `BKP-01` à `BKP-06` restant à la vague 5 comme prévu.
  **Étendue le 2026-09-21** : la tâche `3.5` invente de même un code `STO`, qui n'est pas davantage
  déclaré. La règle de chemin devient **`BKP-10`**, au même endroit et pour la même raison.
  *Exclut* : un `rules.md` dans un adaptateur, et un douzième préfixe de registre non déclaré.
- **N-10 (2026-09-22) — en stratégie `exec`, le dump se connecte à `127.0.0.1` sur le port standard
  du moteur.** *Raison* : l'outil s'exécute **dans le conteneur de la base** ; l'adresse que koffr
  utilise de l'extérieur est un port publié qui n'y veut rien dire. *Exclut* : transmettre l'hôte et
  le port de la configuration à un processus qui ne les voit pas de la même façon — ce qui aurait
  marché sur un poste en réseau `host` et échoué partout ailleurs.
- **N-11 (2026-09-22) — la vague 4 livre le *refus* de `-Fd`, pas le dump répertoire.** *Constat* :
  `-Fd` n'est **pas un flux** — pg_dump remplit un répertoire —, et `E-054` lui impose pour cette
  raison le mode `stage`, qui est la tâche **5.2** (`BKP-03`). *Correction* : `Dump` refuse le format
  répertoire avec deux erreurs typées qui disent laquelle des deux raisons s'applique, et la vague 5
  ajoutera l'écriture en répertoire avec le tampon. *Exclut* : une implémentation à moitié qui
  écrirait un répertoire sans savoir où le mettre.
- **N-12 (2026-09-22) — sans historique, la taille attendue d'une archive est le quart de la base.**
  *Raison* : le § 4.5 donne une fourchette de 10 % à 25 % après zstd ; koffr prend l'extrémité
  **pessimiste**, parce que sous-estimer remplit un disque et que surestimer ne fait que basculer en
  `stream`. La taille de la base vient de la sonde (`RSV-12`), qui a gagné une requête pour cela —
  délibérément, après refus de la garde. *Exclut* : un taux moyen inventé, et un contrôle d'espace
  qui passerait toujours faute de chiffre.
- **N-13 (2026-09-22) — il n'y a pas d'historique des sauvegardes dans ce lot.** *Constat* : le
  catalogue s'écrit au lot 3 ; le port `History` existe, son adaptateur rend « aucune ». *Effet* :
  toute estimation passe par le repli de `N-12`. *Exclut* : écrire dans `backups` au lot 2 sans les
  exigences `E-028` et `E-057` qui le cadrent.
- **N-14 (2026-09-22) — la tâche `5.6` demandait `STO-01`, qui n'existe pas.** Même raison que
  `N-9` : `internal/store` est un adaptateur et `ARCHITECTURE.md` ne déclare pas ce préfixe. Les
  règles de destination sont `BKP-11` et `BKP-12`, écrites à la vague 3. Rien à ajouter.
- **N-15 (2026-09-22) — le scénario 6 est prouvé par un script, comme `E-075`.** *Raison* : une
  archive qui « s'ouvre sans koffr » ne se démontre qu'avec des binaires qui ne sont pas les nôtres,
  et `AR-07` réserve `os/exec` à `internal/engine`. Le test Go démarre les serveurs, lance
  `koffr backup` et **dépose** l'archive et une clé privée dans un répertoire donné par un drapeau
  de test ; `scripts/check-backup-e2e.sh` exécute `age --decrypt | zstd -d` puis `pg_restore --list`
  par-dessus. Exigé en CI par `KOFFR_REQUIRE_AGE=1` et `KOFFR_REQUIRE_DOCKER=1`. *Exclut* : prouver
  le scénario 6 avec notre propre bibliothèque `age`, ce qui ne prouverait qu'un aller-retour.
- **N-7 L'empreinte `sha256_raw` est celle du flux *avant* compression, `sha256_stored` celle de ce
  qui est écrit.** *Raison* : le manifeste du § 5.3 porte les deux, et seule la seconde se vérifie
  sans déchiffrer. *Exclut* : une seule empreinte, qui rendrait `E-062` impossible au lot 3.

## Vagues

### Vague 1 — Chiffrement et clés (`lot2/wave-1-encryption`)

Exigences : `E-072` à `E-076`, `E-132`, et `keygen` de `E-103b`. L'inconnue d'abord : si une archive
`age` produite par koffr ne se déchiffre pas avec l'outil standard, tout le lot change de forme.

- [x] **1.1** Test d'abord `internal/domain/crypto/recipients_test.go` — **`CRY-01`** : une clé
      publique `age` valide est acceptée, une clé mal formée est **refusée en nommant la ligne** du
      fichier de destinataires ; un fichier vide est une erreur.
- [x] **1.2** Test — **`CRY-02`** : au moins **deux** destinataires sont exigés ; une seule clé
      produit un **avertissement au démarrage** qui nomme le séquestre (`E-132`).
- [x] **1.3** Test `internal/pipeline/encrypt_test.go` — **`CRY-03`** : le chiffrement est **en
      flux**, sans matérialiser l'entrée, et l'archive produite est **déchiffrable par la commande
      `age` réelle**, pas par notre propre code (`E-075`). Le test invoque le binaire `age` s'il est
      présent et **se saute bruyamment** sinon, comme les conteneurs.
- [x] **1.4** Test — **`CRY-04`** : l'archive est **illisible** avec une autre clé privée, et
      déchiffrable par **chacun** des destinataires déclarés (`E-073`).
- [x] **1.5** Test `internal/cli/keygen_test.go` — `koffr keygen` affiche la paire, **n'écrit
      aucun fichier**, et le dit (`E-076`, ADR-0007).
- [x] **1.6** `internal/domain/crypto/rules.md` : `CRY-01` à `CRY-04`.
- [x] **1.7** Vague verte : `verify`, commit `feat(crypto): age recipients, streaming encryption and keygen`.

### Vague 2 — La chaîne en flux (`lot2/wave-2-streaming-pipeline`)

Exigences : `E-025`, et la moitié de `E-024`.

- [x] **2.1** Test d'abord `internal/pipeline/pipeline_test.go` — **le dump brut n'est jamais
      matérialisé** : un flux d'entrée de taille connue traverse zstd, `age` et l'empreinte **en une
      passe**, et la mémoire retenue reste bornée quelle que soit l'entrée.
- [x] **2.2** Test — les **deux** empreintes sont calculées au vol : `sha256_raw` avant
      compression, `sha256_stored` sur ce qui sort (`N-7`).
- [x] **2.3** Test — une erreur **au milieu** du flux — dump interrompu, disque plein — remonte
      **typée**, et rien d'incomplet n'est laissé derrière.
- [x] **2.4** Test — la compression est `zstd:3` par défaut et réglable (`N-4`).
- [x] **2.5** `rules.md` du pipeline : **`PIP-01`** (une seule passe), **`PIP-02`** (deux
      empreintes), **`PIP-03`** (erreur en cours de flux).
- [x] **2.6** Vague verte : `verify`, commit `feat(pipeline): stream a dump through zstd, age and two checksums`.

### Vague 3 — Écrire quelque part (`lot2/wave-3-filesystem-store`)

Exigences : `E-012a`, `E-066`, `E-070`.

- [x] **3.1** Test d'abord `internal/store/store_test.go` — **l'interface unique** de `E-066` :
      écrire en flux, lire en flux, lister, supprimer, tester l'accès. Le test est écrit **contre
      l'interface**, pas contre `filesystem`, pour que S3 et SFTP le rejouent au lot 4.
- [x] **3.2** Test — **`STO-01`** : le chemin est déterministe et lisible —
      `<base>/<AAAA>/<MM>/<base>_<horodatage>_<id>.<ext>` — et se reconstruit sans la base locale
      (`E-070`).
- [x] **3.3** Test — une écriture interrompue **ne laisse pas d'archive partielle** visible :
      fichier temporaire puis renommage atomique.
- [x] **3.4** Test — tester l'accès dit **pourquoi** il échoue : répertoire absent, droits, disque
      plein.
- [x] **3.5** `internal/store/rules.md` n'existe pas — `store` est un adaptateur (ADR-0010) :
      `STO-01` va dans `internal/domain/backup/rules.md`, avec sa raison.
- [x] **3.6** Vague verte : `verify`, commit `feat(store): the single destination interface, and the filesystem one`.

### Vague 4 — Dumper pour de vrai (`lot2/wave-4-dump`)

Exigences : `E-054`, `E-055`, `E-056`, et la stratégie `exec` **appliquée au dump**.

- [x] **4.1** Test d'abord `internal/engine/dump_test.go` — contre un **vrai** PostgreSQL : le dump
      sort sur `stdout`, en `-Fc`, avec `--no-owner --no-privileges` (`N-3`), et l'archive obtenue
      est relisible par `pg_restore --list`.
- [x] **4.2** Test — contre une **vraie** MariaDB : `--single-transaction`, routines, déclencheurs
      et événements (`E-056`).
- [x] **4.3** Test — **MyISAM détecté** et signalé : une table MyISAM plantée dans la base rend un
      avertissement que le job porte (`E-056`).
- [x] **4.4** Test — le dump passe par la stratégie `exec` quand la base la déclare, **et `-Fd` y
      est refusé avec un message qui le dit** (`E-054`, ADR-0015).
- [x] **4.5** Test — `E-055` : en `stage`, le sous-process est **terminé et la connexion fermée**
      avant que l'envoi ne commence. Vérifié en observant l'ordre, pas en le supposant.
- [x] **4.6** Vague verte : `verify`, commit `feat(engine): dump a database to a stream, per family`.

### Vague 5 — Le cas d'usage de sauvegarde (`lot2/wave-5-backup-use-case`)

Exigences : `E-024`, `E-029`, `E-030`, `E-051`, `E-053`, `E-061`, et `backup` de `E-103b`.

- [x] **5.1** Test d'abord `internal/domain/backup/lock_test.go` — **`BKP-01`** : un seul job par
      base ; une seconde demande est **refusée** en nommant le job en cours, jamais mise en file
      (`E-051`, `N-5`). Un verrou dont le processus est mort **ne bloque pas** éternellement.
- [x] **5.2** Test `internal/domain/backup/staging_test.go` — **`BKP-02`** : les trois modes ;
      **`BKP-03`** : `stage` est **imposé** par `-Fd`, par plus d'une destination, ou par la
      vérification (`E-030`) ; **`BKP-04`** : `auto` choisit selon l'espace libre et **enregistre le
      mode appliqué** (`E-053`, `N-1`).
- [x] **5.3** Test — **`BKP-05`** : l'espace disque est estimé **avant** de commencer, extrapolé de
      la dernière sauvegarde réussie ou à défaut de la taille de la base, avec la marge de `N-4` ;
      un job qui remplirait le disque **bascule en `stream` ou est refusé** (`E-061`).
- [x] **5.4** Test — **`BKP-06`** : les sept étapes de `E-024` s'enchaînent **dans l'ordre**, et le
      job échoue si l'une manque. Les deux dernières — vérification et manifeste — sont **absentes
      et déclarées telles** jusqu'au lot 3.
- [x] **5.5** Test `internal/cli/backup_test.go` — `koffr backup <db>` et `--dry-run`, qui **dit ce
      qu'il ferait** sans rien écrire.
- [x] **5.6** `internal/domain/backup/rules.md` : `BKP-01` à `BKP-06`, plus `STO-01`.
- [x] **5.7** Vague verte : `verify`, commit `feat(backup): back up a database to an encrypted archive`.

### Vague 6 — De bout en bout, sur un vrai parc (`lot2/wave-6-end-to-end`)

- [x] **6.1** Test d'intégration : `koffr backup` sur un **vrai** PostgreSQL et une **vraie**
      MariaDB, archive écrite sur disque, **déchiffrée par l'outil `age` standard**, et le contenu
      relu par `pg_restore --list` (scénario 6 du § 8).
- [x] **6.2** Test — **scénario 11, première moitié** : en `stage`, aucun fichier de plus que la
      taille compressée n'apparaît, et la connexion se ferme avant l'envoi.
- [x] **6.3** Mesurer le binaire : `internal/state`, `age` et `zstd` y entrent. Noter l'écart.
- [x] **6.4** `README` : la procédure de séquestre (`E-132`), et comment déchiffrer une archive
      **sans koffr**.
- [x] **6.5** Vague verte : `verify`, commit `chore: back up a real fleet end to end`.

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

### 2026-09-22 — vague 6, de bout en bout sur un vrai parc

- **Le scénario 6 est tenu, et par des outils qui ne sont pas les nôtres** : `mise run e2e` lance
  `koffr backup` sur une **vraie** PostgreSQL 16 et une **vraie** MariaDB 11.4, puis ouvre les
  archives avec `age --decrypt | zstd -d` et lit la PostgreSQL avec `pg_restore --list`. `N-15`
  ajoutée : même marché que `N-8`, le test dépose, le script exécute.
- **L'ordre compte et le script le documente** : `age` **puis** `zstd`, parce que le § 4.1 compresse
  avant de chiffrer. C'est la procédure que le `README` donne maintenant à un exploitant qui a perdu
  l'agent et gardé la clé — vérifiée à chaque build, pas seulement écrite.
- **La MariaDB passe par la stratégie `exec`** dans ce test, donc ADR-0015 est vérifié de bout en
  bout : sur un runner qui ne porte que le client MySQL d'Oracle, c'est la seule façon d'atteindre
  le bon client.
- **Scénario 11, première moitié** : sur une base d'environ 20 Mio de dump, le tampon ne dépasse
  jamais la taille de l'archive **compressée** (tolérance 20 % pour le bloc que l'encodeur retient),
  et aucun échantillon ne montre d'écriture vers la destination **pendant** qu'une connexion de dump
  est ouverte. Le détecteur est un détecteur de violation, pas une preuve d'avoir observé l'instant :
  la preuve déterministe est `BKP-14`, qui tient l'ordre par construction.
- **Le test du scénario 11 a trouvé un vrai défaut de la vague 4** : le dump de 60 000 lignes ne
  faisait que 187 Kio. `pg_dump -Fc` **compresse lui-même** par défaut, et le manifeste du § 5.3
  porte `--compress=0` — que j'avais manqué. Corrigé, et `BKP-13` le dit maintenant. C'est ce qu'un
  test de bout en bout sur une base non triviale est censé attraper : aucun test unitaire de la
  vague 4 ne pouvait le voir.
- **Et la mesure a corrigé deux affirmations que j'avais faites sur ce défaut** (2026-09-22, base de
  107 Mo, 400 000 lignes, sur l'instance) : dump brut **75,7 Mo** ; `--compress=0` + zstd:3
  **7,25 Mo** en 0,7 s ; zlib de `pg_dump` + zstd:3 **6,84 Mo** en 0,8 s ; zlib seul **7,11 Mo**.
  Donc **non**, la double compression ne « recompressait pas du compressé pour rien » : elle gagnait
  encore 4 %. Et **non**, l'arithmétique du § 4.5 n'était pas fausse par construction : l'estimation
  d'espace lit la **taille de la base**, pas le dump brut. Ce qui était réellement cassé, c'est le
  **sens des deux tailles** : la commande affichait « 6,8 Mo stockés, 7,1 Mo dumpés », d'où un
  exploitant conclut que la compression ne sert à rien, alors que le dump fait 75,7 Mo. Après
  correctif : 9,6 % du brut, soit l'extrémité optimiste de la fourchette du § 4.5, **validée par la
  mesure**. Et `sha256_raw` porte enfin l'empreinte du dump.
- **Trouvaille inattendue, versée au backlog (`B-09`)** : `pg_dump --compress=zstd:3` seul rend
  **6,69 Mo en 0,4 s** — plus petit et deux fois plus rapide que notre chaîne. Ça ne dispense pas de
  chiffrer, et ça casserait l'uniformité des trois moteurs, mais c'est une piste mesurée qui demande
  un ADR, pas une décision de vague.
- **Ce test lit la sortie de la commande**, à dessein : les deux tailles affichées sont ce qu'un
  exploitant lit pour juger qu'une sauvegarde s'est bien passée. Elles font donc partie de ce qui
  est testé.
- **`database/sql` est interdit jusque dans les tests** : la garde a refusé le pilote MySQL dans
  `internal/cli`, alors que j'allais semer MariaDB avec. Semé par `container.Exec`, avec le client de
  l'image — ce qui est de toute façon plus proche de ce que fait koffr.
- **`6.3` — le binaire mesuré** : **13,4 / 12,6 / 12,9 Mio** (linux/amd64, linux/arm64,
  darwin/arm64), contre **10,0 / 9,3 / 9,5** au début du lot. `age` et `zstd` coûtent donc
  **+3,4 Mio**. Marge avant le seuil : **16,6 Mio**. Le risque du plan est levé pour ce lot ; il se
  repose au lot 4 avec le SDK S3.
- **`6.4` — le `README` porte la procédure de séquestre** (`E-132`, deux clés, celle de séquestre
  ailleurs et hors ligne, l'avertissement qui ne disparaît pas) et la façon d'ouvrir une archive
  **sans koffr**. Le `Status` disait encore « lot 0 en cours » : corrigé, et il dit désormais ce que
  koffr ne sait **pas** encore faire.
- **Le point 6 du critère de sortie n'était pas tenu, et la relecture du critère l'a trouvé** :
  l'avertissement de `E-132` n'existait que sur `koffr backup`. Or c'est `config validate` qu'un
  exploitant lance pour vérifier une configuration — il n'aurait rien vu. Ajouté là aussi, test
  d'abord, et `CRY-02` cite maintenant les deux.
- `mise run verify` : **0**, `mise run e2e` : **0** sur l'instance.

### 2026-09-22 — vague 5, le cas d'usage de sauvegarde

- **`BKP-01` à `BKP-06` écrites avant le code**, et le verrou est un fichier lisible à l'œil
  (`N-5`) : un verrou laissé par un processus mort — ou tronqué par une coupure — **est repris**,
  parce qu'une machine qui perd le courant à 3 h doit se sauvegarder la nuit suivante sans que
  personne ne se connecte pour effacer un fichier.
- **La décision de tampon est pure** : `DecideStaging` ne touche ni disque ni horloge, ce qui la
  rend montrable par `doctor` sans lancer de sauvegarde. Les trois cas d'imposition du § 4.5 passent
  **par-dessus** un `stream` explicite, et la décision **dit lequel** a décidé.
- **Aucun flottant ne décide si un disque a de la place** : la marge × 1,5 est une fraction
  (ADR-0006).
- **`E-055` est tenue par la structure du code**, pas par une intention : en `stage`, `dump.Close()`
  est appelé **avant** la première écriture vers une destination, et le `defer` qui ferme le fichier
  tampon vient après. Un test le constate sur l'ordre.
- **`N-12` et `RSV-12` ajoutées** : `E-061` a besoin de la taille de la base, donc la sonde a gagné
  une requête par famille. La garde de `queries_test.go` **les a refusées toutes les deux** avant
  qu'elles ne soient inscrites. La sonde PostgreSQL n'envoyait **aucune** requête jusqu'ici ; elle en
  envoie une maintenant, et c'est une perte qu'il vaut mieux nommer que découvrir.
- **`N-13`** : pas d'historique des sauvegardes dans ce lot — le catalogue est au lot 3. Le port
  existe, l'adaptateur rend « aucune », et l'estimation passe donc toujours par le repli.
- **`N-14`** : la tâche `5.6` demandait un `STO-01` qui n'existe pas, pour la même raison que `N-9`.
- **Un test m'a repris** : j'avais écrit qu'un `--dry-run` en échec devait quand même afficher
  quelque chose. Il n'affiche rien, et c'est correct — l'erreur nomme la base. L'assertion a été
  réécrite pour dire la règle réelle, pas celle que j'avais supposée.
- **Ce qui manque et qui est dit** : le dry-run d'une base **joignable** et la sauvegarde complète
  ne sont pas prouvés ici — `internal/cli` n'a pas de fixtures de conteneurs. C'est la vague 6, sur
  un vrai parc.
- `mise run verify` : **0**.

### 2026-09-22 — vague 4, dumper pour de vrai

- **`engine.Dump` rend un flux**, jamais un fichier : la sortie standard du sous-process, telle
  quelle. `DumpCommand` est exportée parce qu'un dump qui échoue se diagnostique en relançant la
  commande à la main — et parce qu'un test peut la lire sans démarrer un serveur.
- **Le mot de passe passe par l'environnement du sous-process**, jamais sur la ligne de commande
  (`E-115`), et le sous-process **n'hérite de rien** : `os.Environ()` a été **refusé par le lint**
  (`AR-05`), ce qui était juste. Un `PGPASSWORD` ou un `~/.my.cnf` hérités changeraient en silence
  ce que contient une archive.
- **`Close` attend le process et remonte ce qu'il a dit.** C'est `BKP-14` et ce n'est pas de
  l'hygiène : un dump dont le process meurt en route produit une archive parfaitement formée de
  rien. Un dump abandonné en cours de lecture est **arrêté**, pas laissé tourner sur une base de
  production pendant qu'on téléverse.
- **`E-055` observée, pas supposée** : après `Close`, le test interroge `pg_stat_activity` jusqu'à
  ce que le backend de `pg_dump` ait disparu. Deux cas : lu jusqu'au bout, et abandonné après huit
  octets.
- **La stratégie `exec` porte maintenant le dump lui-même** (ADR-0015) : une MariaDB 11.4 est
  dumpée par le client de sa propre image, une PostgreSQL 16 aussi, et le flux sort du conteneur
  démultiplexé. `N-10` ajoutée pour l'adresse utilisée là-dedans.
- **`-Fd` est refusé deux fois, avec deux messages différents** — dans un conteneur, et en flux —
  et `N-11` dit pourquoi l'écriture en répertoire attend la vague 5.
- **La garde de `queries_test.go` a mordu**, comme prévu : la requête MyISAM de `E-056` a été
  **refusée** avant d'être ajoutée à la liste, délibérément et avec sa raison. Elle lit le
  **catalogue**, pas une table sauvegardée — c'est la ligne que trace ADR-0013.
- `RSV-11` : une base entièrement InnoDB **n'avertit de rien**, et un compte qui ne peut pas lire le
  catalogue produit « koffr n'a pas pu vérifier », jamais « il n'y en a pas ».
- **La CI a échoué sur un test qui n'est pas de cette vague**, et c'était un vrai défaut :
  `internal/obs` utilisait `t.TempDir()` pour la rotation, alors que `lumberjack` supprime ses
  vieilles archives depuis une goroutine qui **survit à `Close`**. Le ménage échouait par
  intermittence sur « directory not empty ». Le test a maintenant son répertoire à lui, retiré avec
  un peu de patience. Le défaut existait depuis le lot 0 ; ce sont les conteneurs de cette vague,
  qui chargent la machine, qui l'ont rendu visible.
- Joué sur l'instance Multipass avec de **vrais serveurs** : 16 tests, tous verts, dont
  `pg_restore --list` sur l'archive produite. `mise run verify` : **0**.

### 2026-09-21 — vague 3, écrire quelque part

- **`internal/store/storetest`** porte la suite de conformité d'`E-066` : **huit promesses**, écrites
  une fois, contre l'**interface**. `filesystem` les passe ; S3 et SFTP les rejoueront au lot 4 sans
  qu'on réécrive une ligne. Une interface que chaque implémentation lit à sa façon n'est pas une
  interface.
- `BKP-10` à `BKP-12` écrites avant le code. **`N-9` étendue** : la tâche `3.5` inventait un code
  `STO`, pas plus déclaré que `PIP` ; la règle de chemin est devenue `BKP-10`.
- **Écriture atomique** : on écrit à côté, on synchronise, on renomme. Un chemin qui porte le nom
  d'une archive porte une archive **entière** — une moitié est pire que rien, parce qu'elle a l'air
  restaurable.
- `BKP-10` teste aussi ce que le CDC ne dit pas : un identifiant de base contenant
  `../../etc/passwd` **ne sort pas** de son répertoire. Une configuration est un fichier qu'on
  édite ; un chemin qui grimpe est une sauvegarde qui écrase autre chose.
- **Le lint a signalé une entorse à nos propres règles** : mes noms de sous-tests étaient en
  **français**, ce que `CLAUDE.md` réserve au pilotage. `misspell` s'en est plaint — il lisait du
  français comme de l'anglais fautif. Traduits.
- **Et j'ai refait la faute du lot 1** : le script d'écriture du journal a échoué sur une ancre
  introuvable, donc ni les cases ni le journal n'étaient écrits, et le commit est parti quand même.
  La leçon était dans le skill `implementer` depuis la clôture du lot 1 — **relire le fichier**, pas
  le code de retour. Rattrapé par amendement du commit.
- `mise run verify` : **0**.

### 2026-09-19 — vague 1, chiffrement et clés

- `CRY-01` à `CRY-04` écrites **avant** le code. `internal/domain/crypto` ne chiffre pas : il
  connaît les destinataires. `internal/pipeline` chiffre, en flux.
- **L'inconnue du lot est retirée** : `scripts/check-age-interop.sh` a fait lire par **`age`
  1.2.1**, sur l'instance, une archive écrite par koffr. `E-075` tient — aucun format maison.
- **`N-8` ajoutée** : cette preuve est un script parce qu'un test Go qui lancerait `age`
  violerait `AR-07`. Le test Go écrit les pièces, le script exécute. Troisième fois que ce schéma
  sert — `-race`, Docker, et maintenant `age` — et il est devenu la façon de traiter un outil
  externe dont l'absence ne doit ni bloquer ni mentir.
- **`E-025` mesurée, pas affirmée** : un lecteur de test surveille l'écart entre ce qui est lu et
  ce qui est sorti ; au-delà de 4 Mio d'avance sur 8 Mio d'entrée, le test échoue. Le flux ne se
  matérialise pas.
- `CRY-04` vérifiée dans les deux sens : **chacun** des destinataires ouvre l'archive, et une clé
  étrangère n'ouvre rien.
- `koffr keygen` affiche la paire, **n'écrit rien** — le test cherche la clé privée dans tous les
  fichiers des répertoires d'état et de journal — et explique le séquestre.
- **Divergence assumée et écrite** dans `rules.md` : une seule clé **avertit** sans empêcher. Le
  § 11 dit « obligatoires », `E-132` dit « avertissement » ; c'est la seconde lecture qui est
  retenue, et la recette tranchera (parcours 2).
- `mise run verify` : **0**, `mise run interop` : **0** sur l'instance.

### 2026-09-19 — vague 2, la chaîne en flux

- `pipeline.Run` enchaîne dump → zstd → `age` → destination, **en une passe**, avec les deux
  empreintes calculées au vol. L'ordre est celui du § 4.1 : compresser **puis** chiffrer — l'inverse
  compresserait des octets d'apparence aléatoire et ne gagnerait rien.
- **`N-9` ajoutée** : le plan demandait un `rules.md` pour `pipeline` et inventait un code `PIP`. Or
  ADR-0010 en fait un adaptateur et `ARCHITECTURE.md` ne déclare pas ce préfixe. Les règles sont
  devenues **`BKP-07` à `BKP-09`** dans `internal/domain/backup/rules.md`.
- **Deux de mes tests mesuraient la mauvaise chose**, et le code était bon : des zéros et des `x`
  répétés se compressent à presque rien, donc « l'écart entre ce qui est lu et ce qui est sorti »
  mesurait le taux de compression. Refaits avec `crypto/rand`.
- **Le détecteur de course a trouvé une vraie course** — dans le test : l'encodeur `zstd` écrit
  depuis **sa propre goroutine**, et mon test lisait en parallèle le `bytes.Buffer` de destination.
  Compté par un `atomic`. C'est la première fois que `-race` sert dans ce projet, et il a servi.
- Les deux pièges sont inscrits en `CLAUDE.md` § Conventions.
- `BKP-09` vérifiée dans les deux sens : un dump interrompu et un disque plein donnent une erreur
  typée et **aucune empreinte** — rien qui puisse passer pour une sauvegarde.
- `mise run verify` : **0**.
