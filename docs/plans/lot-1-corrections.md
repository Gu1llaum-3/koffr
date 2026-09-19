# Plan lot 1 — Corrections de recette

> Statut : **brouillon**. Exécuté par `/executer-plan`. Les règles communes à tous les plans sont
> dans `METHODE.md` § « Exécution d'un plan » et ne sont pas répétées ici.

Travail transverse issu de la session de recette du lot 1 (2026-09-19, instance Multipass `koffr`,
parc réel de trois familles). Il ne rouvre aucune exigence : il corrige ce qu'une vraie machine a
montré, et branche ce que le lot 1 avait livré sans surface d'usage.

## Périmètre

**Les trois anomalies `A-09` à `A-11`**, toutes **bloquantes**. Le registre fait foi :
`docs/recette/anomalies.md`.

| `A-nn` | Constat | Ce qu'il faut |
| --- | --- | --- |
| `A-09` | koffr résout les liens symboliques, ce qui casse l'`argv[0]` du `pg_wrapper` de Debian ; puis il lit « 153 » dans `at … line 153` et en fait une version | Exécuter le chemin **tel qu'il a été trouvé**, et n'accepter une version que d'une réponse qui **nomme l'outil ou sa famille** |
| `A-10` | Les clients MySQL et MariaDB **ne peuvent pas coexister** sur Debian et Ubuntu (`Conflicts: virtual-mysql-client-core`) | Le dire dans le `README`, et désigner la stratégie `exec` comme la réponse — ADR-0015 |
| `A-11` | `doctor` répond `none` sur une base déclarant `strategy: exec` : le `ContainerFinder` n'est branché nulle part | Le brancher dans `doctor` et `config validate` |

**Exigences touchées** — aucune n'est rouverte ; deux sont **mieux** tenues qu'avant :

| `E-nnn` | Ce que la correction change |
| --- | --- |
| `E-013` | La troisième source — le conteneur — devient réellement utilisable, pas seulement implémentée (`A-11`) |
| `E-039` | La version cesse d'être devinable à partir d'un message d'erreur (`A-09`) |
| `E-046` | La stratégie `exec` a enfin un effet observable |

**Écarté de ce plan** :

- **Toute reprise d'ADR-0014.** L'installation gérée reste hors MVP. `A-10` ne la rouvre pas : elle
  montre que la stratégie `exec` en est le complément nécessaire, pas que l'installation gérée
  manque.
- **Les quatre champs restants de `doctor`** (`E-104b`) : lot 6, comme prévu.

**Critère de sortie** :

1. Sur l'instance de recette, `koffr tools list` rapporte `/usr/bin/pg_dump` avec sa **vraie**
   version, et **aucun candidat fantôme** ;
2. une sortie qui ne nomme ni l'outil ni sa famille est **écartée**, quel que soit le nombre qu'elle
   contient ;
3. une base déclarant `tools: { strategy: exec, container: … }` obtient de `doctor` l'outil du
   conteneur, avec la provenance `container` ;
4. le `README` dit qu'un parc mixte MySQL et MariaDB sur une même machine **exige** `exec`, et
   pourquoi ;
5. le scénario `docs/recette/lot-1-scenario.md` est rejoué **en entier**, sans contournement.

## État de départ (vérifié le 2026-09-19)

Constaté sur l'instance, pas supposé.

- **Lot 1 terminé** : six vagues mergées, `main` vert en local et en CI au commit `a6071bf`.
- **Instance reconstruite** par le propriétaire : Ubuntu 26.04 `arm64`, 8 Go, 4 cœurs, 25 Go,
  `mise` et Docker, **sans Go ni compilateur C**. `verify` y passe en 2 min 15 à froid.
- **Parc réel** : PostgreSQL 16.15, MariaDB 11.4.13, MySQL 8.4.11 en conteneurs, plus une base
  éteinte. Les trois familles sont correctement identifiées.
- **`/usr/bin/pg_dump` est un lien** vers `/usr/share/postgresql-common/pg_wrapper`. Appelé par son
  nom : `pg_dump (PostgreSQL) 18.6`. Appelé par le chemin résolu :
  `Can't exec "--version": No such file or directory at … line 153`.
- **`koffr tools list` rapporte aujourd'hui deux candidats PostgreSQL** : le vrai en 18.6, et
  `pg_wrapper` en **153.0**.
