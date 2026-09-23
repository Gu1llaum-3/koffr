# Plan lot 3 — Manifeste, catalogue et vérification

> Statut : **validé par le propriétaire le 2026-09-23**. Exécuté par `/executer-plan`. Les règles
> communes à tous les plans sont dans `METHODE.md` § « Exécution d'un plan » et ne sont pas
> répétées ici.

## Périmètre

Le lot 2 a livré les étapes 01→05 de `E-024`. Les deux dernières — **vérification** et
**manifeste** — sont aujourd'hui *déclarées absentes* par `koffr backup`, alors que le principe
`P4` dit que **rien n'est sauvegardé tant que ce n'est pas vérifié**. koffr écrit donc des archives
dont il ne sait rien, et rien ne permet d'inventorier un dépôt sans lui. Ce lot les livre, avec le
**catalogue local** — dont les tables existent depuis le lot 0 et n'ont **jamais reçu une ligne**.

Exigences couvertes :

| `E-nnn` | Ce qu'elle demande | § |
| --- | --- | --- |
| `E-008` | Une archive n'est valide qu'après intégrité **et** relecture structurelle ; la non-vérifiée est signalée | § 2 `P4` |
| `E-014` | La vérification du MVP couvre l'empreinte et la structure | § 3 |
| `E-057` | Un manifeste JSON non chiffré et sans secret, à côté de l'archive, sur **chaque** destination | § 5.3 `F3.8` |
| `E-058` | Les dix-sept champs du manifeste | § 5.3 |
| `E-059` | Jamais d'identifiant ; lisible sans la clé privée | § 5.3, § 6 |
| `E-062` | Empreinte calculée au vol, **recalculée à la relecture** pour au moins une destination | § 5.4 `F4.1` |
| `E-063` | Structure effective : `pg_restore --list` cohérent, marqueur de fin MySQL | § 5.4 `F4.2` |
| `E-064` | Vérifiée et non vérifiée **visuellement distinctes** ; l'absence n'est jamais un succès | § 5.4 `F4.3` |
| `E-103c` | `list [<db>] [--destination]` et `verify <backup-id>` | § 5.12 |
| `E-113` | Un agent compromis ne déchiffre rien : il n'a qu'une clé publique | § 6 `F6.3` |
| `E-114` | Un stockage compromis n'expose que des métadonnées | § 6 |

