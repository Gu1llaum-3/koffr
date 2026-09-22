# Plan lot 2 — Corrections de recette

> Statut : **validé par le propriétaire le 2026-09-22**. Exécuté par `/executer-plan`. Les règles
> communes à tous les plans sont dans `METHODE.md` § « Exécution d'un plan » et ne sont pas répétées
> ici.

Travail issu de la session de recette du lot 2 (2026-09-22, instance Multipass `koffr`, parc réel
PostgreSQL 16.15 et MariaDB 11.4.13). Le critère de sortie du lot est **tenu** : l'archive s'ouvre
avec `age` et `zstd` seuls et se restaure à l'identique. Ce plan corrige ce qu'une vraie machine a
montré autour, et livre ce qu'**ADR-0016** vient de fixer.

## Périmètre

**Les sept anomalies `A-12` à `A-18`**, dont trois bloquantes, et les quatre décisions d'ADR-0016.
Le registre fait foi : `docs/recette/anomalies.md`.

| `A-nn` | Constat | Ce qu'il faut |
| --- | --- | --- |
| `A-12` | **Toute la sortie texte part sur la sortie d'erreur** : `koffr config show > f.yaml` donne 0 octet. `cmd.Print*` de cobra retombe sur `os.Stderr` quand aucun écrivain n'est posé — ce que les tests font toujours et la production jamais | Le **résultat** sur la sortie standard, les avertissements et les journaux sur la sortie d'erreur. Et un test du **câblage par défaut** |
| `A-13` | L'extension de l'archive ment : `.pgc` pour un fichier compressé **et** chiffré | `.pgc.zst.age` et `.sql.zst.age` |
| `A-14` | L'identifiant fait 27 caractères et n'est **pas** un ULID, alors qu'ADR-0006 le fixe | Un vrai ULID, avant que le catalogue du lot 3 ne le stocke |
| `A-15` | Un `pg_dump` 18 sur un serveur 16 produit un dump dont la restauration émet `unrecognized configuration parameter "transaction_timeout"` | `doctor` signale l'écart de majeure |
| `A-16` | Un `kill -9` laisse **13 Mo de tampon** que rien ne purge : `internal/state.Open` sait le faire, mais `backup` n'ouvre jamais l'état | Purger au démarrage d'un job, et retirer le tampon même quand le job échoue |
| `A-17` | Le champ de destinataires **par base** que `N-2` promettait « dès ce lot » n'existe pas | Livré par ADR-0016 (`Q-04`) |
| `A-18` | Le journal ne contient que `command started` : **rien** sur ce qu'une sauvegarde a fait | Une ligne par étape des sept de `E-024`, plus l'échec nommant l'étape |

**Ce qu'ADR-0016 ajoute**, au-delà des anomalies :

- `Q-04` — le champ `recipients` par base, qui **remplace** la liste globale ;
- `Q-08` — le diviseur d'estimation passe de **4 à 8** (`N-12` amendée) ;
- `Q-07` — le `README` dit ce que koffr **ne** sauvegarde pas : rôles, tablespaces, droits ;
- `keygen` — la clé **publique** sur la sortie standard, la **privée** et l'avertissement sur la
  sortie d'erreur, pour que `koffr keygen >> recipients.txt` soit le geste utile et qu'une
  redirection distraite ne puisse pas écrire un secret sur le disque ;
- le séquestre — l'avertissement de `E-132` sur **toute** commande qui lit les clés.

**Écarté de ce plan** :

- **`B-01`, les objets globaux du cluster.** Remonté du backlog par `Q-07`, mais c'est un lot, pas
  une correction : `D-08` en fixera la place.
- **`B-09`, `pg_dump --compress=zstd:3`.** Mesuré plus rapide et plus petit, mais casserait
  l'uniformité des trois moteurs. Demande un ADR.
- **Le catalogue et le manifeste.** Lot 3, inchangé — `A-18` trace dans le **journal**, pas en base.

**Critère de sortie** :

1. `koffr config show > copie.yaml` produit un fichier **non vide** et sans ligne de journal, et
   `koffr keygen >> recipients.txt` y ajoute la **clé publique** et rien d'autre ;
2. une archive s'appelle `…​.pgc.zst.age`, et son identifiant est un ULID de 26 caractères ;
3. après un `kill -9` en plein job, `/var/lib/koffr/tmp` est **vide** à la relance ;
4. le journal d'une sauvegarde réussie porte ses sept étapes ; celui d'une sauvegarde échouée nomme
   l'étape qui a cassé ;
5. une base déclarant ses propres destinataires est chiffrée **pour eux seuls** ;
6. le scénario de recette est rejoué en entier, sans nouvelle anomalie bloquante.

## État de départ (vérifié le 2026-09-22)