- **`apt` refuse d'installer les deux clients** : `mariadb-client-core : Conflicts:
  virtual-mysql-client-core`, puis `Unable to satisfy dependencies`.
- **`ContainerFinder` n'est importé par aucun paquet hors du sien** : vérifié par `grep` sur
  `cmd/` et `internal/cli/`.
- Le piège du § 5.2 est évité sur cette machine, et `E-041` a tenu sur un cas que la distribution a
  produit elle-même.

## Ce que le cahier des charges dit, et ce qu'il ne dit pas

- **Dit** : § 5.2 `F2.1` nomme les sources, `F2.2` exige la version **par exécution**, `F2.9` fait
  de `exec` une déclaration par base.
- **Ne dit pas** : ce qu'il advient d'un outil empaqueté derrière un wrapper qui dispatche sur
  `argv[0]` — le cas n'existe que sur Debian et ses dérivés → `N-1`. Ni ce qu'on fait quand la
  distribution **interdit** deux clients côte à côte → `N-2` et ADR-0015.
- **Contredit** : rien. `A-10` est une contrainte de distribution, pas une contradiction du CDC.

## Décisions d'implémentation

- **N-1 Un candidat s'exécute par le chemin où il a été trouvé, jamais par son chemin résolu.** La
  résolution des liens sert **seulement** à dédupliquer — deux chemins menant au même inœud sont un
  candidat, pas deux —, et le chemin exécuté et rapporté reste celui de la découverte. *Raison* :
  `pg_wrapper` choisit la version d'après son `argv[0]` ; résoudre le lien lui retire l'information
  dont il vit. *Exclut* : rapporter à l'exploitant un chemin qu'il ne reconnaîtrait pas.
- **N-2 Une réponse ne vaut version que si elle nomme l'outil ou sa famille.** `pg_dump`,
  `PostgreSQL`, `MariaDB`, `mysqldump`, `MySQL` : à défaut, le candidat est **écarté**, même si sa
  sortie contient des chiffres. *Raison* : `RSV-02` promet qu'un candidat qui répond n'importe quoi
  est écarté, et la recette a montré une version fabriquée à partir de `at … line 153`. *Exclut* :
  la déduction de la famille depuis le **nom** du binaire, qui est ce qui a transformé `pg_wrapper`
  en candidat PostgreSQL.
- **N-3 `Diagnose` reçoit deux énumérateurs, l'un pour l'hôte et l'autre pour les conteneurs**, et
  choisit selon ce que la base déclare. *Raison* : `E-046` fait de `exec` une décision **par base**,
  et le domaine doit pouvoir l'exprimer sans connaître Docker. *Exclut* : un énumérateur unique qui
  déciderait tout seul, et un domaine qui importerait l'adaptateur.
- **N-4 Une base en `exec` dont le conteneur ne répond pas est un échec qui nomme le conteneur**,
  jamais un repli silencieux sur un outil de l'hôte. *Raison* : `E-046` — l'exploitant a demandé
  l'outil du conteneur pour une raison. *Exclut* : une résolution qui « se débrouille ».

## Vagues

### Vague 1 — Lire les outils tels qu'ils sont (`lot1/wave-7-read-tools-as-they-are`) — `A-09`

- [ ] **1.1** Test d'abord `internal/engine/discover_test.go` — **`RSV-02` amendée** : une réponse
      qui ne nomme ni l'outil ni sa famille est **écartée**, même si elle contient un nombre. Cas
      tiré de la recette : `Can't exec "--version": No such file or directory at … line 153`.
- [ ] **1.2** Test — un candidat atteint par un **lien symbolique** est exécuté **par le chemin du
      lien** et rapporté sous ce chemin (`N-1`). Fixture : un lien vers un script qui répond
      différemment selon son `argv[0]`, comme le fait `pg_wrapper`.
- [ ] **1.3** Test — deux chemins menant au **même binaire** restent **un** candidat : la
      déduplication survit au changement.
- [ ] **1.4** Le code. `toolFamily` cesse de deviner depuis le nom du fichier (`N-2`).
- [ ] **1.5** `internal/domain/resolve/rules.md` : `RSV-02` amendée, avec le cas réel en exemple.
- [ ] **1.6** **Vérifié sur l'instance** : `koffr tools list` rapporte `/usr/bin/pg_dump` en 18.6 et
      **aucun** candidat en 153.0.
- [ ] **1.7** Vague verte : `verify`, commit `fix(engine): run a tool by the path it was found at, and refuse a version nobody announced`.

### Vague 2 — La stratégie `exec` est branchée (`lot1/wave-8-wire-exec-strategy`) — `A-11`