**Écartées de ce lot** : `E-065` (revérification périodique — lot 6, `F4.4` est « souhaitable ») ;
`E-068` (échec d'une destination — lot 4, il demande S3 et SFTP) ; la restauration (lot 4) ;
`B-01`, `B-09`, `B-10`.

**Critère de sortie**, repris de `ROADMAP.md` et rendu vérifiable :

1. `koffr list` distingue **à l'œil** une archive vérifiée d'une non vérifiée ;
2. `koffr verify <backup-id>` recalcule l'empreinte, met le catalogue à jour, et **dit** ce qu'il ne
   peut pas vérifier ;
3. un dépôt est **inventoriable sans la clé privée et sans koffr**, à partir des seuls manifestes ;
4. **aucun manifeste ne contient d'identifiant de connexion** ;
5. une archive dont l'empreinte relue ne correspond pas fait **échouer** le job ;
6. `koffr backup` ne déclare plus aucune étape absente.

## État de départ (vérifié le 2026-09-22)

Constaté dans le code et le schéma, pas supposé.

- **Les sept tables existent, aucune n'a jamais reçu de ligne hors tests.** `internal/state` ne
  contient **aucune requête métier** : ouverture, migrations, refus de downgrade. Les requêtes du
  catalogue sont à écrire, et elles vivent dans ce paquet — `DB()` dit « Only this package builds
  queries on it ».
- **`backups.verified` existe déjà** : `TEXT NOT NULL DEFAULT 'none' CHECK (verified IN ('none',
  'checksum','structure','failed'))`, avec `verified_at`, `manifest`, `sha256_raw`, `sha256_stored`.
  Le schéma du lot 0 avait anticipé ce lot.
- **`backups.database_id` est `NOT NULL REFERENCES databases(id)`**, `foreign_keys=ON` est posé par
  DSN, et `databases` exige `engine`, `host`, `port`, `database`, `user`, `fingerprint`, `resolved`
  tous non nuls. **Aucune sauvegarde ne se catalogue sans que la configuration résolue de sa base
  soit enregistrée d'abord.**
- **`backup_locations`** a pour clé `(backup_id, destination_id)` et porte `status`, `remote_path`,
  `size_bytes`, `stored_at` — un état de **stockage**, pas de vérification.
- **`AR-04` interdit à `internal/cli` d'importer `internal/state`**, et `internal/arch` le vérifie
  sur le graphe réel des imports. Or `internal/cli/backup.go` construit aujourd'hui tout le
  câblage, et `cmd/koffr/main.go` fait quatre lignes.
- `internal/domain/catalog/` et `internal/domain/verify/` ne contiennent qu'un `doc.go`.
- `main` vert en local et en CI ; 13 paquets de tests verts ; binaire 13,5 Mio, marge 16,5 Mio.

### Mesuré sur l'instance, pas supposé (2026-09-22)

| Constat | Chiffre |
| --- | --- |
| `pg_dump -Fc \| pg_restore --list` depuis un **tube** | fonctionne, code 0, 24 entrées de TOC |
| le même sur un flux **tronqué à 200 Ko** | **code 0 aussi** |
| marqueur de fin d'un dump MariaDB | `-- Dump completed on 2026-09-20 14:12:28` |

## Ce que le cahier des charges dit, et ce qu'il ne dit pas

- **Dit** : `F4.2` veut « `pg_restore --list` sur l'archive » et un « marqueur de fin » pour MySQL.
  `F4.1` veut l'empreinte « recalculée à la relecture de la destination ». `E-024` ordonne les
  étapes : …, écriture, **vérification**, **manifeste** — le manifeste est donc écrit **après** la
  vérification, ce qui résout la question de savoir comment il porte `verified`.
- **Contredit** : `F4.2` demande de relire l'archive **écrite**, qui est chiffrée ; `F6.3`, `E-113`
  et ADR-0007 interdisent à koffr de détenir la clé privée. **Aucune `Q-nn` ne couvrait ce point.**
  Tranché le 2026-09-22, figé par **ADR-0017** (accepté le 2026-09-23), voir `N-1`.
- **Ne dit pas** : sous quel nom le manifeste est déposé → `N-3`. Ni ce qui se passe quand plusieurs
  destinations existent → `Q-02`, **laissée ouverte** le 2026-09-22 et portée par `N-5`.
- **La mesure corrige une lecture naïve** : `pg_restore --list` valide la **table des matières**, et
  rend 0 sur un flux tronqué. L'empreinte et la structure attrapent donc **deux défauts différents**
  — la structure voit un dump qui n'en est pas un, l'empreinte voit une archive corrompue. `P4`
  exige les deux, et aucune ne remplace l'autre.

## Décisions d'implémentation

- **N-1 La structure se vérifie au vol, pendant le dump**, sur le flux en clair — le seul instant
  où koffr le voit. `pg_restore --list` sur la **tête** du flux pour PostgreSQL, le marqueur de fin
  sur la **queue** pour MySQL et MariaDB. L'empreinte, elle, se recalcule en relisant l'archive
  écrite, sans aucune clé. *Raison* : `E-113` interdit la clé privée sur la machine, et une
  vérification structurelle qui l'exigerait rendrait **toute** sauvegarde automatique « non
  vérifiée » au sens de `P4` — donc invisible à la rétention du lot 5. *Exclut* : une commande qui
  manipule une clé privée, et un second fichier de tampon en clair. **Figée par ADR-0017.**
- **N-2 Le contrôle porte sur une fenêtre bornée du flux**, pas sur une seconde passe complète.
  *Raison* : `E-025` interdit de matérialiser le dump, et une seconde lecture intégrale doublerait
  la charge sur la production. *Exclut* : un contrôle qui prétendrait valider tout le contenu — ce
  que `pg_restore --list` ne fait pas non plus, mesure à l'appui.
- **N-3 Le câblage remonte dans `cmd/koffr`** ; `internal/cli` reçoit ses dépendances construites.
  *Raison* : `AR-04`, vérifié par `internal/arch`. *Exclut* : une exception dans les règles
  d'architecture, et un catalogue appelé depuis une commande.
- **N-4 Le manifeste s'appelle `<archive>.json`.** *Raison* : les deux fichiers se suivent dans un
  listing trié, et un inventaire se fait en lisant tous les `*.json` d'un dépôt — sans koffr, sans
  clé, ce que `E-059` demande. *Exclut* : un index unique par base, qu'on perd en entier.
- **N-5 `backups.verified` porte un seul état par archive à ce lot** (hypothèse de `Q-02`, laissée
  ouverte le 2026-09-22). *Raison* : seule la destination `filesystem` existe, donc une seule copie
  par base ; la question demande S3 pour être tranchée sur du réel. *Coût connu et accepté* : le
  lot 4 ajoutera une colonne nullable à `backup_locations` par migration. **Annoncé à la recette.**
- **N-6 La ligne `databases` est écrite par le catalogue avant la sauvegarde.** `fingerprint` est
  l'empreinte SHA-256 de la configuration résolue, `resolved` son JSON **sans secret**. *Raison* :
  la clé étrangère l'exige, et `E-028` veut détecter les changements de configuration. *Exclut* :
  relâcher la contrainte du schéma.

## Vagues

### Vague 1 — La structure se vérifie au vol (`lot3/wave-1-structure-in-flight`)

L'inconnue d'abord, comme au lot 2 : si ce contrôle n'est pas faisable sans clé, tout le lot change
de forme.

- [ ] **1.1** Test d'abord `internal/engine/structure_test.go` — **`VRF-01`** : contre un **vrai**
      PostgreSQL, la tête du flux de `pg_dump -Fc` rend une table des matières cohérente ; un flux
      qui n'est pas un dump est **refusé** ; un flux vide est refusé.
- [ ] **1.2** Test — **`VRF-02`** : contre une **vraie** MariaDB, le marqueur `-- Dump completed`
      est trouvé en queue ; un dump tronqué avant le marqueur est **refusé**.
- [ ] **1.3** Test — le contrôle **ne consomme pas** le flux pour le reste de la chaîne : l'archive
      produite avec contrôle est **identique** à celle produite sans, et le pic mémoire reste borné
      (`E-025`).
- [ ] **1.4** `internal/domain/verify/rules.md` : `VRF-01`, `VRF-02`, et la **divergence écrite** —
      la structure est contrôlée au vol et non sur l'archive écrite, avec son renvoi ADR-0017.
- [ ] **1.5** Vague verte : `verify`, commit `feat(verify): check a dump's structure as it streams past`.

### Vague 2 — Le catalogue écrit pour de vrai (`lot3/wave-2-catalog`)

- [ ] **2.1** Test d'abord `internal/state/catalog_test.go` — sur une **vraie** base SQLite : une
      base est enregistrée puis mise à jour, une sauvegarde est insérée avec ses emplacements, et
      une sauvegarde dont la base n'existe pas est **refusée** par la clé étrangère.
- [ ] **2.2** Test — **`CAT-01`** : la ligne `databases` porte la configuration résolue **sans
      secret** et son empreinte ; deux configurations identiques donnent la même empreinte, une
      modification la change (`N-6`, `E-028`).
- [ ] **2.3** Test `internal/domain/catalog` — **`CAT-02`** : le catalogue est un **index**, jamais
      la source de vérité ; tout ce qu'une restauration exige vit aussi dans le manifeste (ADR-0006).
- [ ] **2.4** `N-3` : le câblage remonte dans `cmd/koffr` ; `internal/cli` reçoit ses dépendances.
      `internal/arch` reste vert sans modification — c'est lui qui prouve que `AR-04` tient.
- [ ] **2.5** `internal/domain/catalog/rules.md` : `CAT-01`, `CAT-02`.
- [ ] **2.6** Vague verte : `verify`, commit `feat(catalog): record backups and where each one is stored`.

### Vague 3 — Le manifeste (`lot3/wave-3-manifest`)

- [ ] **3.1** Test d'abord `internal/domain/catalog/manifest_test.go` — **`CAT-03`** : le manifeste
      porte les **dix-sept champs** de `E-058` dans la forme exacte du § 5.3, et se relit en JSON.
- [ ] **3.2** Test — **`CAT-04`**, `E-059` : aucun identifiant de connexion, sous aucune forme. Le
      test cherche les **valeurs** — mot de passe, chaîne de connexion, chemin de secret — pas les
      noms de champs, et la liste des champs émis est **énumérée** (leçon de `BKP-21`).
- [ ] **3.3** Test — le manifeste est **déposé sur chaque destination**, à côté de l'archive, sous
      `<archive>.json` (`N-4`, `E-057`).
- [ ] **3.4** `internal/domain/catalog/rules.md` : `CAT-03`, `CAT-04`.
- [ ] **3.5** Vague verte : `verify`, commit `feat(catalog): write the manifest beside each archive`.

### Vague 4 — Les sept étapes sont sept (`lot3/wave-4-seven-steps`)

- [ ] **4.1** Test d'abord `internal/domain/backup/service_test.go` — **`BKP-06` amendée** : les
      étapes `verification` et `manifest` sont **faites**, plus jamais `deferred`. Le test qui
      exigeait qu'elles soient déclarées absentes est **retourné**, pas supprimé.
- [ ] **4.2** Test — **`VRF-03`** : l'empreinte est recalculée en **relisant la destination**, pas
      depuis ce que le pipeline a retenu en mémoire (`E-062`). Le test **corrompt l'archive sur le
      disque** entre l'écriture et la vérification et attend `failed`.
- [ ] **4.3** Test — une archive non vérifiée n'est **jamais** comptée comme un succès : le job
      échoue, le catalogue passe à `failed`, et le manifeste écrit le dit (`P4`, `E-008`).
- [ ] **4.4** `rules.md` : `VRF-03`, `BKP-06` amendée.
- [ ] **4.5** Vague verte : `verify`, commit `feat(backup): verify the archive and write its manifest`.

### Vague 5 — `koffr list` et `koffr verify` (`lot3/wave-5-cli`)

- [ ] **5.1** Test d'abord `internal/cli/list_test.go` — `E-103c` : `koffr list [<db>]
      [--destination ID]` liste les archives du catalogue, les plus récentes d'abord.
- [ ] **5.2** Test — **`VRF-04`**, `E-064` : une archive vérifiée est **visuellement distincte**
      d'une non vérifiée, et l'absence de vérification n'est jamais rendue comme un succès. Le test
      lit la sortie que l'exploitant lit.
- [ ] **5.3** Test `internal/cli/verify_test.go` — `koffr verify <backup-id>` relit la destination,
      recalcule l'empreinte, met le catalogue à jour, et **dit** qu'il ne peut pas rejouer la
      structure sans la clé privée (ADR-0017).
- [ ] **5.4** Test — un identifiant inconnu **nomme les archives récentes** de la base plutôt que de
      répondre « introuvable ».
- [ ] **5.5** `internal/domain/verify/rules.md` : `VRF-04`.
- [ ] **5.6** Vague verte : `verify`, commit `feat(cli): list archives and verify one on demand`.

### Vague 6 — Un dépôt s'inventorie sans koffr (`lot3/wave-6-end-to-end`)

- [ ] **6.1** Test d'intégration : sauvegarder une **vraie** PostgreSQL et une **vraie** MariaDB,
      puis inventorier le dépôt **avec `jq` seul** — sans koffr, sans clé privée — et retrouver
      base, taille, horodatage et état de vérification de chaque archive.
- [ ] **6.2** Test — `E-114` : les manifestes ne livrent que des **métadonnées**. Le script cherche
      toute valeur ressemblant à un identifiant dans l'ensemble des `*.json`.
- [ ] **6.3** `scripts/check-inventory.sh` sur le modèle de `N-8` du lot 2 : le test Go dépose, le
      script inventorie avec `jq`. Ajouté à `verify`, sauté bruyamment sans `jq`, exigé en CI.
- [ ] **6.4** Mesurer le binaire et **noter l'écart**. Marge actuelle : 16,5 Mio.
- [ ] **6.5** `README` : comment inventorier un dépôt d'archives sans koffr.
- [ ] **6.6** Vague verte : `verify`, commit `chore: inventory an archive repository without koffr`.

## Vérification de bout en bout

Sur l'instance Multipass, parc réel :

1. `koffr backup boutique` → une archive **et** son `.json` sous `/srv/backups/boutique/2026/09/` ;
2. `jq -r '[.database_id, .size_stored, .verified.checksum] | @tsv' /srv/backups/**/*.json` →
   l'inventaire du dépôt, sans koffr et sans clé ;
3. `koffr list` → l'archive marquée vérifiée, visuellement distincte d'une non vérifiée ;
4. corrompre un octet de l'archive, `koffr verify <id>` → **échec**, catalogue à `failed` ;
5. `grep -r` sur tous les `*.json` pour le mot de passe de la base → **rien** ;
6. `mise run verify` vert, CI verte.

## Recette

- Scénario : `docs/recette/lot-3-scenario.md`, **à écrire avec ce plan**.
- **Ce qui est voulu et pourrait passer pour un bug** : la structure vérifiée **au vol** et non sur
  l'archive écrite (ADR-0017) ; `koffr verify` qui ne rejoue **pas** la structure ; un seul état de
  vérification par archive (`N-5`) ; `pg_restore --list` qui ne détecte **pas** une troncature ;
  aucune revérification périodique (`E-065`, lot 6) ; une seule destination possible (lot 4).
- **Décisions attendues de la session** : la lisibilité de `koffr list` — que veut-on y lire qu'on
  n'y lit pas ? Et le nom du manifeste, vu sur un vrai dépôt. `Q-02` **ne peut pas** être tranchée
  ici : elle demande S3, donc la recette du lot 4.

## Risques et points à vérifier en route

| Risque | Ce qui le borne |
| --- | --- |
| Le contrôle au vol consomme le flux et casse `E-025` | Tâche `1.3` : l'archive produite avec contrôle est **identique** à celle produite sans |
| `pg_restore` lit la TOC puis s'arrête, et le tube se bloque | Fenêtre **bornée** (`N-2`) ; l'écrivain tolère un lecteur parti |
| Le câblage remonté dans `cmd/koffr` casse les tests de commande | `internal/arch` est la garde ; les tests reçoivent des dépendances de test |
| Le binaire grossit | Mesuré en `6.4`. Marge actuelle : 16,5 Mio |
| `jq` absent de la machine | Saut bruyant, exigé en CI — le schéma de `N-8` du lot 2 |

## Ce qui reste ouvert

- **`Q-02`** — quelle destination est relue quand il y en a plusieurs. Portée par `N-5` ; se tranche
  à la recette du **lot 4**, qui apporte S3 et SFTP.
- **`D-08`** — quand livrer les objets globaux d'un cluster (`B-01`). Ne bloque pas ce lot.
- **`B-09`** (`pg_dump --compress=zstd:3`), **`B-10`** (`govulncheck` dans `verify`).

## Journal d'exécution

Rempli par `/executer-plan` : échecs, décisions `N-n` ajoutées en route, écarts au plan, datés.