- **Lot 2 mergé**, six vagues, `main` vert en local et en CI. `mise run e2e` passe sur l'instance.
- **`internal/cli` n'a aucun test du câblage par défaut** : `execute()` pose les deux écrivains, ce
  qui rend `A-12` structurellement invisible.
- **`newJobID`** (`internal/cli/backup.go`) fabrique l'identifiant ; aucune dépendance ULID dans
  `go.mod`.
- **`Service.Run`** ne journalise rien : le port de journalisation n'existe pas dans le domaine.
- **`config.Database`** n'a pas de champ `recipients`.

## Décisions d'implémentation `N-n`

- **N-1 Le domaine ne connaît pas `slog`.** `backup.Service` reçoit un port `Journal` avec une
  méthode par événement de job ; `internal/cli` l'implémente sur `obs`. *Raison* : `AR-01`.
  *Exclut* : un `*slog.Logger` dans une signature du domaine.
- **N-2 L'ULID vient de `github.com/oklog/ulid/v2`** — tranché par le propriétaire le 2026-09-22,
  après mesure. *Raison* : le format a des règles que notre version ratait déjà en silence. Le
  `%010X` de `newJobID` **n'a jamais fixé la largeur** — c'est un minimum, pas un maximum, et
  l'horodatage tient sur 11 chiffres hexadécimaux depuis 2004 : d'où les 27 caractères au lieu de
  26. Le jour où il en faudrait 12, **tous les anciens identifiants trieraient après les
  nouveaux**. Et deux identifiants produits dans la même milliseconde ne sont **pas ordonnés** :
  vérifié, 8 identifiants produits dans l'ordre ne se trient pas dans l'ordre.
  *Mesuré le 2026-09-22* : **+32,7 Kio** sur le binaire (13 490 642 → 13 524 146 octets), sur
  16,6 Mio de marge. Apache-2.0 comme koffr, **722 lignes** en un fichier, aucune dépendance
  transitive liée, 0 avis de sécurité, dernière version en juillet 2026.
  *Comment on l'appelle* : **`ulid.New` avec `ulid.Monotonic(crypto/rand.Reader, 0)`**, jamais
  `ulid.Make`. `Make` utilise `math/rand` — une **régression** par rapport au `crypto/rand`
  d'aujourd'hui — et passe par `MustNew`, qui **panique** si la source d'aléa échoue. Un agent de
  sauvegarde ne panique pas à 2 h du matin.
  *Exclut* : réimplémenter le format, et `ulid.Make`.
- **N-3 L'extension est construite par le domaine**, à partir de ce que le pipeline a appliqué, et
  non écrite en dur dans l'adaptateur. *Exclut* : un `.zst.age` codé dans `internal/cli` qui
  mentirait le jour où la compression change.

## Vagues

### Vague 1 — Ce qu'on écrit va où on croit (`lot2c/wave-1-output-streams`) — `A-12`, `keygen`