- [ ] **2.1** Test d'abord `internal/domain/resolve/diagnose_test.go` — une base qui déclare un
      conteneur est diagnostiquée avec l'énumérateur de conteneurs, une autre avec celui de l'hôte,
      **dans le même parc** (`N-3`).
- [ ] **2.2** Test — un conteneur qui ne répond pas donne un échec qui **nomme le conteneur**, et
      **jamais** un repli sur un outil de l'hôte (`N-4`).
- [ ] **2.3** Le code : `resolve.Subject` porte le conteneur, `Diagnose` reçoit les deux
      énumérateurs, `internal/cli` les câble.
- [ ] **2.4** Test `internal/cli/doctor_test.go` — `doctor` affiche la provenance `container` pour
      une base en `exec`.
- [ ] **2.5** `rules.md` : `RSV-10` amendée — la stratégie a désormais un effet observable.
- [ ] **2.6** **Vérifié sur l'instance**, contre le conteneur MariaDB réel : `doctor` annonce
      l'outil du conteneur avec sa version.
- [ ] **2.7** Vague verte : `verify`, commit `feat(cli): use the container tool for a database that asks for it`.

### Vague 3 — Le parc mixte est dit (`lot1/wave-9-mixed-fleet`) — `A-10`

- [ ] **3.1** **ADR-0015** : un parc mixte MySQL et MariaDB sur une même machine exige la stratégie
      `exec`. Il **borne** ADR-0014 sans le rouvrir, cite le conflit de paquets constaté, et dit ce
      que cela impose à l'image conteneur du lot 7.
- [ ] **3.2** `README.md` : le conflit de paquets, son message exact, et `exec` comme réponse.
- [ ] **3.3** `docs/recette/lot-1-scenario.md` : le parcours 4 gagne le cas réel — `mysqldump` qui
      est celui de MariaDB — et le parcours 7 devient vérifiable.
- [ ] **3.4** `docs/recette/anomalies.md` : colonne « Corrigée » remplie pour les trois.
- [ ] **3.5** **Rejouer le scénario entier sur l'instance**, sans contournement. Consigner.
- [ ] **3.6** Vague verte : `verify`, commit `docs: a mixed MySQL and MariaDB fleet needs the exec strategy`.

## Vérification de bout en bout

Sur l'instance de recette, avec son parc réel :

1. `koffr tools list` → `/usr/bin/pg_dump` en **18.6**, aucun candidat en 153.0 ;
2. un script qui répond `Can't exec … at line 153` → **absent** de la liste ;
3. `koffr doctor` sur la base `erp` déclarée en `exec` → provenance **`container`** ;
4. le même avec un conteneur arrêté → échec **nommant le conteneur** ;
5. les sept parcours du scénario joués **mot à mot**.

## Recette

- Scénario : `docs/recette/lot-1-scenario.md`, corrigé par la vague 3 et **rejoué en entier**.
- **Décision attendue** : aucune nouvelle. `D-01` est répondue, `D-06` tranchée, `Q-15` sans objet.
- **Ce qui est voulu et pourrait passer pour un bug** :
  - `E-042` ne donne toujours pas de commande de correction (ADR-0014) ;
  - `koffr tools install` n'existe toujours pas ;
  - une machine ne peut toujours pas porter les deux clients — c'est la distribution, pas koffr.

## Risques et points à vérifier en route

| Risque | Ce qu'on fait s'il se réalise |
| --- | --- |
| Ne plus résoudre les liens fait réapparaître des doublons que la déduplication attrapait | Traité par `N-1` : on déduplique **par** le chemin résolu, on exécute **par** le chemin trouvé. Vérifié en `1.3` |
| Exiger que la réponse nomme l'outil écarte un outil légitime dont la sortie est inhabituelle | On mesure sur les binaires réels de l'instance — PostgreSQL, MariaDB, MySQL — avant de figer la liste des marqueurs. S'il en manque un, c'est une `N-n` datée |
| Brancher l'énumérateur de conteneurs alourdit la signature de `Diagnose` | Assumé : `E-046` fait de `exec` une décision par base, et le domaine doit pouvoir l'exprimer. Deux ports valent mieux qu'un adaptateur importé |
| Le rejeu révèle de **nouvelles** anomalies | Elles sont numérotées `A-12` et suivantes, et on décide alors — on ne les corrige pas dans la foulée |

## Journal d'exécution

Rempli par `/executer-plan` : échecs, décisions `N-n` ajoutées en route, écarts au plan, datés.