- [ ] **1.1** Test d'abord `internal/cli/streams_test.go` — le **câblage par défaut** : une racine
      construite sans `SetOut` ni `SetErr` écrit son résultat sur la **sortie standard**. Le test
      pose un `os.Pipe` sur les deux descripteurs plutôt que des écrivains cobra, sinon il ne
      prouve rien (c'est exactement ce qui a masqué `A-12`).
- [ ] **1.2** Test — `koffr config show` redirigé produit un fichier **non vide**, et `koffr version`
      aussi ; les avertissements et les journaux restent sur la sortie d'erreur (ADR-0012).
- [ ] **1.3** Test `internal/cli/keygen_test.go` — la clé **publique** sur la sortie standard, la
      **privée** et l'avertissement sur la sortie d'erreur ; `koffr keygen >> f` n'écrit **jamais**
      `AGE-SECRET-KEY`.
- [ ] **1.4** Test — l'avertissement de séquestre apparaît sur **toute** commande qui lit les clés
      (`backup`, `config validate`, `doctor`), et disparaît à deux clés (`E-132`, `CRY-02`).
- [ ] **1.5** Vague verte : `verify`, commit `fix(cli): send results to standard output, warnings to standard error`.

### Vague 2 — Le tampon ne survit pas au job (`lot2c/wave-2-purge-staging`) — `A-16`

- [ ] **2.1** Test d'abord `internal/domain/backup/staging_file_test.go` — **`BKP-17`** : le
      répertoire de tampon est **purgé au démarrage** d'un job ; un fichier laissé par un job mort
      disparaît.
- [ ] **2.2** Test — **`BKP-18`** : un job qui échoue **en cours de tampon** ne laisse rien ; un job
      tué pendant l'écriture laisse un fichier que le job suivant purge.
- [ ] **2.3** Test d'intégration `internal/cli` — après un job interrompu, `/var/lib/koffr/tmp` est
      vide à la relance. Joué avec un vrai conteneur.
- [ ] **2.4** `internal/domain/backup/rules.md` : `BKP-17`, `BKP-18`.
- [ ] **2.5** Vague verte : `verify`, commit `fix(backup): purge the staging directory a killed job left behind`.

### Vague 3 — Une archive dit ce qu'elle est (`lot2c/wave-3-archive-identity`) — `A-13`, `A-14`

- [ ] **3.1** Test d'abord `internal/domain/backup/path_test.go` — **`BKP-10` étendue** :
      l'extension porte la **pile appliquée**, dans l'ordre où on la défait (`.pgc.zst.age`), et se
      construit à partir de ce que le pipeline a fait (`N-3`).
- [ ] **3.2** Test `internal/domain/backup/id_test.go` — **`BKP-19`** : l'identifiant est un **ULID**
      de 26 caractères ; deux identifiants produits dans la même milliseconde sont **différents et
      ordonnés** ; le tri lexicographique suit le temps.
- [ ] **3.3** Test — l'identifiant vient de `ulid.New` avec `ulid.Monotonic(crypto/rand.Reader, 0)`,
      **jamais** `ulid.Make` : la source est cryptographique et rien ne panique. Vérifier le binaire
      contre les **+32,7 Kio** annoncés par `N-2` ; un écart notable se dit.
- [ ] **3.4** `README` : la procédure de déchiffrement porte la nouvelle extension.
- [ ] **3.5** `internal/domain/backup/rules.md` : `BKP-19`, `BKP-10` amendée.
- [ ] **3.6** Vague verte : `verify`, commit `fix(backup): name archives after what they are, and identify them with a real ULID`.

### Vague 4 — Un job laisse une trace (`lot2c/wave-4-job-journal`) — `A-18`

- [ ] **4.1** Test d'abord `internal/domain/backup/journal_test.go` — **`BKP-20`** : les **sept
      étapes** de `E-024` sont journalisées dans l'ordre, chacune avec ce qu'elle a produit ; une
      étape qui échoue est journalisée **avec son erreur** et les suivantes ne le sont pas.
- [ ] **4.2** Test — **`BKP-21`** : aucune ligne de journal ne porte de mot de passe, de chemin de
      secret ni de clé (`E-115`). Le test cherche les valeurs, pas les noms de champs.
- [ ] **4.3** Test `internal/cli` — le port est câblé sur `obs` ; après une sauvegarde réelle, le
      fichier de `E-026` porte les sept étapes, l'archive, les tailles et l'empreinte.
- [ ] **4.4** `internal/domain/backup/rules.md` : `BKP-20`, `BKP-21`.
- [ ] **4.5** Vague verte : `verify`, commit `feat(backup): journal the seven steps of a job`.

### Vague 5 — Ce qu'ADR-0016 a fixé (`lot2c/wave-5-adr-0016`) — `A-17`, `Q-04`, `Q-07`, `Q-08`

- [ ] **5.1** Test d'abord `internal/config` — le champ `recipients` **par base** est accepté,
      valide des clés `age`, et une base qui n'en déclare pas hérite de la liste globale.
- [ ] **5.2** Test `internal/domain/backup` — **`BKP-22`** : la liste par base **remplace** la
      globale, jamais ne la complète ; l'archive n'est déchiffrable que par les clés déclarées.
- [ ] **5.3** Test `staging_test.go` — le diviseur d'estimation est **8** (`N-12` amendée par
      ADR-0016), avec la mesure de recette citée dans le test.
- [ ] **5.4** Test `internal/cli/doctor_test.go` — un client d'une majeure plus récente que son
      serveur est **signalé** (`A-15`).
- [ ] **5.5** `README` : ce que koffr **ne** sauvegarde pas — rôles, tablespaces, droits — et
      pourquoi (`Q-07`).
- [ ] **5.6** `rules.md` : `BKP-22`, et les constantes mises à jour.
- [ ] **5.7** Vague verte : `verify`, commit `feat(backup): per-database recipients, and the estimate the acceptance session measured`.

### Vague 6 — Rejouer la recette (`lot2c/wave-6-replay`)

- [ ] **6.1** Rejouer **les sept parcours** du scénario sur l'instance, parc réel.
- [ ] **6.2** Mettre à jour `docs/recette/lot-2-scenario.md` : historique du rejeu, et ce que les
      décisions ont changé dans « ce qui est voulu et pourrait passer pour un bug ».
- [ ] **6.3** Vague verte : `verify`, commit `chore: replay the lot 2 acceptance scenario`.

## Journal d'exécution

Rempli par `/executer-plan`.
